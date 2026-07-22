package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

// mockSessionManagerAuthenticated implements SessionManager for testing
type mockSessionManagerAuthenticated struct{}

func (m *mockSessionManagerAuthenticated) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerAuthenticated) ClearSession(w http.ResponseWriter, r *http.Request) {}

func (m *mockSessionManagerAuthenticated) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	return sessions.NewSession(nil, session.SessionName), nil
}

func (m *mockSessionManagerAuthenticated) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerAuthenticated) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
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

func (m *mockSessionManagerUnauthenticated) ClearSession(w http.ResponseWriter, r *http.Request) {}

func (m *mockSessionManagerUnauthenticated) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	return sessions.NewSession(nil, session.SessionName), nil
}

func (m *mockSessionManagerUnauthenticated) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerUnauthenticated) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	return false
}

func (m *mockSessionManagerUnauthenticated) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return nil
}

func TestNewServerHandlers(t *testing.T) {
	sm := &mockSessionManagerAuthenticated{}
	serverCtl := &mockServerControl{}
	helper := &mockTemplateHelpers{}
	handlers := NewServerHandlers(sm, serverCtl, helper.AddCommonTemplateData, helper.ServerError)

	if handlers == nil {
		t.Fatal("NewServerHandlers returned nil")
	}
	if handlers.sessionManager != sm {
		t.Error("sessionManager not set correctly")
	}
}

func TestServerShutdownPost_Unauthorized(t *testing.T) {
	sm := &mockSessionManagerUnauthenticated{}
	serverCtl := &mockServerControl{}
	helper := &mockTemplateHelpers{}
	handlers := NewServerHandlers(sm, serverCtl, helper.AddCommonTemplateData, helper.ServerError)

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

	serverCtl := &mockServerControl{}
	helper := &mockTemplateHelpers{}
	handlers := NewServerHandlers(sm, serverCtl, helper.AddCommonTemplateData, helper.ServerError)

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

	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}
	page := testutil.FindElementByID(doc, "server-shutdown-page")
	if page == nil {
		t.Fatal("missing #server-shutdown-page element")
	}
	title := testutil.FindElementByTag(page, "h1")
	if title == nil {
		t.Fatal("missing h1 title inside #server-shutdown-page")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(title)); got != "Shutting Down" {
		t.Errorf("shutdown title = %q, want %q", got, "Shutting Down")
	}
}

func TestServerDiscoveryPost_Unauthorized(t *testing.T) {
	sm := &mockSessionManagerUnauthenticated{}
	serverCtl := &mockServerControl{}
	helper := &mockTemplateHelpers{}
	handlers := NewServerHandlers(sm, serverCtl, helper.AddCommonTemplateData, helper.ServerError)

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

	serverCtl := &mockServerControl{}
	helper := &mockTemplateHelpers{}
	handlers := NewServerHandlers(sm, serverCtl, helper.AddCommonTemplateData, helper.ServerError)

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

	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	toast := testutil.FindElementByID(doc, "discovery-started-toast")
	if toast == nil {
		t.Fatal("missing #discovery-started-toast element")
	}
	msg := testutil.FindElementByTag(toast, "span")
	if msg == nil {
		t.Fatal("missing message span inside #discovery-started-toast")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(msg)); got != "File discovery started" {
		t.Errorf("discovery message = %q, want %q", got, "File discovery started")
	}

	// Discovery should be called
	// Discovery handled by serverControl (mockServerControl.TriggerDiscovery is a no-op)
}

func TestServerDiscoveryPost_NoCommonData(t *testing.T) {
	sm := &mockSessionManagerAuthenticated{}

	serverCtl := &mockServerControl{}
	helper := &mockTemplateHelpers{}
	handlers := NewServerHandlers(sm, serverCtl, helper.AddCommonTemplateData, helper.ServerError)

	req := httptest.NewRequest(http.MethodPost, "/server/discovery", nil)
	rr := httptest.NewRecorder()

	handlers.ServerDiscoveryPost(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	// Discovery is called asynchronously
	// Discovery handled by serverControl (mockServerControl.TriggerDiscovery is a no-op)
}

func TestServerCacheBatchLoadPost_Unauthorized(t *testing.T) {
	sm := &mockSessionManagerUnauthenticated{}
	serverCtl := &mockServerControl{}
	helper := &mockTemplateHelpers{}
	handlers := NewServerHandlers(sm, serverCtl, helper.AddCommonTemplateData, helper.ServerError)

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
	serverCtl := &mockServerControl{
		BatchLoad: interfaces.StartCacheBatchLoadResult{
			Blocked: true,
			Message: "Cache batch load blocked: discovery active",
		},
	}
	helper := &mockTemplateHelpers{}
	handlers := NewServerHandlers(sm, serverCtl, helper.AddCommonTemplateData, helper.ServerError)

	req := httptest.NewRequest(http.MethodPost, "/server/cache-batch-load", nil)
	rr := httptest.NewRecorder()

	handlers.ServerCacheBatchLoadPost(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("expected status %d, got %d", http.StatusConflict, rr.Code)
	}

	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}
	toast := testutil.FindElementByID(doc, "cache-batch-load-toast")
	if toast == nil {
		t.Fatal("missing #cache-batch-load-toast element")
	}
	msg := testutil.FindElementByTag(toast, "span")
	if msg == nil {
		t.Fatal("missing message span inside #cache-batch-load-toast")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(msg)); got != "Cache batch load blocked: discovery active" {
		t.Errorf("cache batch load message = %q, want %q", got, "Cache batch load blocked: discovery active")
	}
}

func TestServerCacheBatchLoadPost_StartsRunWhenIdle(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerAuthenticated{}
	serverCtl := &mockServerControl{
		BatchLoad: interfaces.StartCacheBatchLoadResult{
			Blocked: false,
			Message: "Cache batch load started",
		},
	}
	helper := &mockTemplateHelpers{}
	handlers := NewServerHandlers(sm, serverCtl, helper.AddCommonTemplateData, helper.ServerError)

	req := httptest.NewRequest(http.MethodPost, "/server/cache-batch-load", nil)
	rr := httptest.NewRecorder()

	handlers.ServerCacheBatchLoadPost(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}
	toast := testutil.FindElementByID(doc, "cache-batch-load-toast")
	if toast == nil {
		t.Fatal("missing #cache-batch-load-toast element")
	}
	msg := testutil.FindElementByTag(toast, "span")
	if msg == nil {
		t.Fatal("missing message span inside #cache-batch-load-toast")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(msg)); got != "Cache batch load started" {
		t.Errorf("cache batch load message = %q, want %q", got, "Cache batch load started")
	}
}

func TestServerCacheBatchLoadPost_StartError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockSessionManagerAuthenticated{}
	serverCtl := &mockServerControl{
		BatchErr: errors.New("batch load failed"),
	}
	helper := &mockTemplateHelpers{}
	handlers := NewServerHandlers(sm, serverCtl, helper.AddCommonTemplateData, helper.ServerError)

	req := httptest.NewRequest(http.MethodPost, "/server/cache-batch-load", nil)
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
