package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/log"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/logging"
	"github.com/lbe/sfpg-go/internal/server/security"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/web"
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
// 1. ConfigManager.ConfigMu
// 2. httpServerMu
// Never acquire a lower-ordered lock while holding a higher-ordered one.
type App struct {
	*InfrastructureService
	ConfigManager     *config.ConfigManager
	SessionAuthFacade *SessionAuthFacade
	HandlerManager    *HandlerManager
	RuntimeManager    *RuntimeManager
	SubsystemManager  *SubsystemManager

	logger *log.Logger // Logger manages all logging functionality including rollover and retention
	opt    getopt.Opt

	// testSeams holds optional test doubles for App lifecycle paths.
	// The zero value means use production implementations.
	testSeams AppTestSeams

	// discoveryRunning is true while TriggerDiscovery is walking/enqueueing.
	// Used by cache size calibration quiet checks (independent of module_state DB).
	discoveryRunning atomic.Bool
}

// New creates and initializes a new App instance. It sets up the application
// context, session secret, importer factory, and other core components.
func New(opt getopt.Opt, version string) *App {
	infra := NewInfrastructureService()
	app := &App{
		opt:                   opt,
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade(opt.SessionSecret.String),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
		RuntimeManager:        NewRuntimeManager(context.Background()),
	}
	app.RuntimeManager.version = version
	app.testSeams = defaultNewTestSeams

	// Default ImporterFactory constructs a normal gallerylib.Importer and
	// returns it as the Importer interface.
	app.ImporterFactory = func(conn *sql.Conn, q *gallerydb.CustomQueries) files.Importer {
		return &gallerylib.Importer{Conn: conn, Q: q, OnFolderCreated: app.OnFolderCreated}
	}

	// Initialize templates using the embedded filesystem
	var parseErr error
	if app.testSeams.NewParseTemplates != nil {
		parseErr = app.testSeams.NewParseTemplates(web.FS)
	} else {
		parseErr = ui.ParseTemplates(web.FS)
	}
	if parseErr != nil {
		// We use fmt.Printf because the logger might not be fully initialized yet
		// and this is a fatal startup error.
		fmt.Printf("failed to parse templates: %v\n", parseErr)
		if app.testSeams.NewExit != nil {
			app.testSeams.NewExit(1)
		} else {
			os.Exit(1)
		}
	}

	return app
}

// GetConfig returns the current application configuration.
func (app *App) GetConfig() *config.Config {
	return app.ConfigManager.GetConfig()
}

// SetConfig replaces the current application configuration.
func (app *App) SetConfig(cfg *config.Config) {
	app.ConfigManager.SetConfig(cfg)
}

// SetConfigService sets the ConfigService used by the configuration manager.
func (app *App) SetConfigService(svc config.ConfigService) {
	app.ConfigManager.SetConfigService(svc)
}

// GetETagVersion returns the current ETag version string.
func (app *App) GetETagVersion() string {
	return app.ConfigManager.GetETagVersion()
}

// logLoadedConfigDiagnostics emits startup diagnostics for loaded configuration.
func (app *App) logLoadedConfigDiagnostics(cfg *config.Config) {
	app.ConfigManager.LogLoadedConfigDiagnostics(cfg)
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
	if app.testSeams.Executable != nil {
		exePath, err = app.testSeams.Executable()
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
	if app.testSeams.SetupBootstrapLogging != nil {
		app.logger, err = app.testSeams.SetupBootstrapLogging(app.rootDir, app.SubsystemManager.scheduler, app.RuntimeManager.version)
	} else {
		app.logger, err = logging.SetupBootstrap(app.rootDir, app.SubsystemManager.scheduler, app.RuntimeManager.version)
	}
	if err != nil {
		slog.Error("failed to setup bootstrap logging", "err", err)
		panic("main")
	}
}

// setupDatabase initializes database pools for minimal startup paths.
func (app *App) setupDatabase(ctx context.Context, cfg *config.Config) (database.DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	if app.testSeams.DatabaseSetup != nil {
		return app.testSeams.DatabaseSetup(ctx, app.rootDir, cfg)
	}
	return database.Setup(ctx, app.rootDir, cfg)
}

// reloadLoggingFromConfig delegates to the logging package.
func (app *App) reloadLoggingFromConfig() error {
	// Safely read config values
	app.ConfigManager.ConfigMu.RLock()
	config := app.ConfigManager.Config
	app.ConfigManager.ConfigMu.RUnlock()
	return logging.Reload(app.logger, config, app.SubsystemManager.scheduler)
}

// getCtx returns the runtime context if available, otherwise context.Background().
// During initial startup before the context is set, Background ensures that
// critical operations (cache rotation, state persistence) still complete.
func (app *App) getCtx() context.Context {
	if app.RuntimeManager.ctx != nil {
		return app.RuntimeManager.ctx
	}
	return context.Background()
}

// setConfigDefaults delegates to the config package to apply default configuration values.
func (app *App) setConfigDefaults() {
	config.EnsureDefaults(app.RuntimeManager.ctx, app.rootDir, app.ConfigManager.ConfigService, app.dbRwPool)
}

// loadConfig delegates to the config package.
func (app *App) loadConfig() error {
	var cfg *config.Config
	var err error
	if app.testSeams.LoadConfig != nil {
		cfg, err = app.testSeams.LoadConfig()
	} else {
		cfg, err = config.Load(app.RuntimeManager.ctx, app.rootDir, app.ConfigManager.ConfigService, app.opt)
	}

	app.SetConfig(cfg)
	app.logLoadedConfigDiagnostics(cfg)

	return err
}

// ApplyConfig applies the loaded configuration values to App struct fields.
func (app *App) ApplyConfig() {
	app.ConfigManager.ConfigMu.RLock()
	if app.ConfigManager.Config == nil {
		app.ConfigManager.ConfigMu.RUnlock()
		return
	}

	// Get local copies of config values
	imageDirectory := app.ConfigManager.Config.ImageDirectory
	app.ConfigManager.ConfigMu.RUnlock()

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
	app.ConfigManager.ConfigMu.RLock()
	currentETag := app.ConfigManager.Config.ETagVersion
	app.ConfigManager.ConfigMu.RUnlock()
	oldETag := ui.GetCacheVersion()
	if oldETag != "" && oldETag != currentETag {
		app.InvalidateHTTPCache()
	}
	ui.SetCacheVersion(currentETag)

	// Dynamic enable/disable for cache preload (no restart required)
	if app.SubsystemManager.preloadManager != nil {
		app.ConfigManager.ConfigMu.RLock()
		enablePreload := app.ConfigManager.Config != nil && app.ConfigManager.Config.EnableCachePreload
		app.ConfigManager.ConfigMu.RUnlock()
		app.SubsystemManager.preloadManager.SetEnabled(enablePreload)
	}

	// Hot-reload http_cache_body_codec (no restart required).
	app.ConfigManager.ConfigMu.RLock()
	cfg := app.ConfigManager.Config
	app.ConfigManager.ConfigMu.RUnlock()
	if cfg != nil {
		if err := cachelite.ConfigureBodyCodec(cfg.HTTPCacheBodyCodec); err != nil {
			slog.Error("failed to apply http_cache_body_codec", "codec", cfg.HTTPCacheBodyCodec, "err", err)
		}
	}

	// Hot-reload per-IP login rate limit (no restart required)
	if app.HandlerManager != nil && app.HandlerManager.authHandlers != nil {
		app.ConfigManager.ConfigMu.RLock()
		cfg := app.ConfigManager.Config
		max := security.EffectiveLoginRateLimitPerIP(0)
		if cfg != nil {
			max = security.EffectiveLoginRateLimitPerIP(cfg.LoginRateLimitPerIP)
		}
		app.ConfigManager.ConfigMu.RUnlock()
		app.HandlerManager.authHandlers.SyncLoginRateLimitMax(max)
	}

	// Hot-reload DBOptimizeInterval (no restart required)
	app.ConfigManager.ConfigMu.RLock()
	optimizeInterval := app.ConfigManager.Config.DBOptimizeInterval
	app.ConfigManager.ConfigMu.RUnlock()
	app.setDBOptimizeInterval(optimizeInterval)
}
