package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoopbackOnly(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		wantStatus int
	}{
		{
			name:       "ipv4 loopback",
			remoteAddr: "127.0.0.1:12345",
			wantStatus: http.StatusOK,
		},
		{
			name:       "ipv6 loopback",
			remoteAddr: "[::1]:12345",
			wantStatus: http.StatusOK,
		},
		{
			name:       "non-loopback ipv4",
			remoteAddr: "198.51.100.1:12345",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "ipv4-mapped ipv6",
			remoteAddr: "[::ffff:127.0.0.1]:12345",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "empty remote addr",
			remoteAddr: "",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "malformed remote addr",
			remoteAddr: "not-a-valid-addr",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "empty port only",
			remoteAddr: ":8080",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Stub next handler that writes 200 + "ok".
			stubNext := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("ok"))
			})

			handler := LoopbackOnly(stubNext)

			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.wantStatus {
				t.Errorf("LoopbackOnly returned status %d, want %d", status, tt.wantStatus)
			}

			if tt.wantStatus == http.StatusOK {
				if body := rr.Body.String(); body != "ok" {
					t.Errorf("LoopbackOnly body = %q, want %q", body, "ok")
				}
			}
		})
	}
}
