package handlers

import (
	"net/http"
	"slices"

	"go.local/sfpg/internal/server/config"
)

// ThemeCookieName is the name of the theme cookie.
const ThemeCookieName = "theme"

// ThemeCookieMaxAge is the max age for the theme cookie (1 year in seconds).
const ThemeCookieMaxAge = 365 * 24 * 60 * 60

// ThemeHandlers holds dependencies for theme-related HTTP handlers.
type ThemeHandlers struct {
	GetConfig             func() *config.Config
	AddCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any) map[string]any
	RenderThemeModal      func(http.ResponseWriter, any) error
	ServerError           func(http.ResponseWriter, *http.Request, error)
}

// NewThemeHandlers creates a new ThemeHandlers with the given dependencies.
func NewThemeHandlers(
	getConfig func() *config.Config,
	addCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any) map[string]any,
	renderThemeModal func(http.ResponseWriter, any) error,
	serverError func(http.ResponseWriter, *http.Request, error),
) *ThemeHandlers {
	return &ThemeHandlers{
		GetConfig:             getConfig,
		AddCommonTemplateData: addCommonTemplateData,
		RenderThemeModal:      renderThemeModal,
		ServerError:           serverError,
	}
}

// ThemeModalHandler returns the theme selector modal.
func (h *ThemeHandlers) ThemeModalHandler(w http.ResponseWriter, r *http.Request) {
	cfg := h.GetConfig()
	if cfg == nil {
		http.Error(w, "Configuration not loaded", http.StatusInternalServerError)
		return
	}

	currentTheme := h.GetEffectiveTheme(r)

	data := map[string]any{
		"Themes":       cfg.Themes,
		"CurrentTheme": currentTheme,
	}

	data = h.AddCommonTemplateData(w, r, data)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if h.RenderThemeModal == nil {
		http.Error(w, "Theme modal template not initialized", http.StatusInternalServerError)
		return
	}

	if err := h.RenderThemeModal(w, data); err != nil {
		h.ServerError(w, r, err)
		return
	}
}

// ThemePostHandler handles theme selection from the modal.
func (h *ThemeHandlers) ThemePostHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	selectedTheme := r.FormValue("theme")
	if selectedTheme == "" {
		http.Error(w, "Theme not specified", http.StatusBadRequest)
		return
	}

	cfg := h.GetConfig()
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
	cfg := h.GetConfig()
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
