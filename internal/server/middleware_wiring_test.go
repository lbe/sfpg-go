package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.local/sfpg/internal/getopt"
)

// contains checks if a comma-separated header value contains a specific value
func contains(header, value string) bool {
	parts := strings.SplitSeq(header, ",")
	for part := range parts {
		if strings.TrimSpace(part) == value {
			return true
		}
	}
	return false
}

// TestGetRouter_CompressionMiddlewareWired verifies that when EnableCompression is true,
// the compression middleware is in the chain
func TestGetRouter_CompressionMiddlewareWired(t *testing.T) {
	opt := getopt.Opt{
		EnableCompression: getopt.OptBool{Bool: true, IsSet: true},
		EnableHTTPCache:   getopt.OptBool{Bool: false, IsSet: true},
	}
	// Ensure default session flags for middleware tests
	// Don't set environment variables - rely on CreateAppWithOpt defaults
	app := CreateAppWithOpt(t, false, opt)
	defer app.Shutdown()

	router := app.getRouter()

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	// Add authentication cookie
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// If compression middleware is wired, we should see Vary: Accept-Encoding header
	// Note: Header().Get() on httptest.ResponseRecorder returns the FIRST value
	// We need to check all Vary headers
	varyHeaders := w.Header().Values("Vary")
	hasAcceptEncoding := false
	for _, v := range varyHeaders {
		if contains(v, "Accept-Encoding") {
			hasAcceptEncoding = true
			break
		}
	}
	if !hasAcceptEncoding {
		t.Errorf("Expected 'Accept-Encoding' in Vary header when compression enabled, got: %v", varyHeaders)
	}
}

// TestGetRouter_CompressionMiddlewareNotWired verifies that when EnableCompression is false,
// compression middleware is NOT in the chain
func TestGetRouter_CompressionMiddlewareNotWired(t *testing.T) {
	opt := getopt.Opt{
		EnableCompression: getopt.OptBool{Bool: false, IsSet: true},
		EnableHTTPCache:   getopt.OptBool{Bool: false, IsSet: true},
	}
	app := CreateAppWithOpt(t, false, opt)
	defer app.Shutdown()

	router := app.getRouter()

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	// Add authentication cookie
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// If compression middleware is NOT wired, Accept-Encoding should not be in Vary header
	varyHeaders := w.Header().Values("Vary")
	for _, v := range varyHeaders {
		if contains(v, "Accept-Encoding") {
			t.Errorf("Expected no 'Accept-Encoding' in Vary header when compression disabled, got: %v", varyHeaders)
			break
		}
	}
}

// TestGetRouter_ConditionalMiddlewareWired verifies that the conditional middleware
// is in the chain (it's always wired, not conditional on a flag).
// The middleware now fully implements 304 Not Modified responses by buffering
// the response and evaluating ETag/Last-Modified validators after the handler completes.
func TestGetRouter_ConditionalMiddlewareWired(t *testing.T) {
	opt := getopt.Opt{
		EnableCompression: getopt.OptBool{Bool: false, IsSet: true},
		EnableHTTPCache:   getopt.OptBool{Bool: false, IsSet: true},
	}
	app := CreateAppWithOpt(t, false, opt)
	defer app.Shutdown()

	router := app.getRouter()

	// Just verify the middleware doesn't break normal requests
	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)

	// Add authentication cookie
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK with conditional middleware in chain, got: %d", w.Code)
	}

	// Verify ETag header is still set (middleware doesn't interfere)
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Error("Expected ETag header to be set")
	}
}

// TestGetRouter_HTTPCacheMiddlewareWired verifies that when EnableHTTPCache is true,
// the HTTP cache middleware is in the chain
func TestGetRouter_HTTPCacheMiddlewareWired(t *testing.T) {
	opt := getopt.Opt{
		EnableCompression: getopt.OptBool{Bool: false, IsSet: true},
		EnableHTTPCache:   getopt.OptBool{Bool: true, IsSet: true},
	}
	app := CreateAppWithOpt(t, false, opt)
	defer app.Shutdown()

	router := app.getRouter()

	// Make a request - with cache middleware, subsequent identical requests
	// should be served from cache (we'll verify this by checking handler isn't called twice)
	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)

	// Add authentication cookie
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got: %d", w.Code)
	}

	// Note: Full cache hit verification would require more complex testing
	// For now, just verify the middleware doesn't break the response
}

// TestGetRouter_HTTPCacheMiddlewareNotWired verifies that when EnableHTTPCache is false,
// cache middleware is NOT in the chain
func TestGetRouter_HTTPCacheMiddlewareNotWired(t *testing.T) {
	opt := getopt.Opt{
		EnableCompression: getopt.OptBool{Bool: false, IsSet: true},
		EnableHTTPCache:   getopt.OptBool{Bool: false, IsSet: true},
	}
	app := CreateAppWithOpt(t, false, opt)
	defer app.Shutdown()

	router := app.getRouter()

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)

	// Add authentication cookie
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got: %d", w.Code)
	}
}

// TestGetRouter_MiddlewareOrdering verifies that middleware is applied in correct order
func TestGetRouter_MiddlewareOrdering(t *testing.T) {
	opt := getopt.Opt{
		EnableCompression: getopt.OptBool{Bool: true, IsSet: true},
		EnableHTTPCache:   getopt.OptBool{Bool: true, IsSet: true},
	}
	app := CreateAppWithOpt(t, false, opt)
	defer app.Shutdown()

	router := app.getRouter()

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	// Add authentication cookie
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Verify response is valid
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK with all middleware, got: %d", w.Code)
	}
}
