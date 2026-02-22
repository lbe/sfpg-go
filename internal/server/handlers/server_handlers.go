package handlers

import (
	"log/slog"
	"net/http"

	"go.local/sfpg/internal/server/ui"
)

// ServerHandlers holds dependencies for server management handlers.
type ServerHandlers struct {
	sessionManager SessionManager

	// Server control functions
	ShutdownFunc   func()
	DiscoveryFunc  func()
	StatsResetFunc func()

	// Template helpers
	AddCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any) map[string]any
	ServerError           func(http.ResponseWriter, *http.Request, error)
}

// NewServerHandlers creates a new ServerHandlers with the given dependencies.
func NewServerHandlers(
	sessionManager SessionManager,
	shutdownFunc func(),
	discoveryFunc func(),
	statsResetFunc func(),
	addCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any) map[string]any,
	serverError func(http.ResponseWriter, *http.Request, error),
) *ServerHandlers {
	return &ServerHandlers{
		sessionManager:        sessionManager,
		ShutdownFunc:          shutdownFunc,
		DiscoveryFunc:         discoveryFunc,
		StatsResetFunc:        statsResetFunc,
		AddCommonTemplateData: addCommonTemplateData,
		ServerError:           serverError,
	}
}

// ServerShutdownPost handles POST /server/shutdown requests.
// Requires authentication. Triggers graceful server shutdown.
func (h *ServerHandlers) ServerShutdownPost(w http.ResponseWriter, r *http.Request) {
	if !h.sessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	slog.Info("Shutdown requested via web interface")

	data := map[string]any{
		"PageName": "shutdown",
	}

	if h.AddCommonTemplateData != nil {
		data = h.AddCommonTemplateData(w, r, data)
	}

	// Render the shutdown page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := ui.RenderPage(w, "shutdown", data, false); err != nil {
		slog.Error("failed to render shutdown page", "err", err)
		if h.ServerError != nil {
			h.ServerError(w, r, err)
		}
		return
	}

	// Trigger shutdown after response is sent
	if h.ShutdownFunc != nil {
		go func() {
			h.ShutdownFunc()
		}()
	}
}

// ServerDiscoveryPost handles POST /server/discovery requests.
// Requires authentication. Triggers file discovery.
func (h *ServerHandlers) ServerDiscoveryPost(w http.ResponseWriter, r *http.Request) {
	if !h.sessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	slog.Info("Discovery requested via web interface")

	// Reset stats before starting new discovery
	if h.StatsResetFunc != nil {
		h.StatsResetFunc()
	}

	// Trigger discovery in a goroutine so it doesn't block the HTTP response
	if h.DiscoveryFunc != nil {
		go h.DiscoveryFunc()
	}

	// Return success notification
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	data := map[string]any{
		"Message": "File discovery started",
	}

	if h.AddCommonTemplateData != nil {
		data = h.AddCommonTemplateData(w, r, data)
	}

	if err := ui.RenderPage(w, "discovery-started", data, false); err != nil {
		slog.Error("failed to render discovery started notification", "err", err)
		w.Write([]byte("File discovery started"))
	}
}
