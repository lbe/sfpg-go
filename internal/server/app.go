package server

import (
	"context"
	"database/sql" // Added for template filesystem
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib" // Added for ImporterFactory
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/log"
	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/cachebatch"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/handlers"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/logging"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/web"
)

const (
	// SQLiteDriverName is the name of the SQLite driver to use
	SQLiteDriverName = "sqlite3"
)

// Test hooks for New (nil in production).
var (
	testHookNewParseTemplates func(fs.FS) error
	testHookNewExit           func(code int)
)

// App holds the shared state and resources for the entire application.
// It manages database connections, worker pools, queues, caching, application context,
// and a task scheduler for recurring and one-time tasks.
//
// Lock Ordering: To prevent deadlocks, always acquire locks in this order when holding multiple locks:
// 1. configMu
// 2. httpServerMu
// Never acquire a lower-ordered lock while holding a higher-ordered one.
type App struct {
	*InfrastructureService
	*ConfigManager
	*AuthService
	*HandlerManager
	*RuntimeManager
	*SubsystemManager

	logger *log.Logger // Logger manages all logging functionality including rollover and retention
	opt    getopt.Opt

	// Test seams (nil in production).

	// testHookGetGalleryStatistics delegates getGalleryStatistics to this function
	// instead of querying the database.
	testHookGetGalleryStatistics func(ctx context.Context) (GalleryStats, error)
	// testHookServe replaces App.Serve for tests.
	testHookServe func(handler http.Handler, addr string) error
	// testHookProfilerStart replaces profiler.Start for tests.
	testHookProfilerStart func(cfg profiler.Config) (stop func(), err error)
	// testHookMemoryReclaimer replaces the memoryReclaimer goroutine for tests.
	testHookMemoryReclaimer func(cfg MemoryReclaimerConfig)
	// testHookModuleStateActive replaces moduleStateService.IsActive in StartCacheBatchLoad.
	testHookModuleStateActive func() (bool, error)
	// testHookBatchLoadManagerRun replaces batchLoadManager.Run in StartCacheBatchLoad.
	testHookBatchLoadManagerRun func(ctx context.Context) error
	// testHookGetLastStartedAt replaces moduleStateService.GetLastStartedAt in Run no-discovery branch.
	testHookGetLastStartedAt func(ctx context.Context, module string) (int64, bool, error)
	// testHookAddCommonDataIsActive replaces moduleStateService.IsActive in AddCommonTemplateData.
	testHookAddCommonDataIsActive func(ctx context.Context, name string) (bool, error)
	// testHookAddCommonDataLastStarted replaces moduleStateService.GetLastStartedAt in AddCommonTemplateData.
	testHookAddCommonDataLastStarted func(ctx context.Context, name string) (int64, bool, error)
	// testHookFallbackConfig supplies the config used when loadConfig fails in Run.
	testHookFallbackConfig func() *config.Config
	// testHookConfigService replaces config.NewService(...) in setDB and reconfigurePoolsFromConfig.
	testHookConfigService config.ConfigService
	// testHookLoadConfig replaces config.Load in loadConfig.
	testHookLoadConfig func() (*config.Config, error)
	// testHookExecutable replaces os.Executable() in setRootDir.
	testHookExecutable func() (string, error)
	// testHookSetupBootstrapLogging replaces logging.SetupBootstrap in setupBootstrapLogging.
	testHookSetupBootstrapLogging func(rootDir string, scheduler *scheduler.Scheduler, version string) (*log.Logger, error)
	// testHookDatabaseSetup replaces database.Setup in InitForUnlock/InitForIncrementETag.
	testHookDatabaseSetup func(ctx context.Context, rootDir string, cfg *config.Config) (database.DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error)
}

// New creates and initializes a new App instance. It sets up the application
// context, session secret, importer factory, and other core components.
func New(opt getopt.Opt, version string) *App {
	infra := NewInfrastructureService()
	app := &App{
		opt:                   opt,
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService(opt.SessionSecret.String),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
		RuntimeManager:        NewRuntimeManager(context.Background()),
	}
	app.version = version

	// Default ImporterFactory constructs a normal gallerylib.Importer and
	// returns it as the Importer interface.
	app.ImporterFactory = func(conn *sql.Conn, q *gallerydb.CustomQueries) files.Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}

	// Initialize templates using the embedded filesystem
	var parseErr error
	if testHookNewParseTemplates != nil {
		parseErr = testHookNewParseTemplates(web.FS)
	} else {
		parseErr = ui.ParseTemplates(web.FS)
	}
	if parseErr != nil {
		// We use fmt.Printf because the logger might not be fully initialized yet
		// and this is a fatal startup error.
		fmt.Printf("failed to parse templates: %v\n", parseErr)
		if testHookNewExit != nil {
			testHookNewExit(1)
		} else {
			os.Exit(1)
		}
	}

	return app
}

// parseConfigUITemplates parses all config UI templates from the embedded filesystem.
// Returns a handlers.ConfigTemplates value for direct use in Handlers build.
func parseConfigUITemplates(templateFS fs.FS) (handlers.ConfigTemplates, error) {
	var t handlers.ConfigTemplates
	var err error
	t.SaveRestartAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-save-restart-alert.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-save-restart-alert template: %w", err)
	}
	t.SaveSuccessAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-save-success-alert.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-save-success-alert template: %w", err)
	}
	t.ExportModal, err = template.ParseFS(templateFS, "templates/config-ui/config-export-modal.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-export-modal template: %w", err)
	}
	t.ImportModal, err = template.ParseFS(templateFS, "templates/config-ui/config-import-modal.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-import-modal template: %w", err)
	}
	t.RestoreModal, err = template.ParseFS(templateFS, "templates/config-ui/config-restore-modal.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-restore-modal template: %w", err)
	}
	t.RestoreSuccessAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-restore-success-alert.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-restore-success-alert template: %w", err)
	}
	t.ImportSuccessAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-import-success-alert.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-import-success-alert template: %w", err)
	}
	t.RestartInitiatedAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-restart-initiated-alert.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-restart-initiated-alert template: %w", err)
	}
	return t, nil
}

// setRootDir determines and sets the application's root directory. If a directory
// is provided via the 'd' parameter, it is used; otherwise, the function
// defaults to the directory where the executable is located.
func (app *App) setRootDir(d *string) {
	if d != nil {
		app.rootDir = *d
		return
	}

	// Get the directory where the executable is located
	var exePath string
	var err error
	if app.testHookExecutable != nil {
		exePath, err = app.testHookExecutable()
	} else {
		exePath, err = os.Executable()
	}
	if err != nil {
		slog.Error("failed to get executable path", "err", err)
		panic("main")
	}
	app.rootDir = filepath.Dir(exePath)
}

// setupBootstrapLogging delegates to the logging package.
func (app *App) setupBootstrapLogging() {
	var err error
	if app.testHookSetupBootstrapLogging != nil {
		app.logger, err = app.testHookSetupBootstrapLogging(app.rootDir, app.scheduler, app.version)
	} else {
		app.logger, err = logging.SetupBootstrap(app.rootDir, app.scheduler, app.version)
	}
	if err != nil {
		slog.Error("failed to setup bootstrap logging", "err", err)
		panic("main")
	}
}

// setupDatabase initializes database pools for minimal startup paths.
func (app *App) setupDatabase(ctx context.Context, cfg *config.Config) (database.DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	if app.testHookDatabaseSetup != nil {
		return app.testHookDatabaseSetup(ctx, app.rootDir, cfg)
	}
	return database.Setup(ctx, app.rootDir, cfg)
}

// reloadLoggingFromConfig delegates to the logging package.
func (app *App) reloadLoggingFromConfig() error {
	// Safely read config values
	app.configMu.RLock()
	config := app.config
	app.configMu.RUnlock()
	return logging.Reload(app.logger, config, app.scheduler)
}

// getCtx returns app.ctx if available, otherwise context.Background().
// During initial startup before app.ctx is set, Background ensures that
// critical operations (cache rotation, state persistence) still complete.
func (app *App) getCtx() context.Context {
	if app.ctx != nil {
		return app.ctx
	}
	return context.Background()
}

// buildWriteBatcher creates a WriteBatcher configured with the shared callbacks.
// setDB initializes and configures the database using the database package.
// walCheckpointAfterCommit is called by writebatcher after each successful commit
// and by the maintenance timer (every 5 minutes).
// It checks WAL file size and checkpoints if > 2GB or if 5 minutes have elapsed.
// It also runs PRAGMA optimize every 1 hour.
// This runs in the writebatcher's worker goroutine, ensuring no active transactions.
// setConfigDefaults delegates to the config package.
func (app *App) setConfigDefaults() {
	config.EnsureDefaults(app.ctx, app.rootDir, app.configService, app.dbRwPool)
}

// loadConfig delegates to the config package.
func (app *App) loadConfig() error {
	var cfg *config.Config
	var err error
	if app.testHookLoadConfig != nil {
		cfg, err = app.testHookLoadConfig()
	} else {
		cfg, err = config.Load(app.ctx, app.rootDir, app.configService, app.opt)
	}

	app.SetConfig(cfg)
	app.logLoadedConfigDiagnostics(cfg)

	return err
}

// logLoadedConfigDiagnostics emits startup diagnostics for loaded configuration.
// It keeps normal startup logs low-noise while surfacing anomalies immediately.

// logDBPoolConfiguredVsEffective logs configured pool settings along with the
// effective values currently applied by the active read-write/read-only pools.

// logStartupConfigSummary emits one low-noise startup summary of configured
// versus effective values for critical subsystems.
func (app *App) logStartupConfigSummary(queueSize int, runDiscovery bool) {
	app.configMu.RLock()
	cfg := app.config
	app.configMu.RUnlock()
	if cfg == nil {
		return
	}

	effectivePreload := false
	if app.preloadManager != nil {
		effectivePreload = app.preloadManager.IsEnabled()
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
	if app.pool != nil {
		effectiveWorkerMax = app.pool.MaxWorkers
		effectiveWorkerMinIdle = app.pool.MinWorkers
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

// reconfigurePoolsFromConfig recreates database pools with the loaded configuration.
// This must be called AFTER loadConfig() to ensure the newly loaded config values
// (from database, YAML, or CLI/env) are applied to the connection pools.
// This enforces the precedence: Defaults -> DB -> Env -> CLI.
// applyConfig applies configuration values to App struct fields.
func (app *App) ApplyConfig() {
	app.configMu.RLock()
	if app.config == nil {
		app.configMu.RUnlock()
		return
	}

	// Get local copies of config values
	imageDirectory := app.config.ImageDirectory
	app.configMu.RUnlock()

	// Apply image directory (must be defined)
	if imageDirectory == "" {
		panic("image directory is undefined")
	}

	imagesDir, normalized, err := config.ApplyImageDirectory(imageDirectory)
	app.imagesDir = imagesDir
	app.normalizedImagesDir = normalized
	if err != nil {
		slog.Error("image directory validation failed", "path", app.imagesDir, "err", err)
		// Continue - error is logged but don't fail the config application
	}

	// Reload logging
	if app.logger != nil {
		if err := app.reloadLoggingFromConfig(); err != nil {
			slog.Error("failed to apply logging configuration", "err", err)
		}
	}

	// Sync UI cache version with config. Invalidate HTTP cache only when ETag
	// has changed from a previous in-memory value (not on first load/reboot).
	// This avoids serving stale responses (old ?v= URLs) when ETag changes while
	// preserving cache across restarts when ETag is unchanged.
	app.configMu.RLock()
	currentETag := app.config.ETagVersion
	app.configMu.RUnlock()
	oldETag := ui.GetCacheVersion()
	if oldETag != "" && oldETag != currentETag {
		app.InvalidateHTTPCache()
	}
	ui.SetCacheVersion(currentETag)

	// Dynamic enable/disable for cache preload (no restart required)
	if app.preloadManager != nil {
		app.configMu.RLock()
		enablePreload := app.config != nil && app.config.EnableCachePreload
		app.configMu.RUnlock()
		app.preloadManager.SetEnabled(enablePreload)
	}
}

// StartCacheBatchLoad attempts to start cache batch load. Returns blocked=true when
// discovery is active (caller should return 409). Starts the run in a goroutine on success.
func (app *App) StartCacheBatchLoad() (interfaces.StartCacheBatchLoadResult, error) {
	ctx := app.getCtx()

	var active bool
	var err error
	if app.testHookModuleStateActive != nil {
		active, err = app.testHookModuleStateActive()
	} else if app.moduleStateService != nil {
		active, err = app.moduleStateService.IsActive(ctx, "discovery")
	}
	if err != nil {
		return interfaces.StartCacheBatchLoadResult{}, err
	}
	if active {
		return interfaces.StartCacheBatchLoadResult{
			Blocked: true,
			Message: "Cache batch load blocked: discovery active",
		}, nil
	}

	if app.batchLoadManager == nil && app.testHookBatchLoadManagerRun == nil {
		return interfaces.StartCacheBatchLoadResult{
			Blocked: false,
			Message: "Cache batch load not available",
		}, nil
	}

	go func() {
		var err error
		if app.testHookBatchLoadManagerRun != nil {
			err = app.testHookBatchLoadManagerRun(ctx)
		} else {
			err = app.batchLoadManager.Run(ctx)
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("cache batch load run failed", "err", err)
		}
	}()

	return interfaces.StartCacheBatchLoadResult{
		Blocked: false,
		Message: "Cache batch load started",
	}, nil
}

// Shutdown gracefully shuts down the application. It drains the WriteBatcher
// first, then cancels the main context, waits for background goroutines and
// the worker pool to finish, closes database connections, and closes the log
// file.
// Shutdown gracefully stops all background goroutines, closes the write
// batcher, database pools, and logger. It is safe to call multiple times.
func (app *App) Shutdown() {
	app.shutdownOnce.Do(func() {
		app.InfrastructureService.Shutdown()

		if app.cancel != nil {
			app.cancel()
		}
		if app.poolDone != nil {
			<-app.poolDone
		}
		app.wg.Wait()

		app.SubsystemManager.Shutdown()
		if app.scheduler != nil {
			if err := app.scheduler.Shutdown(); err != nil {
				slog.Error("error shutting down scheduler", "err", err)
			}
		}

		if app.dbRoPool != nil {
			if err := app.dbRoPool.Close(); err != nil {
				slog.Error("error closing read-only pool", "err", err)
			}
		}
		if app.dbRwPool != nil {
			if err := app.dbRwPool.Close(); err != nil {
				slog.Error("error closing read-write pool", "err", err)
			}
		}
		if app.logger != nil {
			if err := app.logger.Shutdown(); err != nil {
				slog.Error("error shutting down logger", "err", err)
			}
		}
	})
}

// LogProfileLocation logs the profile directory and stops the profiler if active.
// This should be called before shutdown to ensure profile location is logged to both console and file.
func (app *App) LogProfileLocation() {
	if app.stopProfiler != nil {
		app.stopProfiler()
		// Log after stopping to ensure profile is flushed
		if dir := profiler.Dir(); dir != "" {
			slog.Info("Profile artifacts written", "dir", dir)
		}
	}
}

// refreshGalleryStatsCache runs getGalleryStatistics, updates the in-memory cache,
// and returns the stats. Uses discoveryLastStartedAt as the cache key.
func (app *App) refreshGalleryStatsCache(ctx context.Context, discoveryLastStartedAt int64) (GalleryStats, error) {
	stats, err := app.getGalleryStatistics(ctx)
	if err != nil {
		return GalleryStats{}, err
	}
	app.SetGalleryStatsCache(&stats, discoveryLastStartedAt)
	return stats, nil
}

// getGalleryStatsCached returns cached stats if valid for current discoveryLastStartedAt.
// If invalid or stale, returns nil and caller should call refreshGalleryStatsCache.
func (app *App) getGalleryStatsCached(discoveryLastStartedAt int64) *GalleryStats {
	return app.GetGalleryStatsCached(discoveryLastStartedAt)
}

// MemoryReclaimerConfig holds the configuration for the memory reclaimer.
type MemoryReclaimerConfig struct {
	InitialDelay  time.Duration
	CheckInterval time.Duration
	IdleThreshold time.Duration
	FreeMemFunc   func()
}

// memoryReclaimer is a background goroutine that periodically checks if the application is idle
// and, if so, triggers a garbage collection and releases unused memory back to the OS.
func (app *App) memoryReclaimer(cfg MemoryReclaimerConfig) {
	app.wg.Add(1)
	defer app.wg.Done()

	// Start checking after an initial delay.
	initialDelay := time.NewTimer(cfg.InitialDelay)
	ctx := app.getCtx()
	select {
	case <-initialDelay.C:
	case <-ctx.Done():
		initialDelay.Stop()
		return
	}

	ticker := time.NewTicker(cfg.CheckInterval)
	defer ticker.Stop()

	for {
		ctx := app.getCtx()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			isQueueEmpty := app.q.IsEmpty()
			timeSinceLastCompletion := app.pool.TimeSinceLastCompletion()

			if isQueueEmpty && timeSinceLastCompletion > cfg.IdleThreshold {
				runtime.GC()      // 1. Trigger a GC
				cfg.FreeMemFunc() // 2. Release unused memory to the OS
			}
		}
	}
}

// Run orchestrates the application startup sequence. It initializes the root
// directory, logging, database, configuration, and command-line parsing.
// It then starts the background worker pool and file discovery process before
// This is a minimal initialization that does not require config to be loaded.
func (app *App) InitForUnlock() error {
	app.setRootDir(nil)
	var err error
	// Setup database with nil config (will use defaults for pool sizes)
	app.dbPaths, app.dbRwPool, app.dbRoPool, err = app.setupDatabase(app.ctx, nil)
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

	err = cpcRw.Queries.UnlockAccount(app.ctx, username)
	if err != nil {
		return fmt.Errorf("failed to unlock account %q: %w", username, err)
	}
	return nil
}

// InitForIncrementETag initializes minimal app state for --increment-etag command.
// Similar to InitForUnlock, this sets up only what's needed for ETag operations.
func (app *App) InitForIncrementETag(opt getopt.Opt) error {
	app.setRootDir(nil)

	// Setup database with nil config (loads defaults)
	var err error
	app.dbPaths, app.dbRwPool, app.dbRoPool, err = app.setupDatabase(app.ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to setup database for increment-etag: %w", err)
	}

	// Initialize config service
	if app.testHookConfigService != nil {
		app.configService = app.testHookConfigService
	} else {
		app.configService = config.NewService(app.dbRwPool, app.dbRoPool)
	}

	// Ensure defaults are set (creates config entries if missing)
	if err := app.configService.EnsureDefaults(app.ctx, app.rootDir); err != nil {
		return fmt.Errorf("failed to ensure config defaults: %w", err)
	}

	return nil
}

// IncrementETag loads current ETag, increments it, saves to database, and returns new value.
func (app *App) IncrementETag() (string, error) {
	return app.InfrastructureService.IncrementETag(app.ctx, app.configService)
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
	app.preloadManager = cachepreload.NewPreloadManager(routes, false)
	app.preloadManager.Configure(cachepreload.PreloadConfig{
		TaskTracker:    &cachepreload.TaskTracker{},
		SessionTracker: &cachepreload.SessionTracker{},
		DBRoPool:       app.dbRoPool,
		GetQueries:     app.GetHandlerQueries,
		GetHandler:     app.getRouter,
		GetETagVersion: app.GetETagVersion,
		Metrics:        &cachepreload.PreloadMetrics{},
	})

	app.processingStats = &files.ProcessingStats{}
	app.ensureSession()
	if err := app.buildHandlers(web.FS); err != nil {
		return fmt.Errorf("build handlers: %w", err)
	}

	app.configMu.RLock()
	cacheEnabled := app.config != nil && app.config.EnableHTTPCache
	app.configMu.RUnlock()

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
	}

	return nil
}

// RunCacheBatchLoad runs the batch load and returns the exit code: 0 success, 1 error, 2 blocked.
func (app *App) RunCacheBatchLoad() int {
	ctx := app.getCtx()

	if app.batchLoadManager == nil {
		slog.Warn("cache batch load not available (HTTP cache disabled or not initialized)")
		return 1
	}

	err := app.batchLoadManager.Run(ctx)
	if errors.Is(err, cachebatch.ErrDiscoveryActive) {
		slog.Warn("cache batch load blocked", "reason", "discovery active")
		return 2
	}
	if err != nil {
		slog.Error("cache batch load failed", "err", err)
		return 1
	}

	m := app.batchLoadManager.Metrics().Snapshot()
	slog.Info("cache batch load completed",
		"total", m.TargetsTotal,
		"scheduled", m.TargetsScheduled,
		"completed", m.TargetsCompleted,
		"failed", m.TargetsFailed,
		"skipped", m.TargetsSkipped)
	return 0
}
