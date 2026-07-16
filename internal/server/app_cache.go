package server

import (
	"log/slog"

	"github.com/lbe/sfpg-go/internal/cachelite"
)

// initializeHTTPCache creates the HTTP cache middleware if caching is enabled.
func (app *App) initializeHTTPCache() {
	app.ConfigManager.ConfigMu.RLock()
	cfg := app.ConfigManager.Config
	app.ConfigManager.ConfigMu.RUnlock()
	if cfg == nil {
		return
	}
	app.InitializeHTTPCache(cfg)
}

// InvalidateHTTPCache clears all HTTP cache entries. Called when ETag version changes
// to avoid serving stale responses that may contain old cache-busting URLs.
func (app *App) InvalidateHTTPCache() {
	app.InfrastructureService.InvalidateHTTPCache()
}

func (app *App) scheduleStaleCacheDrop(trigger string) {
	if app.dbRwPool == nil {
		return
	}
	if !app.RuntimeManager.staleCacheDropInFlight.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer app.RuntimeManager.staleCacheDropInFlight.Store(false)

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
