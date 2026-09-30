package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPprofAccess(t *testing.T) {
	authReached := false
	stubAuth := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		authReached = true
		w.WriteHeader(http.StatusUnauthorized)
	})
	handler := PprofAccess(stubAuth)

	tests := []struct {
		name       string
		remoteAddr string
		setHeader  func(*http.Request)
		wantStatus int
		wantAuth   bool
	}{
		{
			name:       "direct loopback reaches auth",
			remoteAddr: "127.0.0.1:12345",
			wantStatus: http.StatusUnauthorized,
			wantAuth:   true,
		},
		{
			name:       "loopback with X-Forwarded-For blocked",
			remoteAddr: "127.0.0.1:12345",
			setHeader: func(r *http.Request) {
				r.Header.Set("X-Forwarded-For", "203.0.113.1")
			},
			wantStatus: http.StatusNotFound,
			wantAuth:   false,
		},
		{
			name:       "loopback with X-Forwarded-Proto blocked",
			remoteAddr: "127.0.0.1:12345",
			setHeader: func(r *http.Request) {
				r.Header.Set("X-Forwarded-Proto", "https")
			},
			wantStatus: http.StatusNotFound,
			wantAuth:   false,
		},
		{
			name:       "loopback with Forwarded blocked",
			remoteAddr: "127.0.0.1:12345",
			setHeader: func(r *http.Request) {
				r.Header.Set("Forwarded", "for=203.0.113.1")
			},
			wantStatus: http.StatusNotFound,
			wantAuth:   false,
		},
		{
			name:       "remote direct blocked",
			remoteAddr: "198.51.100.1:12345",
			wantStatus: http.StatusNotFound,
			wantAuth:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authReached = false
			req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.setHeader != nil {
				tt.setHeader(req)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if authReached != tt.wantAuth {
				t.Errorf("auth reached = %v, want %v", authReached, tt.wantAuth)
			}
		})
	}
}
