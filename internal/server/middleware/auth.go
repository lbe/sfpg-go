package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/server/session"
)

// AuthConfig holds configuration for auth middleware behavior.
type AuthConfig struct {
	// DebugDelayMS is an optional debug delay in milliseconds to add to requests.
	// If IsSet is false or value is 0, no delay is added.
	DebugDelayMS struct {
		IsSet bool
		Int   int
	}
}

// AuthMiddleware creates a middleware that protects routes by checking for a valid session.
// If the user is not authenticated, it returns 401 Unauthorized.
// It accepts a session store for checking authentication and a session manager for clearing sessions.
func AuthMiddleware(store *sessions.CookieStore, sessionManager session.SessionManager, config *AuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := store.Get(r, session.SessionName)
			if err != nil {
				// Any session retrieval error (e.g., rotated secret, tampered cookie, malformed value)
				// should result in clearing the cookie and returning unauthorized.
				slog.Info("session retrieval error; clearing cookie", "err", err)
				sessionManager.ClearSession(w, r)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			if auth, ok := session.Values["authenticated"].(bool); !ok || !auth {
				slog.Debug("Not authenticated", "path", r.URL.Path)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Apply debug delay if configured
			if config != nil && config.DebugDelayMS.IsSet && config.DebugDelayMS.Int > 0 {
				time.Sleep(time.Duration(config.DebugDelayMS.Int) * time.Millisecond)
			}

			next.ServeHTTP(w, r)
		})
	}
}
