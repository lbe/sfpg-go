package cachelite_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cachelite "github.com/lbe/sfpg-go/internal/cachelite"
)

// TestContentTypePreservation verifies that Content-Type is preserved through cache.
func TestContentTypePreservation(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("fake-jpeg-binary-data"))
	})

	cfg := cachelite.CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/photo.jpg", "/photo2.jpg", "/photo3.jpg", "/photo4.jpg", "/photo5.jpg", "/photo6.jpg", "/photo7.jpg", "/photo8.jpg"},
	}

	cacheMW := cachelite.NewHTTPCacheMiddleware(db, cfg, nil, nil)
	mw := cacheMW.Middleware(handler)

	// First request (MISS)
	req := httptest.NewRequest("GET", "/photo.jpg", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", ct)
	}

	// Second request (HIT) - verify Content-Type preserved from cache
	req2 := httptest.NewRequest("GET", "/photo.jpg", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if w2.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("cached Content-Type = %q, want image/jpeg", w2.Header().Get("Content-Type"))
	}
}

// TestRangeRequest_NoCompression verifies compression is disabled for Range requests.
func TestRangeRequest_NoCompression(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Accept-Ranges", "bytes")

		// Simplified range handling for test
		rangeHdr := r.Header.Get("Range")
		if rangeHdr != "" {
			w.WriteHeader(206)
			_, _ = w.Write(payload)
			return
		}

		w.WriteHeader(200)
		_, _ = w.Write(payload)
	})

	cfg := cachelite.CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}

	cacheMW := cachelite.NewHTTPCacheMiddleware(db, cfg, nil, nil)
	mw := cacheMW.Middleware(handler)

	// Request with Range + Accept-Encoding
	req := httptest.NewRequest("GET", "/photo.jpg", nil)
	req.Header.Set("Range", "bytes=0-10")
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != 206 {
		t.Fatalf("status = %d, want 206", w.Code)
	}
	if ce := w.Header().Get("Content-Encoding"); ce == "gzip" {
		t.Errorf("Content-Encoding = %q, should NOT be gzip for Range request", ce)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", ct)
	}
}

// TestHEADRequest_CacheHeaders verifies HEAD returns correct headers without body.
func TestHEADRequest_CacheHeaders(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		if r.Method == "GET" {
			_, _ = w.Write([]byte("<html>test</html>"))
		}
	})

	cfg := cachelite.CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}

	cacheMW := cachelite.NewHTTPCacheMiddleware(db, cfg, nil, nil)
	mw := cacheMW.Middleware(handler)

	// First GET to populate cache
	req1 := httptest.NewRequest("GET", "/page.html", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	// HEAD request should not have body
	req2 := httptest.NewRequest("HEAD", "/page.html", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("HEAD status = %d, want 200", w2.Code)
	}
	if ct := w2.Header().Get("Content-Type"); ct != "text/html" {
		t.Errorf("HEAD Content-Type = %q, want text/html", ct)
	}
	if w2.Body.Len() != 0 {
		t.Errorf("HEAD body length = %d, want 0", w2.Body.Len())
	}
}

// TestEmptyBody verifies empty responses are handled correctly.
func TestEmptyBody(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		// No body written
	})

	cfg := cachelite.CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}

	cacheMW := cachelite.NewHTTPCacheMiddleware(db, cfg, nil, nil)
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/empty", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("HEAD status = %d, want 200", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("empty body length = %d, want 0", w.Body.Len())
	}
}

// TestSmallBody verifies tiny payloads preserve Content-Type.
func TestSmallBody(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("A"))
	})

	cfg := cachelite.CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}

	cacheMW := cachelite.NewHTTPCacheMiddleware(db, cfg, nil, nil)
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/tiny", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	body := w.Body.Bytes()
	if len(body) == 0 {
		t.Errorf("body length = 0, expected at least 1 byte")
	}
}

// TestWeakETag_304 verifies weak ETag matching for 304 responses.
func TestWeakETag_304(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", `W/"weak-123"`)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("content"))
	})

	cfg := cachelite.CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}

	cacheMW := cachelite.NewHTTPCacheMiddleware(db, cfg, nil, nil)
	mw := cacheMW.Middleware(handler)

	// First request to populate cache
	req1 := httptest.NewRequest("GET", "/weak", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if w1.Code != 200 {
		t.Fatalf("first status = %d, want 200", w1.Code)
	}
	etag := w1.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("ETag empty on first request")
	}

	// Second request with weak ETag
	req2 := httptest.NewRequest("GET", "/weak", nil)
	req2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if w2.Code != 304 {
		t.Fatalf("second status = %d, want 304", w2.Code)
	}
	if w2.Body.Len() != 0 {
		t.Errorf("304 body length = %d, want 0", w2.Body.Len())
	}
}

// TestCacheIntegrity verifies cached content matches original.
func TestCacheIntegrity(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	originalContent := "This is test content that should be cached"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(originalContent))
	})

	cfg := cachelite.CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}

	cacheMW := cachelite.NewHTTPCacheMiddleware(db, cfg, nil, nil)
	mw := cacheMW.Middleware(handler)

	// First request (MISS)
	req1 := httptest.NewRequest("GET", "/test", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if w1.Code != 200 {
		t.Fatalf("MISS status = %d, want 200", w1.Code)
	}
	body1 := w1.Body.String()
	if body1 != originalContent {
		t.Errorf("MISS body = %q, want %q", body1, originalContent)
	}

	// Second request (HIT) - verify cached content matches
	req2 := httptest.NewRequest("GET", "/test", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	body2 := w2.Body.String()
	if body2 != originalContent {
		t.Errorf("HIT body = %q, want %q", body2, originalContent)
	}
}

// TestIdentityEncoding_NoCompression verifies identity encoding prevents compression.
func TestIdentityEncoding_NoCompression(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	content := strings.Repeat("test", 100)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(content))
	})

	cfg := cachelite.CacheConfig{
		Enabled:      true,
		MaxEntrySize: 10 * 1024 * 1024,
		MaxTotalSize: 500 * 1024 * 1024,
		DefaultTTL:   time.Hour,
	}

	cacheMW := cachelite.NewHTTPCacheMiddleware(db, cfg, nil, nil)
	mw := cacheMW.Middleware(handler)

	// Request with identity encoding
	req := httptest.NewRequest("GET", "/identity", nil)
	req.Header.Set("Accept-Encoding", "identity")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ce := w.Header().Get("Content-Encoding"); ce != "" && ce != "identity" {
		t.Errorf("Content-Encoding = %q, should be empty or identity", ce)
	}
	if w.Body.String() != content {
		t.Errorf("body mismatch, got %d bytes want %d", w.Body.Len(), len(content))
	}
}
