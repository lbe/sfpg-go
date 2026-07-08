package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/sessions"
	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/web"
)

// mockSessionManagerAuthenticated implements SessionManager for testing
type mockSessionManagerAuthenticated struct{}

func (m *mockSessionManagerAuthenticated) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerAuthenticated) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return "test-csrf-token"
}

func (m *mockSessionManagerAuthenticated) ValidateCSRFToken(r *http.Request) bool {
	return false // Default to false for testing CSRF failure
}

func (m *mockSessionManagerAuthenticated) ClearSession(w http.ResponseWriter, r *http.Request) {}

func (m *mockSessionManagerAuthenticated) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	return sessions.NewSession(nil, session.SessionName), nil
}

func (m *mockSessionManagerAuthenticated) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerAuthenticated) IsAuthenticated(r *http.Request) bool {
	return true
}

func (m *mockSessionManagerAuthenticated) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return nil
}

// mockSessionManagerUnauthenticated implements SessionManager for testing
type mockSessionManagerUnauthenticated struct{}

func (m *mockSessionManagerUnauthenticated) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerUnauthenticated) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return "test-csrf-token"
}

func (m *mockSessionManagerUnauthenticated) ValidateCSRFToken(r *http.Request) bool {
	return false
}

func (m *mockSessionManagerUnauthenticated) ClearSession(w http.ResponseWriter, r *http.Request) {}

func (m *mockSessionManagerUnauthenticated) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	return sessions.NewSession(nil, session.SessionName), nil
}

func (m *mockSessionManagerUnauthenticated) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerUnauthenticated) IsAuthenticated(r *http.Request) bool {
	return false
}

func (m *mockSessionManagerUnauthenticated) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return nil
}

// mockSessionManagerWithCSRF implements SessionManager that validates CSRF successfully
type mockSessionManagerWithCSRF struct{}

func (m *mockSessionManagerWithCSRF) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerWithCSRF) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return "valid-csrf-token"
}

func (m *mockSessionManagerWithCSRF) ValidateCSRFToken(r *http.Request) bool {
	return true
}

func (m *mockSessionManagerWithCSRF) ClearSession(w http.ResponseWriter, r *http.Request) {}

func (m *mockSessionManagerWithCSRF) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	return sessions.NewSession(nil, session.SessionName), nil
}

func (m *mockSessionManagerWithCSRF) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerWithCSRF) IsAuthenticated(r *http.Request) bool {
	return true
}

func (m *mockSessionManagerWithCSRF) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return nil
}

func TestNewServerHandlers(t *testing.T) {
	sm := &mockSessionManagerAuthenticated{}
	handlers := NewServerHandlers(sm, &mockServerDeps{})

	if handlers == nil {
		t.Fatal("NewServerHandlers returned nil")
	}
	if handlers.sessionManager != sm {
		t.Error("sessionManager not set correctly")
	}
}

func TestServerShutdownPost_Unauthorized(t *testing.T) {
	sm := &mockSessionManagerUnauthenticated{}
	handlers := NewServerHandlers(sm, &mockServerDeps{})

	req := httptest.NewRequest(http.MethodPost, "/server/shutdown", nil)
	rr := httptest.NewRecorder()

	handlers.ServerShutdownPost(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

func TestServerShutdownPost_CSRFFailed(t *testing.T) {
	// Initialize templates
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerAuthenticated{}
	handlers := NewServerHandlers(sm, &mockServerDeps{})

	// Create POST request with CSRF token (but validation returns false)
	req := httptest.NewRequest(http.MethodPost, "/server/shutdown", strings.NewReader("csrf_token=test-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handlers.ServerShutdownPost(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status %d for CSRF failure, got %d", http.StatusForbidden, rr.Code)
	}
}

func TestServerShutdownPost_Authorized(t *testing.T) {
	// Initialize templates
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerWithCSRF{}

	deps := &mockServerDeps{
		CSRFToken: "valid-csrf-token",
	}
	handlers := NewServerHandlers(
		sm,
		deps,
	)

	// Create POST request with valid CSRF token
	req := httptest.NewRequest(http.MethodPost, "/server/shutdown", strings.NewReader("csrf_token=valid-csrf-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
}

func TestServerDiscoveryPost_Unauthorized(t *testing.T) {
	sm := &mockSessionManagerUnauthenticated{}
	handlers := NewServerHandlers(sm, &mockServerDeps{})

	req := httptest.NewRequest(http.MethodPost, "/server/discovery", nil)
	rr := httptest.NewRecorder()

	handlers.ServerDiscoveryPost(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

func TestServerDiscoveryPost_CSRFFailed(t *testing.T) {
	// Initialize templates
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerAuthenticated{}
	handlers := NewServerHandlers(sm, &mockServerDeps{})

	// Create POST request with CSRF token (but validation returns false)
	req := httptest.NewRequest(http.MethodPost, "/server/discovery", strings.NewReader("csrf_token=test-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handlers.ServerDiscoveryPost(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status %d for CSRF failure, got %d", http.StatusForbidden, rr.Code)
	}
}

func TestServerDiscoveryPost_Authorized(t *testing.T) {
	// Initialize templates
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerWithCSRF{}

	deps := &mockServerDeps{
		CSRFToken: "valid-csrf-token",
	}
	handlers := NewServerHandlers(
		sm,
		deps,
	)

	// Create POST request with valid CSRF token
	req := httptest.NewRequest(http.MethodPost, "/server/discovery", strings.NewReader("csrf_token=valid-csrf-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
	// Discovery handled by deps (mockServerDeps.TriggerDiscovery is a no-op)
}

func TestServerDiscoveryPost_NoCommonData(t *testing.T) {
	// This test verifies behavior when AddCommonTemplateData is nil
	// With CSRF validation, we still need a valid CSRF token to proceed
	sm := &mockSessionManagerWithCSRF{}

	handlers := NewServerHandlers(
		sm,
		&mockServerDeps{},
	)

	// Create POST request with valid CSRF token
	req := httptest.NewRequest(http.MethodPost, "/server/discovery", strings.NewReader("csrf_token=valid-csrf-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handlers.ServerDiscoveryPost(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	// Discovery is called asynchronously
	// Discovery handled by deps (mockServerDeps.TriggerDiscovery is a no-op)
}

func TestServerCacheBatchLoadPost_Unauthorized(t *testing.T) {
	sm := &mockSessionManagerUnauthenticated{}
	handlers := NewServerHandlers(sm, &mockServerDeps{})

	req := httptest.NewRequest(http.MethodPost, "/server/cache-batch-load", nil)
	rr := httptest.NewRecorder()

	handlers.ServerCacheBatchLoadPost(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

func TestServerCacheBatchLoadPost_CSRFFailed(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerAuthenticated{}
	handlers := NewServerHandlers(sm, &mockServerDeps{})

	// Create POST request with CSRF token (but validation returns false)
	req := httptest.NewRequest(http.MethodPost, "/server/cache-batch-load", strings.NewReader("csrf_token=test-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handlers.ServerCacheBatchLoadPost(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status %d for CSRF failure, got %d", http.StatusForbidden, rr.Code)
	}
}

func TestServerCacheBatchLoadPost_BlockedWhenDiscoveryActive(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerWithCSRF{}
	deps := &mockServerDeps{
		CSRFToken: "valid-csrf-token",
		BatchLoad: interfaces.StartCacheBatchLoadResult{
			Blocked: true,
			Message: "Cache batch load blocked: discovery active",
		},
	}
	handlers := NewServerHandlers(sm, deps)

	// Create POST request with valid CSRF token
	req := httptest.NewRequest(http.MethodPost, "/server/cache-batch-load", strings.NewReader("csrf_token=valid-csrf-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

	sm := &mockSessionManagerWithCSRF{}
	deps := &mockServerDeps{
		CSRFToken: "valid-csrf-token",
		BatchLoad: interfaces.StartCacheBatchLoadResult{
			Blocked: false,
			Message: "Cache batch load started",
		},
	}
	handlers := NewServerHandlers(sm, deps)

	// Create POST request with valid CSRF token
	req := httptest.NewRequest(http.MethodPost, "/server/cache-batch-load", strings.NewReader("csrf_token=valid-csrf-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

func TestServerCacheBatchLoadPost_StartError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerWithCSRF{}
	deps := &mockServerDeps{
		CSRFToken: "valid-csrf-token",
		BatchErr:  errors.New("batch load failed"),
	}
	handlers := NewServerHandlers(sm, deps)

	req := httptest.NewRequest(http.MethodPost, "/server/cache-batch-load", strings.NewReader("csrf_token=valid-csrf-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handlers.ServerCacheBatchLoadPost(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, rr.Code)
	}
	body := strings.TrimSpace(rr.Body.String())
	if body != "Internal Server Error" {
		t.Errorf("expected %q, got %q", "Internal Server Error", body)
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
