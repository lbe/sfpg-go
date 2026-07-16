// Package server provides the core HTTP server, routing, middleware, and
// handlers for the web application. It integrates all the sub-packages
// like database, caching, and background workers to serve the photo gallery.
package server

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/humanize"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/middleware"
	"github.com/lbe/sfpg-go/internal/server/pathutil"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/template"
	"github.com/lbe/sfpg-go/web"
)

// Compile-time checks: *App satisfies all handler dependency interfaces.
var _ interfaces.ServerDeps = (*App)(nil)
var _ interfaces.GalleryOps = (*App)(nil)
var _ interfaces.ServerControl = (*App)(nil)

// UpdateUsername updates the admin username in the config table.
// Uses gallerydb-generated queries (no inline SQL).
func (app *App) UpdateUsername(ctx context.Context, username string) error {
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		return err
	}
	defer app.dbRwPool.Put(cpcRw)
	now := time.Now().Unix()
	return cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key: "user", Value: username, CreatedAt: now, UpdatedAt: now,
	})
}

// UpdatePassword updates the admin password hash in the config table.
// Uses gallerydb-generated queries (no inline SQL).
func (app *App) UpdatePassword(ctx context.Context, passwordHash string) error {
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		return err
	}
	defer app.dbRwPool.Put(cpcRw)
	now := time.Now().Unix()
	return cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key: "password", Value: passwordHash, CreatedAt: now, UpdatedAt: now,
	})
}

// SetPreloadEnabled enables or disables the cache preload manager.
// Safe to call even if preloadManager is nil (logs a warning).
func (app *App) SetPreloadEnabled(enabled bool) {
	app.SubsystemManager.SetPreloadEnabled(enabled)
}

// SetRestartRequired marks the application as needing a restart for
// configuration changes to take effect.
func (app *App) SetRestartRequired(b bool) {
	app.RuntimeManager.SetRestartRequired(b)
}

// RestartRequired reports whether a restart is required for pending
// configuration changes to take effect.
func (app *App) RestartRequired() bool {
	return app.RuntimeManager.RestartRequired()
}

// ImagesDir returns the current images directory path.
func (app *App) ImagesDir() string {
	return app.imagesDir
}

// UpdateConfigWithPrecedence stores configuration and reapplies CLI/env precedence rules.
func (app *App) UpdateConfigWithPrecedence(c *config.Config, changedFields []string) {
	app.ConfigManager.UpdateConfigWithPrecedence(c, changedFields, app.opt)
}

// ResetStats resets the file processing statistics counters.
func (app *App) ResetStats() {
	app.SubsystemManager.ResetStats()
}

// ensureSession creates the session store and session manager if not already set.
// Called from Run(), Serve(), and CreateApp before building handlers.
func (app *App) ensureSession() {
	app.EnsureSession(app.getSessionOptionsConfig)
}

// buildHandlers parses config UI templates and creates the split handler groups.
func (app *App) buildHandlers(templateFS fs.FS) error {
	return app.Build(
		templateFS,
		app,                                  // interfaces.ServerDeps
		app.SessionAuthFacade.authService,    // auth.AuthService
		app.SessionAuthFacade.sessionManager, // session.SessionManager
		app.dbRoPool,                         // *dbconnpool.DbSQLConnPool
		app.dbRwPool,                         // *dbconnpool.DbSQLConnPool
		app.RuntimeManager.ctx,               // context.Context
		app.ConfigManager.ConfigService,      // config.ConfigService
		app.GetETagVersion,                   // func() string
		app.RuntimeManager.metricsCollector,  // *metrics.Collector
	)
}

// removeImagesDirPrefix removes the leading 'Images' directory from a file path
// and normalizes it to use forward slashes. This creates a relative path
// suitable for database storage and URL generation.
// normalizedImagesDir should be the pre-normalized result of filepath.ToSlash(imagesDir).
// Returns an error if the resulting path contains path traversal sequences (..).
// Delegates to pathutil.RemoveImagesDirPrefix.
func removeImagesDirPrefix(normalizedImagesDir, path string) (string, error) {
	return pathutil.RemoveImagesDirPrefix(normalizedImagesDir, path)
}

// ServerError logs an error and sends a generic 500 Internal Server Error
// response to the client.
func (app *App) ServerError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("server error", "error", err, "path", r.URL.Path)
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

// getSessionOptionsConfig returns session configuration as OptionsConfig for the session manager.
// This is used by the session manager's configGetter function to retrieve current session settings.
func (app *App) getSessionOptionsConfig() *session.OptionsConfig {
	app.ConfigManager.ConfigMu.RLock()
	defer app.ConfigManager.ConfigMu.RUnlock()
	if app.ConfigManager.Config == nil {
		return nil
	}
	return &session.OptionsConfig{
		SessionMaxAge:   app.ConfigManager.Config.SessionMaxAge,
		SessionHttpOnly: app.ConfigManager.Config.SessionHttpOnly,
		SessionSecure:   app.ConfigManager.Config.SessionSecure,
		SessionSameSite: app.ConfigManager.Config.SessionSameSite,
	}
}

// Serve initializes the session store and starts the HTTP server on the configured port.
// It runs until the server encounters a fatal error, a process restart is requested
// (which shuts down the server so Serve returns), or the app context is cancelled.
func (app *App) Serve() error {
	slog.Info("Serve called")
	app.ensureSession()

	app.ConfigManager.ConfigMu.Lock()
	if app.ConfigManager.Config == nil {
		app.ConfigManager.ConfigMu.Unlock()
		if err := app.loadConfig(); err != nil {
			slog.Warn("failed to load configuration in Serve()", "err", err)
			app.ConfigManager.ConfigMu.Lock()
			app.ConfigManager.Config = config.DefaultConfig()
			app.ConfigManager.Config.LoadFromOpt(app.opt)
			app.ConfigManager.ConfigMu.Unlock()
		}
	} else {
		app.ConfigManager.Config.LoadFromOpt(app.opt)
		app.ConfigManager.ConfigMu.Unlock()
	}

	if app.HandlerManager.authHandlers == nil {
		if err := app.buildHandlers(web.FS); err != nil {
			return err
		}
	}
	app.scheduleStaleCacheDrop("serve-startup")

	mux := app.getRouter()
	app.ConfigManager.ConfigMu.RLock()
	addr := fmt.Sprintf("%s:%d", app.ConfigManager.Config.ListenerAddress, app.ConfigManager.Config.ListenerPort)
	app.ConfigManager.ConfigMu.RUnlock()

	if app.testSeams.Serve != nil {
		return app.testSeams.Serve(mux, addr)
	}
	return app.RuntimeManager.Serve(mux, addr)
}

// authMiddleware is a middleware that protects routes by checking for a valid session.
// It delegates to middleware.AuthMiddleware, using the current store and sessionManager.
// If sessionManager is nil (e.g., in tests before Serve() is called), it creates a temporary one.
// After auth succeeds, it sets cache policy for HTMX: any request with HX-Request: true gets
// no-cache so partials are not cached or bfcached (breadcrumb back then gets full page via new request).
// Vary: HX-Request, HX-Target is set so caches do not reuse a partial for a full-page request.
func (app *App) authMiddleware(next http.Handler) http.Handler {
	config := &middleware.AuthConfig{
		DebugDelayMS: struct {
			IsSet bool
			Int   int
		}{
			IsSet: app.opt.DebugDelayMS.IsSet,
			Int:   app.opt.DebugDelayMS.Int,
		},
	}
	// Create middleware function that uses current app.SessionAuthFacade.store and app.SessionAuthFacade.sessionManager
	// This ensures it works even if store is rotated (e.g., in tests)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sm := app.SessionAuthFacade.sessionManager
		if sm == nil && app.SessionAuthFacade.store != nil {
			// Fallback for tests: create temporary session manager
			sm = session.NewManager(app.SessionAuthFacade.store, app.getSessionOptionsConfig)
		}
		authFunc := middleware.AuthMiddleware(app.SessionAuthFacade.store, sm, config)
		// After auth: set cache policy for HTMX before calling handler (see e32e621).
		withHTMXCachePolicy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				w.Header().Set("Pragma", "no-cache")
				w.Header().Set("Expires", "0")
			}
			w.Header().Add("Vary", "HX-Request")
			w.Header().Add("Vary", "HX-Target")
			next.ServeHTTP(w, r)
		})
		authFunc(withHTMXCachePolicy).ServeHTTP(w, r)
	})
}

// AddCommonTemplateData adds common template data (auth state, CSRF token, theme, and gallery statistics) to template data map.
// When partial is true, skips GalleryStats (expensive getGalleryStatistics) since partials (HTMX swaps, modals, toasts)
// don't include the about modal. Full pages need GalleryStats for the about modal in the layout.
func (app *App) AddCommonTemplateData(w http.ResponseWriter, r *http.Request, data map[string]any, partial bool) map[string]any {
	authenticated := app.IsAuthenticated(w, r)
	data = template.AddCommonData(data, authenticated, app.CSRFTokenForPage(w, r, authenticated))
	data["Theme"] = app.getEffectiveTheme(r)
	data["Version"] = app.RuntimeManager.version

	if !partial {
		// Add gallery statistics for the about modal (full pages only)
		if app.SubsystemManager.moduleStateService == nil {
			stats, err := app.getGalleryStatistics(r.Context())
			if err != nil {
				slog.Warn("failed to get gallery statistics", "err", err)
				data["GalleryStats"] = GalleryStats{}
			} else {
				data["GalleryStats"] = stats
			}
		} else {
			ctx := r.Context()
			var isActive bool
			var aErr error
			if app.testSeams.AddCommonDataIsActive != nil {
				isActive, aErr = app.testSeams.AddCommonDataIsActive(ctx, "discovery")
			} else {
				isActive, aErr = app.SubsystemManager.moduleStateService.IsActive(ctx, "discovery")
			}
			if aErr != nil {
				slog.Error("failed to check discovery active state", "err", aErr)
			}
			var lastStarted int64
			var lsErr error
			if app.testSeams.AddCommonDataLastStarted != nil {
				lastStarted, _, lsErr = app.testSeams.AddCommonDataLastStarted(ctx, "discovery")
			} else {
				lastStarted, _, lsErr = app.SubsystemManager.moduleStateService.GetLastStartedAt(ctx, "discovery")
			}
			if lsErr != nil {
				slog.Error("failed to get discovery last started at", "err", lsErr)
			}
			if isActive {
				if cached := app.getGalleryStatsCached(lastStarted); cached != nil {
					data["GalleryStats"] = *cached
				} else {
					data["GalleryStats"] = GalleryStats{}
				}
			} else {
				if cached := app.getGalleryStatsCached(lastStarted); cached != nil {
					data["GalleryStats"] = *cached
				} else {
					stats, err := app.refreshGalleryStatsCache(ctx, lastStarted)
					if err != nil {
						slog.Warn("failed to get gallery statistics", "err", err)
						data["GalleryStats"] = GalleryStats{}
					} else {
						data["GalleryStats"] = stats
					}
				}
			}
		}
	}

	return data
}

// getEffectiveTheme returns the effective theme for a request.
// Priority: 1) Cookie (if valid), 2) Server default.
func (app *App) getEffectiveTheme(r *http.Request) string {
	cfg := app.GetConfig()
	themes := []string{}
	defaultTheme := "dark"
	if cfg != nil {
		themes = cfg.Themes
		defaultTheme = cfg.CurrentTheme
	}
	return app.GetEffectiveTheme(r, func() []string { return themes }, defaultTheme)
}

// GalleryStats holds statistics about the gallery for display in the about modal.
type GalleryStats struct {
	Folders        string
	Images         string
	ImagesSize     int64
	FirstDiscovery string
	LastDiscovery  string
}

// getGalleryStatistics retrieves gallery statistics from the database.
func (app *App) getGalleryStatistics(ctx context.Context) (GalleryStats, error) {
	if app.testSeams.GetGalleryStatistics != nil {
		return app.testSeams.GetGalleryStatistics(ctx)
	}

	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		return GalleryStats{}, fmt.Errorf("failed to get database connection: %w", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	stats, err := cpcRo.Queries.GetGalleryStatistics(ctx)
	if err != nil {
		return GalleryStats{}, fmt.Errorf("failed to get gallery statistics: %w", err)
	}

	// Convert the database result to GalleryStats with formatted numbers
	result := GalleryStats{
		Folders:    humanize.Comma(stats.CtFolders).String(),
		Images:     humanize.Comma(stats.CtFiles).String(),
		ImagesSize: int64(stats.SzFiles.Float64),
	}

	// Convert timestamps to strings if they exist
	if stats.MinCreatedAt != nil {
		if ts, ok := stats.MinCreatedAt.(int64); ok {
			result.FirstDiscovery = time.Unix(ts, 0).Format("2006-01-02 15:04:05")
		} else {
			slog.Warn("stats.MinCreatedAt is not int64", "type", fmt.Sprintf("%T", stats.MinCreatedAt))
		}
	}
	if stats.MaxUpdatedAt != nil {
		if ts, ok := stats.MaxUpdatedAt.(int64); ok {
			result.LastDiscovery = time.Unix(ts, 0).Format("2006-01-02 15:04:05")
		} else {
			slog.Warn("stats.MaxUpdatedAt is not int64", "type", fmt.Sprintf("%T", stats.MaxUpdatedAt))
		}
	}

	return result, nil
}

// GetUser retrieves the stored user details from the database for authentication.
// It returns a session.User struct containing the username and the stored password hash.
func (app *App) GetUser(ctx context.Context, username string) (*session.User, error) {
	return app.SessionAuthFacade.GetUser(ctx, username, app.dbRoPool, app.dbRwPool)
}

// CheckAccountLockout checks if an account is locked and returns true if locked, false otherwise.
// If the lockout has expired, it clears the lockout.
func (app *App) CheckAccountLockout(ctx context.Context, username string) (bool, error) {
	return app.SessionAuthFacade.CheckAccountLockout(ctx, username, app.dbRwPool)
}

// RecordFailedLoginAttempt records a failed login attempt and locks the account after 3 failures.
func (app *App) RecordFailedLoginAttempt(ctx context.Context, username string) error {
	lockout := int64(3600)
	threshold := int64(3)
	cfg := app.GetConfig()
	if cfg != nil && cfg.LockoutDuration > 0 {
		lockout = int64(cfg.LockoutDuration)
	}
	if cfg != nil && cfg.LockoutThreshold > 0 {
		threshold = int64(cfg.LockoutThreshold)
	}
	return app.SessionAuthFacade.RecordFailedLoginAttempt(
		ctx, username, app.dbRwPool, lockout, threshold, app.SubsystemManager.scheduler, app.unlockAccountFromTask,
	)
}

// ClearLoginAttempts clears failed login attempts for a username (called on successful login).
func (app *App) ClearLoginAttempts(ctx context.Context, username string) error {
	return app.SessionAuthFacade.ClearLoginAttempts(ctx, username, app.dbRwPool)
}

// unlockAccountFromTask unlocks a user account (called by scheduled unlock task).
// This function is used by the UnlockAccountTask scheduled when a lockout is set.
func (app *App) unlockAccountFromTask(ctx context.Context, username string) error {
	return app.SessionAuthFacade.UnlockAccountFromTask(ctx, username, app.dbRwPool)
}

// TriggerDiscovery starts a background process to recursively scan the images directory.
// It delegates to files.WalkImageDir with app-specific deps.
// Updates module_state for "discovery" so batch load can guard against concurrent discovery.
func (app *App) TriggerDiscovery() {
	ctx := app.getCtx()

	if app.SubsystemManager.moduleStateService != nil {
		if err := app.SubsystemManager.moduleStateService.SetActive(ctx, "discovery", true); err != nil {
			slog.Error("failed to set discovery active in module_state", "err", err)
		}
		defer func() {
			// Use Background so finish is persisted even if app ctx is cancelled
			if err := app.SubsystemManager.moduleStateService.SetActive(context.Background(), "discovery", false); err != nil {
				slog.Error("failed to set discovery inactive in module_state", "err", err)
			}
		}()
	}

	files.WalkImageDir(&files.WalkDeps{
		Wg:             &app.RuntimeManager.wg,
		QSendersActive: &app.SubsystemManager.qSendersActive,
		Ctx:            ctx,
		ImagesDir:      app.imagesDir,
		Q:              app.SubsystemManager.q,
	})

	// Refresh gallery stats cache after discovery completes (covers both startup and server menu)
	if app.SubsystemManager.moduleStateService != nil {
		if lastStarted, ok, gsErr := app.SubsystemManager.moduleStateService.GetLastStartedAt(ctx, "discovery"); ok && gsErr == nil {
			if _, refreshErr := app.refreshGalleryStatsCache(ctx, lastStarted); refreshErr != nil {
				slog.Error("failed to refresh gallery stats cache", "err", refreshErr)
			}
		} else if gsErr != nil {
			slog.Error("failed to get discovery last started at", "err", gsErr)
		}
	}
	app.scheduleStaleCacheDrop("discovery-complete")
}
