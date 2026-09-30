package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := SecurityHeaders(next)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get(headerXContentTypeOptions); got != valueNoSniff {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, valueNoSniff)
	}
	if got := rr.Header().Get(headerContentSecurityPolicy); got != valueFrameAncestorsNone {
		t.Errorf("Content-Security-Policy = %q, want %q", got, valueFrameAncestorsNone)
	}
}
