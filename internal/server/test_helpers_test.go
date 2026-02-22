package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/session"
)

// addAuthToRequest adds an authenticated session cookie to a request.
// Also sets a CSRF token in the session for form validation.
// This is a helper for tests in the server package.
func addAuthToRequest(t *testing.T, sm session.SessionManager, req *http.Request) {
	t.Helper()
	w := httptest.NewRecorder()

	// Set authenticated via SessionManager
	if err := sm.SetAuthenticated(w, req, true); err != nil {
		t.Fatalf("failed to set authenticated: %v", err)
	}

	// Set a CSRF token in the session for form validation
	// Tests should use "csrf_token=valid-token" in their form data
	session, _ := sm.GetSession(req)
	session.Values["csrf_token"] = "valid-token"
	if err := session.Save(req, w); err != nil {
		t.Fatalf("failed to save session with CSRF token: %v", err)
	}

	// Copy the cookie to the request
	cookies := w.Result().Cookies()
	for _, c := range cookies {
		req.AddCookie(c)
	}
}
