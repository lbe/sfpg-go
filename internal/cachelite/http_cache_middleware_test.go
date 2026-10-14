package cachelite_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"

	cachelite "github.com/lbe/sfpg-go/internal/cachelite"
)

func createTestDBPool(t *testing.T) *dbconnpool.DbSQLConnPool {
	t.Helper()
	return cachelite.AcquireSharedTestPoolForTest(t)
}

func defaultConfig() cachelite.CacheConfig {
	return cachelite.CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/test", "/test1", "/test2", "/large", "/old", "/expired", "/cache", "/nocache", "/br", "/etag", "/gallery"},
	}
}

func createSyncSubmitFunc(db *dbconnpool.DbSQLConnPool) func(*cachelite.HTTPCacheEntry) {
	return func(entry *cachelite.HTTPCacheEntry) {
		ctx := context.Background()
		if err := cachelite.StoreCacheEntry(ctx, db, entry); err != nil {
			fmt.Printf("StoreCacheEntry error for key %s: %v\n", entry.Key, err)
		}
		cachelite.PutHTTPCacheEntry(entry)
	}
}

func createTestMiddlewareWithSubmit(t *testing.T, db *dbconnpool.DbSQLConnPool, cfg cachelite.CacheConfig) *cachelite.HTTPCacheMiddleware {
	t.Helper()
	submitFunc := createSyncSubmitFunc(db)
	return cachelite.NewHTTPCacheMiddlewareForTest(db, cfg, nil, submitFunc)
}
