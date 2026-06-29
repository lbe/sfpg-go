package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// galleryPageWithCSRF returns a minimal gallery page HTML containing a CSRF token
// in the login form, matching the real server's response format.
func galleryPageWithCSRF(token string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html><body>
<input type="hidden" name="csrf_token" value="%s" />
</body></html>`, token)
}

// TestLogin authenticates with username/password using proper CSRF flow.
func TestLogin(t *testing.T) {
	csrfToken := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/gallery/1" && r.Method == "GET":
			// Return gallery page with CSRF token
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, galleryPageWithCSRF(csrfToken))

		case r.URL.Path == "/login" && r.Method == "POST":
			// Verify CSRF token was sent
			if r.FormValue("csrf_token") != csrfToken {
				t.Errorf("csrf_token = %q, want %q", r.FormValue("csrf_token"), csrfToken)
			}
			// Verify Origin header
			if origin := r.Header.Get("Origin"); origin == "" {
				t.Error("Missing Origin header for CSRF protection")
			}
			// Set session cookie matching real server (session-name)
			http.SetCookie(w, &http.Cookie{
				Name:  "session-name",
				Value: "test-session-token",
				Path:  "/",
			})
			// Real server returns HX-Trigger: auth-changed on success
			w.Header().Set("Hx-Trigger", "auth-changed")
			w.WriteHeader(http.StatusOK)

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := New(server.URL)
	if err := c.Login(context.Background(), "admin", "password"); err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	// Verify session cookie was stored
	serverURL, _ := url.Parse(server.URL)
	cookies := c.client.Jar.Cookies(serverURL)
	var found bool
	for _, ck := range cookies {
		if ck.Name == "session-name" && ck.Value == "test-session-token" {
			found = true
			break
		}
	}
	if !found {
		t.Error("session-name cookie not found after login")
	}
}

// TestLoginInvalidCredentials returns error when server rejects credentials.
// The real server returns 200 with a login form (no HX-Trigger) on auth failure.
func TestLoginInvalidCredentials(t *testing.T) {
	csrfToken := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/gallery/1" && r.Method == "GET":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, galleryPageWithCSRF(csrfToken))

		case r.URL.Path == "/login" && r.Method == "POST":
			// Real server returns 200 with login form on failure, NO HX-Trigger
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `<form id="login-form"><div id="login-error-message">Invalid credentials</div></form>`)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.Login(context.Background(), "invalid", "invalid")
	if err == nil {
		t.Fatal("Login should fail with invalid credentials")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Login error = %v, want %v", err, ErrUnauthorized)
	}
}

// TestLoginNetworkError returns error on connection failure.
func TestLoginNetworkError(t *testing.T) {
	c := New("http://localhost:1")
	err := c.Login(context.Background(), "admin", "password")
	if err == nil {
		t.Fatal("Login should fail with network error")
	}
	if !errors.Is(err, ErrNetworkError) {
		t.Errorf("Login error = %v, want %v", err, ErrNetworkError)
	}
}

// TestFetchDashboard retrieves dashboard HTML with valid session.
func TestFetchDashboard(t *testing.T) {
	dashboardHTML := `<!DOCTYPE html>
<html><body>
<div id="dashboard-container">
	<div id="last-updated">22:30:00</div>
</div>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dashboard" {
			// Verify session-name cookie (matching real server)
			cookie, err := r.Cookie("session-name")
			if err != nil {
				t.Error("Missing session-name cookie")
			}
			if cookie != nil && cookie.Value != "test-session-token" {
				t.Errorf("session-name = %s, want test-session-token", cookie.Value)
			}
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, dashboardHTML)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := New(server.URL)
	// Set session cookie manually for test (using real cookie name)
	serverURL, _ := url.Parse(server.URL)
	c.client.Jar.SetCookies(serverURL, []*http.Cookie{
		{Name: "session-name", Value: "test-session-token"},
	})

	html, err := c.FetchDashboard(context.Background())
	if err != nil {
		t.Fatalf("FetchDashboard failed: %v", err)
	}
	if !strings.Contains(html, "dashboard-container") {
		t.Error("Response missing dashboard-container element")
	}
}

// TestFetchDashboardUnauthorized returns error without auth.
func TestFetchDashboardUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dashboard" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.FetchDashboard(context.Background())
	if err == nil {
		t.Fatal("FetchDashboard should fail without auth")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("FetchDashboard error = %v, want %v", err, ErrUnauthorized)
	}
}

// TestFetchDashboardNetworkError returns error on connection failure.
func TestFetchDashboardNetworkError(t *testing.T) {
	c := New("http://localhost:1")
	_, err := c.FetchDashboard(context.Background())
	if err == nil {
		t.Fatal("FetchDashboard should fail with network error")
	}
	if !errors.Is(err, ErrNetworkError) {
		t.Errorf("FetchDashboard error = %v, want %v", err, ErrNetworkError)
	}
}

// TestCookieJarMerge verifies that SetCookies merges by name rather than
// overwriting the entire cookie list for the host.
func TestCookieJarMerge(t *testing.T) {
	jar := newCookieJar()
	u, _ := url.Parse("http://example.com")

	// Set first batch of cookies
	jar.SetCookies(u, []*http.Cookie{
		{Name: "session-name", Value: "abc"},
		{Name: "theme", Value: "dark"},
	})

	// Set second batch — should replace session-name, keep theme
	jar.SetCookies(u, []*http.Cookie{
		{Name: "session-name", Value: "def"},
	})

	cookies := jar.Cookies(u)
	if len(cookies) != 2 {
		t.Fatalf("expected 2 cookies after merge, got %d", len(cookies))
	}

	var sessionVal, themeVal string
	for _, c := range cookies {
		switch c.Name {
		case "session-name":
			sessionVal = c.Value
		case "theme":
			themeVal = c.Value
		}
	}
	if sessionVal != "def" {
		t.Errorf("session-name = %q, want %q", sessionVal, "def")
	}
	if themeVal != "dark" {
		t.Errorf("theme = %q, want %q", themeVal, "dark")
	}
}
