//go:build integration

package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/session"
)

// addAuthToRequest adds an authenticated session cookie to a request.
// This is a helper for tests in the server package.
func addAuthToRequest(t *testing.T, sm session.SessionManager, req *http.Request) {
	t.Helper()
	w := httptest.NewRecorder()

	// Set authenticated via SessionManager
	if err := sm.SetAuthenticated(w, req, true); err != nil {
		t.Fatalf("failed to set authenticated: %v", err)
	}

	// Copy the cookie to the request
	cookies := w.Result().Cookies()
	for _, c := range cookies {
		req.AddCookie(c)
	}
}

// loginAsAdmin performs an admin login and configures the client with authentication cookies.
// CrossOriginProtection is enforced at the router level, so the request must include
// an Origin header matching the server URL to pass COP.
func loginAsAdmin(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()

	// POST login with credentials only (CSRF protection is handled by CrossOriginProtection
	// at the router level in production; handler unit tests bypass the router).
	loginData := url.Values{}
	loginData.Set("username", "admin")
	loginData.Set("password", "admin")
	req, err := http.NewRequest(http.MethodPost, baseURL+"/login", strings.NewReader(loginData.Encode()))
	if err != nil {
		t.Fatalf("failed to create login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", baseURL)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /login failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after login, got %d", resp.StatusCode)
	}
}
