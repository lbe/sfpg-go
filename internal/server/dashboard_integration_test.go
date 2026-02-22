//go:build integration

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.local/sfpg/internal/server/metrics"
	"go.local/sfpg/web"
)

// TestDashboardPageRendering verifies the dashboard page renders correctly
// with proper authentication and template setup.
func TestDashboardPageRendering(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	app := CreateApp(t, false)
	defer app.Shutdown()

	// Parse templates
	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers failed: %v", err)
	}

	// Create a request with the session cookie from app's test session
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("Cookie", "session-name=test-session")
	rr := httptest.NewRecorder()

	// Set up an authenticated session
	if app.store != nil {
		session, _ := app.store.Get(req, "session-name")
		session.Values["authenticated"] = true
		session.Save(req, rr)
	}

	// Get fresh request with the saved session cookie
	cookies := rr.Header()["Set-Cookie"]
	req = httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	for _, c := range cookies {
		req.Header.Add("Cookie", c)
	}
	rr = httptest.NewRecorder()

	// Serve the request
	app.getRouter().ServeHTTP(rr, req)

	// Check status
	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Check content type
	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected text/html content type, got %s", contentType)
	}

	// Check body contains expected elements
	body := rr.Body.String()
	if !strings.Contains(body, "System Dashboard") {
		t.Error("dashboard page should contain 'System Dashboard'")
	}
	if !strings.Contains(body, "Write Batcher") {
		t.Error("dashboard page should contain 'Write Batcher'")
	}
	if !strings.Contains(body, "Memory") {
		t.Error("dashboard page should contain 'Memory'")
	}
}

// TestDashboardHTMXPartialEndpoint verifies the dashboard returns HTML partial for HTMX requests
func TestDashboardHTMXPartialEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	app := CreateApp(t, false)
	defer app.Shutdown()

	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "dashboard-container")
	rr := httptest.NewRecorder()

	// Set up an authenticated session
	if app.store != nil {
		session, _ := app.store.Get(req, "session-name")
		session.Values["authenticated"] = true
		session.Save(req, rr)
	}

	// Get fresh request with the saved session cookie
	cookies := rr.Header()["Set-Cookie"]
	req = httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "dashboard-container")
	for _, c := range cookies {
		req.Header.Add("Cookie", c)
	}
	rr = httptest.NewRecorder()

	app.getRouter().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected text/html content type, got %s", contentType)
	}

	// Verify Vary header is set for HTMX (check all values since there may be multiple)
	varyHeaders := rr.Header().Values("Vary")
	vary := strings.Join(varyHeaders, ", ")
	if !strings.Contains(vary, "HX-Request") {
		t.Errorf("expected Vary header to contain HX-Request, got: %s", vary)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "System Dashboard") {
		t.Error("dashboard partial should contain 'System Dashboard'")
	}

	// Partial should NOT contain the full layout elements
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("dashboard partial should not contain DOCTYPE (it's a partial, not full page)")
	}

	// CRITICAL: Verify the polling element is NOT in the response
	// The polling element must be outside #dashboard-container to survive swaps
	if strings.Contains(body, `hx-trigger="every 5s"`) {
		t.Error("dashboard partial should NOT contain the polling element - it must be outside the swapped container to survive")
	}
}

// TestDashboardPollingPersistsAcrossMultipleRequests verifies that the session
// and authentication persist across multiple HTMX polling requests.
func TestDashboardPollingPersistsAcrossMultipleRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	app := CreateApp(t, false)
	defer app.Shutdown()

	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers failed: %v", err)
	}

	// First, do a full page load to establish session
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()

	// Set up an authenticated session
	if app.store != nil {
		session, _ := app.store.Get(req, "session-name")
		session.Values["authenticated"] = true
		session.Save(req, rr)
	}

	// Get the session cookie
	cookies := rr.Header()["Set-Cookie"]

	// Now simulate multiple HTMX polling requests using the same session
	for i := 0; i < 3; i++ {
		req = httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", "dashboard-container")
		for _, c := range cookies {
			req.Header.Add("Cookie", c)
		}
		rr = httptest.NewRecorder()

		app.getRouter().ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("poll %d: expected status 200, got %d", i+1, rr.Code)
		}

		body := rr.Body.String()
		if !strings.Contains(body, "System Dashboard") {
			t.Errorf("poll %d: dashboard partial should contain 'System Dashboard'", i+1)
		}

		// CRITICAL: Verify no polling element in response (would indicate it's inside container)
		if strings.Contains(body, `hx-trigger="every 5s"`) {
			t.Errorf("poll %d: polling element should NOT be in partial response - it must survive outside swapped container", i+1)
		}
	}
}

// TestDashboardUnauthorized verifies unauthorized access is rejected
func TestDashboardUnauthorized(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	app := CreateApp(t, false)
	defer app.Shutdown()

	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers failed: %v", err)
	}

	// Request without authentication
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()

	app.getRouter().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

// TestMetricsCollectorWiring verifies metrics sources are properly wired
func TestMetricsCollectorWiring(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	app := CreateApp(t, false)
	defer app.Shutdown()

	if app.metricsCollector == nil {
		t.Fatal("metricsCollector should be initialized")
	}

	// Collect metrics and verify structure
	ctx := app.ctx
	snapshot := app.metricsCollector.Collect(ctx)

	// Verify timestamp is set
	if snapshot.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}

	// Verify runtime metrics
	if snapshot.Runtime.NumGoroutine == 0 {
		t.Error("NumGoroutine should be non-zero")
	}
	if snapshot.Runtime.NumCPU == 0 {
		t.Error("NumCPU should be non-zero")
	}

	// Verify uptime is set
	if snapshot.Runtime.Uptime == 0 {
		t.Error("Uptime should be non-zero")
	}
}

// TestDashboardMetricsWithSources verifies dashboard shows metrics from actual sources
func TestDashboardMetricsWithSources(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	app := CreateApp(t, false)
	defer app.Shutdown()

	// Wire up metrics sources
	app.metricsCollector = metrics.NewCollector()
	if app.writeBatcher != nil {
		app.metricsCollector.SetWriteBatcher(&writeBatcherAdapter{wb: app.writeBatcher})
	}
	if app.pool != nil {
		app.metricsCollector.SetWorkerPool(&workerPoolAdapter{pool: app.pool})
	}
	if app.preloadManager != nil {
		app.metricsCollector.SetCachePreload(&cachePreloadAdapter{pm: app.preloadManager})
	}
	if app.q != nil {
		app.metricsCollector.SetQueueInfo(func() int { return app.q.Len() }, 10000)
	}

	// Record some module activity
	app.metricsCollector.RecordModuleActivity("discovery", true)
	app.metricsCollector.RecordModuleActivity("cache_preload", false)

	// Collect and verify
	snapshot := app.metricsCollector.Collect(app.ctx)

	// Should have module statuses
	if len(snapshot.Modules) != 2 {
		t.Errorf("expected 2 module statuses, got %d", len(snapshot.Modules))
	}

	// Check discovery is active
	foundDiscovery := false
	for _, m := range snapshot.Modules {
		if m.Name == "discovery" && m.Status == "active" {
			foundDiscovery = true
			break
		}
	}
	if !foundDiscovery {
		t.Error("discovery module should be active")
	}
}
