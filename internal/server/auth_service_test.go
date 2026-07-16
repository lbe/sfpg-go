package server

import (
	"net/http/httptest"
	"testing"

	"github.com/gorilla/sessions"
)

// TestEnsureCSRFToken_FallsBackToSessionPackage verifies that when
// sessionManager is nil, EnsureCSRFToken falls back to the package-level
// session.EnsureCsrfToken function.
func TestEnsureCSRFToken_FallsBackToSessionPackage(t *testing.T) {
	s := NewSessionAuthFacade("test-secret-with-at-least-32-bytes!")
	s.store = sessions.NewCookieStore([]byte("test-secret-with-at-least-32-bytes!"))
	// sessionManager remains nil — triggers fallback to session.EnsureCsrfToken

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	token := s.EnsureCSRFToken(w, r)
	if token == "" {
		t.Fatal("expected non-empty CSRF token from fallback path")
	}

	// Verify a session cookie was set
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie to be set")
	}
}

// TestCSRFTokenForPage_AuthenticatedNoSessionManager verifies that
// CSRFTokenForPage with authenticated=true works even when sessionManager
// is nil (falls back to package-level EnsureCsrfToken).
func TestCSRFTokenForPage_AuthenticatedNoSessionManager(t *testing.T) {
	s := NewSessionAuthFacade("test-secret-with-at-least-32-bytes!")
	s.store = sessions.NewCookieStore([]byte("test-secret-with-at-least-32-bytes!"))
	// sessionManager remains nil

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	token := s.CSRFTokenForPage(w, r, true)
	if token == "" {
		t.Fatal("expected non-empty CSRF token")
	}
}
