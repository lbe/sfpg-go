package cachelite_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	cachelite "github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

// createSyncSubmitFuncForRoutes creates a submit function for tests that executes synchronously.
func createSyncSubmitFuncForRoutes(db *dbconnpool.DbSQLConnPool) func(*cachelite.HTTPCacheEntry) {
	return func(entry *cachelite.HTTPCacheEntry) {
		ctx := context.Background()

		// Store entry directly (production batcher would handle eviction)
		// For tests, we skip eviction to avoid needing access to unexported fields
		// This is acceptable for unit tests that don't specifically test eviction behavior
		if err := cachelite.StoreCacheEntry(ctx, db, entry); err != nil {
			// Log errors in tests to help debugging
			fmt.Printf("StoreCacheEntry error for key %s: %v\n", entry.Key, err)
		}
		cachelite.PutHTTPCacheEntry(entry)
	}
}

// createTestMiddlewareForRoutes creates a test middleware with a synchronous submit function.
func createTestMiddlewareForRoutes(t *testing.T, db *dbconnpool.DbSQLConnPool, cfg cachelite.CacheConfig) *cachelite.HTTPCacheMiddleware {
	submitFunc := createSyncSubmitFuncForRoutes(db)
	return cachelite.NewHTTPCacheMiddlewareForTest(db, cfg, nil, submitFunc)
}

func TestCacheMiddleware_OnlyCachesSpecifiedRoutes(t *testing.T) {
	db := createTestDBPool(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})

	cfg := defaultConfig()
	cfg.CacheableRoutes = []string{"/cache-me"}

	cacheMW := createTestMiddlewareForRoutes(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	// Should cache /cache-me
	req := httptest.NewRequest("GET", "/cache-me", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Header().Get("X-Cache") != "MISS" {
		t.Errorf("expected X-Cache MISS for /cache-me, got %q", w.Header().Get("X-Cache"))
	}

	// Should NOT cache /do-not-cache
	req2 := httptest.NewRequest("GET", "/do-not-cache", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Cache") == "MISS" || w2.Header().Get("X-Cache") == "HIT" {
		t.Errorf("expected no X-Cache header for /do-not-cache, got %q", w2.Header().Get("X-Cache"))
	}
}
