package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/cachebatch"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/web"
)

// logStartupConfigSummary emits one low-noise startup summary of configured
// versus effective values for critical subsystems.
func (app *App) logStartupConfigSummary(queueSize int, runDiscovery bool) {
	app.ConfigManager.ConfigMu.RLock()
	cfg := app.ConfigManager.Config
	app.ConfigManager.ConfigMu.RUnlock()
	if cfg == nil {
		return
	}

	effectivePreload := false
	if app.SubsystemManager.preloadManager != nil {
		effectivePreload = app.SubsystemManager.preloadManager.IsEnabled()
	}

	rwEffectiveMax := int64(0)
	rwEffectiveMinIdle := int64(0)
	roEffectiveMax := int64(0)
	roEffectiveMinIdle := int64(0)
	if app.dbRwPool != nil {
		rwEffectiveMax = app.dbRwPool.Config.MaxConnections
		rwEffectiveMinIdle = app.dbRwPool.Config.MinIdleConnections
	}
	if app.dbRoPool != nil {
		roEffectiveMax = app.dbRoPool.Config.MaxConnections
		roEffectiveMinIdle = app.dbRoPool.Config.MinIdleConnections
	}

	effectiveWorkerMax := 0
	effectiveWorkerMinIdle := 0
	if app.SubsystemManager.pool != nil {
		effectiveWorkerMax = app.SubsystemManager.pool.MaxWorkers
		effectiveWorkerMinIdle = app.SubsystemManager.pool.MinWorkers
	}

	slog.Info("startup config summary",
		"db_configured_max", cfg.DBMaxPoolSize,
		"db_rw_effective_max", rwEffectiveMax,
		"db_ro_effective_max", roEffectiveMax,
		"db_configured_min_idle", cfg.DBMinIdleConnections,
		"db_rw_effective_min_idle", rwEffectiveMinIdle,
		"db_ro_effective_min_idle", roEffectiveMinIdle,
		"worker_configured_max", cfg.WorkerPoolMax,
		"worker_effective_max", effectiveWorkerMax,
		"worker_configured_min_idle", cfg.WorkerPoolMinIdle,
		"worker_effective_min_idle", effectiveWorkerMinIdle,
		"queue_configured_size", cfg.QueueSize,
		"queue_effective_size", queueSize,
		"cache_configured_enabled", cfg.EnableHTTPCache,
		"cache_effective_enabled", app.cacheMW != nil,
		"preload_configured_enabled", cfg.EnableCachePreload,
		"preload_effective_enabled", effectivePreload,
		"discovery_configured_enabled", cfg.RunFileDiscovery,
		"discovery_effective_enabled", runDiscovery)
}

// Run initializes the application, starts background workers, and blocks until shutdown or error.
func (app *App) Run(minPoolWorkers, maxPoolWorkers int) error {
	if app.rootDir == "" {
		app.setRootDir(nil)
	}

	app.setupBootstrapLogging()
	// Log file closing is handled by Shutdown() via logger.Shutdown()

	// Initialize scheduler (defaults to runtime.NumCPU() when maxConcurrentTasks is 0)
	app.SubsystemManager.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.SubsystemManager.scheduler.Start(app.RuntimeManager.ctx); err != nil {
			slog.Error("scheduler error", "err", err)
		}
	}()

	// Start profiler after logging is configured so messages go to both console and file
	if app.opt.Profile.IsSet && app.opt.Profile.String != "" {
		var stopProfile func()
		var err error
		if app.testSeams.ProfilerStart != nil {
			stopProfile, err = app.testSeams.ProfilerStart(profiler.Config{Mode: app.opt.Profile.String})
		} else {
			stopProfile, err = profiler.Start(profiler.Config{Mode: app.opt.Profile.String})
		}
		if err != nil {
			slog.Error("failed to start profiler", "err", err)
			return err
		}
		app.RuntimeManager.stopProfiler = stopProfile
		slog.Info("Profiler", "mode", app.opt.Profile.String, "dir", profiler.Dir())
	}

	app.setDB()

	app.setConfigDefaults()

	// Handle restore-last-known-good CLI flag - restore before loading config
	if app.opt.RestoreLastKnownGood.IsSet && app.opt.RestoreLastKnownGood.Bool {
		cpcRw, err := app.dbRwPool.Get()
		if err != nil {
			slog.Error("failed to get db connection for restore", "err", err)
			return fmt.Errorf("failed to get db connection for restore: %w", err)
		}
		defer app.dbRwPool.Put(cpcRw)

		// Restore last known good config via ConfigService
		restoredConfig, err := app.ConfigManager.ConfigService.RestoreLastKnownGood(app.RuntimeManager.ctx)
		if err != nil {
			slog.Error("failed to restore last known good config", "err", err)
			return fmt.Errorf("failed to restore last known good config: %w", err)
		}

		// Validate restored config
		if err := app.ConfigManager.ConfigService.Validate(restoredConfig); err != nil {
			slog.Error("restored config is invalid", "err", err)
			return fmt.Errorf("restored config is invalid: %w", err)
		}

		// Save restored config to database via ConfigService
		if err := app.ConfigManager.ConfigService.Save(app.RuntimeManager.ctx, restoredConfig); err != nil {
			slog.Error("failed to save restored config", "err", err)
			return fmt.Errorf("failed to save restored config: %w", err)
		}

		// Apply CLI/env overrides after restore
		restoredConfig.LoadFromOpt(app.opt)

		// Update app.ConfigManager.Config atomically
		app.ConfigManager.ConfigMu.Lock()
		app.ConfigManager.Config = restoredConfig
		app.ConfigManager.ConfigMu.Unlock()
		slog.Info("last known good configuration restored from database")

		// Reconfigure database pools with restored config values
		if err := app.reconfigurePoolsFromConfig(); err != nil {
			slog.Error("failed to reconfigure pools after restoring config", "err", err)
			return fmt.Errorf("reconfigure pools after restore: %w", err)
		}
	} else {
		// Load configuration with precedence: CLI/Env > Database > Defaults
		// This must happen after setConfigDefaults() which initializes defaults in DB
		if err := app.loadConfig(); err != nil {
			slog.Warn("failed to load configuration", "err", err)
			// Continue with defaults
			defaultConfig := config.DefaultConfig()
			if app.testSeams.FallbackConfig != nil {
				defaultConfig = app.testSeams.FallbackConfig()
			}
			defaultConfig.LoadFromOpt(app.opt)
			app.ConfigManager.ConfigMu.Lock()
			app.ConfigManager.Config = defaultConfig
			app.ConfigManager.ConfigMu.Unlock()

			// Reconfigure pools with default config
			if err := app.reconfigurePoolsFromConfig(); err != nil {
				slog.Error("failed to reconfigure pools with default config", "err", err)
				return fmt.Errorf("reconfigure pools with defaults: %w", err)
			}
		} else {
			if err := app.reconfigurePoolsFromConfig(); err != nil {
				slog.Error("failed to reconfigure pools after loading config", "err", err)
				return fmt.Errorf("reconfigure pools after load: %w", err)
			}
		}
	}

	// Read the configured dque disk quota before StartWriteBatcher; ApplyConfig
	// below handles hot-reloads after save, not first boot.
	app.ConfigManager.ConfigMu.RLock()
	dqueMaxDiskBytes := config.DefaultDQueMaxDiskBytes
	if app.ConfigManager.Config != nil {
		dqueMaxDiskBytes = app.ConfigManager.Config.DQueMaxDiskBytes
	}
	app.ConfigManager.ConfigMu.RUnlock()

	app.StartWriteBatcher(app.RuntimeManager.ctx, true, dqueMaxDiskBytes)
	app.startStartupPragmaOptimize()

	// Apply config to app fields
	app.ApplyConfig()

	// Ensure HTTP cache key format is current *before* stale cache drop
	// and initializeHTTPCache, so legacy rows from a previous key version
	// are invalidated before the cache middleware starts serving.
	app.ensureHTTPCacheKeyFormatCurrent()

	app.scheduleStaleCacheDrop("run-startup")

	// Initialize HTTP cache middleware after config is loaded
	app.initializeHTTPCache()

	// --- SubsystemManager: creates queue, fileProcessor, preloadManager,
	//     moduleStateService, pool, and processingStats ---
	app.Start(
		app.RuntimeManager.ctx, app.ConfigManager.Config, minPoolWorkers, maxPoolWorkers,
		app.imagesDir, app.normalizedImagesDir,
		removeImagesDirPrefix,
		app.getRouter,
		app.GetHandlerQueries,
		app.GetETagVersion,
	)

	// Run file discovery based on config value (defaults to true)
	runDiscovery := true // default
	if app.ConfigManager.Config != nil {
		runDiscovery = app.ConfigManager.Config.RunFileDiscovery
	}

	app.startGalleryStatsBaselines()

	app.ConfigManager.ConfigMu.RLock()
	httpCacheEnabled := app.ConfigManager.Config != nil && app.ConfigManager.Config.EnableHTTPCache
	app.ConfigManager.ConfigMu.RUnlock()
	if httpCacheEnabled {
		app.startHTTPCacheBaselines(app.getCtx())
	}

	// Incremental gallery stats counters are wired through two different
	// patterns below. The split is structurally necessary:
	//
	//   Folders — simple field callback (OnFolderCreated). The importer
	//   lives in gallerylib which has no import restrictions, so a func()
	//   field on InfrastructureService works directly.
	//
	//   Files — drilled callback (onFileInserted). internal/server/files
	//   cannot import internal/server (circular), so a plain func(int64)
	//   is threaded through StartPool → NewPoolFuncWithProcessor →
	//   runPoolWorkerWithProcessor, where it fires on SubmitFileForWrite
	//   success.
	app.OnFolderCreated = func() {
		app.RuntimeManager.GalleryStats().addFolder()
	}

	if runDiscovery {
		if app.testSeams.TriggerDiscovery != nil {
			go app.testSeams.TriggerDiscovery()
		} else {
			go app.TriggerDiscovery()
		}
	}

	// Worker pool startup
	app.RuntimeManager.poolDone = make(chan struct{})
	app.StartPool(app.RuntimeManager.ctx, app.RuntimeManager.poolDone, app.normalizedImagesDir, removeImagesDirPrefix, app.SubsystemManager.fileProcessor,
		func(size int64) { app.RuntimeManager.GalleryStats().addFile(size) })

	// Completion monitor for initial batch processing
	if runDiscovery {
		go func() {
			// 1. Wait for discovery to start (optional, but handles fast/slow starts)
			// We check periodically until active senders or processing starts
			timeout := time.After(30 * time.Second)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-app.RuntimeManager.ctx.Done():
					return
				case <-timeout:
					// If nothing happened in 30s, just exit monitor
					return
				case <-ticker.C:
					if app.SubsystemManager.qSendersActive.Load() > 0 || app.SubsystemManager.processingStats.TotalFound.Load() > 0 {
						goto wait_for_end
					}
				}
			}

		wait_for_end:
			// 2. Wait for discovery to finish AND queue to drain AND workers to finish
			for {
				select {
				case <-app.RuntimeManager.ctx.Done():
					return
				case <-ticker.C:
					activeSenders := app.SubsystemManager.qSendersActive.Load()
					queueLen := app.SubsystemManager.q.Len()
					inFlight := app.SubsystemManager.processingStats.InFlight.Load()
					pendingWrites := app.SubsystemManager.fileProcessor.PendingWriteCount()

					if activeSenders == 0 && queueLen == 0 && inFlight == 0 && pendingWrites == 0 {
						slog.Info("File processing completed",
							"found", app.SubsystemManager.processingStats.TotalFound.Load(),
							"existing", app.SubsystemManager.processingStats.AlreadyExisting.Load(),
							"inserted", app.SubsystemManager.processingStats.NewlyInserted.Load(),
							"skipped_invalid", app.SubsystemManager.processingStats.SkippedInvalid.Load(),
						)
						app.scheduleDiscoveryCompletePragmaOptimize()
						return
					}
				}
			}
		}()
	}

	prodReclaimerCfg := MemoryReclaimerConfig{
		InitialDelay:  1 * time.Minute,
		CheckInterval: 30 * time.Second,
		IdleThreshold: 10 * time.Second,
		FreeMemFunc:   debug.FreeOSMemory,
	}
	if app.testSeams.MemoryReclaimer != nil {
		go app.testSeams.MemoryReclaimer(prodReclaimerCfg)
	} else {
		go app.memoryReclaimer(prodReclaimerCfg)
	}

	// Start HTTP cache cleanup goroutine if caching is enabled in config
	app.ConfigManager.ConfigMu.RLock()
	cacheEnabled := app.ConfigManager.Config != nil && app.ConfigManager.Config.EnableHTTPCache
	app.ConfigManager.ConfigMu.RUnlock()

	if cacheEnabled {
		app.RuntimeManager.wg.Go(func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()

			for {
				ctx := app.getCtx()
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					deleted, err := cachelite.CleanupExpired(ctx, app.dbRwPool)
					if err != nil {
						slog.Error("HTTP cache cleanup failed", "err", err)
					} // else if deleted > 0 {
					// slog.Info("HTTP cache cleanup completed", "deleted_entries", deleted)
					//}
					_ = deleted
				}
			}
		})
	}

	// --- Metrics ---
	app.RuntimeManager.metricsCollector = metrics.NewCollector()
	app.WireMetrics(app.RuntimeManager.metricsCollector)
	// Wire up batch load manager (created below if cacheEnabled)
	// app.RuntimeManager.metricsCollector.SetCacheBatchLoad handled below

	// Queue info
	queueSize := 10000
	if app.ConfigManager.Config != nil {
		queueSize = app.ConfigManager.Config.QueueSize
	}
	app.RuntimeManager.metricsCollector.SetQueueInfo(func() int { return app.SubsystemManager.q.Len() }, queueSize)
	app.logStartupConfigSummary(queueSize, runDiscovery)

	// Ensure the session store and manager exist before building handlers.
	app.ensureSession()
	if err := app.buildHandlers(web.FS); err != nil {
		return fmt.Errorf("build handlers: %w", err)
	}

	// Wire preload service into gallery handlers (must be after both
	// SubsystemManager.Start and HandlerManager.Build)
	app.SetPreloadService(app.SubsystemManager.preloadManager)

	// Create batch load manager and wire to metrics (requires buildHandlers for getRouter)
	if cacheEnabled && app.SubsystemManager.moduleStateService != nil {
		app.SubsystemManager.batchLoadManager = cachebatch.NewManager(cachebatch.Config{
			GetQueries: func() (cachebatch.BatchLoadQueries, func()) {
				cpcRo, err := app.dbRoPool.Get()
				if err != nil {
					return nil, nil
				}
				return cpcRo.Queries, func() { app.dbRoPool.Put(cpcRo) }
			},
			GetHandler:         app.getRouter,
			GetETagVersion:     app.GetETagVersion,
			ModuleStateService: app.SubsystemManager.moduleStateService,
		})
		app.RuntimeManager.metricsCollector.SetCacheBatchLoad(app.SubsystemManager.batchLoadManager)
	}

	slog.Info("Calling app.Serve()")
	app.RuntimeManager.SetOnListen(func() {
		app.onServerListening()
	})
	if err := app.Serve(); err != nil {
		slog.Error("server error", "err", err)
		time.Sleep(1 * time.Second) // Give logger time to write
		panic("main")
	}

	// If a restart was requested, replace the current process image. On success
	// this does not return; on failure it exits the process.
	if app.IsRestartRequested() {
		app.Shutdown()
		app.ExecRestart()
	}

	return nil
}

// startGalleryStatsBaselines launches three parallel goroutines to populate
// gallery statistics (folders, file count/timestamps, file size sum) from
// the database asynchronously. Each goroutine increments the running counter
// synchronously before launching so that display helpers see running > 0
// immediately (rendering "N/A" for unpopulated fields).
func (app *App) startGalleryStatsBaselines() {
	if app.testSeams.GalleryStatsStartup != nil {
		app.testSeams.GalleryStatsStartup()
		return
	}
	gs := app.RuntimeManager.GalleryStats()
	ctx := app.getCtx()

	runBaseline := func(name string, fn func(ctx context.Context, cpc *dbconnpool.CpConn) error) {
		gs.markRunning(1)
		go func() {
			defer gs.markRunning(-1)
			cpcRo, err := app.dbRoPool.Get()
			if err != nil {
				slog.Error("gallery stats baseline: DB connection failed", "query", name, "err", err)
				return
			}
			defer app.dbRoPool.Put(cpcRo)
			if err := fn(ctx, cpcRo); err != nil {
				if !errors.Is(err, context.DeadlineExceeded) {
					slog.Error("gallery stats baseline failed", "query", name, "err", err)
				}
			}
		}()
	}

	runBaseline("folder_count", func(ctx context.Context, cpc *dbconnpool.CpConn) error {
		ct, err := cpc.Queries.GetFolderCount(ctx)
		if err != nil {
			return err
		}
		gs.setFolders(ct)
		return nil
	})
	runBaseline("file_count", func(ctx context.Context, cpc *dbconnpool.CpConn) error {
		row, err := cpc.Queries.GetFileCountAndTimestamps(ctx)
		if err != nil {
			return err
		}
		gs.setFileCountAndTimestamps(row.CtFiles, row.MinCreatedAt, row.MaxUpdatedAt)
		return nil
	})
	runBaseline("file_size_sum", func(ctx context.Context, cpc *dbconnpool.CpConn) error {
		sz, err := cpc.Queries.GetFileSizeSum(ctx)
		if err != nil {
			return err
		}
		gs.addImagesSize(sz)
		return nil
	})
}
