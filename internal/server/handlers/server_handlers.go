package handlers

import (
	"log/slog"
	"net/http"

	"github.com/lbe/sfpg-go/internal/server/ui"
)

// StartCacheBatchLoadResult describes the result of attempting to start cache batch load.
type StartCacheBatchLoadResult struct {
	Blocked bool   // true if discovery is active
	Message string // toast message
}

// ServerHandlers holds dependencies for server management handlers.
type ServerHandlers struct {
	sessionManager SessionManager

	// Server control functions
	ShutdownFunc        func()
	DiscoveryFunc       func()
	StatsResetFunc      func()
	StartCacheBatchLoad func() (StartCacheBatchLoadResult, error)

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
	startCacheBatchLoad func() (StartCacheBatchLoadResult, error),
	addCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any) map[string]any,
	serverError func(http.ResponseWriter, *http.Request, error),
) *ServerHandlers {
	return &ServerHandlers{
		sessionManager:        sessionManager,
		ShutdownFunc:          shutdownFunc,
		DiscoveryFunc:         discoveryFunc,
		StatsResetFunc:        statsResetFunc,
		StartCacheBatchLoad:   startCacheBatchLoad,
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

// ServerCacheBatchLoadPost handles POST /server/cache-batch-load requests.
// Requires authentication. Blocks if discovery is active (returns 409). Otherwise
// starts batch load in a goroutine and returns success toast.
func (h *ServerHandlers) ServerCacheBatchLoadPost(w http.ResponseWriter, r *http.Request) {
	if !h.sessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if h.StartCacheBatchLoad == nil {
		http.Error(w, "Cache batch load not available", http.StatusServiceUnavailable)
		return
	}

	result, err := h.StartCacheBatchLoad()
	if err != nil {
		slog.Error("cache batch load start failed", "err", err)
		if h.ServerError != nil {
			h.ServerError(w, r, err)
		}
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
	if h.AddCommonTemplateData != nil {
		data = h.AddCommonTemplateData(w, r, data)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := ui.RenderPage(w, "cache-batch-load-started", data, false); err != nil {
		slog.Error("failed to render cache batch load toast", "err", err)
		w.Write([]byte(result.Message))
	}
}
