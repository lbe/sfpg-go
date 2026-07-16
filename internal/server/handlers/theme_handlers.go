package handlers

import (
	"log/slog"
	"net/http"
	"slices"

	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/ui"
)

// ThemeCookieName is the name of the theme cookie.
const ThemeCookieName = "theme"

// ThemeCookieMaxAge is the max age for the theme cookie (1 year in seconds).
const ThemeCookieMaxAge = 365 * 24 * 60 * 60

// ThemeHandlers holds dependencies for theme-related HTTP handlers.
type ThemeHandlers struct {
	sessionManager        SessionManager
	getConfig             func() *config.Config
	AddCommonTemplateData func(w http.ResponseWriter, r *http.Request, data map[string]any, fullPage bool) map[string]any
	ServerError           func(w http.ResponseWriter, r *http.Request, err error)
}

// NewThemeHandlers creates a new ThemeHandlers with the given dependencies.
func NewThemeHandlers(
	sessionManager SessionManager,
	getConfig func() *config.Config,
	addCommonTemplateData func(w http.ResponseWriter, r *http.Request, data map[string]any, fullPage bool) map[string]any,
	serverError func(w http.ResponseWriter, r *http.Request, err error),
) *ThemeHandlers {
	return &ThemeHandlers{
		sessionManager:        sessionManager,
		getConfig:             getConfig,
		AddCommonTemplateData: addCommonTemplateData,
		ServerError:           serverError,
	}
}

// ThemeModalHandler returns the theme selector modal.
func (h *ThemeHandlers) ThemeModalHandler(w http.ResponseWriter, r *http.Request) {
	cfg := h.getConfig()
	if cfg == nil {
		http.Error(w, "Configuration not loaded", http.StatusInternalServerError)
		return
	}

	currentTheme := h.GetEffectiveTheme(r)

	data := map[string]any{
		"Themes":       cfg.Themes,
		"CurrentTheme": currentTheme,
	}

	data = h.AddCommonTemplateData(w, r, data, true)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := ui.RenderTemplate(w, "theme-modal.html.tmpl", data); err != nil {
		h.ServerError(w, r, err)
		return
	}
}

// ThemePostHandler handles theme selection from the modal.
func (h *ThemeHandlers) ThemePostHandler(w http.ResponseWriter, r *http.Request) {
	// Validate CSRF token
	if !h.sessionManager.ValidateCSRFToken(r) {
		slog.Warn("CSRF validation failed for theme post", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	selectedTheme := r.FormValue("theme")
	if selectedTheme == "" {
		http.Error(w, "Theme not specified", http.StatusBadRequest)
		return
	}

	cfg := h.getConfig()
	if cfg == nil {
		http.Error(w, "Configuration not loaded", http.StatusInternalServerError)
		return
	}

	if !isValidTheme(selectedTheme, cfg.Themes) {
		http.Error(w, "Invalid theme", http.StatusBadRequest)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     ThemeCookieName,
		Value:    selectedTheme,
		Path:     "/",
		MaxAge:   ThemeCookieMaxAge,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<div class="alert alert-success">Theme updated</div>`))
}

// GetEffectiveTheme returns the effective theme for a request.
// Priority: 1) Cookie (if valid), 2) Server default.
func (h *ThemeHandlers) GetEffectiveTheme(r *http.Request) string {
	cfg := h.getConfig()
	if cfg == nil {
		return "dark"
	}

	if cookie, err := r.Cookie(ThemeCookieName); err == nil {
		if isValidTheme(cookie.Value, cfg.Themes) {
			return cookie.Value
		}
	}

	return cfg.CurrentTheme
}

// isValidTheme checks if a theme is in the configured themes list.
func isValidTheme(theme string, themes []string) bool {
	return slices.Contains(themes, theme)
}
