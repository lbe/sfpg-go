package handlers

import (
	"log/slog"
	"net/http"

	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/ui"
)

// ServerHandlers holds dependencies for server management handlers.
type ServerHandlers struct {
	sessionManager SessionManager
	deps           interfaces.ServerDeps
}

// NewServerHandlers creates a new ServerHandlers with the given dependencies.
func NewServerHandlers(
	sessionManager SessionManager,
	deps interfaces.ServerDeps,
) *ServerHandlers {
	return &ServerHandlers{
		sessionManager: sessionManager,
		deps:           deps,
	}
}

// validateCsrf returns true if CSRF validation passes, false otherwise.
func (h *ServerHandlers) validateCsrf(r *http.Request) bool {
	return h.sessionManager.ValidateCSRFToken(r)
}

// ServerShutdownPost handles POST /server/shutdown requests.
// Requires authentication and valid CSRF token. Triggers graceful server shutdown.
func (h *ServerHandlers) ServerShutdownPost(w http.ResponseWriter, r *http.Request) {
	if !h.sessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !h.validateCsrf(r) {
		slog.Warn("CSRF validation failed for shutdown", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	slog.Info("Shutdown requested via web interface")

	data := map[string]any{
		"PageName": "shutdown",
	}

	data = h.deps.AddCommonTemplateData(w, r, data, false)

	// Render the shutdown page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := ui.RenderPage(w, "shutdown", data, false); err != nil {
		slog.Error("failed to render shutdown page", "err", err)
		h.deps.ServerError(w, r, err)
		return
	}

	// Trigger shutdown after response is sent
	go func() {
		h.deps.Shutdown()
	}()
}

// ServerDiscoveryPost handles POST /server/discovery requests.
// Requires authentication and valid CSRF token. Triggers file discovery.
func (h *ServerHandlers) ServerDiscoveryPost(w http.ResponseWriter, r *http.Request) {
	if !h.sessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !h.validateCsrf(r) {
		slog.Warn("CSRF validation failed for discovery", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	slog.Info("Discovery requested via web interface")

	// Reset stats before starting new discovery
	h.deps.ResetStats()

	// Trigger discovery in a goroutine so it doesn't block the HTTP response
	go h.deps.TriggerDiscovery()

	// Return success notification
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	data := map[string]any{
		"Message": "File discovery started",
	}

	data = h.deps.AddCommonTemplateData(w, r, data, true)

	if err := ui.RenderPage(w, "discovery-started", data, false); err != nil {
		slog.Error("failed to render discovery started notification", "err", err)
		w.Write([]byte("File discovery started"))
	}
}

// ServerCacheBatchLoadPost handles POST /server/cache-batch-load requests.
// Requires authentication and valid CSRF token. Blocks if discovery is active (returns 409). Otherwise
// starts batch load in a goroutine and returns success toast.
func (h *ServerHandlers) ServerCacheBatchLoadPost(w http.ResponseWriter, r *http.Request) {
	if !h.sessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !h.validateCsrf(r) {
		slog.Warn("CSRF validation failed for cache batch load", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	result, err := h.deps.StartCacheBatchLoad()
	if err != nil {
		slog.Error("cache batch load start failed", "err", err)
		h.deps.ServerError(w, r, err)
		return
	}

	status := http.StatusOK
	alertClass := "alert-success"
	if result.Blocked {
		status = http.StatusConflict
		alertClass = "alert-warning"
	}

	data := map[string]any{
		"Message":    result.Message,
		"AlertClass": alertClass,
	}
	data = h.deps.AddCommonTemplateData(w, r, data, true)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := ui.RenderPage(w, "cache-batch-load-started", data, false); err != nil {
		slog.Error("failed to render cache batch load toast", "err", err)
		w.Write([]byte(result.Message))
	}
}
