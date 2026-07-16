//go:build integration && benchmark

// Preload benchmark for Appendix B (Reduce HTTP Cache Preload Allocations).
// Exercises the allocation site replaced by gensyncpool's PreloadTask pool.
//
// Run: go test -tags integration -bench=BenchmarkE2E_PreloadPath -benchmem ./internal/server/cachepreload/
package cachepreload

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

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

	tracker := &TaskTracker{}
	metrics := &PreloadMetrics{}
	cfg := InternalRequestConfig{
		Handler:     handler,
		ETagVersion: "v1",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cacheKey := fmt.Sprintf("GET:/gallery/%d?v=v1|identity", i)
		path := fmt.Sprintf("/gallery/%d", i)
		tracker.RegisterTask(cacheKey, "bench-sess", "task-"+strconv.Itoa(i))

		task := GetPreloadTask()
		task.CacheKey = cacheKey
		task.Path = path
		task.TaskTracker = tracker
		task.RequestConfig = cfg
		task.Metrics = metrics

		_ = task.Run(context.Background())
	}
}
