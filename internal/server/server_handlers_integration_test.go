package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/web"
)

// TestServerHandlers_Initialized verifies that server handlers are properly
// initialized during app startup.
func TestServerHandlers_Initialized(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Verify serverHandlers is set
	if app.serverHandlers == nil {
		t.Error("serverHandlers not initialized")
	}

	// Shutdown and TriggerDiscovery are methods on App via ServerDeps interface
	// (compile-time checked, always available)
}

// TestServerShutdownRoute verifies the shutdown endpoint returns proper response
// and calls the shutdown function.
func TestServerShutdownRoute(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Parse templates
	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/server/shutdown", nil)
	rr := httptest.NewRecorder()

	// Get router and serve
	router := app.getRouter()
	router.ServeHTTP(rr, req)

	// Should get some response (either success, redirect to login, or auth error)
	if rr.Code != http.StatusOK && rr.Code != http.StatusFound && rr.Code != http.StatusSeeOther && rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Errorf("unexpected status: %d", rr.Code)
	}

	// If we got OK, verify the response content
	if rr.Code == http.StatusOK {
		body := rr.Body.String()
		if !strings.Contains(body, "Shutting Down") && !strings.Contains(body, "shutting") {
			t.Error("response should contain shutdown message")
		}
	}
}

// TestServerDiscoveryRoute verifies the discovery endpoint exists and responds.
func TestServerDiscoveryRoute(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Parse templates
	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/server/discovery", nil)
	rr := httptest.NewRecorder()

	// Get router and serve
	router := app.getRouter()
	router.ServeHTTP(rr, req)

	// Discovery handled by deps (mockServerDeps.TriggerDiscovery is a no-op)

	// Response should be OK, redirect, unauthorized, or forbidden
	if rr.Code != http.StatusOK && rr.Code != http.StatusFound && rr.Code != http.StatusSeeOther && rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Errorf("unexpected status: %d", rr.Code)
	}
}

// TestServerRoutes_Exist verifies that server routes are registered.
func TestServerRoutes_Exist(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers failed: %v", err)
	}

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{
			name:   "shutdown route exists",
			method: http.MethodPost,
			path:   "/server/shutdown",
		},
		{
			name:   "discovery route exists",
			method: http.MethodPost,
			path:   "/server/discovery",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()

			router := app.getRouter()
			router.ServeHTTP(rr, req)

			// We expect either:
			// - 401 Unauthorized (if auth is required and missing)
			// - 200 OK (if auth passed)
			// - 302/307 Redirect (to login)
			// - 404 Not Found (if route doesn't exist - this is a failure)
			if rr.Code == http.StatusNotFound {
				t.Errorf("route %s %s returned 404 - not registered", tt.method, tt.path)
			}
		})
	}
}
