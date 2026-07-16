package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
	"golang.org/x/net/html"
)

// mockSessionManager is a mock implementation for testing
type mockSessionManager struct {
	isAuthenticated bool
	csrfToken       string
}

func (m *mockSessionManager) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	return m.isAuthenticated
}

func (m *mockSessionManager) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return m.csrfToken
}

func (m *mockSessionManager) ValidateCSRFToken(r *http.Request) bool {
	return true
}

func (m *mockSessionManager) ClearSession(w http.ResponseWriter, r *http.Request) {}

// mockCollector is a mock metrics collector for testing
type mockCollector struct {
	snapshot metrics.Snapshot
}

func (m *mockCollector) Collect(ctx context.Context) metrics.Snapshot {
	return m.snapshot
}

func (m *mockCollector) RecordModuleActivity(name string, isActive bool) {}

func (m *mockCollector) GetModuleStatuses() []metrics.ModuleStatus {
	return m.snapshot.Modules
}

func TestDashboardHandlers_DashboardGet_Unauthorized(t *testing.T) {
	// Test that unauthorized requests get 401
	sessionMgr := &mockSessionManager{isAuthenticated: false}

	handlers := NewDashboardHandlers(sessionMgr, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()

	handlers.DashboardGet(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestDashboardHandlers_DashboardGet_Authorized(t *testing.T) {
	// Test that authorized requests get 200 (with mocked UI to avoid template dependency)
	sessionMgr := &mockSessionManager{isAuthenticated: true}

	snapshot := metrics.Snapshot{
		Timestamp: time.Now(),
		Runtime: metrics.RuntimeMetrics{
			NumGoroutine: 10,
			NumCPU:       4,
		},
		Modules: []metrics.ModuleStatus{
			{Name: "discovery", Status: "active"},
		},
	}

	collector := &mockCollector{snapshot: snapshot}

	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any, _ bool) map[string]any {
		data["IsAuthenticated"] = true
		return data
	}

	serverError := func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	handlers := NewDashboardHandlers(sessionMgr, collector, addCommonData, serverError)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()

	handlers.DashboardGet(rr, req)

	// The handler may return 500 if templates aren't initialized, which is expected in unit tests
	// We mainly care that it doesn't return 401 for authenticated users
	if rr.Code == http.StatusUnauthorized {
		t.Error("authenticated user should not get 401")
	}
}

func TestNewDashboardHandlers(t *testing.T) {
	sessionMgr := &mockSessionManager{}

	handlers := NewDashboardHandlers(sessionMgr, nil, nil, nil)

	if handlers == nil {
		t.Fatal("NewDashboardHandlers returned nil")
	}

	if handlers.sessionManager != sessionMgr {
		t.Error("sessionManager not set correctly")
	}
}

func TestDashboardHandlers_DashboardGet_HTMXPartial(t *testing.T) {
	// Initialize templates for this test
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Test that HTMX requests to /dashboard return partial content (just the body)
	sessionMgr := &mockSessionManager{isAuthenticated: true}

	snapshot := metrics.Snapshot{
		Timestamp: time.Now(),
		Runtime: metrics.RuntimeMetrics{
			NumGoroutine: 25,
		},
	}

	collector := &mockCollector{snapshot: snapshot}

	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any, _ bool) map[string]any {
		data["IsAuthenticated"] = true
		return data
	}

	serverError := func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	handlers := NewDashboardHandlers(sessionMgr, collector, addCommonData, serverError)

	// Test with HTMX headers (partial request)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "dashboard-container")
	rr := httptest.NewRecorder()

	handlers.DashboardGet(rr, req)

	// The handler may return 500 if templates aren't initialized, which is expected in unit tests
	// We mainly care that it doesn't return 401 and handles HTMX headers appropriately
	if rr.Code == http.StatusUnauthorized {
		t.Error("authenticated HTMX user should not get 401")
	}

	// Check for Vary header on HX-Request (set by handler)
	vary := rr.Header().Get("Vary")
	if !strings.Contains(vary, "HX-Request") {
		t.Errorf("expected Vary header to contain HX-Request, got: %s", vary)
	}
}

func TestDashboardHandlers_MetricsJSON_Unauthorized(t *testing.T) {
	sessionMgr := &mockSessionManager{isAuthenticated: false}
	handlers := NewDashboardHandlers(sessionMgr, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	rr := httptest.NewRecorder()

	handlers.MetricsJSON(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["error"] != "Unauthorized" {
		t.Errorf("error = %q, want %q", body["error"], "Unauthorized")
	}
}

func TestDashboardHandlers_MetricsJSON_Authorized(t *testing.T) {
	snapshot := metrics.Snapshot{
		Timestamp: time.Now(),
		Runtime: metrics.RuntimeMetrics{
			NumGoroutine: 10,
			NumCPU:       4,
		},
		Modules: []metrics.ModuleStatus{
			{Name: "discovery", Status: "active"},
		},
	}

	sessionMgr := &mockSessionManager{isAuthenticated: true}
	collector := &mockCollector{snapshot: snapshot}
	handlers := NewDashboardHandlers(sessionMgr, collector, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	rr := httptest.NewRecorder()

	handlers.MetricsJSON(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json; charset=utf-8")
	}

	var got metrics.Snapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode metrics JSON: %v", err)
	}
	if got.Runtime.NumGoroutine != snapshot.Runtime.NumGoroutine {
		t.Errorf("NumGoroutine = %d, want %d", got.Runtime.NumGoroutine, snapshot.Runtime.NumGoroutine)
	}
	if got.Runtime.NumCPU != snapshot.Runtime.NumCPU {
		t.Errorf("NumCPU = %d, want %d", got.Runtime.NumCPU, snapshot.Runtime.NumCPU)
	}
	if len(got.Modules) != 1 || got.Modules[0].Name != "discovery" {
		t.Errorf("Modules = %+v, want [discovery]", got.Modules)
	}
}

func TestDashboardHandlers_MetricsJSON_EncodeError(t *testing.T) {
	snapshot := metrics.Snapshot{
		Timestamp: time.Now(),
		Runtime: metrics.RuntimeMetrics{
			NumGoroutine: 10,
		},
	}

	sessionMgr := &mockSessionManager{isAuthenticated: true}
	collector := &mockCollector{snapshot: snapshot}
	handlers := NewDashboardHandlers(sessionMgr, collector, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	w := &errorResponseWriter{}

	// The handler logs the encode error; it must not panic.
	handlers.MetricsJSON(w, req)

	if w.status != http.StatusOK {
		t.Errorf("expected WriteHeader status 200 before encode error, got %d", w.status)
	}
}

// TestDashboardHandlers_DashboardGet_PageRendering verifies that the dashboard
// page renders successfully for an authenticated user with text/html content type.
func TestDashboardHandlers_DashboardGet_PageRendering(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sessionMgr := &mockSessionManager{isAuthenticated: true}
	snapshot := metrics.Snapshot{
		Timestamp: time.Now(),
		Runtime: metrics.RuntimeMetrics{
			NumGoroutine: 10,
			NumCPU:       4,
		},
	}
	collector := &mockCollector{snapshot: snapshot}

	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any, _ bool) map[string]any {
		data["IsAuthenticated"] = true
		return data
	}
	serverError := func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	handlers := NewDashboardHandlers(sessionMgr, collector, addCommonData, serverError)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()

	handlers.DashboardGet(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected text/html content type, got %s", contentType)
	}

	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("failed to parse dashboard HTML: %v", err)
	}
	h1 := testutil.FindElementByTag(doc, "h1")
	if h1 == nil {
		t.Fatal("dashboard page missing h1 element")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(h1)); got != "System Dashboard" {
		t.Errorf("dashboard h1 text = %q, want %q", got, "System Dashboard")
	}
}

// TestDashboardHandlers_PollingPersistsAcrossMultipleRequests verifies that
// the session and authentication persist across multiple HTMX polling requests.
func TestDashboardHandlers_PollingPersistsAcrossMultipleRequests(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sessionMgr := &mockSessionManager{isAuthenticated: true}
	snapshot := metrics.Snapshot{
		Timestamp: time.Now(),
		Runtime: metrics.RuntimeMetrics{
			NumGoroutine: 10,
			NumCPU:       4,
		},
	}
	collector := &mockCollector{snapshot: snapshot}

	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any, _ bool) map[string]any {
		data["IsAuthenticated"] = true
		return data
	}
	serverError := func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	handlers := NewDashboardHandlers(sessionMgr, collector, addCommonData, serverError)

	// Simulate multiple HTMX polling requests with the same authenticated session
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", "dashboard-container")
		rr := httptest.NewRecorder()

		handlers.DashboardGet(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("poll %d: expected status 200, got %d: %s", i+1, rr.Code, rr.Body.String())
		}

		vary := rr.Header().Get("Vary")
		if !strings.Contains(vary, "HX-Request") {
			t.Errorf("poll %d: expected Vary header to contain HX-Request, got: %s", i+1, vary)
		}

		doc, err := testutil.ParseHTML(rr.Body)
		if err != nil {
			t.Fatalf("poll %d: failed to parse partial HTML: %v", i+1, err)
		}

		h1 := testutil.FindElementByTag(doc, "h1")
		if h1 == nil {
			t.Errorf("poll %d: dashboard partial missing h1 element", i+1)
		} else if got := strings.TrimSpace(testutil.GetTextContent(h1)); got != "System Dashboard" {
			t.Errorf("poll %d: dashboard h1 text = %q, want %q", i+1, got, "System Dashboard")
		}

		// The polling element must be outside the swapped container to survive swaps.
		pollers := testutil.FindAllElements(doc, func(n *html.Node) bool {
			return testutil.GetAttr(n, "hx-trigger") == "every 5s"
		})
		if len(pollers) != 0 {
			t.Errorf("poll %d: polling element should NOT be in partial response", i+1)
		}
	}
}
