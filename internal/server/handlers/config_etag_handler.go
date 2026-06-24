package handlers

import (
	"log/slog"
	"net/http"

	"github.com/lbe/sfpg-go/internal/server/ui"
)

// ConfigETagHandler holds dependencies for ETag-related HTTP handlers.
type ConfigETagHandler struct {
	*ConfigHandlers
}

// NewConfigETagHandler wraps an existing ConfigHandlers to provide ETag endpoints.
func NewConfigETagHandler(h *ConfigHandlers) *ConfigETagHandler {
	return &ConfigETagHandler{ConfigHandlers: h}
}

// ConfigIncrementETag increments the application ETag version.
// POST /config/increment-etag
func (h *ConfigETagHandler) ConfigIncrementETag(w http.ResponseWriter, r *http.Request) {
	// Parse form to get CSRF token
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Validate CSRF token
	if !h.validateCsrf(r) {
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	// Increment ETag version using wired service/logic
	_, err := h.ConfigService.IncrementETag(h.Ctx)
	if err != nil {
		slog.Error("failed to increment etag version", "err", err)
		w.Header().Set("HX-Retarget", "#config-error-message")
		w.Header().Set("HX-Swap", "outerHTML")
		w.WriteHeader(http.StatusInternalServerError)
		if rtErr := ui.RenderTemplate(w, "config-generic-error.html.tmpl", map[string]any{
			"Error": "Failed to increment ETag version",
		}); rtErr != nil {
			slog.Error("failed to render config error template", "err", rtErr)
		}
		return
	}

	// Reload config to get the updated ETag and update in-memory state
	cfg, err := h.ConfigService.Load(h.Ctx)
	if err != nil {
		slog.Error("failed to reload config after etag increment", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Update in-memory app state
	if h.UpdateConfig != nil {
		h.UpdateConfig(cfg, nil)
	}

	// Update UI-wide cache version
	ui.SetCacheVersion(cfg.ETagVersion)

	// Invalidate HTTP cache so stale responses with old cache-busting URLs are not served.
	if h.InvalidateHTTPCache != nil {
		h.InvalidateHTTPCache()
	}

	slog.Info("etag version incremented and app config updated",
		"new", cfg.ETagVersion)

	// Return updated field (HTMX will swap this into the page)
	data := map[string]any{
		"ETagVersion": cfg.ETagVersion,
	}

	w.WriteHeader(http.StatusOK)
	if rtErr := ui.RenderTemplate(w, "config-etag-field.html.tmpl", data); rtErr != nil {
		slog.Error("failed to render config etag field template", "err", rtErr)
	}
}
