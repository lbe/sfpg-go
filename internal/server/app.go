package server

import (
	"context"
	"database/sql" // Added for template filesystem
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath" // Added for setRootDir, setDBDirectory, and setImageDirectory logic
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/sessions"

	"go.local/sfpg/internal/cachelite"
	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/gallerylib" // Added for ImporterFactory
	"go.local/sfpg/internal/getopt"
	"go.local/sfpg/internal/log"
	"go.local/sfpg/internal/profiler"
	"go.local/sfpg/internal/queue"
	"go.local/sfpg/internal/scheduler"
	"go.local/sfpg/internal/server/auth"
	"go.local/sfpg/internal/server/cachepreload"
	"go.local/sfpg/internal/server/config"
	"go.local/sfpg/internal/server/database"
	"go.local/sfpg/internal/server/files"
	"go.local/sfpg/internal/server/handlers"
	"go.local/sfpg/internal/server/interfaces"
	"go.local/sfpg/internal/server/logging"
	"go.local/sfpg/internal/server/metrics"
	"go.local/sfpg/internal/server/session"
	"go.local/sfpg/internal/server/ui"
	"go.local/sfpg/internal/workerpool"
	"go.local/sfpg/internal/writebatcher"
	"go.local/sfpg/web"
)

const (
	// SQLiteDriverName is the name of the SQLite driver to use
	SQLiteDriverName = "sqlite3"
)

// App holds the shared state and resources for the entire application.
// It manages database connections, worker pools, queues, caching, application context,
// and a task scheduler for recurring and one-time tasks.
//
// Lock Ordering: To prevent deadlocks, always acquire locks in this order when holding multiple locks:
// 1. ctxMu
// 2. configMu
// 3. restartMu
// Never acquire a lower-ordered lock while holding a higher-ordered one.
type App struct {
	cancel         context.CancelFunc
	cacheStore     cachelite.CacheStore // CacheStore for cache operations
	cacheSizeBytes atomic.Int64         // atomic cache size in bytes (avoids DB query)
	cacheMW        *cachelite.HTTPCacheMiddleware
	ctx            context.Context
	ctxMu          sync.RWMutex // protects ctx from concurrent access (ORDER: 1)
	dbDir          string
	dbPath         string
	dbRoPool       *dbconnpool.DbSQLConnPool
	dbRwPool       *dbconnpool.DbSQLConnPool
	// hqOverride allows tests to inject query behavior for handlers
	hqOverride interfaces.HandlerQueries
	imagesDir  string
	// ImporterFactory allows tests or callers to override how Importer instances are constructed.
	ImporterFactory     func(conn *sql.Conn, q *gallerydb.CustomQueries) files.Importer
	logger              *log.Logger // Logger manages all logging functionality including rollover and retention
	normalizedImagesDir string      // Cached filepath.ToSlash(imagesDir) to avoid repeated allocations
	opt                 getopt.Opt
	pool                *workerpool.Pool
	q                   *queue.Queue[string]
	qSendersActive      atomic.Int64
	rootDir             string
	sessionSecret       string
	stopProfiler        func()
	store               *sessions.CookieStore // session cookie store used for managing user authentication sessions
	sessionManager      *session.Manager      // session manager encapsulating session operations
	wg                  sync.WaitGroup
	config              *config.Config         // Application configuration
	configMu            sync.RWMutex           // protects config from concurrent access (ORDER: 2)
	configService       config.ConfigService   // ConfigService for config operations
	authService         auth.AuthService       // AuthService for authentication operations
	fileProcessor       files.FileProcessor    // FileProcessor for file processing operations
	processingStats     *files.ProcessingStats // File processing statistics for dashboard
	restartRequired     bool                   // Flag indicating if restart is needed
	restartMu           sync.RWMutex           // protects httpServer and restartCh from concurrent access (ORDER: 3)
	httpServer          *http.Server           // HTTP server instance for restart support
	restartCh           chan struct{}          // Channel to signal server restart
	poolDone            chan struct{}          // closed when StartWorkerPool goroutine exits; nil if pool never started
	scheduler           *scheduler.Scheduler   // Application-level task scheduler
	authHandlers        *handlers.AuthHandlers
	configHandlers      *handlers.ConfigHandlers
	galleryHandlers     *handlers.GalleryHandlers
	healthHandlers      *handlers.HealthHandlers
	dashboardHandlers   *handlers.DashboardHandlers
	serverHandlers      *handlers.ServerHandlers
	themeHandlers       *handlers.ThemeHandlers
	preloadManager      *cachepreload.PreloadManager
	writeBatcher        *writebatcher.WriteBatcher[BatchedWrite]
	metricsCollector    *metrics.Collector // Centralized metrics collector for dashboard
	version             string             // Application version for display in UI and logs
}

// New creates and initializes a new App instance. It sets up the application
// context, session secret, importer factory, and other core components.
//
//nolint:captLocal // Version matches exported const from gen_version.sh
func New(opt getopt.Opt, Version string) *App {
	app := &App{ // Use &App{} because we need app pointer to the struct
		opt:           opt,
		sessionSecret: opt.SessionSecret.String, // SessionSecret is required, validated in Parse()
		version:       Version,                  // Set the application version
	}
	// Initialize context - WithCancel allows graceful shutdown
	app.ctxMu.Lock()
	app.ctx, app.cancel = context.WithCancel(context.Background())
	app.ctxMu.Unlock()

	// Default ImporterFactory constructs a normal gallerylib.Importer and
	// returns it as the Importer interface.
	app.ImporterFactory = func(conn *sql.Conn, q *gallerydb.CustomQueries) files.Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}

	// Initialize templates using the embedded filesystem
	if err := ui.ParseTemplates(web.FS); err != nil {
		// We use fmt.Printf because the logger might not be fully initialized yet
		// and this is a fatal startup error.
		fmt.Printf("failed to parse templates: %v\n", err)
		os.Exit(1)
	}

	return app
}

// configUITemplates holds parsed config UI templates. Used when building Handlers;
// not stored on App per APP_STRUCT_DESIGN §2.5.
type configUITemplates struct {
	SaveRestartAlert      *template.Template
	SaveSuccessAlert      *template.Template
	ExportModal           *template.Template
	ImportModal           *template.Template
	ImportSuccessAlert    *template.Template
	RestoreModal          *template.Template
	RestoreSuccessAlert   *template.Template
	RestartInitiatedAlert *template.Template
}

// parseConfigUITemplates parses all config UI templates from the embedded filesystem.
// Returns a struct of templates for Handlers build; does not store on App.
func parseConfigUITemplates(templateFS fs.FS) (*configUITemplates, error) {
	var err error
	t := &configUITemplates{}
	t.SaveRestartAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-save-restart-alert.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse config-save-restart-alert template: %w", err)
	}
	t.SaveSuccessAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-save-success-alert.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse config-save-success-alert template: %w", err)
	}
	t.ExportModal, err = template.ParseFS(templateFS, "templates/config-ui/config-export-modal.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse config-export-modal template: %w", err)
	}
	t.ImportModal, err = template.ParseFS(templateFS, "templates/config-ui/config-import-modal.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse config-import-modal template: %w", err)
	}
	t.RestoreModal, err = template.ParseFS(templateFS, "templates/config-ui/config-restore-modal.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse config-restore-modal template: %w", err)
	}
	t.RestoreSuccessAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-restore-success-alert.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse config-restore-success-alert template: %w", err)
	}
	t.ImportSuccessAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-import-success-alert.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse config-import-success-alert template: %w", err)
	}
	t.RestartInitiatedAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-restart-initiated-alert.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse config-restart-initiated-alert template: %w", err)
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
	exePath, err := os.Executable()
	if err != nil {
		slog.Error("failed to get executable path", "err", err)
		panic("main")
	}
	app.rootDir = filepath.Dir(exePath)
}

// setupBootstrapLogging delegates to the logging package.
func (app *App) setupBootstrapLogging() {
	var err error
	app.logger, err = logging.SetupBootstrap(app.rootDir, app.scheduler, app.version)
	if err != nil {
		slog.Error("failed to setup bootstrap logging", "err", err)
		panic("main")
	}
}

// reloadLoggingFromConfig delegates to the logging package.
func (app *App) reloadLoggingFromConfig() error {
	// Safely read config values
	app.configMu.RLock()
	config := app.config
	app.configMu.RUnlock()
	return logging.Reload(app.logger, config, app.scheduler)
}

// initializeHTTPCache initializes the HTTP cache middleware if enabled in config.
// This must be called after loadConfig() so that app.config.EnableHTTPCache is available.
func (app *App) initializeHTTPCache() {
	// Check if cache is enabled in config (follows precedence: Default->DB->Env->CLI)
	app.configMu.RLock()
	if app.config == nil || !app.config.EnableHTTPCache {
		app.configMu.RUnlock()
		return
	}

	// Get cache settings from config
	maxEntrySize := app.config.CacheMaxEntrySize
	maxTotalSize := app.config.CacheMaxSize
	defaultTTL := app.config.CacheMaxTime
	app.configMu.RUnlock()

	cfg := cachelite.CacheConfig{
		Enabled:      true,
		MaxEntrySize: maxEntrySize,
		MaxTotalSize: maxTotalSize,
		DefaultTTL:   defaultTTL,
		CacheableRoutes: []string{
			"/gallery/",
			"/lightbox/",
			"/info/folder/",
			"/info/image/",
		},
		// OnGalleryCacheHit: callback to schedule preload when gallery is served from cache.
		// Uses closure to check app.preloadManager at runtime (it's created after initializeHTTPCache).
		OnGalleryCacheHit: func(ctx context.Context, folderID int64, sessionID string, acceptEncoding string) {
			if app.preloadManager != nil {
				app.preloadManager.ScheduleFolderPreload(ctx, folderID, sessionID, acceptEncoding)
			}
		},
		SessionCookieName:     session.SessionName,
		SkipPreloadWhenHeader: cachepreload.InternalPreloadHeader,
		SkipPreloadWhenValue:  "true",
	}

	// Use unified batcher for cache writes if available
	var submitFunc func(*cachelite.HTTPCacheEntry)
	if app.writeBatcher != nil {
		submitFunc = app.submitCacheWrite
	}
	app.cacheMW = cachelite.NewHTTPCacheMiddleware(
		app.dbRwPool,
		cfg,
		&app.cacheSizeBytes,
		submitFunc,
	)
	// Note: HTTP cache is wired to metrics collector after the collector is created in Run()
}

// invalidateHTTPCache clears all HTTP cache entries. Called when ETag version changes
// to avoid serving stale responses that may contain old cache-busting URLs.
func (app *App) invalidateHTTPCache() {
	if app.dbRwPool == nil {
		return
	}
	app.ctxMu.RLock()
	ctx := app.ctx
	app.ctxMu.RUnlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := cachelite.ClearCache(ctx, app.dbRwPool); err != nil {
		slog.Error("failed to invalidate HTTP cache", "err", err)
	}
}

// setDB initializes and configures the database using the database package.
func (app *App) setDB() {
	var err error
	app.dbPath, app.dbRwPool, app.dbRoPool, err = database.Setup(app.ctx, app.rootDir, app.config)
	if err != nil {
		slog.Error("failed to setup database", "err", err)
		panic("main")
	}

	// Initialize unified WriteBatcher for all high-volume writes
	// Runs in parallel with old batchers during migration
	app.writeBatcher, err = writebatcher.New[BatchedWrite](app.ctx, writebatcher.Config[BatchedWrite]{
		MaxBatchSize:  100,             // Increased for better throughput during preloading
		MaxBatchBytes: 8 * 1024 * 1024, // 8MB ceiling
		FlushInterval: 200 * time.Millisecond,
		ChannelSize:   4096, // Larger buffer for high throughput
		SizeFunc: func(bw BatchedWrite) int64 {
			return bw.Size()
		},
		BeginTx: func(ctx context.Context) (*sql.Tx, error) {
			return app.dbRwPool.DB().BeginTx(ctx, nil)
		},
		Flush: app.flushBatchedWrites,
		OnSuccess: func(batch []BatchedWrite) {
			// Handle cache eviction BEFORE cleanup (needs CacheEntry data)
			app.maybeEvictCacheEntries(batch)
			// Then cleanup resources
			cleanupBatchedWriteResources(batch)
		},
		OnError: func(err error, batch []BatchedWrite) {
			// Count by type for debugging
			var filesCount, invalidFilesCount, cacheEntriesCount int
			for _, bw := range batch {
				switch {
				case bw.File != nil:
					filesCount++
				case bw.InvalidFile != nil:
					invalidFilesCount++
				case bw.CacheEntry != nil:
					cacheEntriesCount++
				}
			}
			slog.Error("failed to flush unified batch",
				"err", err,
				"files", filesCount,
				"invalid_files", invalidFilesCount,
				"cache_entries", cacheEntriesCount)

			// Cleanup pooled resources even on error
			cleanupBatchedWriteResources(batch)
		},
	})
	if err != nil {
		slog.Error("failed to create unified WriteBatcher", "err", err)
		panic("failed to create unified WriteBatcher")
	}
	slog.Info("unified WriteBatcher initialized",
		"max_batch_size", 100,
		"max_batch_bytes", 8*1024*1024,
		"channel_size", 4096)

	// Initialize CacheStore using the RW pool
	app.cacheStore = cachelite.NewSQLiteCacheStore(app.dbRwPool)

	// Initialize atomic cache size counter
	if size, err := app.cacheStore.SizeBytes(app.ctx); err == nil {
		app.cacheSizeBytes.Store(size)
		slog.Debug("Initialized cache size counter", "bytes", size)
	} else {
		slog.Warn("Failed to initialize cache size counter", "err", err)
	}

	app.schedulePeriodicOptimization()

	// Initialize ConfigService after database pools are created
	app.configService = config.NewService(app.dbRwPool, app.dbRoPool)

	// Initialize AuthService
	app.authService = auth.NewService(&loginStoreAdapter{app: app})

	// Keep rebuild logic
	if app.authHandlers != nil {
		app.ensureSessionAndRestart()
		if err := app.buildHandlers(web.FS); err != nil {
			slog.Error("failed to rebuild handlers after setDB", "err", err)
			panic(fmt.Sprintf("rebuild handlers after setDB: %v", err))
		}
	}
}

// schedulePeriodicOptimization delegates to the database package.
func (app *App) schedulePeriodicOptimization() {
	database.ScheduleOptimization(app.ctx, app.dbRwPool, &app.wg)
}

// setConfigDefaults delegates to the config package.
func (app *App) setConfigDefaults() {
	config.EnsureDefaults(app.ctx, app.rootDir, app.configService, app.dbRwPool)
}

// loadConfig delegates to the config package.
func (app *App) loadConfig() error {
	cfg, err := config.Load(app.ctx, app.rootDir, app.configService, app.opt)

	// Write lock to update app.config atomically
	app.configMu.Lock()
	app.config = cfg
	app.configMu.Unlock()
	return err
}

// applyConfig applies configuration values to App struct fields.
func (app *App) applyConfig() {
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
		app.invalidateHTTPCache()
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

// Shutdown gracefully shuts down the application. It cancels the main context,
// waits for background goroutines and the worker pool to finish, closes
// database connections, and closes the log file.
func (app *App) Shutdown() {
	// Signal everything to stop
	if app.cancel != nil {
		app.cancel()
	}

	// Wait for worker pool goroutine to exit (StartWorkerPool blocks on errgroup.Wait;
	// we avoid calling pool.G.Wait() here to prevent race with Go() during startup).
	if app.poolDone != nil {
		<-app.poolDone
	}

	// Wait for all background tasks except the worker pool
	app.wg.Wait()

	// Shutdown preload manager (stops cache preload scheduler)
	if app.preloadManager != nil {
		app.preloadManager.Shutdown()
	}

	// Shutdown scheduler
	if app.scheduler != nil {
		if err := app.scheduler.Shutdown(); err != nil {
			slog.Error("error shutting down scheduler", "err", err)
		}
	}

	// Close file processor (flushes invalid file batches)
	if app.fileProcessor != nil {
		if err := app.fileProcessor.Close(); err != nil {
			slog.Error("error closing file processor", "err", err)
		}
	}

	// Close database pools in sequence
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

	// Shutdown logger (closes log file)
	if app.logger != nil {
		if err := app.logger.Shutdown(); err != nil {
			slog.Error("failed to shutdown logger", "err", err)
		}
	}
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
	app.ctxMu.RLock()
	ctx := app.ctx
	app.ctxMu.RUnlock()
	select {
	case <-initialDelay.C:
	case <-ctx.Done():
		initialDelay.Stop()
		return
	}

	ticker := time.NewTicker(cfg.CheckInterval)
	defer ticker.Stop()

	for {
		app.ctxMu.RLock()
		ctx = app.ctx
		app.ctxMu.RUnlock()
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
// starting the HTTP server.
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
		}
	}

	// Apply config to app fields
	app.applyConfig()

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
				app.ctxMu.RLock()
				ctx := app.ctx
				app.ctxMu.RUnlock()
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					app.ctxMu.RLock()
					ctx = app.ctx
					app.ctxMu.RUnlock()
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

	app.ensureSessionAndRestart()
	if err := app.buildHandlers(web.FS); err != nil {
		return fmt.Errorf("build handlers: %w", err)
	}

	slog.Info("Calling app.Serve()")
	if err := app.Serve(); err != nil {
		slog.Error("server error", "err", err)
		time.Sleep(1 * time.Second) // Give logger time to write
		panic("main")
	}

	return nil
}

// InitForUnlock performs minimal initialization needed for unlock operations.
// It sets up root directory and database using the database package.
// This is a minimal initialization that does not require config to be loaded.
func (app *App) InitForUnlock() error {
	app.setRootDir(nil)
	var err error
	// Setup database with nil config (will use defaults for pool sizes)
	app.dbPath, app.dbRwPool, app.dbRoPool, err = database.Setup(app.ctx, app.rootDir, nil)
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
	app.dbPath, app.dbRwPool, app.dbRoPool, err = database.Setup(app.ctx, app.rootDir, nil)
	if err != nil {
		return fmt.Errorf("failed to setup database for increment-etag: %w", err)
	}

	// Initialize config service
	app.configService = config.NewService(app.dbRwPool, app.dbRoPool)

	// Ensure defaults are set (creates config entries if missing)
	if err := app.configService.EnsureDefaults(app.ctx, app.rootDir); err != nil {
		return fmt.Errorf("failed to ensure config defaults: %w", err)
	}

	return nil
}

// IncrementETag loads current ETag, increments it, saves to database, and returns new value.
func (app *App) IncrementETag() (string, error) {
	cfg, err := app.configService.Load(app.ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}

	newETag := config.IncrementETagVersion(cfg.ETagVersion)
	cfg.ETagVersion = newETag

	if err := app.configService.Save(app.ctx, cfg); err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}

	return newETag, nil
}
