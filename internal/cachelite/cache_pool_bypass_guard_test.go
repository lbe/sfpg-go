package cachelite_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestGetCacheEntry_NoRawDBBypass is a static regression guard for the hot lookup path.
// It fails while GetCacheEntry uses gallerydb.New(db.DB()) directly.
func TestGetCacheEntry_NoRawDBBypass(t *testing.T) {
	t.Helper()

	src, err := os.ReadFile("cache.go")
	if err != nil {
		t.Fatalf("failed reading cache.go: %v", err)
	}

	text := string(src)
	start := strings.Index(text, "func GetCacheEntry(")
	if start == -1 {
		t.Fatal("GetCacheEntry function not found in cache.go")
	}
	end := strings.Index(text[start:], "\n// StoreCacheEntry inserts or updates a cache entry in the database.")
	if end == -1 {
		t.Fatal("could not locate end of GetCacheEntry function")
	}

	getCacheEntryBody := text[start : start+end]
	if strings.Contains(getCacheEntryBody, "gallerydb.New(db.DB())") {
		t.Fatalf("GetCacheEntry must not bypass DbSQLConnPool via raw db.DB(); found forbidden call in GetCacheEntry body")
	}
}

// TestCacheRuntimeHelpers_NoRawDBBypass is a static regression guard ensuring
// runtime helper functions do not instantiate queries with raw db.DB().
func TestCacheRuntimeHelpers_NoRawDBBypass(t *testing.T) {
	t.Helper()

	src, err := os.ReadFile("cache.go")
	if err != nil {
		t.Fatalf("failed reading cache.go: %v", err)
	}
	text := string(src)

	sections := map[string]string{
		"StoreCacheEntry":   "\n// StoreCacheEntryInTx stores a cache entry within an existing transaction.",
		"DeleteCacheEntry":  "\n// ClearCache deletes all cache entries from the database.",
		"ClearCache":        "\n// EvictLRU removes oldest cache entries until at least targetFreeBytes are available.",
		"EvictLRU":          "\n// GetCacheSizeBytes returns the total size of all cache entries in bytes.",
		"GetCacheSizeBytes": "\n// CountCacheEntries returns the number of entries in the cache.",
		"CountCacheEntries": "\n// CleanupExpired removes all expired cache entries from the database.",
		"CleanupExpired":    "\n// CanCacheResponse determines if an HTTP response is eligible for caching.",
	}

	for fnName, endMarker := range sections {
		start := strings.Index(text, "func "+fnName+"(")
		if start == -1 {
			t.Fatalf("%s function not found in cache.go", fnName)
		}
		end := strings.Index(text[start:], endMarker)
		if end == -1 {
			t.Fatalf("could not locate end marker for %s", fnName)
		}
		body := text[start : start+end]
		if strings.Contains(body, "gallerydb.New(db.DB())") {
			t.Fatalf("%s must not use gallerydb.New(db.DB()); use DbSQLConnPool Get/Put with CpConn queries", fnName)
		}
	}
}

// TestHTTPCacheMiddleware_ConcurrentLookupPath ensures checkCache/GetCacheEntry
// remains stable under preload-like concurrent reads to the same key.
func TestHTTPCacheMiddleware_ConcurrentLookupPath(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	var handlerCalls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls.Add(1)
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>concurrent cache payload</body></html>"))
	})

	cfg := defaultConfig()
	cfg.CacheableRoutes = []string{"/gallery"}
	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	// Prime cache so concurrent requests exercise lookup path.
	wPrime := httptest.NewRecorder()
	reqPrime := httptest.NewRequest(http.MethodGet, "/gallery/123", nil)
	mw.ServeHTTP(wPrime, reqPrime)
	if got := wPrime.Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("prime request X-Cache = %q, want MISS", got)
	}

	const workers = 24
	var wg sync.WaitGroup
	errCh := make(chan string, workers)

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/gallery/123", nil)
			req.Header.Set("Hx-Request", "true")
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				errCh <- "unexpected status: " + w.Result().Status
				return
			}
			xCache := w.Header().Get("X-Cache")
			if xCache != "HIT" && xCache != "MISS" {
				errCh <- "unexpected X-Cache value: " + xCache
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for errMsg := range errCh {
		t.Fatal(errMsg)
	}

	// Give async callbacks (if any) a tiny settle window to avoid goroutine tail effects.
	time.Sleep(10 * time.Millisecond)
	if handlerCalls.Load() < 1 {
		t.Fatal("handler was never called; expected at least cache-prime miss")
	}
}
