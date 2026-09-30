package middleware

import (
	"net/http"

	"github.com/lbe/sfpg-go/internal/server/security"
)

// isLoopbackRemoteAddr reports whether RemoteAddr is 127.0.0.1 or ::1 using the
// same host extraction as rate limiting (IPv4-mapped addresses are not loopback).
func isLoopbackRemoteAddr(r *http.Request) bool {
	host := security.RateLimitFromRequestKey(r.RemoteAddr)
	return host == "127.0.0.1" || host == "::1"
}

// requestHasForwardProxyHeaders reports whether the request appears to have
// passed through a reverse proxy (Caddy, nginx, etc.). Any standard forward
// header is treated as a proxy signal so pprof is not exposed on the public
// origin when the upstream connection is loopback.
func requestHasForwardProxyHeaders(r *http.Request) bool {
	return r.Header.Get("X-Forwarded-For") != "" ||
		r.Header.Get("X-Forwarded-Proto") != "" ||
		r.Header.Get("X-Forwarded-Host") != "" ||
		r.Header.Get("Forwarded") != ""
}

// PprofAccess gates Go pprof HTTP endpoints. Proxied requests and non-loopback
// direct requests receive 404 before inner handlers run. Direct loopback requests
// reach next (typically authMiddleware + pprof handler).
func PprofAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestHasForwardProxyHeaders(r) {
			http.NotFound(w, r)
			return
		}
		if !isLoopbackRemoteAddr(r) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
