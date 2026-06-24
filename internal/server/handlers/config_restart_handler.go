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
// It initiates an asynchronous application restart.
// Response: HTML alert (bufferable, caching disabled).
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
	// receives the confirmation before the server potentially restarts.
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Trigger restart
	go h.triggerRestart()
}

// triggerRestart sends a restart signal to the application's restart channel after a brief
// delay, allowing the HTTP response to be fully flushed before the server may shut down.
// If the channel is full (restart already pending), it logs a warning instead of blocking.
// If the channel is nil (not initialized), it logs an error.
func (h *ConfigRestartHandler) triggerRestart() {
	time.Sleep(500 * time.Millisecond)
	slog.Info("Restart requested via web interface, sending restart signal")

	restartCh := h.GetRestartCh()
	if restartCh != nil {
		select {
		case restartCh <- struct{}{}:
			slog.Info("Restart signal sent successfully")
		default:
			slog.Warn("Restart channel full, restart already pending")
		}
	} else {
		slog.Error("Restart channel not initialized")
	}
}
