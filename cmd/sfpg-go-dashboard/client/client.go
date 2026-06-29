// Package client provides HTTP client functionality for communicating
// with the sfpg-go server's dashboard and authentication endpoints.
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Error definitions for client operations.
var (
	// ErrUnauthorized is returned when authentication fails or is required.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrNetworkError is returned when a network connection fails.
	ErrNetworkError = errors.New("network error")
	// ErrLoginFailed is returned when the server rejects the login credentials.
	ErrLoginFailed = errors.New("login failed")
)

// Client handles HTTP communication with the sfpg-go server.
// It manages session cookies and provides methods for authentication
// and fetching dashboard data.
type Client struct {
	baseURL    string       // Base URL of the sfpg-go server
	httpClient *http.Client // Underlying HTTP client (unused, kept for compatibility)
	client     *http.Client // HTTP client with cookie jar for session management
}

// New creates a new Client configured to communicate with the given server URL.
// The client is initialized with a cookie jar to manage session cookies.
//
// Example:
//
//	c := client.New("http://localhost:8083")
func New(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{},
		client: &http.Client{
			Jar: newCookieJar(),
		},
	}
}

// simpleCookieJar is a minimal cookie jar implementation for session management.
// It stores cookies by host and merges new cookies with existing ones rather
// than overwriting the entire cookie list on each SetCookies call.
type simpleCookieJar struct {
	cookies map[string][]*http.Cookie
}

// newCookieJar creates a new empty cookie jar.
func newCookieJar() *simpleCookieJar {
	return &simpleCookieJar{
		cookies: make(map[string][]*http.Cookie),
	}
}

// SetCookies stores cookies for the given URL's host.
// Merges new cookies with existing ones, replacing by cookie name.
func (j *simpleCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if u == nil {
		return
	}
	existing := j.cookies[u.Host]
	byName := make(map[string]*http.Cookie, len(existing)+len(cookies))
	for _, c := range existing {
		byName[c.Name] = c
	}
	for _, c := range cookies {
		byName[c.Name] = c
	}
	merged := make([]*http.Cookie, 0, len(byName))
	for _, c := range byName {
		merged = append(merged, c)
	}
	j.cookies[u.Host] = merged
}

// Cookies returns cookies stored for the given URL's host.
func (j *simpleCookieJar) Cookies(u *url.URL) []*http.Cookie {
	if u == nil {
		return nil
	}
	return j.cookies[u.Host]
}

// csrfRe matches the CSRF token in an HTML response body.
var csrfRe = regexp.MustCompile(`csrf_token"\s*value="([a-f0-9]+)"`)

// extractCSRFToken fetches /gallery/1 from the server and extracts the CSRF
// token from the login form. This token must be included in the subsequent
// POST /login request.
func (c *Client) extractCSRFToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/gallery/1", nil)
	if err != nil {
		return "", fmt.Errorf("create gallery request: %w", err)
	}
	req.Header.Set("Origin", c.baseURL)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", ErrNetworkError
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read gallery response: %w", err)
	}

	matches := csrfRe.FindStringSubmatch(string(body))
	if len(matches) < 2 {
		return "", fmt.Errorf("CSRF token not found in /gallery/1")
	}
	return matches[1], nil
}

// Login authenticates with the sfpg-go server using username and password.
// It first fetches a CSRF token from /gallery/1, then sends a POST request
// to /login with the credentials and token. The session cookie is stored
// in the client's cookie jar for subsequent requests.
//
// The Origin header is set to the server URL to satisfy CSRF protection.
//
// Returns:
//   - nil on successful authentication
//   - ErrUnauthorized on authentication failure (invalid credentials)
//   - ErrNetworkError on connection failure
//   - other errors on unexpected failures
//
// Example:
//
//	err := c.Login(ctx, "admin", "password")
//	if errors.Is(err, client.ErrUnauthorized) {
//	    // handle invalid credentials
//	}
func (c *Client) Login(ctx context.Context, username, password string) error {
	// Step 1: Extract CSRF token from gallery page
	csrfToken, err := c.extractCSRFToken(ctx)
	if err != nil {
		// If CSRF extraction fails, try without (server may allow new sessions)
		// but propagate the underlying issue if this is a network error.
		if errors.Is(err, ErrNetworkError) {
			return err
		}
		csrfToken = ""
	}

	// Step 2: POST login with credentials and CSRF token
	formData := url.Values{}
	formData.Set("username", username)
	formData.Set("password", password)
	if csrfToken != "" {
		formData.Set("csrf_token", csrfToken)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/login", strings.NewReader(formData.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Origin", c.baseURL)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.client.Do(req)
	if err != nil {
		return ErrNetworkError
	}
	defer resp.Body.Close()

	// The server returns 200 for both success and failure. Distinguish by
	// checking the HX-Trigger response header: on success it is set to
	// "auth-changed"; on failure the response is a login form with an error.
	// The "auth-changed" event triggers the hamburger menu refresh on the server.
	if resp.Header.Get("Hx-Trigger") != "auth-changed" {
		return ErrUnauthorized
	}

	return nil
}

// FetchDashboard retrieves the dashboard HTML from the server.
// It requires an authenticated session (call Login first or set cookies).
//
// Returns:
//   - HTML content string on success
//   - ErrUnauthorized on 401 response (session expired or not authenticated)
//   - ErrNetworkError on connection failure
//   - other errors on unexpected failures
//
// Example:
//
//	html, err := c.FetchDashboard(ctx)
//	if err != nil {
//	    // handle error
//	}
//	metrics, err := parser.ParseDashboard(strings.NewReader(html))
func (c *Client) FetchDashboard(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/dashboard", nil)
	if err != nil {
		return "", err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", ErrNetworkError
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrUnauthorized
	}

	if resp.StatusCode != http.StatusOK {
		return "", errors.New("failed to fetch dashboard")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}
