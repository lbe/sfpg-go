package server

import (
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/gallerydb"
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

// ensureHTTPCacheKeyFormatCurrent checks whether the persisted cache key
// format version matches the current code-level version. If the stored
// version is missing or < cachelite.CacheKeyFormatVersion, it invalidates
// the entire cache via table rotation and updates the stored version.
//
// Always persists the current version when the stored version differs,
// regardless of whether invalidation was triggered.
//
// Must be called from app_startup.go after ApplyConfig() and before
// initializeHTTPCache.
func (app *App) ensureHTTPCacheKeyFormatCurrent() {
	if app.dbRwPool == nil {
		slog.Warn("dbRwPool not available, skipping cache key format check")
		return
	}

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		slog.Error("failed to get RW connection for cache key format check", "err", err)
		return
	}
	defer app.dbRwPool.Put(cpcRw)

	ctx := app.getCtx()

	// Read stored format version (missing/empty = version 0 = legacy).
	storedVersion, err := cpcRw.Queries.GetConfigValueByKey(ctx, "http_cache_key_format_version")
	var storedVersionInt int
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("failed to read cache key format version from config", "err", err)
			return
		}
		// Key does not exist yet — treat as version 0 (legacy).
		storedVersionInt = 0
	} else if storedVersion != "" {
		storedVersionInt, err = strconv.Atoi(storedVersion)
		if err != nil {
			slog.Warn("invalid cache key format version in config, treating as 0", "stored", storedVersion, "err", err)
			storedVersionInt = 0
		}
	}

	needsInvalidate := storedVersionInt < cachelite.CacheKeyFormatVersion

	if needsInvalidate {
		slog.Info("cache key format version changed, invalidating HTTP cache",
			"stored_version", storedVersionInt,
			"current_version", cachelite.CacheKeyFormatVersion)
		app.InvalidateHTTPCache()
	}

	// Always persist current version when stored version differs.
	storedVersionStr := strconv.Itoa(storedVersionInt)
	if storedVersionStr != cachelite.CacheKeyFormatVersionString {
		now := time.Now().Unix()
		if err := cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
			Key:       "http_cache_key_format_version",
			Value:     cachelite.CacheKeyFormatVersionString,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			slog.Error("failed to persist cache key format version", "err", err)
		}
	}
}
