package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/ui"
)

// AuthHandlers holds dependencies for authentication handlers.
// It has minimal dependencies compared to the main Handlers struct.
type AuthHandlers struct {
	AuthService    auth.AuthService
	SessionManager session.SessionManager
}

// NewAuthHandlers creates a new AuthHandlers with the given dependencies.
func NewAuthHandlers(authService auth.AuthService, sessionManager session.SessionManager) *AuthHandlers {
	return &AuthHandlers{
		AuthService:    authService,
		SessionManager: sessionManager,
	}
}

// Login handles POST /login, authenticating users against the database.
// On successful authentication, it sets a session cookie and sets HX-Trigger: auth-changed
// to trigger the hamburger menu refresh via /hamburger-menu (HTTP 200).
// On failed authentication, it renders the login form with an appropriate error message
// and returns HTTP 200.
func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	// Validate CSRF token
	if !h.SessionManager.ValidateCSRFToken(r) {
		// Check if this is a new session without CSRF token (allowed)
		sess, gsErr := h.SessionManager.GetSession(w, r)
		hasCsrfToken := false
		if gsErr == nil && sess != nil {
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
		if rtErr := ui.RenderTemplate(w, "login-form.html.tmpl", map[string]any{
			"ErrorMessage": "Account locked. Please try again later.",
			"Username":     username,
			"CSRFToken":    h.SessionManager.EnsureCSRFToken(w, r),
		}); rtErr != nil {
			slog.Error("failed to render login form", "err", rtErr)
		}
		return
	}

	// Authenticate via AuthService
	_, err = h.AuthService.Authenticate(ctx, username, password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			if rtErr := ui.RenderTemplate(w, "login-form.html.tmpl", map[string]any{
				"ErrorMessage": "Invalid credentials",
				"Username":     username,
				"CSRFToken":    h.SessionManager.EnsureCSRFToken(w, r),
			}); rtErr != nil {
				slog.Error("failed to render login form", "err", rtErr)
			}
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

	w.Header().Set("HX-Trigger", "auth-changed")
	w.WriteHeader(http.StatusOK)
}

// LoginFormHandler handles GET /login-form and returns the login form HTML with a
// fresh CSRF token from an uncached endpoint. This prevents 403 failures caused by
// stale CSRF tokens baked into the 30-day cached gallery page.
func (h *AuthHandlers) LoginFormHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	if err := ui.RenderTemplate(w, "login-form-inner.html.tmpl", map[string]any{
		"CSRFToken":    h.SessionManager.EnsureCSRFToken(w, r),
		"ErrorMessage": "",
		"Username":     "",
	}); err != nil {
		slog.Error("failed to render login form", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Logout handles POST /logout, destroying the session and triggering the auth-changed event
// to refresh the hamburger menu via the /hamburger-menu endpoint.
func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	// Clear the session via SessionManager
	if err := h.SessionManager.SetAuthenticated(w, r, false); err != nil {
		slog.Error("failed to clear authenticated session", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", "auth-changed")
	w.WriteHeader(http.StatusOK)
}
