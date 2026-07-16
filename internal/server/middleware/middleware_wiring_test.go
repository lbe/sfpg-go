package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// contains checks if a comma-separated header value contains a specific value.
func contains(header, value string) bool {
	parts := strings.SplitSeq(header, ",")
	for part := range parts {
		if strings.TrimSpace(part) == value {
			return true
		}
	}
	return false
}

// TestMiddleware_CompressionWrapping verifies that CompressMiddleware wraps
// a handler and adds Vary: Accept-Encoding to the response when the client
// sends Accept-Encoding: gzip.
func TestMiddleware_CompressionWrapping(t *testing.T) {
	// Test the middleware function directly (unit test, no router needed).
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := CompressMiddleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	// CompressMiddleware should add Vary: Accept-Encoding
	varyHeaders := rr.Header().Values("Vary")
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

// TestMiddleware_CSRFProtectionWrapping verifies that CSRFProtection middleware
// allows safe methods through without an Origin header.
func TestMiddleware_CSRFProtectionWrapping(t *testing.T) {
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := CSRFProtection(handler)

	// Safe methods (GET) should pass through
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for GET through CSRF protection, got %d", rr.Code)
	}

	// Unsafe methods (POST) without Origin should be rejected
	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	rr2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden for POST without Origin, got %d", rr2.Code)
	}
}
