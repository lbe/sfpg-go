package cachelite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestSkipSetCookie_ResponseNotCached(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Set-Cookie", "session=abc123; Path=/; HttpOnly; Secure")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("sensitive content with cookie"))
	})

	cfg := defaultIntegrationConfig()
	mw := newHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	router := mw.Middleware(handler)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Errorf("first request X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}
	if w1.Header().Get("Set-Cookie") == "" {
		t.Error("expected Set-Cookie header to be passed through")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Cache") != "MISS" {
		t.Errorf("second request X-Cache = %q, want MISS (Set-Cookie must not be cached)", w2.Header().Get("X-Cache"))
	}

	params := CacheKeyParams{Method: "GET", Path: "/test", Variant: "full"}
	key := NewCacheKey(params)
	entry, _ := GetCacheEntry(context.Background(), db, key)
	if entry != nil {
		t.Fatalf("expected no cache entry for Set-Cookie response, got key=%s", entry.Key)
	}
}

func TestSkipSetCookie_NotCachedEvenWithNoStoreException(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Set-Cookie", "session=xyz; Path=/")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("content with no-store and cookie"))
	})

	cfg := defaultIntegrationConfig()
	mw := newHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	router := mw.Middleware(handler)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Errorf("first request X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Cache") != "MISS" {
		t.Errorf("second request X-Cache = %q, want MISS", w2.Header().Get("X-Cache"))
	}

	params := CacheKeyParams{Method: "GET", Path: "/test", Variant: "full"}
	key := NewCacheKey(params)
	entry, _ := GetCacheEntry(context.Background(), db, key)
	if entry != nil {
		t.Fatalf("expected no cache entry for no-store+Set-Cookie response, got key=%s", entry.Key)
	}
}

func TestBypassOnClientNoCache(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write([]byte("fresh"))
	})

	cfg := defaultIntegrationConfig()
	mw := newHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	router := mw.Middleware(handler)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.Header.Set("Cache-Control", "no-cache")
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	if w1.Header().Get("X-Cache") != "BYPASS" {
		t.Fatalf("X-Cache = %q, want BYPASS", w1.Header().Get("X-Cache"))
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls after bypass = %d, want 1", handlerCalls)
	}

	params := CacheKeyParams{Method: "GET", Path: "/test", Variant: "full"}
	key := NewCacheKey(params)
	if entry, _ := GetCacheEntry(context.Background(), db, key); entry != nil {
		t.Fatalf("expected no cache entry after bypass, got %#v", entry)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("second request X-Cache = %q, want MISS", w2.Header().Get("X-Cache"))
	}
	if handlerCalls != 2 {
		t.Fatalf("handler calls after second request = %d, want 2", handlerCalls)
	}
}

func TestHTTPCacheMiddleware_ComprehensiveBypass(t *testing.T) {
	db := createTestDBPoolInternal(t)

	cfg := defaultIntegrationConfig()
	cfg.DefaultTTL = 0

	var handlerCalls int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>fresh content</body></html>"))
	})

	mw := newHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	router := mw.Middleware(handler)

	tests := []struct {
		name         string
		cacheControl string
		shouldBypass bool
	}{
		{"no-cache", "no-cache", true},
		{"no-store", "no-store", true},
		{"max-age=0", "max-age=0", true},
		{"no-cache with value", "no-cache=set-cookie", true},
		{"compound no-cache", "public, no-cache", true},
		{"compound no-store", "no-store, public", true},
		{"compound max-age=0", "max-age=0, public", true},
		{"mixed case", "No-Cache", true},
		{"whitespace", " no-cache , max-age=3600 ", true},
		{"no bypass", "public, max-age=3600", false},
		{"empty cache control", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlerCalls = 0
			_ = ClearCache(context.Background(), db)

			req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.cacheControl != "" {
				req1.Header.Set("Cache-Control", tt.cacheControl)
			}
			w1 := httptest.NewRecorder()
			router.ServeHTTP(w1, req1)

			expectedStatus := "MISS"
			if tt.shouldBypass {
				expectedStatus = "BYPASS"
			}
			if got := w1.Header().Get("X-Cache"); got != expectedStatus {
				t.Fatalf("expected X-Cache %s, got %s", expectedStatus, got)
			}
			if handlerCalls != 1 {
				t.Fatalf("expected 1 handler call, got %d", handlerCalls)
			}

			req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.cacheControl != "" {
				req2.Header.Set("Cache-Control", tt.cacheControl)
			}
			w2 := httptest.NewRecorder()
			router.ServeHTTP(w2, req2)

			expectedStatus2 := "HIT"
			if tt.shouldBypass {
				expectedStatus2 = "BYPASS"
			}
			if got := w2.Header().Get("X-Cache"); got != expectedStatus2 {
				t.Fatalf("second request: expected X-Cache %s, got %s", expectedStatus2, got)
			}

			expectedCalls := 1
			if tt.shouldBypass {
				expectedCalls = 2
			}
			if handlerCalls != expectedCalls {
				t.Fatalf("expected %d handler calls after second request, got %d", expectedCalls, handlerCalls)
			}
		})
	}
}

func TestGalleryCacheHit_SkipWhenInternalPreload(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>gallery content</body></html>"))
	})

	callbackCalled := false
	var callbackMu sync.Mutex

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/gallery"},
		OnGalleryCacheHit: func(ctx context.Context, folderID int64, sessionID string) {
			callbackMu.Lock()
			callbackCalled = true
			callbackMu.Unlock()
		},
		SessionCookieName:     "session-name",
		SkipPreloadWhenHeader: "X-SFPG-Internal-Preload",
		SkipPreloadWhenValue:  "true",
	}

	mw := newHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	router := mw.Middleware(handler)

	req1 := httptest.NewRequest(http.MethodGet, "/gallery/99", nil)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/gallery/99", nil)
	req2.Header.Set("X-SFPG-Internal-Preload", "true")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}

	callbackMu.Lock()
	if callbackCalled {
		t.Error("callback was called when SkipPreloadWhenHeader matched")
	}
	callbackMu.Unlock()
}

func TestGalleryCacheHit_SessionIDFromRemoteAddr(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>gallery content</body></html>"))
	})

	sessionCh := make(chan string, 1)

	cfg := CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/gallery"},
		OnGalleryCacheHit: func(ctx context.Context, folderID int64, sessionID string) {
			sessionCh <- sessionID
		},
		SessionCookieName:     "session-name",
		SkipPreloadWhenHeader: "X-SFPG-Internal-Preload",
		SkipPreloadWhenValue:  "true",
	}

	mw := newHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	router := mw.Middleware(handler)

	req1 := httptest.NewRequest(http.MethodGet, "/gallery/7", nil)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/gallery/7", nil)
	req2.RemoteAddr = "192.168.1.100:54321"
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	select {
	case got := <-sessionCh:
		if got != "192.168.1.100:54321" {
			t.Errorf("sessionID = %q, want 192.168.1.100:54321", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OnGalleryCacheHit callback")
	}
}
