package server

import (
	"context"
	"log/slog"
	"time"
)

const (
	defaultCacheCalibrationPollInterval = 10 * time.Second
	defaultCacheCalibrationMaxWait      = time.Hour
	cacheCalibrationQuietPendingMax     = int64(100)
)

// CacheSizeQuietFunc reports whether the system is idle enough for a cache size SUM.
type CacheSizeQuietFunc func(ctx context.Context) bool

// ResetCacheSizeCounterAtStartup clears the in-memory counter. Calibration runs later.
func (s *InfrastructureService) ResetCacheSizeCounterAtStartup() {
	s.cacheSizeBytes.Store(0)
	s.cacheSizeCalibrated.Store(false)
}

// CacheSizeCalibrated reports whether the HTTP cache byte counter matches the database.
func (s *InfrastructureService) CacheSizeCalibrated() bool {
	return s.cacheSizeCalibrated.Load()
}

// SetCacheCalibrationListening enables quiet-based calibration after the HTTP server is listening.
func (s *InfrastructureService) SetCacheCalibrationListening(listening bool) {
	s.cacheCalibListening.Store(listening)
}

// CalibrateCacheSizeNow runs the cache size SUM immediately (CLI / tests).
func (s *InfrastructureService) CalibrateCacheSizeNow(ctx context.Context) {
	s.calibrateCacheSize(ctx, "immediate")
}

// StartCacheSizeCalibration schedules background calibration: when quiet after listen,
// or after maxWait from startup, whichever comes first.
func (s *InfrastructureService) StartCacheSizeCalibration(ctx context.Context, quiet CacheSizeQuietFunc, run func(func())) {
	if quiet == nil {
		return
	}
	if !s.cacheCalibStarted.CompareAndSwap(false, true) {
		return
	}

	s.ResetCacheSizeCounterAtStartup()

	scheduleQuietOnce(ctx, quietOnceConfig{
		PollInterval:  s.cacheCalibrationPollInterval(),
		MaxWait:       s.cacheCalibrationMaxWait(),
		Listening:     func() bool { return s.cacheCalibListening.Load() },
		Quiet:         quiet,
		Done:          func() bool { return s.cacheSizeCalibrated.Load() },
		PreAttempt:    nil,
		QuietReason:   "quiet",
		TimeoutReason: "timeout",
		Attempt:       func(ctx context.Context, r string) bool { return s.calibrateCacheSize(ctx, r) },
	}, run)
}

func (s *InfrastructureService) cacheCalibrationPollInterval() time.Duration {
	if s.testSeams.CacheCalibrationPollInterval > 0 {
		return s.testSeams.CacheCalibrationPollInterval
	}
	return defaultCacheCalibrationPollInterval
}

func (s *InfrastructureService) cacheCalibrationMaxWait() time.Duration {
	if s.testSeams.CacheCalibrationMaxWait > 0 {
		return s.testSeams.CacheCalibrationMaxWait
	}
	return defaultCacheCalibrationMaxWait
}

// calibrateCacheSize queries the RO pool and replaces the in-memory counter.
// Returns true when calibration succeeded.
func (s *InfrastructureService) calibrateCacheSize(ctx context.Context, reason string) bool {
	if s.cacheSizeCalibrated.Load() {
		return true
	}
	if s.dbRoPool == nil {
		return false
	}

	size, err := s.testSeams.GetCacheSizeBytes(ctx, s.dbRoPool)
	if err != nil {
		slog.Warn("cache size calibration failed", "reason", reason, "err", err)
		return false
	}

	s.cacheSizeBytes.Store(size)
	s.cacheSizeCalibrated.Store(true)
	slog.Info("cache size counter calibrated", "reason", reason, "bytes", size)
	return true
}

// resyncCacheSizeFromDB refreshes the counter after a failed cache batch flush.
func (s *InfrastructureService) resyncCacheSizeFromDB(ctx context.Context) {
	if s.dbRoPool == nil {
		return
	}
	size, err := s.testSeams.GetCacheSizeBytes(ctx, s.dbRoPool)
	if err != nil {
		slog.Warn("failed to resync cache size counter after batch error", "err", err)
		return
	}
	s.cacheSizeBytes.Store(size)
	s.cacheSizeCalibrated.Store(true)
}
