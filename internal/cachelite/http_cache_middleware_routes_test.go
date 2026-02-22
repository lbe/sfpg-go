package cachelite_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	cachelite "github.com/lbe/sfpg-go/internal/cachelite"
)

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

	mw := cachelite.NewHTTPCacheMiddleware(db, cfg, nil, nil).Middleware(handler)

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
