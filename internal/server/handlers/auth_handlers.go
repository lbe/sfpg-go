package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/security"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/ui"
)

// AuthHandlers holds dependencies for authentication handlers.
type AuthHandlers struct {
	AuthService    auth.AuthService
	SessionManager session.SessionManager
	rateLimiter    *security.IPRateLimiter
}

// NewAuthHandlers creates a new AuthHandlers with the given dependencies.
func NewAuthHandlers(authService auth.AuthService, sessionManager session.SessionManager) *AuthHandlers {
	return &AuthHandlers{
		AuthService:    authService,
		SessionManager: sessionManager,
	}
}

// SetRateLimiter sets an optional per-IP rate limiter for login requests.
// If set, the limiter is checked before processing each login attempt.
func (h *AuthHandlers) SetRateLimiter(rl *security.IPRateLimiter) {
	h.rateLimiter = rl
}

// SyncLoginRateLimitMax applies a new per-IP login limit at runtime (config
// apply / hot reload). It always SetMax + Clear on the same limiter instance
// so a config save starts a fresh window; a max <= 0 means unlimited.
func (h *AuthHandlers) SyncLoginRateLimitMax(max int) {
	if h.rateLimiter == nil {
		h.rateLimiter = security.NewIPRateLimiter(max, security.DefaultRateLimitWindow)
		return
	}
	h.rateLimiter.SetMax(max)
	h.rateLimiter.Clear()
}

// Login handles POST /login, authenticating users against the database.
// On successful authentication, it sets a session cookie and sets HX-Trigger: auth-changed
// to trigger the hamburger menu refresh via /hamburger-menu (HTTP 200).
// On failed authentication, it renders the login form with an appropriate error message
// and returns HTTP 200.
func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	// Per-IP rate limit runs before CSRF validation so rejected attempts still
	// count toward the cap (see security.IPRateLimiter).
	if h.rateLimiter != nil {
		ip := security.RateLimitFromRequestKey(r.RemoteAddr)
		if !h.rateLimiter.Allow(ip) {
			slog.Warn("IP rate limit exceeded for login", "remote_addr", r.RemoteAddr)
			http.Error(w, "Too many login attempts. Please try again later.", http.StatusTooManyRequests)
			return
		}
	}

	// Validate CSRF token
	if !h.SessionManager.ValidateCSRFToken(r) {
		slog.Warn("CSRF validation failed for login", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	ctx := r.Context()

	_, err := h.AuthService.Authenticate(ctx, username, password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrAccountLocked):
			h.renderLoginForm(w, r, username, "Account locked. Please try again later.")
			return
		case errors.Is(err, auth.ErrInvalidCredentials):
			h.renderLoginForm(w, r, username, "Invalid credentials")
			return
		default:
			slog.Error("failed to authenticate user", "username", username, "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
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

// renderLoginForm renders the login form with the supplied error message.
func (h *AuthHandlers) renderLoginForm(w http.ResponseWriter, r *http.Request, username, errorMessage string) {
	if err := ui.RenderTemplate(w, "login-form.html.tmpl", map[string]any{
		"ErrorMessage": errorMessage,
		"Username":     username,
		"CSRFToken":    h.SessionManager.EnsureCSRFToken(w, r),
	}); err != nil {
		slog.Error("failed to render login form", "err", err)
	}
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

// LogoutFormHandler handles GET /logout-form and returns the logout form HTML with a
// fresh CSRF token from an uncached endpoint. This prevents 403 failures caused by
// stale CSRF tokens baked into the 30-day cached gallery page.
func (h *AuthHandlers) LogoutFormHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	if err := ui.RenderTemplate(w, "logout-form-inner.html.tmpl", map[string]any{
		"CSRFToken": h.SessionManager.EnsureCSRFToken(w, r),
	}); err != nil {
		slog.Error("failed to render logout form", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Logout handles POST /logout, destroying the session and triggering the auth-changed event
// to refresh the hamburger menu via the /hamburger-menu endpoint.
func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	// Validate CSRF token
	if !h.SessionManager.ValidateCSRFToken(r) {
		slog.Warn("CSRF validation failed for logout", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

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
