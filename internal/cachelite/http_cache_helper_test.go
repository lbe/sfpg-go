package cachelite

import (
	"sync/atomic"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

func newHTTPCacheMiddlewareForTest(
	db *dbconnpool.DbSQLConnPool,
	cfg CacheConfig,
	counters *HTTPCacheCounterState,
	submitFunc func(*HTTPCacheEntry),
) *HTTPCacheMiddleware {
	return &HTTPCacheMiddleware{
		db:         db,
		config:     cfg,
		counters:   counters,
		submitFunc: submitFunc,
		syncMode:   true,
	}
}

func httpCacheCountersForTest(size *atomic.Int64) *HTTPCacheCounterState {
	if size == nil {
		return nil
	}
	return &HTTPCacheCounterState{SizeBytes: size, EntryCount: &atomic.Int64{}, BaselineRunning: &atomic.Int32{}}
}
