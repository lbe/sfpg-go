//go:build integration

// cache_appendix_b_benchmark_test.go: E2E benchmarks for Appendix B (Reduce HTTP Cache Preload Allocations).
// Exercises the allocation sites replaced by gensyncpool:
//   - BenchmarkE2E_CacheWritePath: HTTPCacheEntry (cache middleware)
//   - BenchmarkE2E_PreloadPath: PreloadTask (cache preload)
//
// Compare pre- vs post-Appendix B: run on current code, record allocs/op and B/op;
// apply Appendix B, run again, compare (expect fewer allocations).
//
// Run: go test -tags integration -bench=BenchmarkE2E -benchmem ./internal/server/
package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"go.local/sfpg/internal/cachelite"
	"go.local/sfpg/internal/server/cachepreload"
)

// BenchmarkE2E_CacheWritePath exercises the full HTTP cache write path:
// middleware cache miss -> HTTPCacheEntry allocation -> queue -> worker stores.
// Covers the allocation site that Appendix B replaces with HTTPCacheEntryPool.
// Run with: go test -tags integration -bench=BenchmarkE2E_CacheWritePath -benchmem
func BenchmarkE2E_CacheWritePath(b *testing.B) {
	app := CreateAppWithTB(b, false)
	defer app.Shutdown()

	// Representative body size (2-5 KB range, ~46% of production)
	bodySize := 3500
	body := make([]byte, bodySize)
	for i := range body {
		body[i] = byte(i & 0xff)
	}

	cfg := cachelite.CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
		CacheableRoutes: []string{
			"/gallery/",
			"/info/",
		},
	}

	cacheMW := cachelite.NewHTTPCacheMiddleware(app.dbRwPool, cfg, &app.cacheSizeBytes, app.submitCacheWrite)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	mw := cacheMW.Middleware(handler)

	defer app.writeBatcher.Close() // Drain and flush all pending cache writes

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := "/gallery/" + strconv.Itoa(i)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
	}
	b.StopTimer()

	// Cache flush handled by Close()
}

// BenchmarkE2E_PreloadPath exercises the full PreloadTask path:
// PreloadTask allocation -> Run (MakeInternalRequest) -> completion.
// Covers the allocation site that Appendix B replaces with preloadTaskPool.
// Run with: go test -tags integration -bench=BenchmarkE2E_PreloadPath -benchmem
func BenchmarkE2E_PreloadPath(b *testing.B) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("preload response"))
	})

	tracker := &cachepreload.TaskTracker{}
	metrics := &cachepreload.PreloadMetrics{}
	cfg := cachepreload.InternalRequestConfig{
		Handler:     handler,
		ETagVersion: "v1",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cacheKey := fmt.Sprintf("GET:/gallery/%d?v=v1|identity", i)
		path := fmt.Sprintf("/gallery/%d", i)
		tracker.RegisterTask(cacheKey, "bench-sess", "task-"+strconv.Itoa(i))

		task := cachepreload.GetPreloadTask()
		task.CacheKey = cacheKey
		task.Path = path
		task.TaskTracker = tracker
		task.RequestConfig = cfg
		task.Metrics = metrics

		_ = task.Run(context.Background())
	}
}
