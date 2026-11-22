package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/session"
)

// cacheMiddlewareForEvict is the subset of *cachelite.HTTPCacheMiddleware used by cacheStored.
type cacheMiddlewareForEvict interface {
	Config() cachelite.CacheConfig
}

// cacheRotator abstracts cachelite.RotateCacheTable.
type cacheRotator interface {
	RotateCacheTable(ctx context.Context, pool *dbconnpool.DbSQLConnPool) error
}

// defaultCacheRotator is the production implementation of cacheRotator.
type defaultCacheRotator struct{}

func (defaultCacheRotator) RotateCacheTable(ctx context.Context, pool *dbconnpool.DbSQLConnPool) error {
	return cachelite.RotateCacheTable(ctx, pool)
}

// =====================================================================
// HTTP cache
// =====================================================================

// InitializeHTTPCache creates HTTP cache middleware when caching is enabled in config.
func (s *InfrastructureService) InitializeHTTPCache(config *config.Config) {
	if config == nil || !config.EnableHTTPCache {
		return
	}

	// Configure body codec at startup (empty OK — ConfigureBodyCodec maps "" → "zstd-1").
	codec := config.HTTPCacheBodyCodec
	if err := cachelite.ConfigureBodyCodec(codec); err != nil {
		slog.Error("invalid http_cache_body_codec", "codec", codec, "err", err)
	}

	cfg := cachelite.CacheConfig{
		Enabled:      true,
		MaxEntrySize: config.CacheMaxEntrySize,
		MaxTotalSize: config.CacheMaxSize,
		DefaultTTL:   config.CacheMaxTime,

		CacheableRoutes: cacheableRoutes(),
		OnGalleryCacheHit: func(ctx context.Context, folderID int64, sessionID string) {
			// preloadManager callback set later via SetCacheOnGalleryHit
		},
		SessionCookieName:     session.SessionName,
		SkipPreloadWhenHeader: cachepreload.InternalPreloadHeader,
		SkipPreloadWhenValue:  "true",
	}
	var submitFunc func(*cachelite.HTTPCacheEntry)
	if s.writer != nil {
		submitFunc = s.writer.SubmitCacheEntry
	}
	counters := &cachelite.HTTPCacheCounterState{
		SizeBytes:       &s.cacheSizeBytes,
		EntryCount:      &s.cacheEntryCount,
		BaselineRunning: &s.cacheBaselineRunning,
	}
	s.cacheMW = cachelite.NewHTTPCacheMiddleware(s.dbRoPool, cfg, counters, submitFunc)
	s.cacheMWForEvict = s.cacheMW
}

// SetCacheOnGalleryHit replaces the OnGalleryCacheHit callback (wired by
// SubsystemManager after preloadManager is created).
func (s *InfrastructureService) SetCacheOnGalleryHit(fn func(ctx context.Context, folderID int64, sessionID string)) {
	if s.cacheMW != nil {
		s.cacheMW.SetOnGalleryCacheHit(fn)
	}
}

// InvalidateHTTPCache rotates the HTTP cache table to drop all cached responses.
func (s *InfrastructureService) InvalidateHTTPCache() {
	if s.dbRwPool == nil {
		return
	}
	if err := s.cacheRotator.RotateCacheTable(context.Background(), s.dbRwPool); err != nil {
		slog.Error("failed to invalidate HTTP cache", "err", err)
		return
	}
	s.cacheSizeBytes.Store(0)
	s.cacheEntryCount.Store(0)
}

// cacheStored is the writer's callback after a batch that stored cache
// entries commits: it counts them and evicts the oldest entries when the
// cache is over its size limit.
func (s *InfrastructureService) cacheStored(bytes, entries int64) {
	s.cacheSizeBytes.Add(bytes)
	s.cacheEntryCount.Add(entries)
	if s.cacheMWForEvict == nil {
		return
	}
	cfg := s.cacheMWForEvict.Config()
	if cfg.MaxTotalSize <= 0 {
		return
	}

	currentSize := s.cacheSizeBytes.Load()
	if currentSize > cfg.MaxTotalSize {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		targetFree := currentSize - cfg.MaxTotalSize + cfg.MaxTotalSize/10
		freed, deleted, evErr := s.testSeams.EvictLRU(ctx, s.dbRwPool, targetFree)
		if evErr != nil {
			slog.Warn("cache eviction failed", "err", evErr, "target", targetFree, "freed", freed)
		}
		if freed > 0 {
			s.cacheSizeBytes.Add(-freed)
		}
		if deleted > 0 {
			s.cacheEntryCount.Add(-deleted)
		}
	}
}

// cacheFlushFailed is the writer's callback after a batch with cache entries
// fails: the counters are read again from the database.
func (s *InfrastructureService) cacheFlushFailed() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s.resyncCacheSizeFromDB(ctx)
}

// cacheableRoutes returns the only paths whose responses the HTTP cache
// stores. The bodycodec decode path is content-type-agnostic, but these routes
// are all text/html; keep the list in sync with the response types the
// handlers serve.
func cacheableRoutes() []string {
	return []string{"/gallery/", "/lightbox/", "/info/folder/", "/info/image/"}
}

// preloadRoutes returns the routes the cache preloader warms: the cache's
// configured routes, or the default ones when the cache is off.
func (s *InfrastructureService) preloadRoutes() []string {
	if s.cacheMW != nil {
		return s.cacheMW.Config().CacheableRoutes
	}
	return cacheableRoutes()
}
