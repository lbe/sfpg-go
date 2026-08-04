package middleware

import (
	"net/http"

	"github.com/lbe/sfpg-go/internal/server/security"
)

// LoopbackOnly returns a middleware that rejects any request whose RemoteAddr
// is not the loopback addresses 127.0.0.1 or ::1. Non-loopback requests
// receive a 404 Not Found response.
//
// Host extraction uses security.RateLimitFromRequestKey for string-equality
// comparison (no IP normalization), ensuring RFC 4291 IPv4-mapped IPv6
// addresses like ::ffff:127.0.0.1 are rejected.
func LoopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := security.RateLimitFromRequestKey(r.RemoteAddr)
		if host != "127.0.0.1" && host != "::1" {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
