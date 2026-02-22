package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"go.local/sfpg/internal/server/metrics"
	"go.local/sfpg/internal/server/ui"
)

// DashboardHandlers holds dependencies for dashboard handlers.
type DashboardHandlers struct {
	sessionManager SessionManager
	collector      MetricsCollector

	// Template helpers
	AddCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any) map[string]any
	ServerError           func(http.ResponseWriter, *http.Request, error)
}

// SessionManager interface for session management.
type SessionManager interface {
	IsAuthenticated(r *http.Request) bool
}

// MetricsCollector interface for collecting metrics.
type MetricsCollector interface {
	Collect(ctx context.Context) metrics.Snapshot
}

// NewDashboardHandlers creates a new DashboardHandlers with the given dependencies.
func NewDashboardHandlers(
	sessionManager SessionManager,
	collector MetricsCollector,
	addCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any) map[string]any,
	serverError func(http.ResponseWriter, *http.Request, error),
) *DashboardHandlers {
	return &DashboardHandlers{
		sessionManager:        sessionManager,
		collector:             collector,
		AddCommonTemplateData: addCommonTemplateData,
		ServerError:           serverError,
	}
}

// DashboardGet handles GET /dashboard requests, rendering the dashboard page.
// Requires authentication. Supports HTMX partial updates when HX-Request header is present.
func (h *DashboardHandlers) DashboardGet(w http.ResponseWriter, r *http.Request) {
	if !h.sessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	snapshot := h.collector.Collect(ctx)

	data := map[string]any{
		"Metrics":  snapshot,
		"PageName": "dashboard",
	}

	if h.AddCommonTemplateData != nil {
		data = h.AddCommonTemplateData(w, r, data)
	}

	// Detect HTMX request for partial rendering
	hxRequest := r.Header.Get("HX-Request") == "true"
	hxTarget := r.Header.Get("HX-Target")
	isHTMX := hxRequest && hxTarget == "dashboard-container"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Vary on HTMX headers so the browser does not serve a cached partial for a full page request
	w.Header().Add("Vary", "HX-Request")
	w.Header().Add("Vary", "HX-Target")
	// Partial responses must not be cached or stored in bfcache
	if isHTMX {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.WriteHeader(http.StatusOK)

	if err := ui.RenderPage(w, "dashboard", data, isHTMX); err != nil {
		slog.Error("failed to render dashboard", "err", err)
		if h.ServerError != nil {
			h.ServerError(w, r, err)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}
}
