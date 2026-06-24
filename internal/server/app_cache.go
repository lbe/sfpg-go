package server

import (
	"context"
	"log/slog"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/session"
)

// initializeHTTPCache creates the HTTP cache middleware if caching is enabled.
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
		app.dbRoPool,
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
	ctx := app.getCtx()
	if err := cachelite.RotateCacheTable(ctx, app.dbRwPool); err != nil {
		slog.Error("failed to invalidate HTTP cache", "err", err)
		return
	}
	app.cacheSizeBytes.Store(0)
}

func (app *App) scheduleStaleCacheDrop(trigger string) {
	if app.dbRwPool == nil {
		return
	}
	if !app.staleCacheDropInFlight.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer app.staleCacheDropInFlight.Store(false)

		ctx := app.getCtx()

		dropped, err := cachelite.DropStaleCacheTableIfExists(ctx, app.dbRwPool)
		if err != nil {
			slog.Warn("failed to drop stale HTTP cache table", "trigger", trigger, "err", err)
			return
		}
		if !dropped {
			return
		}
		// Note: WAL checkpointing is now handled by writebatcher's OnAfterCommit callback
		// which runs every 5 minutes or when WAL file exceeds 256MB. This avoids race
		// conditions with writebatcher's active transactions.
		slog.Info("stale HTTP cache table dropped", "trigger", trigger)
	}()
}
