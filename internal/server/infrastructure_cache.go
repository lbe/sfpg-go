package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// cacheMiddlewareForEvict is the subset of *cachelite.HTTPCacheMiddleware used by maybeEvictCacheEntries.
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

		// CacheableRoutes lists the only paths whose responses are stored in the
		// HTTP cache. The bodycodec decode path is content-type-agnostic, but the
		// cached routes are all text/html; keep this list in sync with the actual
		// response types served by the handlers.
		CacheableRoutes: []string{
			"/gallery/", "/lightbox/", "/info/folder/", "/info/image/",
		},
		OnGalleryCacheHit: func(ctx context.Context, folderID int64, sessionID string) {
			// preloadManager callback set later via SetCacheOnGalleryHit
		},
		SessionCookieName:     session.SessionName,
		SkipPreloadWhenHeader: cachepreload.InternalPreloadHeader,
		SkipPreloadWhenValue:  "true",
	}
	var submitFunc func(*cachelite.HTTPCacheEntry)
	if s.writeBatcher != nil {
		submitFunc = s.submitCacheWrite
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

func (s *InfrastructureService) submitCacheWrite(entry *cachelite.HTTPCacheEntry) {
	if s.writeBatcher == nil {
		slog.Warn("unified batcher not available, dropping cache write", "path", entry.Path)
		cachelite.PutHTTPCacheEntry(entry)
		return
	}

	err := s.writeBatcher.Submit(BatchedWrite{CacheEntry: entry})
	if errors.Is(err, writebatcher.ErrFull) {
		slog.Warn("unified batcher full, dropping cache write",
			"path", entry.Path,
			"pending", s.writeBatcher.PendingCount())
	}
	if err != nil {
		slog.Debug("failed to submit cache write", "path", entry.Path, "err", err)
		cachelite.PutHTTPCacheEntry(entry)
	}
}

func (s *InfrastructureService) maybeEvictCacheEntries(batch []BatchedWrite) {
	hasCacheEntries := false
	for _, bw := range batch {
		if bw.CacheEntry != nil {
			hasCacheEntries = true
			break
		}
	}
	if !hasCacheEntries || s.cacheMWForEvict == nil {
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
