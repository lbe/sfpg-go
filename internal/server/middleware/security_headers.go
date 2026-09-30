package middleware

import "net/http"

const (
	headerXContentTypeOptions   = "X-Content-Type-Options"
	headerContentSecurityPolicy = "Content-Security-Policy"
	valueNoSniff                = "nosniff"
	valueFrameAncestorsNone     = "frame-ancestors 'none'"
)

// SecurityHeaders sets baseline response headers on every response: nosniff and
// a minimal CSP that blocks cross-origin framing (clickjacking).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerXContentTypeOptions, valueNoSniff)
		w.Header().Set(headerContentSecurityPolicy, valueFrameAncestorsNone)
		next.ServeHTTP(w, r)
	})
}
