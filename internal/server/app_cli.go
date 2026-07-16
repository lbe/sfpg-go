package server

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/cachebatch"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/web"
)

// InitForUnlock performs minimal initialization for the --unlock CLI command.
// Sets root dir and opens database pools; does not load config or start workers.
func (app *App) InitForUnlock() error {
	app.setRootDir(nil)
	var err error
	// Setup database with nil config (will use defaults for pool sizes)
	app.dbPaths, app.dbRwPool, app.dbRoPool, err = app.setupDatabase(app.RuntimeManager.ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to setup database for unlock: %w", err)
	}
	return nil
}

// UnlockAccount unlocks a locked account by clearing failed attempts and removing the lockout.
func (app *App) UnlockAccount(username string) error {
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = cpcRw.Queries.UnlockAccount(app.RuntimeManager.ctx, username)
	if err != nil {
		return fmt.Errorf("failed to unlock account %q: %w", username, err)
	}
	return nil
}

// InitForIncrementETag initializes minimal app state for the --increment-etag command.
// Similar to InitForUnlock, this sets up only what's needed for ETag operations.
func (app *App) InitForIncrementETag(opt getopt.Opt) error {
	app.setRootDir(nil)

	// Setup database with nil config (loads defaults)
	var err error
	app.dbPaths, app.dbRwPool, app.dbRoPool, err = app.setupDatabase(app.RuntimeManager.ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to setup database for increment-etag: %w", err)
	}

	// Initialize config service
	if app.testSeams.ConfigService != nil {
		app.ConfigManager.ConfigService = app.testSeams.ConfigService
	} else {
		app.ConfigManager.ConfigService = config.NewService(app.dbRwPool, app.dbRoPool)
	}

	// Ensure defaults are set (creates config entries if missing)
	if err := app.ConfigManager.ConfigService.EnsureDefaults(app.RuntimeManager.ctx, app.rootDir); err != nil {
		return fmt.Errorf("failed to ensure config defaults: %w", err)
	}

	return nil
}

// IncrementETag loads current ETag, increments it, saves to database, and returns new value.
func (app *App) IncrementETag() (string, error) {
	return app.InfrastructureService.IncrementETag(app.RuntimeManager.ctx, app.ConfigManager.ConfigService)
}

// InitForBatchLoad performs minimal initialization for cache batch load CLI.
// Sets root dir, opens DB pools, loads config, initializes HTTP cache, builds handler
// chain. No server start, no discovery, no worker pool.
func (app *App) InitForBatchLoad(opt getopt.Opt) error {
	app.setRootDir(nil)
	app.setupBootstrapLogging()

	app.setDB()
	app.setConfigDefaults()
	if err := app.loadConfig(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := app.reconfigurePoolsFromConfig(); err != nil {
		return fmt.Errorf("reconfigure pools: %w", err)
	}
	app.ApplyConfig()
	app.initializeHTTPCache()

	routes := []string{"/gallery/", "/lightbox/", "/info/folder/", "/info/image/"}
	if app.cacheMW != nil {
		routes = app.cacheMW.Config().CacheableRoutes
	}
	app.SubsystemManager.preloadManager = cachepreload.NewPreloadManager(routes, false)
	app.SubsystemManager.preloadManager.Configure(cachepreload.PreloadConfig{
		TaskTracker:    &cachepreload.TaskTracker{},
		SessionTracker: &cachepreload.SessionTracker{},
		DBRoPool:       app.dbRoPool,
		GetQueries:     app.GetHandlerQueries,
		GetHandler:     app.getRouter,
		GetETagVersion: app.GetETagVersion,
		Metrics:        &cachepreload.PreloadMetrics{},
	})

	app.SubsystemManager.processingStats = &files.ProcessingStats{}
	app.ensureSession()
	if err := app.buildHandlers(web.FS); err != nil {
		return fmt.Errorf("build handlers: %w", err)
	}

	app.ConfigManager.ConfigMu.RLock()
	cacheEnabled := app.ConfigManager.Config != nil && app.ConfigManager.Config.EnableHTTPCache
	app.ConfigManager.ConfigMu.RUnlock()

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
	}

	return nil
}

// RunCacheBatchLoad runs the batch load and returns the exit code: 0 success, 1 error, 2 blocked.
func (app *App) RunCacheBatchLoad() int {
	ctx := app.getCtx()

	if app.SubsystemManager.batchLoadManager == nil {
		slog.Warn("cache batch load not available (HTTP cache disabled or not initialized)")
		return 1
	}

	err := app.SubsystemManager.batchLoadManager.Run(ctx)
	if errors.Is(err, cachebatch.ErrDiscoveryActive) {
		slog.Warn("cache batch load blocked", "reason", "discovery active")
		return 2
	}
	if err != nil {
		slog.Error("cache batch load failed", "err", err)
		return 1
	}

	m := app.SubsystemManager.batchLoadManager.Metrics().Snapshot()
	slog.Info("cache batch load completed",
		"total", m.TargetsTotal,
		"scheduled", m.TargetsScheduled,
		"completed", m.TargetsCompleted,
		"failed", m.TargetsFailed,
		"skipped", m.TargetsSkipped)
	return 0
}
