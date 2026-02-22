package middleware

import (
	"net/http"
)

// CSRFProtection enforces a same-origin check on unsafe HTTP methods.
// For non-idempotent methods it requires an Origin header that matches
// the request host (including port). If the check fails, it returns 403.
func CSRFProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow safe methods without Origin checks
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			next.ServeHTTP(w, r)
			return
		}

		origin := r.Header.Get("Origin")
		if origin == "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// Accept origin if it matches the request host with http or https
		host := r.Host
		if origin == "http://"+host || origin == "https://"+host {
			next.ServeHTTP(w, r)
			return
		}

		// Not a same-origin request
		http.Error(w, "Forbidden", http.StatusForbidden)
	})
}
