// Package client provides HTTP client functionality for communicating
// with the sfpg-go server's dashboard and authentication endpoints.
package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Error definitions for client operations.
var (
	// ErrUnauthorized is returned when authentication fails or is required.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrNetworkError is returned when a network connection fails.
	ErrNetworkError = errors.New("network error")
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
func (j *simpleCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if u == nil {
		return
	}
	j.cookies[u.Host] = cookies
}

// Cookies returns cookies stored for the given URL's host.
func (j *simpleCookieJar) Cookies(u *url.URL) []*http.Cookie {
	if u == nil {
		return nil
	}
	return j.cookies[u.Host]
}

// Login authenticates with the sfpg-go server using username and password.
// It sends a POST request to /login with form data and stores the session
// cookie in the client's cookie jar.
//
// The Origin header is set to the server URL to satisfy CSRF protection.
//
// Returns:
//   - nil on successful authentication
//   - ErrUnauthorized on 401 response
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
	formData := url.Values{}
	formData.Set("username", username)
	formData.Set("password", password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/login", strings.NewReader(formData.Encode()))
	if err != nil {
		return err
	}

	// Set Origin header for CSRF protection
	req.Header.Set("Origin", c.baseURL)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.client.Do(req)
	if err != nil {
		return ErrNetworkError
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}

	if resp.StatusCode != http.StatusOK {
		return errors.New("login failed")
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
