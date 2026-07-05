package handlers

import (
	"log/slog"
	"net/http"
	"time"
)

// ConfigRestartHandler holds dependencies for restart and ETag-related HTTP handlers.
type ConfigRestartHandler struct {
	*ConfigHandlers
}

// NewConfigRestartHandler wraps an existing ConfigHandlers to provide restart/ETag endpoints.
func NewConfigRestartHandler(h *ConfigHandlers) *ConfigRestartHandler {
	return &ConfigRestartHandler{ConfigHandlers: h}
}

// RestartHandler handles POST /config/restart and POST /server/restart requests.
// It renders the restart-initiated alert, flushes the response, and then triggers
// an asynchronous process restart.
// Authentication and CSRF protection are required.
func (h *ConfigRestartHandler) RestartHandler(w http.ResponseWriter, r *http.Request) {
	if !h.validateCsrf(r) {
		slog.Warn("CSRF validation failed for restart", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusOK)
	h.executeConfigTemplate(w, h.Templates.RestartInitiatedAlert, "config-restart-initiated-alert.html.tmpl", nil)

	// Flush the response before triggering the restart goroutine so the client
	// receives the confirmation before the server process is replaced.
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Trigger restart in the background.
	go h.triggerRestart()
}

// triggerRestart waits briefly for the HTTP response to flush, then asks the
// application to perform a real process restart via ServerDeps.
func (h *ConfigRestartHandler) triggerRestart() {
	time.Sleep(500 * time.Millisecond)
	slog.Info("Restart requested via web interface, initiating process restart")

	h.deps.TriggerRestart()
}
