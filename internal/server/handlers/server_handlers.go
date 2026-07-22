package handlers

import (
	"log/slog"
	"net/http"

	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/ui"
)

// ServerHandlers holds dependencies for server management handlers.
type ServerHandlers struct {
	sessionManager        SessionManager
	serverControl         interfaces.ServerControl
	AddCommonTemplateData func(w http.ResponseWriter, r *http.Request, data map[string]any, fullPage bool) map[string]any
	ServerError           func(w http.ResponseWriter, r *http.Request, err error)
}

// NewServerHandlers creates a new ServerHandlers with the given dependencies.
func NewServerHandlers(
	sessionManager SessionManager,
	serverControl interfaces.ServerControl,
	addCommonTemplateData func(w http.ResponseWriter, r *http.Request, data map[string]any, fullPage bool) map[string]any,
	serverError func(w http.ResponseWriter, r *http.Request, err error),
) *ServerHandlers {
	return &ServerHandlers{
		sessionManager:        sessionManager,
		serverControl:         serverControl,
		AddCommonTemplateData: addCommonTemplateData,
		ServerError:           serverError,
	}
}

// ServerShutdownPost handles POST /server/shutdown requests.
// Requires authentication. Triggers graceful server shutdown.
func (h *ServerHandlers) ServerShutdownPost(w http.ResponseWriter, r *http.Request) {
	if !h.sessionManager.IsAuthenticated(w, r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	slog.Info("Shutdown requested via web interface")

	data := map[string]any{
		"PageName": "shutdown",
	}

	data = h.AddCommonTemplateData(w, r, data, false)

	// Render the shutdown page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := ui.RenderPage(w, "shutdown", data, false); err != nil {
		slog.Error("failed to render shutdown page", "err", err)
		h.ServerError(w, r, err)
		return
	}

	// Trigger shutdown after response is sent
	go func() {
		h.serverControl.Shutdown()
	}()
}

// ServerDiscoveryPost handles POST /server/discovery requests.
// Requires authentication. Triggers file discovery.
func (h *ServerHandlers) ServerDiscoveryPost(w http.ResponseWriter, r *http.Request) {
	if !h.sessionManager.IsAuthenticated(w, r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	slog.Info("Discovery requested via web interface")

	// Reset stats before starting new discovery
	h.serverControl.ResetStats()

	// Trigger discovery in a goroutine so it doesn't block the HTTP response
	go h.serverControl.TriggerDiscovery()

	// Return success notification
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	data := map[string]any{
		"Message": "File discovery started",
	}

	data = h.AddCommonTemplateData(w, r, data, true)

	if err := ui.RenderPage(w, "discovery-started", data, false); err != nil {
		slog.Error("failed to render discovery started notification", "err", err)
		w.Write([]byte("File discovery started"))
	}
}

// ServerCacheBatchLoadPost handles POST /server/cache-batch-load requests.
// Requires authentication. Blocks if discovery is active (returns 409). Otherwise
// starts batch load in a goroutine and returns success toast.
func (h *ServerHandlers) ServerCacheBatchLoadPost(w http.ResponseWriter, r *http.Request) {
	if !h.sessionManager.IsAuthenticated(w, r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	result, err := h.serverControl.StartCacheBatchLoad()
	if err != nil {
		slog.Error("cache batch load start failed", "err", err)
		h.ServerError(w, r, err)
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
	data = h.AddCommonTemplateData(w, r, data, true)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := ui.RenderPage(w, "cache-batch-load-started", data, false); err != nil {
		slog.Error("failed to render cache batch load toast", "err", err)
		w.Write([]byte(result.Message))
	}
}
