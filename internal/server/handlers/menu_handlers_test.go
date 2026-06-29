package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/web"
)

func TestMenuHandlers_HamburgerMenu_Unauthenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockMenuSessionManager{authenticated: false}
	menuHandlers := NewMenuHandlers(sm, nil)

	req := httptest.NewRequest(http.MethodGet, "/hamburger-menu", nil)
	w := httptest.NewRecorder()

	menuHandlers.HamburgerMenu(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Verify Cache-Control: no-store header
	cacheControl := w.Header().Get("Cache-Control")
	if cacheControl != "no-store, no-cache, must-revalidate" {
		t.Errorf("expected Cache-Control: no-store, no-cache, must-revalidate, got %q", cacheControl)
	}

	// Verify Content-Type
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("expected Content-Type: text/html; charset=utf-8, got %q", contentType)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Login") {
		t.Error("expected unauthenticated menu to contain 'Login'")
	}
	if strings.Contains(body, "Dashboard") {
		t.Error("expected unauthenticated menu to NOT contain 'Dashboard'")
	}
}

func TestMenuHandlers_HamburgerMenu_Authenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockMenuSessionManager{authenticated: true}
	menuHandlers := NewMenuHandlers(sm, nil)

	req := httptest.NewRequest(http.MethodGet, "/hamburger-menu", nil)
	w := httptest.NewRecorder()

	menuHandlers.HamburgerMenu(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Verify Cache-Control: no-store header
	cacheControl := w.Header().Get("Cache-Control")
	if cacheControl != "no-store, no-cache, must-revalidate" {
		t.Errorf("expected Cache-Control: no-store, no-cache, must-revalidate, got %q", cacheControl)
	}

	// Verify Content-Type
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("expected Content-Type: text/html; charset=utf-8, got %q", contentType)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Dashboard") {
		t.Error("expected authenticated menu to contain 'Dashboard'")
	}
	if strings.Contains(body, "Login") {
		t.Error("expected authenticated menu to NOT contain 'Login'")
	}
}

func TestMenuHandlers_HamburgerMenu_RenderError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	var serverErrorCalled bool
	var capturedErr error
	sm := &mockMenuSessionManager{authenticated: false}
	menuHandlers := NewMenuHandlers(sm, func(w http.ResponseWriter, r *http.Request, err error) {
		serverErrorCalled = true
		capturedErr = err
	})

	req := httptest.NewRequest(http.MethodGet, "/hamburger-menu", nil)
	// errorResponseWriter fails on Write, forcing RenderTemplate to return error
	w := &errorResponseWriter{}
	menuHandlers.HamburgerMenu(w, req)

	if !serverErrorCalled {
		t.Error("expected ServerError to be called on render failure")
	}
	if capturedErr == nil {
		t.Error("expected a non-nil error from render failure")
	}
}

// mockMenuSessionManager satisfies the SessionManager interface for menu handler tests.
type mockMenuSessionManager struct {
	authenticated bool
}

func (m *mockMenuSessionManager) IsAuthenticated(r *http.Request) bool {
	return m.authenticated
}
