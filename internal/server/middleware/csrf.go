package middleware

import (
	"net/http"
	"strings"
)

// CSRFProtection enforces a same-origin check on unsafe HTTP methods.
// For non-idempotent methods it requires an Origin header that matches
// the request host (including port). When Origin is absent it falls back
// to the Referer header for the same same-origin check. If both are
// missing or neither matches, it returns 403.
func CSRFProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow safe methods without Origin checks
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			next.ServeHTTP(w, r)
			return
		}

		// Check Origin header first (most reliable indicator)
		origin := r.Header.Get("Origin")
		if origin != "" {
			host := r.Host
			if origin == "http://"+host || origin == "https://"+host {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// Fall back to Referer header when Origin is absent.
		// Some browsers don't send Origin for same-origin requests
		// but do include a Referer header indicating the source origin.
		referer := r.Header.Get("Referer")
		if referer == "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		host := r.Host
		// Check that the Referer URL shares the same origin (scheme + host + port)
		if referer == "http://"+host || strings.HasPrefix(referer, "http://"+host+"/") ||
			referer == "https://"+host || strings.HasPrefix(referer, "https://"+host+"/") {
			next.ServeHTTP(w, r)
			return
		}

		http.Error(w, "Forbidden", http.StatusForbidden)
	})
}
