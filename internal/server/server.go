// Package server provides the core HTTP server, routing, middleware, and
// handlers for the web application. It integrates all the sub-packages
// like database, caching, and background workers to serve the photo gallery.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
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

// persistFileProcessingStats writes the completed discovery run's counters to
// module_state.payload key file_processing on the discovery row so a
// skip-startup-discovery restart can hydrate them. It is a no-op (nil error)
// when processingStats or the module state service are nil, matching
// waitForFileProcessingDrain and HydrateFileProcessingStats — nil stats never
// call GetStats(). Errors are returned for the caller to log; a persist
// failure must not fail discovery.
func (app *App) persistFileProcessingStats(ctx context.Context) error {
	sm := app.SubsystemManager
	if sm.processingStats == nil || sm.moduleStateService == nil {
		return nil
	}
	return sm.moduleStateService.SaveFileProcessing(ctx, "discovery", sm.processingStats.GetStats())
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

// AddCommonTemplateData adds common template data (auth state, theme, version, and gallery statistics)
// to the template data map. GalleryStats is always included regardless of partial/full page:
// partial HTMX responses (dashboard polls, modal swaps) now render cards that need GalleryStats.
// GalleryStats is a live atomic-counter cache populated by async startup queries and
// incremental discovery updates.
func (app *App) AddCommonTemplateData(w http.ResponseWriter, r *http.Request, data map[string]any, partial bool) map[string]any {
	authenticated := app.IsAuthenticated(w, r)
	data = template.AddCommonData(data, authenticated)
	// Theme: use site default (CurrentTheme) for SSR; client cookie overrides via hyperscript.
	// Do NOT use getEffectiveTheme(r) which reads the request cookie — that would vary cached
	// HTML per-user. The cookie is applied client-side in body-behavior.html.tmpl.
	currentTheme := "dark"
	if cfg := app.GetConfig(); cfg != nil && cfg.CurrentTheme != "" {
		currentTheme = cfg.CurrentTheme
	}
	data["Theme"] = currentTheme
	data["Version"] = app.RuntimeManager.version

	data["GalleryStats"] = app.RuntimeManager.GalleryStats()

	return data
}

// GalleryStats holds live atomic counters for gallery statistics.
// Display methods return "N/A" when the counter is zero and a population
// process is running, or formatted values otherwise.
type GalleryStats struct {
	folders    atomic.Int64
	images     atomic.Int64
	imagesSize atomic.Int64
	firstDisc  atomic.Int64 // epoch seconds
	lastDisc   atomic.Int64 // epoch seconds
	running    atomic.Int32 // active population processes
}

// Folders returns the formatted folder count or "N/A" if unpopulated.
func (gs *GalleryStats) Folders() string {
	c := gs.folders.Load()
	if c == 0 && gs.running.Load() > 0 {
		return "N/A"
	}
	return humanize.Comma(c).String()
}

// Images returns the formatted image count or "N/A" if unpopulated.
func (gs *GalleryStats) Images() string {
	c := gs.images.Load()
	if c == 0 && gs.running.Load() > 0 {
		return "N/A"
	}
	return humanize.Comma(c).String()
}

// ImagesSize returns the total image size in bytes, or -1 if stats are
// still being populated (running > 0 and counter is 0).
func (gs *GalleryStats) ImagesSize() int64 {
	if gs.imagesSize.Load() == 0 && gs.running.Load() > 0 {
		return -1
	}
	return gs.imagesSize.Load()
}

// FoldersCount returns the raw folder count for expected-total calculations.
func (gs *GalleryStats) FoldersCount() int64 { return gs.folders.Load() }

// ImagesCount returns the raw image count for expected-total calculations.
func (gs *GalleryStats) ImagesCount() int64 { return gs.images.Load() }

// FirstDiscovery returns the formatted timestamp of the first discovered file.
func (gs *GalleryStats) FirstDiscovery() string {
	ts := gs.firstDisc.Load()
	if ts == 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

// LastDiscovery returns the formatted timestamp of the last discovered file.
func (gs *GalleryStats) LastDiscovery() string {
	ts := gs.lastDisc.Load()
	if ts == 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

// Internal helpers.
func (gs *GalleryStats) addFolder()         { gs.folders.Add(1) }
func (gs *GalleryStats) setFolders(n int64) { gs.folders.Add(n) }
func (gs *GalleryStats) addFile(size int64) {
	gs.images.Add(1)
	gs.imagesSize.Add(size)
	gs.firstDisc.CompareAndSwap(0, time.Now().Unix())
	gs.lastDisc.Store(time.Now().Unix())
}
func (gs *GalleryStats) setFileStats(countCt, sizeBytes, minCreated, maxUpdated int64) {
	gs.images.Add(countCt)
	gs.imagesSize.Add(sizeBytes)
	gs.firstDisc.CompareAndSwap(0, minCreated)
	gs.lastDisc.Store(maxUpdated)
}
func (gs *GalleryStats) setFileCountAndTimestamps(countCt, minCreated, maxUpdated int64) {
	gs.images.Add(countCt)
	gs.firstDisc.CompareAndSwap(0, minCreated)
	gs.lastDisc.Store(maxUpdated)
}
func (gs *GalleryStats) addImagesSize(n int64) { gs.imagesSize.Add(n) }
func (gs *GalleryStats) markRunning(d int32)   { gs.running.Add(d) }

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

// lockoutParamsFromConfig returns the configured lockout duration and threshold.
// A nil config is seeded from DefaultConfig(); zero values pass through raw —
// zero-to-default resolution happens in security.CalculateLockout.
func lockoutParamsFromConfig(cfg *config.Config) (lockout, threshold int64) {
	if cfg == nil {
		def := config.DefaultConfig()
		return int64(def.LockoutDuration), int64(def.LockoutThreshold)
	}
	return int64(cfg.LockoutDuration), int64(cfg.LockoutThreshold)
}

// RecordFailedLoginAttempt records a failed login attempt and locks the account
// after the configured lockout threshold is exceeded.
func (app *App) RecordFailedLoginAttempt(ctx context.Context, username string) error {
	lockout, threshold := lockoutParamsFromConfig(app.GetConfig())
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

// TriggerDiscovery walks the images directory, waits for file processing to
// drain, then rebuilds file_folder_index. It updates module_state for
// "discovery" so batch load can guard against concurrent discovery.
func (app *App) TriggerDiscovery(ctx context.Context) error {
	if !app.discoveryRunning.CompareAndSwap(false, true) {
		slog.Info("discovery already in flight")
		return nil
	}
	defer app.discoveryRunning.Store(false)

	// Zero live counters only after winning the CAS, so a failed CAS leaves
	// live counters and the persisted payload unchanged. A starting run must
	// not add onto hydrated skip-startup last-run totals. Reset happens before
	// the testSeams.TriggerDiscovery check so tests observe it via the seam.
	app.ResetStats()

	app.RuntimeManager.GalleryStats().markRunning(1)
	defer app.RuntimeManager.GalleryStats().markRunning(-1)

	if app.SubsystemManager.moduleStateService != nil {
		if err := app.SubsystemManager.moduleStateService.SetActive(app.getCtx(), "discovery", true); err != nil {
			slog.Error("failed to set discovery active in module_state", "err", err)
		}
		defer func() {
			// Use Background so finish is persisted even if app ctx is cancelled
			if err := app.SubsystemManager.moduleStateService.SetActive(context.Background(), "discovery", false); err != nil {
				slog.Error("failed to set discovery inactive in module_state", "err", err)
			}
		}()
	}

	if app.testSeams.TriggerDiscovery != nil {
		return app.testSeams.TriggerDiscovery(ctx)
	}

	lifecycleCtx := app.getCtx()

	files.WalkImageDir(&files.WalkDeps{
		Wg:             &app.RuntimeManager.wg,
		QSendersActive: &app.SubsystemManager.qSendersActive,
		Ctx:            lifecycleCtx,
		ImagesDir:      app.imagesDir,
		Q:              app.SubsystemManager.q,
	})

	if err := app.waitForFileProcessingDrain(lifecycleCtx); err != nil {
		slog.Error("discovery drain cancelled during shutdown", "err", err)
		return lifecycleCtx.Err()
	}

	// Persist the completed run's counters so a skip-startup-discovery restart
	// can hydrate them. Skipped on drain cancel above; a persist failure is
	// logged and does not fail discovery.
	if err := app.persistFileProcessingStats(lifecycleCtx); err != nil {
		slog.Error("failed to persist file processing stats", "err", err)
	}

	if app.dbRwPool == nil {
		slog.Error("cannot rebuild file_folder_index: RW pool is nil")
		return nil
	}

	rebuild := app.testSeams.RebuildFileFolderIndex
	if rebuild == nil {
		rebuild = func(ctx context.Context, db *dbconnpool.DbSQLConnPool) error {
			if app.SubsystemManager.unifiedBatcher == nil {
				return fmt.Errorf("%w: unified batcher is nil", files.ErrFolderIndexRebuild)
			}
			return files.RebuildFileFolderIndex(ctx, db, app.dbRoPool, app.SubsystemManager.unifiedBatcher)
		}
	}
	if err := rebuild(lifecycleCtx, app.dbRwPool); err != nil {
		slog.Error("failed to rebuild file_folder_index", "err", err)
		if errors.Is(err, context.Canceled) {
			return lifecycleCtx.Err()
		}
		// Non-cancel rebuild failures are already wrapped with
		// files.ErrFolderIndexRebuild; return them so the startup goroutine can
		// Shutdown instead of exec'ing a skip-discovery child.
		return err
	}

	return nil
}
