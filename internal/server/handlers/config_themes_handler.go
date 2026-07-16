package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/lbe/sfpg-go/internal/server/config"
)

// ConfigThemesHandler handles admin management of the available themes list.
type ConfigThemesHandler struct {
	*ConfigHandlers
}

// NewConfigThemesHandler wraps an existing ConfigHandlers to provide themes-specific endpoints.
func NewConfigThemesHandler(h *ConfigHandlers) *ConfigThemesHandler {
	return &ConfigThemesHandler{ConfigHandlers: h}
}

// UpdateThemesHandler handles POST /config/themes requests.
// It updates the list of available themes and adjusts current_theme if needed.
// Authentication and CSRF protection are required.
func (h *ConfigThemesHandler) UpdateThemesHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		slog.Warn("failed to parse form in themes update", "err", err)
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	if !h.validateCsrf(r) {
		slog.Warn("CSRF validation failed for themes update", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	oldConfig, err := h.ConfigService.Load(h.Ctx)
	if err != nil {
		slog.Error("failed to load current config for themes update", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	newConfig := *oldConfig

	if themes, ok := r.Form["themes"]; ok && len(themes) > 0 {
		newConfig.Themes = themes
		if newConfig.CurrentTheme != "" {
			if !slices.Contains(themes, newConfig.CurrentTheme) {
				newConfig.CurrentTheme = themes[0]
			}
		}
	}

	applyResult, err := config.ApplyConfig(h.Ctx, h.ConfigService, oldConfig, &newConfig)
	if err != nil {
		slog.Error("failed to apply themes config", "err", err)
		http.Error(w, fmt.Sprintf("Failed to save themes: %v", err), http.StatusInternalServerError)
		return
	}

	h.cfgOps.UpdateConfigWithPrecedence(applyResult.Config, applyResult.RestartRequiredKeys)
	h.cfgOps.ApplyConfig()

	w.Header().Set("HX-Trigger", "config-saved")
	w.WriteHeader(http.StatusOK)
	h.executeConfigTemplate(w, h.Templates.SaveSuccessAlert, "config-save-success-alert.html.tmpl", nil)
}
