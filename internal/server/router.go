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
		app.authHandlers.Login(w, r, app.GetETagVersion())
	})
	mux.HandleFunc("GET /health", app.healthHandlers.Health)

	// Theme routes (available to all users, authenticated or not)
	mux.HandleFunc("GET /theme/modal", app.themeHandlers.ThemeModalHandler)
	mux.HandleFunc("POST /theme", app.themeHandlers.ThemePostHandler)

	mux.Handle("POST /logout", app.authMiddleware(http.HandlerFunc(app.authHandlers.Logout)))
	mux.Handle("GET /config", app.authMiddleware(http.HandlerFunc(app.configHandlers.ConfigGet)))
	mux.Handle("POST /config", app.authMiddleware(http.HandlerFunc(app.configHandlers.ConfigPost)))
	mux.Handle("POST /config/increment-etag", app.authMiddleware(http.HandlerFunc(app.configHandlers.ConfigIncrementETag)))
	mux.Handle("GET /config/export/download", withConditional(app.authMiddleware(http.HandlerFunc(app.configHandlers.ExportConfigDownloadHandler))))
	mux.Handle("POST /config/export/to-file", app.authMiddleware(http.HandlerFunc(app.configHandlers.ExportConfigToFileHandler)))
	mux.Handle("POST /config/import/preview", app.authMiddleware(http.HandlerFunc(app.configHandlers.ImportConfigPreviewHandler)))
	mux.Handle("POST /config/import/commit", app.authMiddleware(http.HandlerFunc(app.configHandlers.ImportConfigCommitHandler)))
	mux.Handle("POST /config/restore-last-known-good", app.authMiddleware(http.HandlerFunc(app.configHandlers.RestoreLastKnownGoodHandler)))
	mux.Handle("POST /config/restart", app.authMiddleware(http.HandlerFunc(app.configHandlers.RestartHandler)))

	// Dashboard routes (protected by authentication)
	// GET /dashboard returns full page or partial based on HX-Request header
	mux.Handle("GET /dashboard", app.authMiddleware(http.HandlerFunc(app.dashboardHandlers.DashboardGet)))

	// Server management routes (protected by authentication)
	mux.Handle("POST /server/shutdown", app.authMiddleware(http.HandlerFunc(app.serverHandlers.ServerShutdownPost)))
	mux.Handle("POST /server/discovery", app.authMiddleware(http.HandlerFunc(app.serverHandlers.ServerDiscoveryPost)))
	mux.Handle("POST /server/cache-batch-load", app.authMiddleware(http.HandlerFunc(app.serverHandlers.ServerCacheBatchLoadPost)))

	mux.Handle("GET /", http.HandlerFunc(app.healthHandlers.RootRedirect))

	mux.Handle("GET /gallery/{id}", withConditional(http.HandlerFunc(app.galleryHandlers.GalleryByID)))
	mux.Handle("GET /image/{id}", withConditional(http.HandlerFunc(app.galleryHandlers.ImageByID)))
	mux.Handle("GET /raw-image/{id}", http.HandlerFunc(app.galleryHandlers.RawImageByID))
	mux.Handle("GET /thumbnail/file/{id}", withConditional(http.HandlerFunc(app.galleryHandlers.ThumbnailByID)))
	mux.Handle("GET /thumbnail/folder/{id}", http.HandlerFunc(app.galleryHandlers.FolderThumbnailByID))
	mux.Handle("GET /lightbox/{id}", withConditional(http.HandlerFunc(app.galleryHandlers.LightboxByID)))

	mux.Handle("GET /info/folder/{id}", withConditional(http.HandlerFunc(app.galleryHandlers.InfoBoxFolder)))
	mux.Handle("GET /info/image/{id}", withConditional(http.HandlerFunc(app.galleryHandlers.InfoBoxImage)))

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
