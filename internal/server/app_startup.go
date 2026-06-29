package server

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/cachebatch"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/workerpool"
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
	app.applyConfig()
	app.scheduleStaleCacheDrop("run-startup")

	// Initialize HTTP cache middleware after config is loaded
	app.initializeHTTPCache()

	// Initialize cache preload manager (dynamic enable/disable, no restart)
	enablePreload := true
	if app.config != nil {
		enablePreload = app.config.EnableCachePreload
	}
	routes := []string{"/gallery/", "/lightbox/", "/info/folder/", "/info/image/"}
	if app.cacheMW != nil {
		routes = app.cacheMW.Config().CacheableRoutes
	}
	app.preloadManager = cachepreload.NewPreloadManager(routes, enablePreload)
	app.preloadManager.Configure(cachepreload.PreloadConfig{
		TaskTracker:    &cachepreload.TaskTracker{},
		SessionTracker: &cachepreload.SessionTracker{},
		DBRoPool:       app.dbRoPool,
		GetQueries:     app.getHandlerQueries,
		GetHandler:     app.getRouter,
		GetETagVersion: func() string {
			app.configMu.RLock()
			v := ""
			if app.config != nil {
				v = app.config.ETagVersion
			}
			app.configMu.RUnlock()
			if v == "" {
				return config.DefaultConfig().ETagVersion
			}
			return v
		},
		Metrics: &cachepreload.PreloadMetrics{},
	})

	// Initialize FileProcessor after imagesDir is set
	app.fileProcessor = files.NewFileProcessor(app.dbRoPool, app.dbRwPool, app.ImporterFactory, app.imagesDir,
		newBatcherAdapter(app.writeBatcher))

	// Use config value for queue size, with default if config not loaded yet
	queueSize := 10000
	if app.config != nil {
		queueSize = app.config.QueueSize
	}
	app.q = queue.NewQueue[string](queueSize)

	// Run file discovery based on config value (defaults to true)
	runDiscovery := true // default
	if app.config != nil {
		runDiscovery = app.config.RunFileDiscovery
	}
	if runDiscovery {
		go app.walkImageDir()
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

	// Use config values for worker pool, with defaults if config not loaded yet
	maxIdleTime := 10 * time.Second
	if app.config != nil {
		maxIdleTime = app.config.WorkerPoolMaxIdleTime
		// If config specifies worker pool sizes, use them (0 means auto-calculate)
		if app.config.WorkerPoolMax > 0 {
			maxPoolWorkers = app.config.WorkerPoolMax
		}
		if app.config.WorkerPoolMinIdle > 0 {
			minPoolWorkers = app.config.WorkerPoolMinIdle
		}
	}
	app.pool = workerpool.NewPool(app.ctx, maxPoolWorkers, minPoolWorkers, maxIdleTime)

	app.poolDone = make(chan struct{})
	app.processingStats = &files.ProcessingStats{}
	pf := files.NewPoolFuncWithProcessor(app.fileProcessor, app.q, app.normalizedImagesDir, removeImagesDirPrefix, app.processingStats)
	go func() {
		defer close(app.poolDone)
		app.pool.StartWorkerPool(pf, app.dbRoPool, app.dbRwPool, app.q.Len)
	}()

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

	// Initialize and wire up metrics collector for dashboard
	app.metricsCollector = metrics.NewCollector()

	// Wire up metrics sources (use adapter pattern to avoid circular dependencies)
	if app.writeBatcher != nil {
		app.metricsCollector.SetWriteBatcher(&writeBatcherAdapter{wb: app.writeBatcher})
	}
	if app.pool != nil {
		app.metricsCollector.SetWorkerPool(&workerPoolAdapter{pool: app.pool})
	}
	if app.preloadManager != nil {
		app.metricsCollector.SetCachePreload(&cachePreloadAdapter{pm: app.preloadManager})
	}
	// Wire up HTTP cache if it was initialized earlier
	if app.cacheMW != nil {
		app.metricsCollector.SetHTTPCache(&httpCacheAdapter{cache: app.cacheMW})
	}
	// Wire up file processing stats for dashboard
	if app.processingStats != nil {
		app.metricsCollector.SetFileProcessor(&fileProcessorAdapter{stats: app.processingStats})
	}

	// Record initial module activities
	app.metricsCollector.RecordModuleActivity("discovery", runDiscovery)
	app.metricsCollector.RecordModuleActivity("cache_preload", app.config != nil && app.config.EnableCachePreload)

	// Queue info
	app.metricsCollector.SetQueueInfo(func() int { return app.q.Len() }, queueSize)
	app.logStartupConfigSummary(queueSize, runDiscovery)

	app.ensureSessionAndRestart()
	if err := app.buildHandlers(web.FS); err != nil {
		return fmt.Errorf("build handlers: %w", err)
	}

	// Create batch load manager and wire to metrics (requires buildHandlers for getRouter)
	if cacheEnabled && app.moduleStateService != nil {
		app.batchLoadManager = cachebatch.NewManager(cachebatch.Config{
			GetQueries: func() (cachebatch.BatchLoadQueries, func()) {
				cpc, err := app.dbRoPool.Get()
				if err != nil {
					return nil, nil
				}
				return cpc.Queries, func() { app.dbRoPool.Put(cpc) }
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

	return nil
}
