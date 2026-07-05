package server

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/cachebatch"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/web"
)

func (app *App) Run(minPoolWorkers, maxPoolWorkers int) error {
	app.setRootDir(nil)

	app.setupBootstrapLogging()
	// Log file closing is handled by Shutdown() via logger.Shutdown()

	// Initialize scheduler (defaults to runtime.NumCPU() when maxConcurrentTasks is 0)
	app.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.scheduler.Start(app.ctx); err != nil {
			slog.Error("scheduler error", "err", err)
		}
	}()

	// Start profiler after logging is configured so messages go to both console and file
	if app.opt.Profile.IsSet && app.opt.Profile.String != "" {
		stopProfile, err := profiler.Start(profiler.Config{Mode: app.opt.Profile.String})
		if err != nil {
			slog.Error("failed to start profiler", "err", err)
			return err
		}
		app.stopProfiler = stopProfile
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
		restoredConfig, err := app.configService.RestoreLastKnownGood(app.ctx)
		if err != nil {
			slog.Error("failed to restore last known good config", "err", err)
			return fmt.Errorf("failed to restore last known good config: %w", err)
		}

		// Validate restored config
		if err := app.configService.Validate(restoredConfig); err != nil {
			slog.Error("restored config is invalid", "err", err)
			return fmt.Errorf("restored config is invalid: %w", err)
		}

		// Save restored config to database via ConfigService
		if err := app.configService.Save(app.ctx, restoredConfig); err != nil {
			slog.Error("failed to save restored config", "err", err)
			return fmt.Errorf("failed to save restored config: %w", err)
		}

		// Apply CLI/env overrides after restore
		restoredConfig.LoadFromOpt(app.opt)

		// Update app.config atomically
		app.configMu.Lock()
		app.config = restoredConfig
		app.configMu.Unlock()
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
			defaultConfig.LoadFromOpt(app.opt)
			app.configMu.Lock()
			app.config = defaultConfig
			app.configMu.Unlock()

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

	// Apply config to app fields
	app.ApplyConfig()
	app.scheduleStaleCacheDrop("run-startup")

	// Initialize HTTP cache middleware after config is loaded
	app.initializeHTTPCache()

	// --- SubsystemManager: creates queue, fileProcessor, preloadManager,
	//     moduleStateService, pool, and processingStats ---
	app.Start(
		app.ctx, app.config,
		app.imagesDir, app.normalizedImagesDir,
		removeImagesDirPrefix,
		app.getRouter,
		app.GetHandlerQueries,
		app.GetETagVersion,
	)

	// Run file discovery based on config value (defaults to true)
	runDiscovery := true // default
	if app.config != nil {
		runDiscovery = app.config.RunFileDiscovery
	}
	if runDiscovery {
		go app.TriggerDiscovery()
	} else if app.moduleStateService != nil {
		go func() {
			ctx := app.getCtx()
			lastStarted, ok, err := app.moduleStateService.GetLastStartedAt(ctx, "discovery")
			if err != nil {
				slog.Error("failed to get last started at", "err", err)
			}
			if ok {
				if _, err := app.refreshGalleryStatsCache(ctx, lastStarted); err != nil {
					slog.Error("failed to refresh gallery stats cache", "err", err)
				}
			} else {
				if _, err := app.refreshGalleryStatsCache(ctx, 0); err != nil {
					slog.Error("failed to refresh gallery stats cache", "err", err)
				}
			}
		}()
	}

	// Worker pool startup
	app.poolDone = make(chan struct{})
	app.StartPool(app.ctx, app.poolDone, app.normalizedImagesDir, removeImagesDirPrefix)

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
				case <-app.ctx.Done():
					return
				case <-timeout:
					// If nothing happened in 30s, just exit monitor
					return
				case <-ticker.C:
					if app.qSendersActive.Load() > 0 || app.processingStats.TotalFound.Load() > 0 {
						goto wait_for_end
					}
				}
			}

		wait_for_end:
			// 2. Wait for discovery to finish AND queue to drain AND workers to finish
			for {
				select {
				case <-app.ctx.Done():
					return
				case <-ticker.C:
					activeSenders := app.qSendersActive.Load()
					queueLen := app.q.Len()
					inFlight := app.processingStats.InFlight.Load()
					pendingWrites := app.fileProcessor.PendingWriteCount()

					if activeSenders == 0 && queueLen == 0 && inFlight == 0 && pendingWrites == 0 {
						slog.Info("File processing completed",
							"found", app.processingStats.TotalFound.Load(),
							"existing", app.processingStats.AlreadyExisting.Load(),
							"inserted", app.processingStats.NewlyInserted.Load(),
							"skipped_invalid", app.processingStats.SkippedInvalid.Load(),
						)
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
	go app.memoryReclaimer(prodReclaimerCfg)

	// Start HTTP cache cleanup goroutine if caching is enabled in config
	app.configMu.RLock()
	cacheEnabled := app.config != nil && app.config.EnableHTTPCache
	app.configMu.RUnlock()

	if cacheEnabled {
		app.wg.Go(func() {
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
	app.metricsCollector = metrics.NewCollector()
	app.WireMetrics(app.metricsCollector)
	// Wire up batch load manager (created below if cacheEnabled)
	// app.metricsCollector.SetCacheBatchLoad handled below

	// Queue info
	queueSize := 10000
	if app.config != nil {
		queueSize = app.config.QueueSize
	}
	app.metricsCollector.SetQueueInfo(func() int { return app.q.Len() }, queueSize)
	app.metricsCollector.RecordModuleActivity("discovery", runDiscovery)
	app.metricsCollector.RecordModuleActivity("cache_preload",
		app.config != nil && app.config.EnableCachePreload)
	app.logStartupConfigSummary(queueSize, runDiscovery)

	// Ensure the session store and manager exist before building handlers.
	app.ensureSession()
	if err := app.buildHandlers(web.FS); err != nil {
		return fmt.Errorf("build handlers: %w", err)
	}

	// Wire preload service into gallery handlers (must be after both
	// SubsystemManager.Start and HandlerManager.Build)
	app.SetPreloadService(app.preloadManager)

	// Create batch load manager and wire to metrics (requires buildHandlers for getRouter)
	if cacheEnabled && app.moduleStateService != nil {
		app.batchLoadManager = cachebatch.NewManager(cachebatch.Config{
			GetQueries: func() (cachebatch.BatchLoadQueries, func()) {
				cpcRo, err := app.dbRoPool.Get()
				if err != nil {
					return nil, nil
				}
				return cpcRo.Queries, func() { app.dbRoPool.Put(cpcRo) }
			},
			GetHandler:         app.getRouter,
			GetETagVersion:     app.GetETagVersion,
			ModuleStateService: app.moduleStateService,
		})
		app.metricsCollector.SetCacheBatchLoad(&cacheBatchLoadAdapter{m: app.batchLoadManager})
	}

	slog.Info("Calling app.Serve()")
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
