package server

import (
	"context"
	"log/slog"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

const (
	cacheCalibrationQuietPendingMax = int64(100)
)

// CalibrateCacheSizeNow runs the cache size SUM immediately (CLI / tests).
func (s *InfrastructureService) CalibrateCacheSizeNow(ctx context.Context) {
	s.calibrateCacheSize(ctx, "immediate")
}

// calibrateCacheSize queries the RO pool and replaces the in-memory counters.
// Returns true when calibration succeeded.
func (s *InfrastructureService) calibrateCacheSize(ctx context.Context, reason string) bool {
	if s.dbRoPool == nil {
		return false
	}

	size, err := s.testSeams.GetCacheSizeBytes(ctx, s.dbRoPool)
	if err != nil {
		slog.Warn("cache size calibration failed", "reason", reason, "err", err)
		return false
	}

	count, countErr := s.testSeams.GetCacheEntryCount(ctx, s.dbRoPool)
	if countErr != nil {
		slog.Warn("cache entry count calibration failed, storing 0", "reason", reason, "err", countErr)
	}

	s.cacheSizeBytes.Store(size)
	s.cacheEntryCount.Store(count)
	slog.Info("cache size counter calibrated", "reason", reason, "bytes", size, "entries", count)
	return true
}

// resyncCacheSizeFromDB refreshes the counters after a failed cache batch flush.
func (s *InfrastructureService) resyncCacheSizeFromDB(ctx context.Context) {
	if s.dbRoPool == nil {
		return
	}
	size, err := s.testSeams.GetCacheSizeBytes(ctx, s.dbRoPool)
	if err != nil {
		slog.Warn("failed to resync cache size counter after batch error", "err", err)
		return
	}
	count, countErr := s.testSeams.GetCacheEntryCount(ctx, s.dbRoPool)
	if countErr != nil {
		slog.Warn("failed to resync cache entry count after batch error", "err", countErr)
	}
	s.cacheSizeBytes.Store(size)
	s.cacheEntryCount.Store(count)
}

// startHTTPCacheBaselines launches two parallel goroutines to populate the
// HTTP cache entry count and size byte counters from the database. Each
// goroutine increments cacheBaselineRunning synchronously before launching
// so that display helpers see running > 0 immediately (rendering "N/A" for
// unpopulated fields).
func (s *InfrastructureService) startHTTPCacheBaselines(ctx context.Context) {
	if s.dbRoPool == nil {
		return
	}
	s.cacheSizeBytes.Store(0)
	s.cacheEntryCount.Store(0)

	run := func(name string, fn func(context.Context, *dbconnpool.CpConn) error) {
		s.cacheBaselineRunning.Add(1)
		go func() {
			defer s.cacheBaselineRunning.Add(-1)
			cpc, err := s.dbRoPool.Get()
			if err != nil {
				slog.Warn("http cache baseline: connection failed", "query", name, "err", err)
				return
			}
			defer s.dbRoPool.Put(cpc)
			if err := fn(ctx, cpc); err != nil {
				slog.Warn("http cache baseline failed", "query", name, "err", err)
			}
		}()
	}

	run("entry_count", func(ctx context.Context, cpc *dbconnpool.CpConn) error {
		n, err := cpc.Queries.CountHttpCacheEntries(ctx)
		if err != nil {
			return err
		}
		s.cacheEntryCount.Add(n)
		return nil
	})
	run("size_bytes", func(ctx context.Context, cpc *dbconnpool.CpConn) error {
		n, err := cpc.Queries.GetHttpCacheSizeBytes(ctx)
		if err != nil {
			return err
		}
		s.cacheSizeBytes.Add(n)
		return nil
	})
}
