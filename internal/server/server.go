// Package server provides the core HTTP server, routing, middleware, and
// handlers for the web application. It integrates all the sub-packages
// like database, caching, and background workers to serve the photo gallery.
package server

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/humanize"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/handlers"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/server/middleware"
	"github.com/lbe/sfpg-go/internal/server/pathutil"
	"github.com/lbe/sfpg-go/internal/server/security"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/template"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/web"
)

// loginStoreAdapter adapts App to handlers.LoginStore for auth handlers.
type loginStoreAdapter struct {
	app *App
}

func (a *loginStoreAdapter) CheckAccountLockout(ctx context.Context, username string) (bool, error) {
	return a.app.checkAccountLockout(username)
}

func (a *loginStoreAdapter) GetUser(ctx context.Context, username string) (*session.User, error) {
	return a.app.getUser(username)
}

func (a *loginStoreAdapter) RecordFailedLoginAttempt(ctx context.Context, username string) error {
	return a.app.recordFailedLoginAttempt(username)
}

func (a *loginStoreAdapter) ClearLoginAttempts(ctx context.Context, username string) error {
	return a.app.clearLoginAttempts(username)
}

// credentialStoreAdapter adapts App to auth.CredentialStore.
type credentialStoreAdapter struct {
	loginStoreAdapter
}

func (a *credentialStoreAdapter) UpdateUsername(ctx context.Context, username string) error {
	// Use dbRwPool to update config table
	cpc, err := a.app.dbRwPool.Get()
	if err != nil {
		return err
	}
	defer a.app.dbRwPool.Put(cpc)
	_, err = cpc.Conn.ExecContext(ctx, "UPDATE config SET value = ? WHERE key = 'user'", username)
	return err
}

func (a *credentialStoreAdapter) UpdatePassword(ctx context.Context, passwordHash string) error {
	cpc, err := a.app.dbRwPool.Get()
	if err != nil {
		return err
	}
	defer a.app.dbRwPool.Put(cpc)
	_, err = cpc.Conn.ExecContext(ctx, "UPDATE config SET value = ? WHERE key = 'password'", passwordHash)
	return err
}

// metadataQueriesAdapter wraps *dbconnpool.CpConn to implement handlers.MetadataQueries.
type metadataQueriesAdapter struct {
	cpc *dbconnpool.CpConn
}

func (a *metadataQueriesAdapter) GetExifByFile(ctx context.Context, fileID int64) (gallerydb.ExifMetadatum, error) {
	return a.cpc.Queries.GetExifByFile(ctx, fileID)
}

func (a *metadataQueriesAdapter) GetIPTCByFile(ctx context.Context, fileID int64) (gallerydb.IptcMetadatum, error) {
	return a.cpc.Queries.GetIPTCByFile(ctx, fileID)
}

// ensureSessionAndRestart creates store, sessionManager, and restartCh if not already set.
// Called from Run(), Serve(), and CreateApp before building Handlers.
func (app *App) ensureSessionAndRestart() {
	if app.store == nil {
		app.store = sessions.NewCookieStore([]byte(app.sessionSecret))
		app.store.Options = app.getSessionOptions()
	}
	if app.sessionManager == nil && app.store != nil {
		app.sessionManager = session.NewManager(app.store, app.getSessionOptionsConfig)
	}
	app.restartMu.Lock()
	if app.restartCh == nil {
		app.restartCh = make(chan struct{}, 1)
	}
	app.restartMu.Unlock()
}

// buildHandlers parses config UI templates and creates the split handler groups.
func (app *App) buildHandlers(templateFS fs.FS) error {
	tmpl, err := parseConfigUITemplates(templateFS)
	if err != nil {
		return err
	}
	sm := app.sessionManager
	if sm == nil && app.store != nil {
		sm = session.NewManager(app.store, app.getSessionOptionsConfig)
	}

	// Create and store the new split handler groups
	app.authHandlers = handlers.NewAuthHandlers(
		app.authService,
		sm,
		app.ensureCsrfToken,
	)

	app.configHandlers = handlers.NewConfigHandlers(
		app.configService,
		app.authService,
		&credentialStoreAdapter{loginStoreAdapter{app: app}},
		sm,
		app.dbRoPool,
		app.dbRwPool,
		handlers.ConfigTemplates{
			SaveRestartAlert:      tmpl.SaveRestartAlert,
			SaveSuccessAlert:      tmpl.SaveSuccessAlert,
			ExportModal:           tmpl.ExportModal,
			ImportModal:           tmpl.ImportModal,
			ImportSuccessAlert:    tmpl.ImportSuccessAlert,
			RestoreModal:          tmpl.RestoreModal,
			RestoreSuccessAlert:   tmpl.RestoreSuccessAlert,
			RestartInitiatedAlert: tmpl.RestartInitiatedAlert,
		},
		app.ctx,
	)
	// Set callback fields on ConfigHandlers
	app.configHandlers.UpdateConfig = func(c *config.Config, changedFields []string) {
		app.configMu.Lock()
		app.config = c
		// Apply CLI/opt overrides only to fields NOT changed by the user.
		// This ensures:
		// 1. User changes persist for fields they explicitly changed
		// 2. CLI/env values take precedence for fields they didn't change (Case 1)
		c.LoadFromOptExcluding(app.opt, changedFields)
		app.configMu.Unlock()
	}
	app.configHandlers.ApplyConfig = app.applyConfig
	app.configHandlers.IncrementETag = app.IncrementETag
	app.configHandlers.InvalidateHTTPCache = app.invalidateHTTPCache
	app.configHandlers.SetPreloadEnabled = func(enabled bool) {
		if app.preloadManager != nil {
			app.preloadManager.SetEnabled(enabled)
		}
	}
	app.configHandlers.SetRestartRequired = func(b bool) { app.restartRequired = b }
	app.configHandlers.GetRestartCh = func() chan struct{} {
		app.restartMu.RLock()
		ch := app.restartCh
		app.restartMu.RUnlock()
		return ch
	}
	app.configHandlers.AddCommonTemplateData = app.addCommonTemplateData
	app.configHandlers.ServerError = app.serverError

	if err := app.configHandlers.Validate(); err != nil {
		return err
	}

	app.galleryHandlers = handlers.NewGalleryHandlers(
		app.dbRoPool,
		app.ctx,
		app.imagesDir,
		func() string { return app.imagesDir },
		app.getHandlerQueries,
		func(cpc *dbconnpool.CpConn) handlers.MetadataQueries {
			return &metadataQueriesAdapter{cpc: cpc}
		},
		app.GetETagVersion,
		app.addCommonTemplateData,
		app.serverError,
	)
	if app.galleryHandlers != nil && app.preloadManager != nil {
		app.galleryHandlers.PreloadService = app.preloadManager
	}

	app.healthHandlers = handlers.NewHealthHandlers(app.GetETagVersion)

	// Initialize dashboard handlers with metrics collector
	// Ensure metrics collector is initialized
	if app.metricsCollector == nil {
		app.metricsCollector = metrics.NewCollector()
	}

	app.dashboardHandlers = handlers.NewDashboardHandlers(
		sm,
		app.metricsCollector,
		app.addCommonTemplateData,
		app.serverError,
	)

	// Initialize server management handlers
	app.serverHandlers = handlers.NewServerHandlers(
		sm,
		app.Shutdown,
		app.walkImageDir,
		app.processingStats.Reset,
		app.startCacheBatchLoad,
		app.addCommonTemplateData,
		app.serverError,
	)

	// Initialize theme handlers
	app.themeHandlers = handlers.NewThemeHandlers(
		func() *config.Config {
			app.configMu.RLock()
			defer app.configMu.RUnlock()
			return app.config
		},
		app.addCommonTemplateData,
		func(w http.ResponseWriter, data any) error {
			return ui.RenderTemplate(w, "theme-modal.html.tmpl", data)
		},
		app.serverError,
	)

	return nil
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

// serverError logs an error and sends a generic 500 Internal Server Error
// response to the client.
func (app *App) serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("server error", "error", err, "path", r.URL.Path)
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

// getSessionOptionsConfig returns session configuration as OptionsConfig for the session manager.
// This is used by the session manager's configGetter function to retrieve current session settings.
func (app *App) getSessionOptionsConfig() *session.OptionsConfig {
	app.configMu.RLock()
	defer app.configMu.RUnlock()
	if app.config == nil {
		return nil
	}
	return &session.OptionsConfig{
		SessionMaxAge:   app.config.SessionMaxAge,
		SessionHttpOnly: app.config.SessionHttpOnly,
		SessionSecure:   app.config.SessionSecure,
		SessionSameSite: app.config.SessionSameSite,
	}
}

// getSessionOptions returns session cookie options configured from app.config.
// It delegates to the session manager.
func (app *App) getSessionOptions() *sessions.Options {
	if app.sessionManager != nil {
		return app.sessionManager.GetOptions()
	}
	// Fallback for tests or early initialization before sessionManager is created
	return session.GetSessionOptions(app.getSessionOptionsConfig())
}

// ensureCsrfToken ensures a CSRF token exists in the session and returns it.
// It delegates to the session manager.
func (app *App) ensureCsrfToken(w http.ResponseWriter, r *http.Request) string {
	if app.sessionManager != nil {
		return app.sessionManager.EnsureCSRFToken(w, r)
	}
	// Fallback for tests or early initialization
	return session.EnsureCsrfToken(app.store, w, r)
}

// validateCsrfToken validates the CSRF token in the request form against the session.
// It delegates to the session manager.
func (app *App) validateCsrfToken(r *http.Request) bool {
	if app.sessionManager != nil {
		return app.sessionManager.ValidateCSRFToken(r)
	}
	// Fallback for tests or early initialization
	return session.ValidateCsrfToken(app.store, r)
}

// Serve initializes the session store and starts the HTTP server on the configured port.
// It supports graceful restart via the restart channel.
func (app *App) Serve() error {
	slog.Info("Serve called")

	app.ensureSessionAndRestart()

	// Ensure config is loaded (needed when Serve() is called directly without Run())
	app.configMu.Lock()
	if app.config == nil {
		app.configMu.Unlock()
		if err := app.loadConfig(); err != nil {
			slog.Warn("failed to load configuration in Serve()", "err", err)
			// Continue with defaults
			app.configMu.Lock()
			app.config = config.DefaultConfig()
			app.config.LoadFromOpt(app.opt)
			app.configMu.Unlock()
		} else {
			app.configMu.Lock()
		}
	} else {
		// Config already loaded, but reapply opt values in case they changed after app creation
		// (e.g., tests setting app.opt.Port after CreateApp)
		app.config.LoadFromOpt(app.opt)
	}
	app.configMu.Unlock()

	if app.authHandlers == nil {
		if err := app.buildHandlers(web.FS); err != nil {
			return fmt.Errorf("build handlers: %w", err)
		}
	}

	for {
		mux := app.getRouter()
		// Read listener address and port directly from app.config
		app.configMu.RLock()
		listenerAddress := app.config.ListenerAddress
		listenerPort := app.config.ListenerPort
		app.configMu.RUnlock()
		addr := fmt.Sprintf("%s:%d", listenerAddress, listenerPort)

		app.restartMu.Lock()
		app.httpServer = &http.Server{
			Addr:    addr,
			Handler: mux,
		}
		// Get local copies to avoid holding lock during server operations
		restartCh := app.restartCh
		httpServer := app.httpServer
		app.restartMu.Unlock()

		slog.Info("starting web server", "addr", addr)

		// Start server in goroutine
		serverErr := make(chan error, 1)
		go func() {
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				serverErr <- err
			}
			close(serverErr)
		}()

		// Wait for either: server error, restart signal, or context cancellation
		select {
		case err := <-serverErr:
			if err != nil {
				return fmt.Errorf("server error: %w", err)
			}
			// Server closed normally, check if restart was requested
		case <-restartCh:
			slog.Info("restart signal received, shutting down server")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			// Get local copy to avoid holding lock during shutdown operation
			app.restartMu.RLock()
			shutdownServer := app.httpServer
			app.restartMu.RUnlock()
			if shutdownServer != nil {
				if err := shutdownServer.Shutdown(shutdownCtx); err != nil {
					slog.Error("error during server shutdown", "err", err)
				}
			}
			cancel()
			app.restartRequired = false
			// Apply config to app state (log level, directories, etc.)
			app.applyConfig()
			slog.Info("server shutdown complete, restarting with updated configuration...")
			continue // Restart the server loop - will read updated address/port from app.config on next iteration
		case <-app.ctx.Done():
			slog.Info("context cancelled, shutting down server")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			// Get local copy to avoid holding lock during shutdown operation
			app.restartMu.RLock()
			shutdownServer := app.httpServer
			app.restartMu.RUnlock()
			if shutdownServer != nil {
				if err := shutdownServer.Shutdown(shutdownCtx); err != nil {
					slog.Error("error during server shutdown", "err", err)
				}
			}
			cancel()
			return nil // Exit gracefully
		}
	}
}

// getAdminUsername retrieves the administrator's username from the database 'config' table.
// Delegates to ConfigService.GetConfigValue when available.
func (app *App) getAdminUsername() (string, error) {
	if app.configService != nil {
		username, err := app.configService.GetConfigValue(app.ctx, "user")
		if err != nil {
			return "", fmt.Errorf("failed to get admin username from config: %w", err)
		}
		return username, nil
	}
	var username string
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get database connection: %w", err)
	}
	defer app.dbRoPool.Put(cpcRo)
	username, err = cpcRo.Queries.GetConfigValueByKey(app.ctx, "user")
	if err != nil {
		return "", fmt.Errorf("failed to get admin username from config: %w", err)
	}
	return username, nil
}

// GetETagVersion returns the current ETag version for cache-busting URLs.
// Thread-safe: acquires configMu.RLock to read app.config.ETagVersion.
func (app *App) GetETagVersion() string {
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
}

// responseWriter wraps http.ResponseWriter to capture status code and bytes written.
type responseWriter struct {
	http.ResponseWriter
	status       int
	bytesWritten int
	wroteHeader  bool
	etag         string
	lastModified string
}

// SetETag stores the ETag value for later retrieval
func (rw *responseWriter) SetETag(etag string) {
	rw.etag = etag
}

// GetETag returns the stored ETag value
func (rw *responseWriter) GetETag() string {
	return rw.etag
}

// SetLastModified stores the Last-Modified time in HTTP format
func (rw *responseWriter) SetLastModified(t time.Time) {
	rw.lastModified = t.UTC().Format(http.TimeFormat)
}

// GetLastModified returns the stored Last-Modified value
func (rw *responseWriter) GetLastModified() string {
	return rw.lastModified
}

// GetContentType extracts Content-Type from response headers
func (rw *responseWriter) GetContentType() string {
	return rw.Header().Get("Content-Type")
}

// GetCacheControl extracts Cache-Control from response headers
func (rw *responseWriter) GetCacheControl() string {
	return rw.Header().Get("Cache-Control")
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.status = code
		rw.wroteHeader = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
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
	// Create middleware function that uses current app.store and app.sessionManager
	// This ensures it works even if store is rotated (e.g., in tests)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sm := app.sessionManager
		if sm == nil && app.store != nil {
			// Fallback for tests: create temporary session manager
			sm = session.NewManager(app.store, app.getSessionOptionsConfig)
		}
		authFunc := middleware.AuthMiddleware(app.store, sm, config)
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

// isAuthenticated checks if the current request has a valid authenticated session.
func (app *App) isAuthenticated(r *http.Request) bool {
	session, err := app.store.Get(r, "session-name")
	if err != nil {
		return false
	}
	auth, ok := session.Values["authenticated"].(bool)
	return ok && auth
}

// addAuthToTemplateData adds authentication state to template data map
// Delegates to template.AddAuthToData
func (app *App) addAuthToTemplateData(r *http.Request, data map[string]any) map[string]any {
	return template.AddAuthToData(data, app.isAuthenticated(r))
}

// addCommonTemplateData adds common template data (auth state, CSRF token, theme, and gallery statistics) to template data map
// This is used for pages that include modals which need the CSRF token
// Delegates to template.AddCommonData and adds theme and version
func (app *App) addCommonTemplateData(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
	data = template.AddCommonData(data, app.isAuthenticated(r), app.ensureCsrfToken(w, r))
	data["Theme"] = app.getEffectiveTheme(r)
	data["Version"] = app.version

	// Add gallery statistics for the about modal
	stats, err := app.getGalleryStatistics(r.Context())
	if err != nil {
		slog.Warn("failed to get gallery statistics", "err", err)
		// Set zero values on error
		data["GalleryStats"] = GalleryStats{}
	} else {
		data["GalleryStats"] = stats
	}

	return data
}

// getEffectiveTheme returns the effective theme for a request.
// Priority: 1) Cookie (if valid), 2) Server default.
func (app *App) getEffectiveTheme(r *http.Request) string {
	app.configMu.RLock()
	cfg := app.config
	app.configMu.RUnlock()

	if cfg == nil {
		return "dark"
	}

	// Priority 1: Cookie (if valid theme)
	if cookie, err := r.Cookie("theme"); err == nil {
		if slices.Contains(cfg.Themes, cookie.Value) {
			return cookie.Value
		}
	}

	// Priority 2: Server default
	return cfg.CurrentTheme
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
		}
	}
	if stats.MaxUpdatedAt != nil {
		if ts, ok := stats.MaxUpdatedAt.(int64); ok {
			result.LastDiscovery = time.Unix(ts, 0).Format("2006-01-02 15:04:05")
		}
	}

	return result, nil
}

// getUser retrieves the stored user details from the database for authentication.
// It returns a session.User struct containing the username and the stored password hash.
func (app *App) getUser(username string) (*session.User, error) {
	user := &session.User{}
	var storedUsername string
	var storedPasswordHash string

	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	storedUsername, err = cpcRo.Queries.GetConfigValueByKey(app.ctx, "user")
	if err != nil {
		return nil, fmt.Errorf("failed to get username from config: %w", err)
	}
	storedPasswordHash, err = cpcRo.Queries.GetConfigValueByKey(app.ctx, "password")
	if err != nil {
		return nil, fmt.Errorf("failed to get password hash from config: %w", err)
	}

	if storedUsername != username {
		return nil, sql.ErrNoRows // Username mismatch
	}

	user.Username = storedUsername
	user.Password = storedPasswordHash // This is the hashed password

	return user, nil
}

// checkAccountLockout checks if an account is locked and returns true if locked, false otherwise.
// If the lockout has expired, it clears the lockout.
func (app *App) checkAccountLockout(username string) (bool, error) {
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		return false, fmt.Errorf("failed to get database connection: %w", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	attempt, err := cpcRw.Queries.GetLoginAttempt(app.ctx, username)
	if err != nil {
		if err == sql.ErrNoRows {
			// No login attempts recorded, account is not locked
			return false, nil
		}
		return false, fmt.Errorf("failed to get login attempt for username %q: %w", username, err)
	}

	// If not locked, return false
	if !attempt.LockedUntil.Valid {
		return false, nil
	}

	// Check if lockout has expired
	now := time.Now().Unix()
	if security.ShouldClearLockout(attempt.LockedUntil, now) {
		// Lockout expired, clear it
		err = cpcRw.Queries.ClearLoginAttempts(app.ctx, username)
		if err != nil {
			return false, fmt.Errorf("failed to clear expired lockout for username %q: %w", username, err)
		}
		return false, nil
	}

	// Account is locked
	return security.IsLocked(attempt.LockedUntil, now), nil
}

// recordFailedLoginAttempt records a failed login attempt and locks the account after 3 failures.
func (app *App) recordFailedLoginAttempt(username string) error {
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	now := time.Now().Unix()
	var failedAttempts int64 = 1

	// Check if there's an existing record
	attempt, err := cpcRw.Queries.GetLoginAttempt(app.ctx, username)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to get login attempt for username %q: %w", username, err)
	}

	if err == nil {
		// Existing record found, increment failed attempts
		failedAttempts = security.IncrementFailedAttempts(attempt.FailedAttempts)
	}

	// Calculate lockout using pure function
	lockedUntil := security.CalculateLockout(failedAttempts, now)

	// Upsert the login attempt record
	err = cpcRw.Queries.UpsertLoginAttempt(app.ctx, gallerydb.UpsertLoginAttemptParams{
		Username:       username,
		FailedAttempts: failedAttempts,
		LastAttemptAt:  now,
		LockedUntil:    lockedUntil,
	})
	if err != nil {
		return fmt.Errorf("failed to upsert login attempt for username %q: %w", username, err)
	}

	// Schedule a task to unlock the account when the lockout period expires
	if lockedUntil.Valid {
		// Schedule unlock task to run 1 second after locked_until time
		unlockTime := time.Unix(lockedUntil.Int64, 0).Add(1 * time.Second)
		task := &security.UnlockAccountTask{
			Username: username,
			UnlockFn: app.unlockAccountFromTask,
		}
		// Only schedule if scheduler is initialized (may not be in tests)
		if app.scheduler != nil {
			_, err = app.scheduler.AddTask(task, scheduler.OneTime, unlockTime)
			if err != nil {
				// Log the error but don't fail the login attempt recording
				slog.Error("failed to schedule unlock task", "username", username, "unlock_time", unlockTime, "error", err)
			} else {
				slog.Debug("scheduled account unlock task", "username", username, "unlock_time", unlockTime)
			}
		}
	}

	return nil
}

// clearLoginAttempts clears failed login attempts for a username (called on successful login).
func (app *App) clearLoginAttempts(username string) error {
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = cpcRw.Queries.ClearLoginAttempts(app.ctx, username)
	if err != nil {
		return fmt.Errorf("failed to clear login attempts for username %q: %w", username, err)
	}
	return nil
}

// unlockAccountFromTask unlocks a user account (called by scheduled unlock task).
// This function is used by the UnlockAccountTask scheduled when a lockout is set.
func (app *App) unlockAccountFromTask(ctx context.Context, username string) error {
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = cpcRw.Queries.UnlockAccount(ctx, username)
	if err != nil {
		return fmt.Errorf("failed to unlock account for username %q: %w", username, err)
	}
	return nil
}

// walkImageDir starts a background process to recursively scan the images directory.
// It delegates to files.WalkImageDir with app-specific deps.
// Updates module_state for "discovery" so batch load can guard against concurrent discovery.
func (app *App) walkImageDir() {
	app.ctxMu.RLock()
	ctx := app.ctx
	app.ctxMu.RUnlock()

	if app.moduleStateService != nil {
		if err := app.moduleStateService.SetActive(ctx, "discovery", true); err != nil {
			slog.Error("failed to set discovery active in module_state", "err", err)
		}
		defer func() {
			// Use Background so finish is persisted even if app ctx is cancelled
			if err := app.moduleStateService.SetActive(context.Background(), "discovery", false); err != nil {
				slog.Error("failed to set discovery inactive in module_state", "err", err)
			}
		}()
	}

	files.WalkImageDir(&files.WalkDeps{
		Wg:             &app.wg,
		QSendersActive: &app.qSendersActive,
		Ctx:            ctx,
		ImagesDir:      app.imagesDir,
		Q:              app.q,
	})
}
