package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/web"
)

// mockSessionManagerAuthenticated implements SessionManager for testing
type mockSessionManagerAuthenticated struct{}

func (m *mockSessionManagerAuthenticated) IsAuthenticated(r *http.Request) bool {
	return true
}

// mockSessionManagerUnauthenticated implements SessionManager for testing
type mockSessionManagerUnauthenticated struct{}

func (m *mockSessionManagerUnauthenticated) IsAuthenticated(r *http.Request) bool {
	return false
}

func TestNewServerHandlers(t *testing.T) {
	sm := &mockSessionManagerAuthenticated{}
	shutdownCalled := make(chan bool, 1)
	discoveryCalled := make(chan bool, 1)

	handlers := NewServerHandlers(
		sm,
		func() { shutdownCalled <- true },
		func() { discoveryCalled <- true },
		nil,
		nil, // StartCacheBatchLoad
		nil,
		nil,
	)

	if handlers == nil {
		t.Fatal("NewServerHandlers returned nil")
	}
	if handlers.sessionManager != sm {
		t.Error("sessionManager not set correctly")
	}
	if handlers.ShutdownFunc == nil {
		t.Error("ShutdownFunc not set")
	}
	if handlers.DiscoveryFunc == nil {
		t.Error("DiscoveryFunc not set")
	}

	// Verify the functions work
	handlers.ShutdownFunc()
	handlers.DiscoveryFunc()

	select {
	case <-shutdownCalled:
		// Good
	default:
		t.Error("ShutdownFunc callback not received")
	}
	select {
	case <-discoveryCalled:
		// Good
	default:
		t.Error("DiscoveryFunc callback not received")
	}
}

func TestServerShutdownPost_Unauthorized(t *testing.T) {
	sm := &mockSessionManagerUnauthenticated{}
	handlers := NewServerHandlers(sm, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/server/shutdown", nil)
	rr := httptest.NewRecorder()

	handlers.ServerShutdownPost(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

func TestServerShutdownPost_Authorized(t *testing.T) {
	// Initialize templates
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerAuthenticated{}
	shutdownCalled := make(chan bool, 1)

	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any, _ bool) map[string]any {
		data["CSRFToken"] = "test-token"
		return data
	}

	handlers := NewServerHandlers(
		sm,
		func() { shutdownCalled <- true },
		nil,
		nil,
		nil, // StartCacheBatchLoad
		addCommonData,
		nil,
	)

	req := httptest.NewRequest(http.MethodPost, "/server/shutdown", nil)
	rr := httptest.NewRecorder()

	handlers.ServerShutdownPost(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected Content-Type text/html, got %s", contentType)
	}

	doc, err := html.Parse(strings.NewReader(rr.Body.String()))
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}
	msg := findTextContains(doc, "Shutting Down")
	if msg == "" {
		t.Error("response body should contain 'Shutting Down'")
	}

	// Shutdown should be called asynchronously
	select {
	case <-shutdownCalled:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("ShutdownFunc was not called")
	}
}

func TestServerDiscoveryPost_Unauthorized(t *testing.T) {
	sm := &mockSessionManagerUnauthenticated{}
	handlers := NewServerHandlers(sm, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/server/discovery", nil)
	rr := httptest.NewRecorder()

	handlers.ServerDiscoveryPost(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

func TestServerDiscoveryPost_Authorized(t *testing.T) {
	// Initialize templates
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerAuthenticated{}
	discoveryCalled := make(chan bool, 1)

	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any, _ bool) map[string]any {
		data["CSRFToken"] = "test-token"
		return data
	}

	handlers := NewServerHandlers(
		sm,
		nil,
		func() { discoveryCalled <- true },
		nil,
		nil, // StartCacheBatchLoad
		addCommonData,
		nil,
	)

	req := httptest.NewRequest(http.MethodPost, "/server/discovery", nil)
	rr := httptest.NewRecorder()

	handlers.ServerDiscoveryPost(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected Content-Type text/html, got %s", contentType)
	}

	doc, err := html.Parse(strings.NewReader(rr.Body.String()))
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	// Check for discovery message in body
	hasDiscovery := findTextContains(doc, "discovery")
	hasStarted := findTextContains(doc, "started")

	if hasDiscovery == "" || hasStarted == "" {
		t.Error("response body should contain discovery and started message")
	}

	// Discovery should be called
	select {
	case <-discoveryCalled:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("DiscoveryFunc should have been called")
	}
}

func TestServerDiscoveryPost_NoCommonData(t *testing.T) {
	sm := &mockSessionManagerAuthenticated{}
	discoveryCalled := make(chan bool, 1)

	handlers := NewServerHandlers(
		sm,
		nil,
		func() { discoveryCalled <- true },
		nil,
		nil, // StartCacheBatchLoad
		nil, // No AddCommonTemplateData
		nil,
	)

	req := httptest.NewRequest(http.MethodPost, "/server/discovery", nil)
	rr := httptest.NewRecorder()

	handlers.ServerDiscoveryPost(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	// Discovery is called asynchronously
	select {
	case <-discoveryCalled:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("DiscoveryFunc should have been called")
	}
}

func TestServerCacheBatchLoadPost_Unauthorized(t *testing.T) {
	sm := &mockSessionManagerUnauthenticated{}
	handlers := NewServerHandlers(sm, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/server/cache-batch-load", nil)
	rr := httptest.NewRecorder()

	handlers.ServerCacheBatchLoadPost(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

func TestServerCacheBatchLoadPost_BlockedWhenDiscoveryActive(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerAuthenticated{}
	startFunc := func() (StartCacheBatchLoadResult, error) {
		return StartCacheBatchLoadResult{
			Blocked: true,
			Message: "Cache batch load blocked: discovery active",
		}, nil
	}
	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any, _ bool) map[string]any {
		data["CSRFToken"] = "test-token"
		return data
	}

	handlers := NewServerHandlers(sm, nil, nil, nil, startFunc, addCommonData, nil)

	req := httptest.NewRequest(http.MethodPost, "/server/cache-batch-load", nil)
	rr := httptest.NewRecorder()

	handlers.ServerCacheBatchLoadPost(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("expected status %d, got %d", http.StatusConflict, rr.Code)
	}

	doc, err := html.Parse(strings.NewReader(rr.Body.String()))
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}
	msg := findTextContains(doc, "discovery active")
	if msg == "" {
		t.Error("expected response body to contain 'discovery active'")
	}
}

func TestServerCacheBatchLoadPost_StartsRunWhenIdle(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerAuthenticated{}
	startFunc := func() (StartCacheBatchLoadResult, error) {
		return StartCacheBatchLoadResult{
			Blocked: false,
			Message: "Cache batch load started",
		}, nil
	}
	addCommonData := func(w http.ResponseWriter, r *http.Request, data map[string]any, _ bool) map[string]any {
		data["CSRFToken"] = "test-token"
		return data
	}

	handlers := NewServerHandlers(sm, nil, nil, nil, startFunc, addCommonData, nil)

	req := httptest.NewRequest(http.MethodPost, "/server/cache-batch-load", nil)
	rr := httptest.NewRecorder()

	handlers.ServerCacheBatchLoadPost(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	doc, err := html.Parse(strings.NewReader(rr.Body.String()))
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}
	msg := findTextContains(doc, "Cache batch load started")
	if msg == "" {
		t.Error("expected response body to contain 'Cache batch load started'")
	}
}

// findTextContains searches the HTML tree for a text node containing s.
func findTextContains(n *html.Node, s string) string {
	if n.Type == html.TextNode && strings.Contains(n.Data, s) {
		return n.Data
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findTextContains(c, s); found != "" {
			return found
		}
	}
	return ""
}
