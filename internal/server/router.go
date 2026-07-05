// Package server provides the core HTTP server, routing, middleware, and
// handlers for the web application. This file holds route registration.
package server

import (
	"net/http"
	"net/http/pprof"

	"github.com/lbe/sfpg-go/internal/server/middleware"
	"github.com/lbe/sfpg-go/web"
)

// getRouter sets up the application's HTTP routes, including static assets,
// authentication, configuration, and all content-serving endpoints.
func (app *App) getRouter() http.Handler {
	mux := http.NewServeMux()

	// Helper to selectively apply ConditionalMiddleware (ETag/304 handling).
	// We apply this to handlers that explicitly set ETag/Last-Modified headers
	// but do NOT stream large files (to avoid memory buffering).
	// Per instructions: Applied to gallery, lightbox, info, and thumbnails.
	// Excluded from image and raw-image.
	withConditional := middleware.ConditionalMiddleware

	// Serve a tiny inlined favicon without auth to avoid auth spam on invalid sessions.
	// The SVG is kept as an inline string literal (rather than a template) because:
	// - It's a minimal, static 16x16 pixel image (simple gray rectangle)
	// - No dynamic data or conditional logic needed
	// - Performance-sensitive: executed on every missing favicon request
	// - Deployment simplicity: no external file or template parsing required
	// - Single-executable binary constraint: inline strings work seamlessly
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect width="16" height="16" fill="#888"/></svg>`))
	})

	// Serve static assets
	mux.Handle("GET /static/", http.FileServer(http.FS(web.FS)))

	// Use new split handler groups
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		app.authHandlers.Login(w, r)
	})
	mux.HandleFunc("GET /health", app.healthHandlers.Health)

	// Theme routes (available to all users, authenticated or not)
	mux.HandleFunc("GET /theme/modal", app.themeHandlers.ThemeModalHandler)
	mux.HandleFunc("POST /theme", app.themeHandlers.ThemePostHandler)

	mux.HandleFunc("GET /hamburger-menu", app.menuHandlers.HamburgerMenu)
	mux.HandleFunc("GET /login-form", app.authHandlers.LoginFormHandler)
	mux.HandleFunc("GET /logout-form", app.authHandlers.LogoutFormHandler)

	mux.Handle("POST /logout", app.authMiddleware(http.HandlerFunc(app.authHandlers.Logout)))

	cfgAuth := app.configHandlers.ConfigAuthMiddleware

	configRoutes := []struct {
		method  string
		pattern string
		handler http.HandlerFunc
	}{
		{http.MethodGet, "/config", app.configHandlers.ConfigGet},
		{http.MethodPost, "/config", app.configHandlers.ConfigPost},
		{http.MethodPost, "/config/themes", app.configThemesHandler.UpdateThemesHandler},
		{http.MethodPost, "/config/increment-etag", app.configETagHandler.ConfigIncrementETag},
		{http.MethodPost, "/config/export/to-file", app.configHandlers.ExportConfigToFileHandler},
		{http.MethodPost, "/config/import/preview", app.configHandlers.ImportConfigPreviewHandler},
		{http.MethodPost, "/config/import/commit", app.configHandlers.ImportConfigCommitHandler},
		{http.MethodPost, "/config/restore-last-known-good", app.configHandlers.RestoreLastKnownGoodHandler},
		{http.MethodPost, "/config/restart", app.configRestartHandler.RestartHandler},
	}
	for _, rt := range configRoutes {
		mux.Handle(rt.method+" "+rt.pattern, app.authMiddleware(cfgAuth(rt.handler)))
	}

	// Export download is the only config route that also uses conditional caching.
	mux.Handle("GET /config/export/download", withConditional(app.authMiddleware(cfgAuth(app.configHandlers.ExportConfigDownloadHandler))))

	authRoutes := []struct {
		method  string
		pattern string
		handler http.HandlerFunc
	}{
		{http.MethodGet, "/dashboard", app.dashboardHandlers.DashboardGet},
		{http.MethodGet, "/api/metrics", app.dashboardHandlers.MetricsJSON},
		{http.MethodPost, "/server/shutdown", app.serverHandlers.ServerShutdownPost},
		{http.MethodPost, "/server/discovery", app.serverHandlers.ServerDiscoveryPost},
		{http.MethodPost, "/server/cache-batch-load", app.serverHandlers.ServerCacheBatchLoadPost},
		{http.MethodPost, "/server/restart", app.configRestartHandler.RestartHandler},
	}
	for _, rt := range authRoutes {
		mux.Handle(rt.method+" "+rt.pattern, app.authMiddleware(http.HandlerFunc(rt.handler)))
	}

	mux.Handle("GET /", http.HandlerFunc(app.healthHandlers.RootRedirect))

	conditionalRoutes := []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"GET /gallery/{id}", app.galleryHandlers.GalleryByID},
		{"GET /image/{id}", app.galleryHandlers.ImageByID},
		{"GET /thumbnail/file/{id}", app.galleryHandlers.ThumbnailByID},
		{"GET /lightbox/{id}", app.galleryHandlers.LightboxByID},
		{"GET /info/folder/{id}", app.galleryHandlers.InfoBoxFolder},
		{"GET /info/image/{id}", app.galleryHandlers.InfoBoxImage},
	}
	for _, rt := range conditionalRoutes {
		mux.Handle(rt.pattern, withConditional(http.HandlerFunc(rt.handler)))
	}

	mux.Handle("GET /raw-image/{id}", http.HandlerFunc(app.galleryHandlers.RawImageByID))
	mux.Handle("GET /thumbnail/folder/{id}", http.HandlerFunc(app.galleryHandlers.FolderThumbnailByID))

	// Register pprof routes (protected by authentication)
	// These expose profiling data and should only be accessible to authenticated users
	mux.Handle("GET /debug/pprof/", app.authMiddleware(http.HandlerFunc(pprof.Index)))
	mux.Handle("GET /debug/pprof/cmdline", app.authMiddleware(http.HandlerFunc(pprof.Cmdline)))
	mux.Handle("GET /debug/pprof/profile", app.authMiddleware(http.HandlerFunc(pprof.Profile)))
	mux.Handle("GET /debug/pprof/symbol", app.authMiddleware(http.HandlerFunc(pprof.Symbol)))
	mux.Handle("GET /debug/pprof/trace", app.authMiddleware(http.HandlerFunc(pprof.Trace)))

	// Build middleware chain from innermost to outermost
	var handler http.Handler = mux

	// Layer 1: Cross-Origin protection - security layer applied first
	handler = middleware.CSRFProtection(handler)

	// Layer 2: Compression middleware (if enabled)
	// Read from app.config (runtime config), fall back to app.opt (startup CLI/env) if config not loaded
	app.configMu.RLock()
	enableCompression := false
	enableHTTPCache := false
	if app.config != nil {
		enableCompression = app.config.ServerCompressionEnable
		enableHTTPCache = app.config.EnableHTTPCache
	} else {
		// Fall back to app.opt if config not loaded yet (e.g., in tests)
		enableCompression = app.opt.EnableCompression.IsSet && app.opt.EnableCompression.Bool
		enableHTTPCache = app.opt.EnableHTTPCache.IsSet && app.opt.EnableHTTPCache.Bool
	}
	app.configMu.RUnlock()

	if enableCompression {
		handler = middleware.CompressMiddleware(handler)
	}

	// Layer 3: HTTP cache middleware (if enabled)
	if enableHTTPCache {
		if app.cacheMW != nil {
			handler = app.cacheMW.Middleware(handler)
		}
	}

	// Layer 5: Global logging middleware (outermost)
	handler = middleware.NewLoggingMiddleware(nil)(handler)

	return handler
}
