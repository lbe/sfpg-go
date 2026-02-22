package handlers

import (
	"fmt"
	"log/slog"
	"net/http"

	"go.local/sfpg/internal/server/auth"
	"go.local/sfpg/internal/server/session"
	"go.local/sfpg/internal/server/ui"
)

// AuthHandlers holds dependencies for authentication handlers.
// It has minimal dependencies (~3) compared to the main Handlers struct.
type AuthHandlers struct {
	AuthService     auth.AuthService
	SessionManager  session.SessionManager
	EnsureCsrfToken func(http.ResponseWriter, *http.Request) string
}

// NewAuthHandlers creates a new AuthHandlers with the given dependencies.
func NewAuthHandlers(authService auth.AuthService, sessionManager session.SessionManager, ensureCsrfToken func(http.ResponseWriter, *http.Request) string) *AuthHandlers {
	return &AuthHandlers{
		AuthService:     authService,
		SessionManager:  sessionManager,
		EnsureCsrfToken: ensureCsrfToken,
	}
}

// Login handles POST /login, authenticating users against the database.
// On successful authentication, it sets a session cookie and returns the hamburger menu HTML
// with hx-swap-oob attribute (HTTP 200). On failed authentication, it renders the login form
// with an appropriate error message and returns HTTP 200.
func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request, etagVersion string) {
	// Validate CSRF token
	if !h.SessionManager.ValidateCSRFToken(r) {
		// Check if this is a new session without CSRF token (allowed)
		sess, _ := h.SessionManager.GetSession(r)
		hasCsrfToken := false
		if sess != nil {
			if token, ok := sess.Values["csrf_token"].(string); ok && token != "" {
				hasCsrfToken = true
			}
		}
		isNewSession := sess == nil || sess.IsNew

		if isNewSession || !hasCsrfToken {
			slog.Info("CSRF validation failed but session is new/invalid or missing token - allowing login", "remote_addr", r.RemoteAddr, "is_new", isNewSession, "has_csrf_token", hasCsrfToken)
		} else {
			slog.Warn("CSRF validation failed for login attempt", "remote_addr", r.RemoteAddr)
			http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
			return
		}
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	ctx := r.Context()

	// Check lockout first
	locked, err := h.AuthService.CheckLockout(ctx, username)
	if err != nil {
		slog.Error("failed to check account lockout", "username", username, "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if locked {
		_ = ui.RenderTemplate(w, "login-form.html.tmpl", map[string]any{
			"ErrorMessage": "Account locked. Please try again later.",
			"Username":     username,
			"CSRFToken":    h.SessionManager.EnsureCSRFToken(w, r),
		})
		return
	}

	// Authenticate via AuthService
	_, err = h.AuthService.Authenticate(ctx, username, password)
	if err != nil {
		if err == auth.ErrInvalidCredentials {
			_ = ui.RenderTemplate(w, "login-form.html.tmpl", map[string]any{
				"ErrorMessage": "Invalid credentials",
				"Username":     username,
				"CSRFToken":    h.SessionManager.EnsureCSRFToken(w, r),
			})
		} else {
			slog.Error("authentication error", "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Set authenticated status via SessionManager
	if authErr := h.SessionManager.SetAuthenticated(w, r, true); authErr != nil {
		slog.Error("failed to set authenticated session", "err", authErr)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "login-success")
	err = ui.RenderTemplate(w, "hamburger-menu-items.html.tmpl", map[string]any{
		"IsAuthenticated": true,
		"cacheVersion":    etagVersion,
	})
	if err != nil {
		slog.Error("failed to render hamburger menu", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Logout handles POST /logout, destroying the session and returning an OOB swap to update the hamburger menu.
func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	// Clear the session via SessionManager
	if err := h.SessionManager.SetAuthenticated(w, r, false); err != nil {
		slog.Error("failed to clear authenticated session", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `<ul id="hamburger-menu-items" class="menu dropdown-content bg-base-100 rounded-box z-[60] w-52 border p-2 shadow-lg" tabindex="0" hx-swap-oob="true">
	<li>
		<label for="about_modal" class="cursor-pointer" aria-label="About">About</label>
	</li>
	<div class="divider my-1"></div>
	<li>
		<label for="login_modal" class="cursor-pointer" aria-label="Login">Login</label>
	</li>
</ul>`)
}
