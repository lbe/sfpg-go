package handlers

import (
	"context"
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
}

func (m *mockSessionManager) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	return m.isAuthenticated
}

func (m *mockSessionManager) ClearSession(w http.ResponseWriter, r *http.Request) {}

// mockCollector is a mock metrics collector for testing
type mockCollector struct {
	snapshot metrics.Snapshot
}

func (m *mockCollector) Collect(ctx context.Context) metrics.Snapshot {
	return m.snapshot
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
	if got := strings.TrimSpace(testutil.GetTextContent(h1)); got != "Performance & Health Dashboard" {
		t.Errorf("dashboard h1 text = %q, want %q", got, "Performance & Health Dashboard")
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
		} else if got := strings.TrimSpace(testutil.GetTextContent(h1)); got != "Performance & Health Dashboard" {
			t.Errorf("poll %d: dashboard h1 text = %q, want %q", i+1, got, "Performance & Health Dashboard")
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

// fakeGalleryStats is a locally defined fake that satisfies every method
// the dashboard templates call on GalleryStats. It is used to render the
// dashboard partial without importing internal/server.
type fakeGalleryStats struct{}

func (fakeGalleryStats) Folders() string        { return "10" }
func (fakeGalleryStats) Images() string         { return "100" }
func (fakeGalleryStats) ImagesSize() int64      { return 1024 }
func (fakeGalleryStats) FirstDiscovery() string { return "2026-01-01 00:00:00" }
func (fakeGalleryStats) LastDiscovery() string  { return "2026-01-02 00:00:00" }
func (fakeGalleryStats) FoldersCount() int64    { return 10 }
func (fakeGalleryStats) ImagesCount() int64     { return 100 }

// TestDashboardHandlers_CardTitleOrder renders the dashboard as an HTMX partial
// (HX-Request: true, HX-Target: dashboard-container) and asserts that all card
// titles appear in the correct diagram order. It uses a locally defined
// fakeGalleryStats so the {{ if .GalleryStats }} cards render without importing
// internal/server.
func TestDashboardHandlers_CardTitleOrder(t *testing.T) {
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
		data["Version"] = "0.0.0-test"
		data["GalleryStats"] = fakeGalleryStats{}
		return data
	}
	serverError := func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	handlers := NewDashboardHandlers(sessionMgr, collector, addCommonData, serverError)

	// Render as HTMX partial so the About modal's duplicate "Gallery Statistics"
	// text is NOT in the document.
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "dashboard-container")
	rr := httptest.NewRecorder()

	handlers.DashboardGet(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("failed to parse partial HTML: %v", err)
	}

	// Collect all h3 element text content in document order.
	var titles []string
	h3Nodes := testutil.FindAllElements(doc, func(n *html.Node) bool {
		return n.Data == "h3"
	})
	for _, h3 := range h3Nodes {
		text := strings.TrimSpace(testutil.GetTextContent(h3))
		if text != "" {
			titles = append(titles, text)
		}
	}

	expected := []string{
		"Memory",
		"Runtime",
		"Gallery Statistics",
		"File Processing",
		"Cache Preload",
		"Cache Batch Load",
		"HTTP Cache",
		"Worker Pool",
		"Write Batcher",
	}

	if len(titles) != len(expected) {
		t.Errorf("expected %d card titles, got %d\ngot:  %q", len(expected), len(titles), titles)
	} else {
		for i := range expected {
			if titles[i] != expected[i] {
				t.Errorf("title[%d] = %q, want %q\nfull list: %q", i, titles[i], expected[i], titles)
			}
		}
	}
}
