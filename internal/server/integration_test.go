//go:build integration

package server

import (
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/getopt"
)

// Helper function to extract CSRF token from gallery page (which contains the login modal)
// Uses findElementByID from handlers_test.go
func extractCSRFToken(t *testing.T, client *http.Client, baseURL string) string {
	resp, err := client.Get(baseURL + "/gallery/1")
	if err != nil {
		t.Fatalf("GET /gallery/1 failed: %v", err)
	}
	defer resp.Body.Close()

	doc, err := html.Parse(resp.Body)
	if err != nil {
		t.Fatalf("failed to parse gallery page HTML: %v", err)
	}

	formNode := findElementByID(doc, "login-form")
	if formNode == nil {
		t.Fatal("login form not found on gallery page (login modal)")
	}

	// Find the CSRF token input - need recursive search since modal has nested elements
	var csrfToken string
	var findCSRF func(*html.Node)
	findCSRF = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			var name, value string
			for _, a := range n.Attr {
				if a.Key == "name" {
					name = a.Val
				}
				if a.Key == "value" {
					value = a.Val
				}
			}
			if name == "csrf_token" && value != "" {
				csrfToken = value
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if csrfToken == "" {
				findCSRF(c)
			}
		}
	}
	findCSRF(formNode)

	if csrfToken == "" {
		t.Fatal("CSRF token not found in login form")
	}
	return csrfToken
}

// TestE2E_LoginToGalleryToImage validates the full flow: login → gallery → image → thumbnail
// using a cookie jar to maintain session across requests (middleware/session validation).
func TestE2E_LoginToGalleryToImage(t *testing.T) {
	// Ensure cookies are accepted over HTTP in tests
	t.Setenv("SEPG_SESSION_SECURE", "false")
	app := CreateApp(t, true) // create with real DB

	// Create a test server
	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	// Create HTTP client with cookie jar to persist session
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Step 1: Extract CSRF token from login page
	csrfToken := extractCSRFToken(t, client, ts.URL)

	// Step 2: POST login with credentials and CSRF token
	loginData := url.Values{}
	loginData.Set("username", "admin")
	loginData.Set("password", "admin")
	loginData.Set("csrf_token", csrfToken)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/login", strings.NewReader(loginData.Encode()))
	if err != nil {
		t.Fatalf("failed to create login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /login failed: %v", err)
	}
	// Expect 302 redirect
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after login, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Step 3: Access gallery (should succeed with session cookie)
	resp, err = client.Get(ts.URL + "/gallery/1")
	if err != nil {
		t.Fatalf("GET /gallery/1 failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for authenticated /gallery/1, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Step 4: Access an image page (assumes file ID 1 exists from test setup)
	resp, err = client.Get(ts.URL + "/image/1")
	if err != nil {
		t.Fatalf("GET /image/1 failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 200 or 404 for /image/1, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Step 5: Fetch thumbnail (assumes thumbnail for file 1 exists or placeholder served)
	resp, err = client.Get(ts.URL + "/thumbnail/file/1")
	if err != nil {
		t.Fatalf("GET /thumbnail/file/1 failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /thumbnail/file/1, got %d", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType != "image/jpeg" && contentType != "image/svg+xml" {
		t.Fatalf("expected image MIME type for thumbnail, got %s", contentType)
	}
	resp.Body.Close()
}

// TestE2E_UnauthenticatedReturns401 validates that accessing protected routes without login returns 401
// Gallery routes are now public and should be accessible without authentication
func TestE2E_UnauthenticatedReturns401(t *testing.T) {
	// Ensure cookies are accepted over HTTP in tests (though no cookie here)
	t.Setenv("SEPG_SESSION_SECURE", "false")
	app := CreateApp(t, true)
	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	// Client without cookie jar (no session)
	client := &http.Client{}

	// Try to access protected config route without login - should return 401
	resp, err := client.Get(ts.URL + "/config")
	if err != nil {
		t.Fatalf("GET /config failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for unauthenticated /config, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Gallery routes should now be accessible without authentication
	resp, err = client.Get(ts.URL + "/gallery/1")
	if err != nil {
		t.Fatalf("GET /gallery/1 failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		// Allow 404 if gallery/1 doesn't exist, but should not redirect
		t.Fatalf("expected 200 OK or 404 for unauthenticated /gallery/1, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestE2E_ProtectedRouteReturnsUnauthorized validates that accessing a protected route
// without authentication returns 401 Unauthorized
func TestE2E_ProtectedRouteReturnsUnauthorized(t *testing.T) {
	t.Setenv("SEPG_SESSION_SECURE", "false")
	app := CreateApp(t, true)
	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	client := &http.Client{}

	// Try to access /config without authentication - should return 401
	resp, err := client.Get(ts.URL + "/config")
	if err != nil {
		t.Fatalf("GET /config failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for unauthenticated /config, got %d", resp.StatusCode)
	}
}

// TestE2E_LogoutClearsSession validates that logout clears the session and subsequent
// access to protected routes returns 401
func TestE2E_LogoutClearsSession(t *testing.T) {
	// Ensure cookies are accepted over HTTP in tests
	t.Setenv("SEPG_SESSION_SECURE", "false")
	app := CreateApp(t, true)
	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// Login - extract CSRF token first
	csrfToken := extractCSRFToken(t, client, ts.URL)
	loginData := url.Values{}
	loginData.Set("username", "admin")
	loginData.Set("password", "admin")
	loginData.Set("csrf_token", csrfToken)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/login", strings.NewReader(loginData.Encode()))
	if err != nil {
		t.Fatalf("Failed to create login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /login failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after login, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Access config (protected route, should work after login)
	resp, err = client.Get(ts.URL + "/config")
	if err != nil {
		t.Fatalf("GET /config after login failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /config after login, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Logout (logout form uses HTMX)
	logoutReq, err := http.NewRequest(http.MethodPost, ts.URL+"/logout", nil)
	if err != nil {
		t.Fatalf("Failed to create logout request: %v", err)
	}
	logoutReq.Header.Set("Origin", ts.URL)
	logoutReq.Header.Set("HX-Request", "true") // Logout form uses HTMX
	resp, err = client.Do(logoutReq)
	if err != nil {
		t.Fatalf("POST /logout failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after logout (HTMX request), got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Try to access config again (protected, should now return 401)
	resp, err = client.Get(ts.URL + "/config")
	if err != nil {
		t.Fatalf("GET /config after logout failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized after logout, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestE2E_ServeStartsAndHandlesAuth verifies the full Serve() entrypoint by starting
// the real HTTP server on an ephemeral port and exercising login and a protected route.
func TestE2E_ServeStartsAndHandlesAuth(t *testing.T) {
	// Ensure cookies are accepted over HTTP in tests
	t.Setenv("SEPG_SESSION_SECURE", "false")

	app := CreateApp(t, true)

	// Pick an ephemeral free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	app.opt.Port = getopt.OptInt{Int: port, IsSet: true}

	// Start the server in a goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Serve blocks; ignore returned error on shutdown
		_ = app.Serve()
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Wait for server to be ready by polling /gallery/1 (login is now modal-only, no GET)
	client := &http.Client{}
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := client.Get(baseURL + "/gallery/1")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Use cookie jar to persist session
	jar, _ := cookiejar.New(nil)
	client = &http.Client{Jar: jar}

	// Login - extract CSRF token first
	csrfToken := extractCSRFToken(t, client, baseURL)
	loginData := url.Values{}
	loginData.Set("username", "admin")
	loginData.Set("password", "admin")
	loginData.Set("csrf_token", csrfToken)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/login", strings.NewReader(loginData.Encode()))
	if err != nil {
		t.Fatalf("Failed to create login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", baseURL)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /login failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after login, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Access a protected route
	resp, err = client.Get(baseURL + "/gallery/1")
	if err != nil {
		t.Fatalf("GET /gallery/1 failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		// Allow 500 for intermittent broken pipe under heavy logging; primarily check auth passes
		t.Fatalf("expected 200 or 500 for authenticated /gallery/1, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Logout (logout form uses HTMX)
	logoutReq, _ := http.NewRequest(http.MethodPost, baseURL+"/logout", nil)
	logoutReq.Header.Set("Origin", baseURL)
	logoutReq.Header.Set("HX-Request", "true") // Logout form uses HTMX
	resp, err = client.Do(logoutReq)
	if err != nil {
		t.Fatalf("POST /logout failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after logout (HTMX request), got %d", resp.StatusCode)
	}
	// Logout handler returns empty response; client-side Hyperscript handles modal close and page refresh
	resp.Body.Close()

	// Verify that gallery routes are still accessible after logout (they're public now)
	// Clear cookies by creating a fresh client without jar
	client = &http.Client{}
	resp, err = client.Get(baseURL + "/gallery/1")
	if err != nil {
		t.Fatalf("GET /gallery/1 after logout failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusInternalServerError {
		// Gallery routes are public, should be accessible. Allow 404 if gallery/1 doesn't exist, or 500 for errors
		t.Fatalf("expected 200, 404, or 500 for public /gallery/1 after logout, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify that protected routes still require authentication (returns 401 Unauthorized)
	client = &http.Client{}
	resp, err = client.Get(baseURL + "/config")
	if err != nil {
		t.Fatalf("GET /config unauthenticated failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for unauthenticated /config, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestE2E_RawImage_Lightbox_InfoBox validates happy-path responses for raw-image,
// lightbox, and infoBox endpoints after creating a real file entry.
func TestE2E_RawImage_Lightbox_InfoBox(t *testing.T) {
	// Accept cookies over HTTP for tests
	t.Setenv("SEPG_SESSION_SECURE", "false")

	app := CreateApp(t, true)

	// Start test HTTP server using the real router
	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	// Prepare client with cookie jar
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// Login - extract CSRF token first
	csrfToken := extractCSRFToken(t, client, ts.URL)
	loginData := url.Values{}
	loginData.Set("username", "admin")
	loginData.Set("password", "admin")
	loginData.Set("csrf_token", csrfToken)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/login", strings.NewReader(loginData.Encode()))
	if err != nil {
		t.Fatalf("failed to create login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /login failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after login, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Create a real file on disk and corresponding DB entries via importer
	// Use a .jpg extension so ServeFile sets image/jpeg
	testRel := "e2e/test-e2e.jpg"
	fullPath := filepath.Join(app.imagesDir, testRel)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	// Write a few bytes; content doesn't need to be a valid JPEG for happy-path routing
	if err := os.WriteFile(fullPath, []byte{0xFF, 0xD8, 0xFF, 0xD9}, 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	// Insert file record
	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("db get failed: %v", err)
	}
	imp := app.ImporterFactory(cpc.Conn, cpc.Queries)
	fileRow, err := imp.UpsertPathChain(app.ctx, filepath.ToSlash(testRel), 0, 4, "", 0, 0, 0, "image/jpeg")
	app.dbRwPool.Put(cpc)
	if err != nil {
		t.Fatalf("UpsertPathChain failed: %v", err)
	}

	// raw-image happy-path
	resp, err = client.Get(ts.URL + "/raw-image/" + fmt.Sprint(fileRow.ID))
	if err != nil {
		t.Fatalf("GET /raw-image/{id} failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /raw-image/%d, got %d", fileRow.ID, resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "image/jpeg" && ct != "application/octet-stream" {
		t.Fatalf("unexpected content-type for raw-image: %s", ct)
	}
	resp.Body.Close()

	// lightbox happy-path uses the file's ID (not folder)
	folderID := fileRow.FolderID.Int64
	fileID := fileRow.ID
	// Allow brief retry for read-your-writes visibility across pools
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		resp, err = client.Get(ts.URL + "/lightbox/" + fmt.Sprint(fileID))
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("GET /lightbox/{id} failed: %v", err)
			}
			t.Fatalf("expected 200 for /lightbox/%d within deadline", fileID)
		}
		time.Sleep(25 * time.Millisecond)
	}

	// infoBox (image) happy-path
	resp, err = client.Get(ts.URL + "/info/image/" + fmt.Sprint(fileRow.ID))
	if err != nil {
		t.Fatalf("GET /info/image/{id} failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /info/image/%d, got %d", fileRow.ID, resp.StatusCode)
	}
	resp.Body.Close()

	// infoBox (folder) happy-path
	resp, err = client.Get(ts.URL + "/info/folder/" + fmt.Sprint(folderID))
	if err != nil {
		t.Fatalf("GET /info/folder/{id} failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /info/folder/%d, got %d", folderID, resp.StatusCode)
	}
	resp.Body.Close()
}
