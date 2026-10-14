package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/server/ui"
)

// ManualDiscoveryErrorSource exposes the in-memory manual discovery rebuild
// error for the dashboard alert. Narrow interface so tests can inject a mock.
type ManualDiscoveryErrorSource interface {
	ManualDiscoveryError() string
	SetManualDiscoveryError(msg string)
}

// DashboardHandlers holds dependencies for dashboard handlers.
type DashboardHandlers struct {
	sessionManager SessionManager
	collector      MetricsCollector
	discoveryError ManualDiscoveryErrorSource

	// Template helpers
	AddCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any) map[string]any
	ServerError           func(http.ResponseWriter, *http.Request, error)
}

// SessionManager interface for session authentication.
type SessionManager interface {
	IsAuthenticated(w http.ResponseWriter, r *http.Request) bool
}

// MetricsCollector interface for collecting metrics.
type MetricsCollector interface {
	Collect(ctx context.Context) metrics.Snapshot
}

// NewDashboardHandlers creates a new DashboardHandlers with the given dependencies.
func NewDashboardHandlers(
	sessionManager SessionManager,
	collector MetricsCollector,
	discoveryError ManualDiscoveryErrorSource,
	addCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any) map[string]any,
	serverError func(http.ResponseWriter, *http.Request, error),
) *DashboardHandlers {
	return &DashboardHandlers{
		sessionManager:        sessionManager,
		collector:             collector,
		discoveryError:        discoveryError,
		AddCommonTemplateData: addCommonTemplateData,
		ServerError:           serverError,
	}
}

// DashboardGet handles GET /dashboard requests, rendering the dashboard page.
// Requires authentication. Renders an HTMX partial when HX-Request is true;
// HX-Target does not affect partial vs full for this route.
func (h *DashboardHandlers) DashboardGet(w http.ResponseWriter, r *http.Request) {
	if !h.sessionManager.IsAuthenticated(w, r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	snapshot := h.collector.Collect(ctx)

	data := map[string]any{
		"Metrics":                 snapshot,
		"PageName":                "dashboard",
		"FolderIndexRebuildError": h.discoveryError.ManualDiscoveryError(),
	}

	partial := ui.IsPartialRequest(r, ui.PartialRuleHXRequestOnly, "")

	if h.AddCommonTemplateData != nil {
		data = h.AddCommonTemplateData(w, r, data)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	ui.SetLayoutPartialVary(w, false)
	if partial {
		ui.SetPartialNoStore(w)
	}
	w.WriteHeader(http.StatusOK)

	if err := ui.RenderPage(w, "dashboard", data, partial); err != nil {
		slog.Error("failed to render dashboard", "err", err)
		if h.ServerError != nil {
			h.ServerError(w, r, err)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}
}
