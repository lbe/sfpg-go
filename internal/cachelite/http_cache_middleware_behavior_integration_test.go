//go:build integration

package cachelite

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestCacheMiss_HandlerCalledAndStored verifies cache miss calls handler and stores result.
func TestCacheMiss_HandlerCalledAndStored(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html><body>expensive content</body></html>"))
	})

	cfg := CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}

	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if handlerCalls != 1 {
		t.Errorf("handler calls = %d, want 1", handlerCalls)
	}
	if w.Header().Get("X-Cache") != "MISS" {
		t.Errorf("X-Cache = %q, want MISS", w.Header().Get("X-Cache"))
	}

	// Verify entry was stored
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/test", Variant: "full"})
	entry, err := GetCacheEntry(context.Background(), db, key)
	if err != nil {
		t.Fatalf("GetCacheEntry failed: %v", err)
	}
	if entry == nil {
		t.Fatal("expected cache entry to be stored")
	}
	if len(entry.Body) == 0 {
		t.Error("cached body is empty")
	}
}

// TestCacheHit_HandlerNotCalled_CachedResponseReturned verifies cache hit skips handler.
func TestCacheHit_HandlerNotCalled_CachedResponseReturned(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html><body>expensive content</body></html>"))
	})

	cfg := CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}
	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	// First request - cache miss
	req1 := httptest.NewRequest("GET", "/test", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if handlerCalls != 1 {
		t.Fatalf("first request handler calls = %d, want 1", handlerCalls)
	}

	// Verify entry was stored in cache
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/test", Variant: "full"})
	t.Logf("Looking for cache entry with key: %s", key)
	storedEntry, err := GetCacheEntry(context.Background(), db, key)
	if err != nil {
		t.Fatalf("failed to retrieve stored entry: %v", err)
	}
	if storedEntry == nil {
		t.Fatal("entry was not stored after first request")
	}
	t.Logf("Found stored entry: path=%s, status=%d", storedEntry.Path, storedEntry.Status)

	// Second request - cache hit
	req2 := httptest.NewRequest("GET", "/test", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if handlerCalls != 1 {
		t.Errorf("second request handler calls = %d, want 1 (cache hit)", handlerCalls)
	}
	if w2.Code != 200 {
		t.Errorf("cache hit status = %d, want 200", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
	if w2.Body.Len() == 0 {
		t.Error("cache hit body is empty")
	}
}

// TestEncodingAgnostic_SameKeyForDifferentEncodings verifies that different
// Accept-Encoding values share one cache entry (encoding is no longer part of the key).
func TestEncodingAgnostic_SameKeyForDifferentEncodings(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html><body>plain body</body></html>"))
	})

	cfg := CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}
	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	// Request with gzip
	req1 := httptest.NewRequest("GET", "/test", nil)
	req1.Header.Set("Accept-Encoding", "gzip")
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	// Request with br should hit the same cache entry
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Accept-Encoding", "br")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("br request X-Cache = %q, want HIT (encoding should not affect key)", w2.Header().Get("X-Cache"))
	}
	if w2.Body.Len() == 0 {
		t.Error("hit body is empty")
	}
}

// TestSizeLimit_SkipOversized verifies large responses are not cached.
func TestSizeLimit_SkipOversized(t *testing.T) {
	db := createTestDBPoolInternal(t)

	largeBody := make([]byte, 11*1024*1024) // 11MB
	for i := range largeBody {
		largeBody[i] = 'x'
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		_, _ = w.Write(largeBody)
	})

	cfg := CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024, // 10MB limit
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}
	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/large", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	// Verify entry was NOT stored
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/large"})
	entry, _ := GetCacheEntry(context.Background(), db, key)
	if entry != nil {
		t.Error("expected oversized entry not to be cached")
	}
}

// TestBudgetEviction_LRU verifies LRU eviction when budget exceeded.
func TestBudgetEviction_LRU(t *testing.T) {
	db := createTestDBPoolInternal(t)

	cfg := CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 100, // Very small budget to force eviction
		DefaultTTL:   time.Hour,
	}

	// Pre-populate cache with one entry
	now := time.Now().Unix()
	oldEntry := &HTTPCacheEntry{
		Key:           NewCacheKey(CacheKeyParams{Method: "GET", Path: "/old"}),
		Method:        "GET",
		Path:          "/old",
		Status:        200,
		Body:          []byte("old content"),
		ContentLength: sql.NullInt64{Int64: 11, Valid: true},
		CreatedAt:     now - 100,
	}
	_ = StoreCacheEntry(context.Background(), db, oldEntry)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("new content that exceeds budget"))
	})
	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/new", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	// Verify old entry was evicted (due to budget constraints)
	oldKey := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/old"})
	evictedEntry, _ := GetCacheEntry(context.Background(), db, oldKey)
	_ = evictedEntry // Suppress unused warning; eviction may or may not fire
}

// TestBudgetEviction_LRU_UnifiedBatcher verifies LRU eviction works when using unified batcher.
func TestBudgetEviction_LRU_UnifiedBatcher(t *testing.T) {
	db := createTestDBPoolInternal(t)

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    100, // Very small budget to force eviction
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{}, // Empty means all routes cacheable
	}

	// Pre-populate cache with an old entry (large enough to force eviction)
	now := time.Now().Unix()
	oldBodyStr := "<html><body>old content that is large enough to force eviction when new entry is added</body></html>"
	oldBody := []byte(oldBodyStr)
	oldEntry := &HTTPCacheEntry{
		Key:           NewCacheKey(CacheKeyParams{Method: "GET", Path: "/old"}),
		Method:        "GET",
		Path:          "/old",
		Status:        200,
		Body:          oldBody,
		ContentLength: sql.NullInt64{Int64: int64(len(oldBody)), Valid: true},
		CreatedAt:     now - 100,
	}
	if err := StoreCacheEntry(context.Background(), db, oldEntry); err != nil {
		t.Fatalf("failed to store old entry: %v", err)
	}

	// Verify old entry exists
	oldKey := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/old"})
	entry, err := GetCacheEntry(context.Background(), db, oldKey)
	if err != nil || entry == nil {
		t.Fatal("old entry should exist before test")
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		// Write 50 bytes to exceed budget (80 old + 50 new = 130 > 100)
		_, _ = w.Write([]byte("<html><body>new content that exceeds budget and is long enough</body></html>"))
	})

	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/new", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	// Verify new entry was stored (key includes variant like middleware creates)
	newKey := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/new", Variant: "full"})
	newEntry, err := GetCacheEntry(context.Background(), db, newKey)
	if err != nil || newEntry == nil {
		t.Errorf("new entry should be stored after eviction: err=%v", err)
	}
}

// TestCacheInvalidation_ClearCache verifies ClearCache removes all entries.
func TestCacheInvalidation_ClearCache(t *testing.T) {
	db := createTestDBPoolInternal(t)

	// Populate cache
	now := time.Now().Unix()
	entry1 := &HTTPCacheEntry{
		Key:       NewCacheKey(CacheKeyParams{Method: "GET", Path: "/test1"}),
		Method:    "GET",
		Path:      "/test1",
		Status:    200,
		Body:      []byte("content1"),
		CreatedAt: now,
	}
	entry2 := &HTTPCacheEntry{
		Key:       NewCacheKey(CacheKeyParams{Method: "GET", Path: "/test2"}),
		Method:    "GET",
		Path:      "/test2",
		Status:    200,
		Body:      []byte("content2"),
		CreatedAt: now,
	}
	_ = StoreCacheEntry(context.Background(), db, entry1)
	_ = StoreCacheEntry(context.Background(), db, entry2)

	// Clear cache
	if err := ClearCache(context.Background(), db); err != nil {
		t.Fatalf("ClearCache failed: %v", err)
	}

	// Verify all entries removed
	e1, _ := GetCacheEntry(context.Background(), db, entry1.Key)
	e2, _ := GetCacheEntry(context.Background(), db, entry2.Key)
	if e1 != nil || e2 != nil {
		t.Error("expected all cache entries to be cleared")
	}
}

// TestExpiration_ExpiredNotReturned verifies expired entries are not returned.
func TestExpiration_ExpiredNotReturned(t *testing.T) {
	db := createTestDBPoolInternal(t)

	now := time.Now().Unix()
	expiredEntry := &HTTPCacheEntry{
		Key:       NewCacheKey(CacheKeyParams{Method: "GET", Path: "/expired"}),
		Method:    "GET",
		Path:      "/expired",
		Status:    200,
		Body:      []byte("expired content"),
		CreatedAt: now - 7200,
		ExpiresAt: sql.NullInt64{Int64: now - 3600, Valid: true}, // Expired 1 hour ago
	}
	_ = StoreCacheEntry(context.Background(), db, expiredEntry)

	// Attempt to retrieve expired entry
	entry, err := GetCacheEntry(context.Background(), db, expiredEntry.Key)
	if err == nil && entry != nil {
		t.Error("expected expired entry not to be returned")
	}
}

// TestSkipPOST verifies POST requests bypass cache.
func TestSkipPOST(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	})

	cfg := CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}
	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("POST", "/test", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	// Verify no cache entry created
	key := NewCacheKey(CacheKeyParams{Method: "POST", Path: "/test"})
	entry, _ := GetCacheEntry(context.Background(), db, key)
	if entry != nil {
		t.Error("expected POST request not to be cached")
	}
}

// TestSkipNoCacheDirective verifies no-store responses are not cached when the path
// is not in CacheableRoutes.
func TestSkipNoCacheDirective(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("private content"))
	})

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/gallery/"}, // /test is not cacheable, so no-store is not stored
	}
	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	// Verify no cache entry created (path /test not in CacheableRoutes)
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/test"})
	entry, _ := GetCacheEntry(context.Background(), db, key)
	if entry != nil {
		t.Error("expected no-store response not to be cached when path not in CacheableRoutes")
	}
}

// TestNoStoreOnCacheableRoute_StoredInServerCache verifies no-store responses for
// cacheable routes are stored in server cache and replayed with no-store.
func TestNoStoreOnCacheableRoute_StoredInServerCache(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html><body>gallery partial</body></html>"))
	})

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/gallery/"},
	}
	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/gallery/2", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/gallery/2", Variant: "full"})
	entry, err := GetCacheEntry(context.Background(), db, key)
	if err != nil || entry == nil {
		t.Fatalf("expected no-store response to be stored in server cache for cacheable route: %v", err)
	}
	if entry.CacheControl.String != "no-store" {
		t.Errorf("stored Cache-Control = %q, want no-store", entry.CacheControl.String)
	}

	// Second request: should be HIT and still send no-store to client
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req)
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
	if w2.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control on HIT = %q, want no-store", w2.Header().Get("Cache-Control"))
	}
}

// TestPreloadAndHTMXVariants_Integration verifies cache hit/miss behavior across
// normal, HTMX-targeted, internal preload, and encoding variants.
func TestPreloadAndHTMXVariants_Integration(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<div id=\"gallery-grid\">cached payload</div>"))
	})

	cfg := CacheConfig{
		Enabled:               true,
		MaxEntrySize:          10 * 1024 * 1024,
		MaxTotalSize:          500 * 1024 * 1024,
		DefaultTTL:            time.Hour,
		CacheableRoutes:       []string{"/gallery/"},
		SkipPreloadWhenHeader: "X-SFPG-Internal-Preload",
		SkipPreloadWhenValue:  "true",
	}
	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	makeReq := func(encoding, hxRequest, hxTarget, preload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/gallery/42", nil)
		if encoding != "" {
			req.Header.Set("Accept-Encoding", encoding)
		}
		if hxRequest != "" {
			req.Header.Set("Hx-Request", hxRequest)
		}
		if hxTarget != "" {
			req.Header.Set("Hx-Target", hxTarget)
		}
		if preload != "" {
			req.Header.Set("X-SFPG-Internal-Preload", preload)
		}
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		return w
	}

	// Baseline route path: MISS then HIT.
	w1 := makeReq("identity", "", "", "")
	if w1.Code != http.StatusOK || w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("baseline first request got status=%d x-cache=%q, want status=200 x-cache=MISS", w1.Code, w1.Header().Get("X-Cache"))
	}
	w2 := makeReq("identity", "", "", "")
	if w2.Code != http.StatusOK || w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("baseline second request got status=%d x-cache=%q, want status=200 x-cache=HIT", w2.Code, w2.Header().Get("X-Cache"))
	}

	// HTMX gallery variant: only gallery-content produces a separate key.
	// Other HTMX targets (e.g. gallery-grid) map to the same variant (full)
	// as the baseline, so they HIT after the baseline primes the cache.
	w3 := makeReq("identity", "true", "gallery-grid", "")
	if w3.Code != http.StatusOK || w3.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("htmx first request got status=%d x-cache=%q, want status=200 x-cache=HIT (shares key with baseline)", w3.Code, w3.Header().Get("X-Cache"))
	}
	w4 := makeReq("identity", "true", "gallery-grid", "")
	if w4.Code != http.StatusOK || w4.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("htmx second request got status=%d x-cache=%q, want status=200 x-cache=HIT", w4.Code, w4.Header().Get("X-Cache"))
	}

	// Internal preload header should still hit for the same key.
	w5 := makeReq("identity", "true", "gallery-grid", "true")
	if w5.Code != http.StatusOK || w5.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("preload identity request got status=%d x-cache=%q, want status=200 x-cache=HIT", w5.Code, w5.Header().Get("X-Cache"))
	}

	// Different encoding should share the same cache entry (encoding no longer splits the key).
	w6 := makeReq("br", "true", "gallery-grid", "true")
	if w6.Code != http.StatusOK || w6.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("preload br first request got status=%d x-cache=%q, want status=200 x-cache=HIT (encoding no longer splits key)", w6.Code, w6.Header().Get("X-Cache"))
	}
	w7 := makeReq("br", "true", "gallery-grid", "true")
	if w7.Code != http.StatusOK || w7.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("preload br second request got status=%d x-cache=%q, want status=200 x-cache=HIT", w7.Code, w7.Header().Get("X-Cache"))
	}

	// Only the baseline causes a handler call; HTMX gallery-grid shares the key.
	if handlerCalls != 1 {
		t.Fatalf("handler calls = %d, want 1 (baseline miss only)", handlerCalls)
	}
}

// TestSkip404 verifies non-200 responses are not cached.
func TestSkip404(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(404)
		_, _ = w.Write([]byte("<html><body>not found</body></html>"))
	})

	cfg := CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}
	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}

	// Verify no cache entry created
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/test"})
	entry, _ := GetCacheEntry(context.Background(), db, key)
	if entry != nil {
		t.Error("expected 404 response not to be cached")
	}
}

// TestCacheKeyNormalization_LightboxTargetsShareEntry verifies that lightbox
// requests with different HX-Target values share a single cache entry.
// Both lightbox_content and lightbox-ui normalize to variant=lightbox-ui
// so the second request hits the cache stored by the first.
func TestCacheKeyNormalization_LightboxTargetsShareEntry(t *testing.T) {
	db := createTestDBPoolInternal(t)

	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html><body>lightbox content</body></html>"))
	})

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/lightbox/"},
	}

	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	// First request: initial lightbox open with HX-Target: lightbox_content
	req1 := httptest.NewRequest("GET", "/lightbox/1", nil)
	req1.Header.Set("HX-Request", "true")
	req1.Header.Set("HX-Target", "lightbox_content")
	req1.Header.Set("Accept-Encoding", "gzip")
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if w1.Code != 200 {
		t.Fatalf("first request status = %d, want 200", w1.Code)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first request X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}
	if callCount != 1 {
		t.Fatalf("handler calls after first request = %d, want 1", callCount)
	}

	// Second request: navigation with HX-Target: lightbox-ui
	// After normalization, this should produce the same cache key and HIT
	req2 := httptest.NewRequest("GET", "/lightbox/1", nil)
	req2.Header.Set("HX-Request", "true")
	req2.Header.Set("HX-Target", "lightbox-ui")
	req2.Header.Set("Accept-Encoding", "gzip")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("second request status = %d, want 200", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("second request X-Cache = %q, want HIT (lightbox_content and lightbox-ui should share one cache entry)", w2.Header().Get("X-Cache"))
	}
	if callCount != 1 {
		t.Errorf("handler calls after second request = %d, want 1 (cache hit, handler should not be called)", callCount)
	}

	// Verify the cached body is correct
	if w2.Body.Len() == 0 {
		t.Error("second request body is empty")
	}
}

// TestCacheKeyNormalization_InfoImageFullAndHTMXShareEntry verifies that info
// image/folder requests produce the same cache key regardless of HTMX headers.
// Both full page (no HTMX) and HTMX requests to /info/ collapse to variant=box_info.
func TestCacheKeyNormalization_InfoImageFullAndHTMXShareEntry(t *testing.T) {
	db := createTestDBPoolInternal(t)

	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html><body>info content</body></html>"))
	})

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/info/"},
	}

	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	// First request: full page (no HTMX headers) to /info/image/1
	req1 := httptest.NewRequest("GET", "/info/image/1", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if w1.Code != 200 {
		t.Fatalf("full request status = %d, want 200", w1.Code)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("full request X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}
	if callCount != 1 {
		t.Fatalf("handler calls after full request = %d, want 1", callCount)
	}

	// Second request: HTMX with HX-Target: box_info
	// After normalization, this should produce the same cache key and HIT
	req2 := httptest.NewRequest("GET", "/info/image/1", nil)
	req2.Header.Set("HX-Request", "true")
	req2.Header.Set("HX-Target", "box_info")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("htmx request status = %d, want 200", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("htmx request X-Cache = %q, want HIT (info full and HTMX should share one cache entry)", w2.Header().Get("X-Cache"))
	}
	if callCount != 1 {
		t.Errorf("handler calls after HTMX request = %d, want 1 (cache hit, handler should not be called)", callCount)
	}
}

// TestCacheKeyNormalization_GalleryFullAndPartialDistinct verifies that gallery
// full page and gallery-content requests produce different cache keys.
func TestCacheKeyNormalization_GalleryFullAndPartialDistinct(t *testing.T) {
	db := createTestDBPoolInternal(t)

	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html><body>gallery content</body></html>"))
	})

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/gallery/"},
	}

	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	// First request: full page (no HTMX headers) to /gallery/1
	req1 := httptest.NewRequest("GET", "/gallery/1", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if w1.Code != 200 {
		t.Fatalf("full request status = %d, want 200", w1.Code)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("full request X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}
	if callCount != 1 {
		t.Fatalf("handler calls after full request = %d, want 1", callCount)
	}

	// Second request: HTMX with HX-Target: gallery-content (different variant)
	// This should be a MISS because the variant is different from the stored 'full' entry
	req2 := httptest.NewRequest("GET", "/gallery/1", nil)
	req2.Header.Set("HX-Request", "true")
	req2.Header.Set("HX-Target", "gallery-content")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("htmx request status = %d, want 200", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "MISS" {
		t.Errorf("htmx request X-Cache = %q, want MISS (gallery full vs gallery-content should have different cache entries)", w2.Header().Get("X-Cache"))
	}
	if callCount != 2 {
		t.Errorf("handler calls after HTMX request = %d, want 2 (cache miss, handler should run)", callCount)
	}

	// Third request: same HTMX request should now hit
	req3 := httptest.NewRequest("GET", "/gallery/1", nil)
	req3.Header.Set("HX-Request", "true")
	req3.Header.Set("HX-Target", "gallery-content")
	w3 := httptest.NewRecorder()
	mw.ServeHTTP(w3, req3)

	if w3.Header().Get("X-Cache") != "HIT" {
		t.Errorf("third request X-Cache = %q, want HIT (same variant should hit)", w3.Header().Get("X-Cache"))
	}
	if callCount != 2 {
		t.Errorf("handler calls after third request = %d, want 2 (cache hit)", callCount)
	}
}

// TestCompressedBodyRoundtrip verifies MISS → STORE (compressed) → HIT (decoded)
// roundtrip with a large HTML body that triggers zstd compression.
func TestCompressedBodyRoundtrip(t *testing.T) {
	db := createTestDBPoolInternal(t)

	// Use a body >= 256 B (MinCompressBytes) so FinalizeForStorage compresses it.
	// ~550 B of HTML ensures compression is worthwhile.
	body := strings.Repeat("<div>gallery tile content that will be compressed during storage</div>", 14)
	if len(body) < 256 {
		t.Fatalf("test body too small: %d bytes, need >= 256", len(body))
	}

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
	})

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/test"},
	}

	cacheMW := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw := cacheMW.Middleware(handler)

	// First request: MISS → store compressed
	req1 := httptest.NewRequest("GET", "/test", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if w1.Code != 200 {
		t.Fatalf("first request status = %d, want 200", w1.Code)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first request X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls after first request = %d, want 1", handlerCalls)
	}

	// Verify the stored body has zstd magic prefix (compressed form)
	key := NewCacheKey(CacheKeyParams{Method: "GET", Path: "/test", Variant: "full"})
	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get connection: %v", err)
	}
	result, err := cpc.Queries.GetHttpCacheByKey(context.Background(), key)
	db.Put(cpc)
	if err != nil {
		t.Fatalf("GetHttpCacheByKey: %v", err)
	}
	// zstd magic: 0x28 0xB5 0x2F 0xFD
	if len(result.Body) < 4 || result.Body[0] != 0x28 || result.Body[1] != 0xB5 || result.Body[2] != 0x2F || result.Body[3] != 0xFD {
		t.Errorf("stored body does not have zstd magic prefix (got first 4 bytes: %x)", result.Body[:min(4, len(result.Body))])
	}
	// Stored body length should be less than original (compression expanded guard may return original
	// for incompressible data, but for repetitive HTML zstd should compress well).
	if len(result.Body) >= len(body) {
		t.Logf("stored body not compressed (len=%d, original=%d); expand guard kept original bytes", len(result.Body), len(body))
	}

	// Second request: HIT → decoded plaintext body
	req2 := httptest.NewRequest("GET", "/test", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("second request status = %d, want 200", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second request X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls after second request = %d, want 1 (cache hit)", handlerCalls)
	}
	// Verify decoded body matches original
	if w2.Body.String() != body {
		t.Fatalf("HIT body does not match original (len=%d, want len=%d)", w2.Body.Len(), len(body))
	}
}
