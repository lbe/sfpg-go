package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestLogin authenticates with username/password
func TestLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			// Verify POST method
			if r.Method != http.MethodPost {
				t.Errorf("Login method = %s, want POST", r.Method)
			}
			// Verify Origin header for CSRF
			origin := r.Header.Get("Origin")
			if origin == "" {
				t.Error("Missing Origin header for CSRF protection")
			}
			// Verify Content-Type
			contentType := r.Header.Get("Content-Type")
			if !strings.Contains(contentType, "application/x-www-form-urlencoded") {
				t.Errorf("Content-Type = %s, want application/x-www-form-urlencoded", contentType)
			}
			// Set session cookie
			http.SetCookie(w, &http.Cookie{
				Name:  "session",
				Value: "test-session-token",
			})
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.Login(context.Background(), "admin", "password")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	// Verify cookie was stored by making a follow-up request
	serverURL, _ := url.Parse(server.URL)
	cookies := c.client.Jar.Cookies(serverURL)
	if len(cookies) == 0 {
		t.Error("No cookies stored after login")
	}
}

// TestLoginInvalidCredentials returns error on auth failure
func TestLoginInvalidCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.Login(context.Background(), "invalid", "invalid")
	if err == nil {
		t.Fatal("Login should fail with invalid credentials")
	}
	if err != ErrUnauthorized {
		t.Errorf("Login error = %v, want %v", err, ErrUnauthorized)
	}
}

// TestFetchDashboard retrieves dashboard HTML
func TestFetchDashboard(t *testing.T) {
	dashboardHTML := `<!DOCTYPE html>
<html>
<body>
<div id="dashboard-container">
	<div id="last-updated">22:30:00</div>
</div>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dashboard" {
			// Verify session cookie
			cookie, err := r.Cookie("session")
			if err != nil {
				t.Error("Missing session cookie")
			}
			if cookie != nil && cookie.Value != "test-session-token" {
				t.Errorf("Session cookie = %s, want test-session-token", cookie.Value)
			}
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(dashboardHTML))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := New(server.URL)
	// Set session cookie manually for test
	serverURL, _ := url.Parse(server.URL)
	c.client.Jar.SetCookies(serverURL, []*http.Cookie{
		{Name: "session", Value: "test-session-token"},
	})

	html, err := c.FetchDashboard(context.Background())
	if err != nil {
		t.Fatalf("FetchDashboard failed: %v", err)
	}

	// Verify HTML content using structural assertions
	if !strings.Contains(html, "dashboard-container") {
		t.Error("Response missing dashboard-container element")
	}
}

// TestFetchDashboardUnauthorized returns error without auth
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
	if err != ErrUnauthorized {
		t.Errorf("FetchDashboard error = %v, want %v", err, ErrUnauthorized)
	}
}

// TestNetworkError returns error on connection failure
func TestNetworkError(t *testing.T) {
	c := New("http://localhost:1") // Invalid port
	_, err := c.FetchDashboard(context.Background())
	if err == nil {
		t.Fatal("FetchDashboard should fail with network error")
	}
	if err != ErrNetworkError {
		t.Errorf("FetchDashboard error = %v, want %v", err, ErrNetworkError)
	}
}
