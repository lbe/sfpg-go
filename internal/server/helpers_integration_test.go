//go:build integration

package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/testutil"
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
	session, _ := sm.GetSession(w, req)
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

// extractCSRFTokenFromConfig extracts the CSRF token from the config form in the HTML response.
func extractCSRFTokenFromConfig(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	resp, err := client.Get(baseURL + "/config")
	if err != nil {
		t.Fatalf("failed to GET /config: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /config, got %d", resp.StatusCode)
	}

	// Parse HTML to find CSRF token
	doc, err := html.Parse(resp.Body)
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	var csrfToken string
	var findCSRF func(*html.Node)
	findCSRF = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			for _, attr := range n.Attr {
				if attr.Key == "name" && attr.Val == "csrf_token" {
					for _, a := range n.Attr {
						if a.Key == "value" {
							csrfToken = a.Val
							return
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if csrfToken == "" {
				findCSRF(c)
			}
		}
	}
	findCSRF(doc)

	if csrfToken == "" {
		t.Fatal("CSRF token not found in config form")
	}
	return csrfToken
}

// extractCSRFTokenFromLogin extracts the CSRF token from the login form on the gallery page.
func extractCSRFTokenFromLogin(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	resp, err := client.Get(baseURL + "/login-form")
	if err != nil {
		t.Fatalf("GET /login-form failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /login-form, got %d", resp.StatusCode)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	// Find login form
	formNode := testutil.FindElementByID(doc, "login-form")
	if formNode == nil {
		t.Fatal("login form not found in /login-form response")
	}

	// Find CSRF token
	var csrfToken string
	for c := formNode.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "input" {
			var name, value string
			for _, a := range c.Attr {
				if a.Key == "name" {
					name = a.Val
				}
				if a.Key == "value" {
					value = a.Val
				}
			}
			if name == "csrf_token" && value != "" {
				csrfToken = value
				break
			}
		}
	}

	if csrfToken == "" {
		t.Fatal("CSRF token not found in login form")
	}
	return csrfToken
}

// loginAsAdmin performs an admin login and configures the client with authentication cookies.
func loginAsAdmin(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	// Extract CSRF token from login page
	csrfToken := extractCSRFTokenFromLogin(t, client, baseURL)

	// POST login
	loginData := url.Values{}
	loginData.Set("username", "admin")
	loginData.Set("password", "admin")
	loginData.Set("csrf_token", csrfToken)
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
