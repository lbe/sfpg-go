//go:build integration && benchmark

// cache_benchmark_integration_test.go: E2E benchmarks for Appendix B (Reduce HTTP Cache Preload Allocations).
// Exercises the allocation sites replaced by gensyncpool.
//
// Compare pre- vs post-Appendix B: run on current code, record allocs/op and B/op;
// apply Appendix B, run again, compare (expect fewer allocations).
//
// Run: go test -tags integration -bench=BenchmarkE2E -benchmem ./internal/cachelite/
package cachelite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

// createSyncSubmitForBenchmark creates a synchronous cache submit function for benchmarks.
func createSyncSubmitForBenchmark(db *dbconnpool.DbSQLConnPool) func(*HTTPCacheEntry) {
	return func(entry *HTTPCacheEntry) {
		ctx := context.Background()
		_ = StoreCacheEntry(ctx, db, entry)
		PutHTTPCacheEntry(entry)
	}
}

// BenchmarkE2E_CacheWritePath exercises the full HTTP cache write path:
// middleware cache miss -> HTTPCacheEntry allocation -> queue -> worker stores.
// Covers the allocation site that Appendix B replaces with HTTPCacheEntryPool.
// Run with: go test -tags integration -bench=BenchmarkE2E_CacheWritePath -benchmem
func BenchmarkE2E_CacheWritePath(b *testing.B) {
	db := createTestDBPoolTB(b)

	// Representative body size (2-5 KB range, ~46% of production)
	bodySize := 3500
	body := make([]byte, bodySize)
	for i := range body {
		body[i] = byte(i & 0xff)
	}

	cfg := CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
		CacheableRoutes: []string{
			"/gallery/",
			"/info/",
		},
	}

	var sizeCounter atomic.Int64
	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, HTTPCacheCountersForTest(&sizeCounter), createSyncSubmitForBenchmark(db))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	mw := cacheMW.Middleware(handler)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := "/gallery/" + strconv.Itoa(i)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
	}
	b.StopTimer()
}
