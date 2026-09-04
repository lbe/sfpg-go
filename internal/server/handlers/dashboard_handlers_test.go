package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
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

	handlers := NewDashboardHandlers(sessionMgr, nil, &mockServerControl{}, nil, nil)

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

	handlers := NewDashboardHandlers(sessionMgr, collector, &mockServerControl{}, addCommonData, serverError)

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

	handlers := NewDashboardHandlers(sessionMgr, nil, &mockServerControl{}, nil, nil)

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

	handlers := NewDashboardHandlers(sessionMgr, collector, &mockServerControl{}, addCommonData, serverError)

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

	handlers := NewDashboardHandlers(sessionMgr, collector, &mockServerControl{}, addCommonData, serverError)

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

	handlers := NewDashboardHandlers(sessionMgr, collector, &mockServerControl{}, addCommonData, serverError)

	// Simulate multiple HTMX polling requests with the same authenticated session
	for i := range 3 {
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

	handlers := NewDashboardHandlers(sessionMgr, collector, &mockServerControl{}, addCommonData, serverError)

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

// TestDashboardHandlers_DashboardVersionID renders the dashboard as an HTMX
// partial and asserts the #dashboard-version element carries the version text.
func TestDashboardHandlers_DashboardVersionID(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sessionMgr := &mockSessionManager{isAuthenticated: true}
	snapshot := metrics.Snapshot{
		Timestamp: time.Now(),
		Runtime:   metrics.RuntimeMetrics{NumGoroutine: 10, NumCPU: 4},
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

	handlers := NewDashboardHandlers(sessionMgr, collector, &mockServerControl{}, addCommonData, serverError)

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

	ver := testutil.FindElementByID(doc, "dashboard-version")
	if ver == nil {
		t.Fatal("missing #dashboard-version")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(ver)); got != "0.0.0-test" {
		t.Errorf("#dashboard-version text = %q, want %q", got, "0.0.0-test")
	}
}

// TestDashboardHandlers_GalleryStatIDs renders the dashboard as an HTMX
// partial and asserts the #gallery-folders, #gallery-images, and
// #gallery-images-size stat values plus the discovery date attributes on
// #gallery-discovery-tip.
func TestDashboardHandlers_GalleryStatIDs(t *testing.T) {
	h := newTestDashboardHandlers(t, &stubManualDiscoveryErrorSource{})

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "dashboard-container")
	rr := httptest.NewRecorder()

	h.DashboardGet(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("failed to parse partial HTML: %v", err)
	}

	folders := testutil.FindElementByID(doc, "gallery-folders")
	if folders == nil {
		t.Fatal("missing #gallery-folders")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(folders)); got != "10" {
		t.Errorf("#gallery-folders text = %q, want %q", got, "10")
	}
	images := testutil.FindElementByID(doc, "gallery-images")
	if images == nil {
		t.Fatal("missing #gallery-images")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(images)); got != "100" {
		t.Errorf("#gallery-images text = %q, want %q", got, "100")
	}
	size := testutil.FindElementByID(doc, "gallery-images-size")
	if size == nil {
		t.Fatal("missing #gallery-images-size")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(size)); got == "" {
		t.Error("#gallery-images-size text is empty")
	}
	tip := testutil.FindElementByID(doc, "gallery-discovery-tip")
	if tip == nil {
		t.Fatal("missing #gallery-discovery-tip")
	}
	if got := testutil.GetAttr(tip, "data-first-discovery"); got != "2026-01-01 00:00:00" {
		t.Errorf("data-first-discovery = %q, want %q", got, "2026-01-01 00:00:00")
	}
	if got := testutil.GetAttr(tip, "data-last-discovery"); got != "2026-01-02 00:00:00" {
		t.Errorf("data-last-discovery = %q, want %q", got, "2026-01-02 00:00:00")
	}
}

// TestDashboardHandlers_FileProcessingStatIDs renders the full dashboard page
// with a non-zero FileProcessing snapshot and asserts the comma-formatted
// values carried by the #fp-* and #queue-queued stat elements. A template
// that always renders zero would fail this lock.
func TestDashboardHandlers_FileProcessingStatIDs(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}
	sessionMgr := &mockSessionManager{isAuthenticated: true}
	snapshot := metrics.Snapshot{
		Timestamp: time.Now(),
		Runtime:   metrics.RuntimeMetrics{NumGoroutine: 10, NumCPU: 4},
		FileProcessing: metrics.FileProcessingMetrics{
			TotalFound:      15666608,
			AlreadyExisting: 15620677,
			NewlyInserted:   40000,
			SkippedInvalid:  5931,
			InFlight:        0,
		},
		QueueLength: 0,
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

	h := NewDashboardHandlers(sessionMgr, collector, &stubManualDiscoveryErrorSource{}, addCommonData, serverError)
	doc := renderDashboardDocument(t, h)

	want := map[string]string{
		"fp-total":     "15,666,608",
		"fp-existing":  "15,620,677",
		"fp-new":       "40,000",
		"fp-invalid":   "5,931",
		"fp-inflight":  "0",
		"queue-queued": "0",
	}
	for id, wantText := range want {
		el := testutil.FindElementByID(doc, id)
		if el == nil {
			t.Errorf("missing #%s in full-page dashboard render", id)
			continue
		}
		if got := strings.TrimSpace(testutil.GetTextContent(el)); got != wantText {
			t.Errorf("#%s text = %q, want %q", id, got, wantText)
		}
	}
}

// stubManualDiscoveryErrorSource is a controllable ManualDiscoveryErrorSource
// for dashboard handler tests.
type stubManualDiscoveryErrorSource struct {
	msg string
}

func (s *stubManualDiscoveryErrorSource) ManualDiscoveryError() string       { return s.msg }
func (s *stubManualDiscoveryErrorSource) SetManualDiscoveryError(msg string) { s.msg = msg }

// newTestDashboardHandlers builds a DashboardHandlers wired to a controllable
// discovery-error source and renders templates from the embedded filesystem.
func newTestDashboardHandlers(t *testing.T, disc ManualDiscoveryErrorSource) *DashboardHandlers {
	t.Helper()
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}
	sessionMgr := &mockSessionManager{isAuthenticated: true}
	snapshot := metrics.Snapshot{
		Timestamp: time.Now(),
		Runtime:   metrics.RuntimeMetrics{NumGoroutine: 10, NumCPU: 4},
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
	return NewDashboardHandlers(sessionMgr, collector, disc, addCommonData, serverError)
}

// TestDashboardHandlers_RebuildErrorMsgID asserts the body message element
// #folder-index-rebuild-error-msg carries the collapsed rebuild-error sentence.
func TestDashboardHandlers_RebuildErrorMsgID(t *testing.T) {
	disc := &stubManualDiscoveryErrorSource{msg: "file_folder_index rebuild failed: disk full"}
	h := newTestDashboardHandlers(t, disc)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()
	h.DashboardGet(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("parse html: %v", err)
	}
	el := testutil.FindElementByID(doc, "folder-index-rebuild-error-msg")
	if el == nil {
		t.Fatal("missing #folder-index-rebuild-error-msg")
	}
	got := strings.Join(strings.Fields(testutil.GetTextContent(el)), " ")
	const want = "Manual discovery failed to rebuild the file's folder index. The live index is unchanged."
	if got != want {
		t.Errorf("#folder-index-rebuild-error-msg text = %q, want %q", got, want)
	}
}

// TestDashboardHandlers_RebuildErrorShownWhenSet asserts the alert element is
// present when a manual discovery rebuild error is set, for both full and HTMX
// partial renders, and absent after it is cleared.
func TestDashboardHandlers_RebuildErrorShownWhenSet(t *testing.T) {
	disc := &stubManualDiscoveryErrorSource{msg: "file_folder_index rebuild failed: disk full"}
	h := newTestDashboardHandlers(t, disc)

	renderAndAssert := func(t *testing.T, partial bool) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		if partial {
			req.Header.Set("HX-Request", "true")
			req.Header.Set("HX-Target", "dashboard-container")
		}
		rr := httptest.NewRecorder()
		h.DashboardGet(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		doc, err := testutil.ParseHTML(rr.Body)
		if err != nil {
			t.Fatalf("parse html: %v", err)
		}
		alert := testutil.FindElementByID(doc, "folder-index-rebuild-error")
		if alert == nil {
			t.Fatalf("expected #folder-index-rebuild-error for partial=%v", partial)
		}
		ack := testutil.FindElementByID(doc, "folder-index-rebuild-error-ack")
		if ack == nil {
			t.Fatalf("expected #folder-index-rebuild-error-ack for partial=%v", partial)
		}
	}

	renderAndAssert(t, false)
	renderAndAssert(t, true)

	// Clearing the error removes the alert.
	disc.SetManualDiscoveryError("")
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()
	h.DashboardGet(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("parse html: %v", err)
	}
	if testutil.FindElementByID(doc, "folder-index-rebuild-error") != nil {
		t.Error("expected #folder-index-rebuild-error to be absent after clearing")
	}
}

// TestDashboardHandlers_RebuildErrorAbsentWhenClear verifies that no alert is
// rendered when there is no manual discovery rebuild error.
func TestDashboardHandlers_RebuildErrorAbsentWhenClear(t *testing.T) {
	h := newTestDashboardHandlers(t, &stubManualDiscoveryErrorSource{})

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()
	h.DashboardGet(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("parse html: %v", err)
	}
	if testutil.FindElementByID(doc, "folder-index-rebuild-error") != nil {
		t.Error("expected no #folder-index-rebuild-error when error is empty")
	}
}

// TestDashboardHandlers_RebuildErrorUnauthorized verifies GET /dashboard needs
// auth and does not leak the alert to unauthenticated callers.
func TestDashboardHandlers_RebuildErrorUnauthorized(t *testing.T) {
	sessionMgr := &mockSessionManager{isAuthenticated: false}
	h := NewDashboardHandlers(sessionMgr, &mockCollector{}, &stubManualDiscoveryErrorSource{msg: "boom"}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()
	h.DashboardGet(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// renderDashboardDocument performs a full-page GET /dashboard render (no HTMX
// headers) so #dashboard-container exists, and returns the parsed document.
func renderDashboardDocument(t *testing.T, h *DashboardHandlers) *html.Node {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()
	h.DashboardGet(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("failed to parse dashboard HTML: %v", err)
	}
	return doc
}

// collectElementIDs walks the node tree and returns the count of every id
// attribute present.
func collectElementIDs(n *html.Node) map[string]int {
	ids := make(map[string]int)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if id := testutil.GetAttr(n, "id"); id != "" {
				ids[id]++
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return ids
}

// isDecorativeSVG reports whether n is an svg icon whose nearest element
// ancestor is a card-title h3. Decorative header icons are allowlisted in the
// scrapable-node walk.
func isDecorativeSVG(n *html.Node) bool {
	if n.Type != html.ElementNode || n.Data != "svg" {
		return false
	}
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && p.Data == "h3" {
			return true
		}
	}
	return false
}

// isStatTitle reports whether n is a stat label div (class "stat-title").
// These labels never need scrapable ids and are allowlisted.
func isStatTitle(n *html.Node) bool {
	if n.Type != html.ElementNode || n.Data != "div" {
		return false
	}
	return slices.Contains(strings.Fields(testutil.GetAttr(n, "class")), "stat-title")
}

// TestDashboardHandlers_NoDuplicateIDs renders the full dashboard page and
// fails when any id attribute appears more than once in the document. This is
// a supplemental rendered check; if/else branches render only one id per
// request, so the source-level gate is TestDashboardTemplates_NoDuplicateIDAttributesInSource.
func TestDashboardHandlers_NoDuplicateIDs(t *testing.T) {
	h := newTestDashboardHandlers(t, &stubManualDiscoveryErrorSource{})
	doc := renderDashboardDocument(t, h)

	for id, count := range collectElementIDs(doc) {
		if count > 1 {
			t.Errorf("id %q appears %d times in full-page dashboard render", id, count)
		}
	}
}

// TestDashboardHandlers_ScrapableNodesHaveIDs renders the full dashboard page
// and fails when any scrapable node under #dashboard-container lacks an id.
// Scrapable nodes are stat-value divs, progress elements, card-title h3
// elements, and nodes carrying the known scraped ids. Decorative header svgs
// and stat-title label divs are allowlisted.
func TestDashboardHandlers_ScrapableNodesHaveIDs(t *testing.T) {
	h := newTestDashboardHandlers(t, &stubManualDiscoveryErrorSource{msg: "file_folder_index rebuild failed: disk full"})
	doc := renderDashboardDocument(t, h)

	container := testutil.FindElementByID(doc, "dashboard-container")
	if container == nil {
		t.Fatal("full-page dashboard render missing #dashboard-container")
	}

	knownCardTitles := map[string]bool{
		"Memory":             true,
		"Runtime":            true,
		"Gallery Statistics": true,
		"File Processing":    true,
		"Cache Preload":      true,
		"Cache Batch Load":   true,
		"HTTP Cache":         true,
		"Worker Pool":        true,
		"Write Batcher":      true,
	}
	knownIDs := map[string]bool{
		"dashboard-title":                  true,
		"dashboard-live":                   true,
		"dashboard-version":                true,
		"last-updated":                     true,
		"folder-index-rebuild-error-title": true,
		"folder-index-rebuild-error-msg":   true,
		"gallery-loading":                  true,
		"preload-status":                   true,
		"batch-status":                     true,
		"http-status":                      true,
	}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if !isDecorativeSVG(n) && !isStatTitle(n) {
				scrapable := slices.Contains(strings.Fields(testutil.GetAttr(n, "class")), "stat-value")
				if n.Data == "progress" {
					scrapable = true
				}
				if n.Data == "h3" && knownCardTitles[strings.TrimSpace(testutil.GetTextContent(n))] {
					scrapable = true
				}
				if knownIDs[testutil.GetAttr(n, "id")] {
					scrapable = true
				}
				if scrapable && testutil.GetAttr(n, "id") == "" {
					t.Errorf("scrapable <%s> under #dashboard-container has no id", n.Data)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(container)
}

// TestDashboardHandlers_CardTitleIDs renders the full dashboard page with a
// gallery and a folder-index rebuild error present, and asserts every scraped
// card title and header element carries its expected id.
func TestDashboardHandlers_CardTitleIDs(t *testing.T) {
	h := newTestDashboardHandlers(t, &stubManualDiscoveryErrorSource{msg: "file_folder_index rebuild failed: disk full"})
	doc := renderDashboardDocument(t, h)

	ids := []string{
		"card-memory",
		"card-runtime",
		"card-gallery-stats",
		"card-file-processing",
		"card-cache-preload",
		"card-cache-batch-load",
		"card-http-cache",
		"card-worker-pool",
		"card-write-batcher",
		"dashboard-title",
		"dashboard-live",
		"folder-index-rebuild-error-title",
	}
	for _, id := range ids {
		if testutil.FindElementByID(doc, id) == nil {
			t.Errorf("missing #%s in full-page dashboard render", id)
		}
	}
}

// TestDashboardHandlers_GalleryLoadingIDs renders the full dashboard page
// without GalleryStats data so the loading branch of the gallery card renders,
// and asserts the loading branch carries #card-gallery-stats and
// #gallery-loading.
func TestDashboardHandlers_GalleryLoadingIDs(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}
	sessionMgr := &mockSessionManager{isAuthenticated: true}
	snapshot := metrics.Snapshot{
		Timestamp: time.Now(),
		Runtime:   metrics.RuntimeMetrics{NumGoroutine: 10, NumCPU: 4},
	}
	collector := &mockCollector{snapshot: snapshot}
	// Intentionally omit the GalleryStats key so the loading branch renders.
	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any, _ bool) map[string]any {
		data["IsAuthenticated"] = true
		data["Version"] = "0.0.0-test"
		return data
	}
	serverError := func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	h := NewDashboardHandlers(sessionMgr, collector, &stubManualDiscoveryErrorSource{}, addCommonData, serverError)
	doc := renderDashboardDocument(t, h)

	if testutil.FindElementByID(doc, "card-gallery-stats") == nil {
		t.Error("missing #card-gallery-stats in gallery loading branch")
	}
	if testutil.FindElementByID(doc, "gallery-loading") == nil {
		t.Error("missing #gallery-loading in gallery loading branch")
	}
}
