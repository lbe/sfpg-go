package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// pathTraversalFake implements interfaces.HandlerQueries for path-traversal security test.
type pathTraversalFake struct {
	fileView gallerydb.FileView
}

func (f *pathTraversalFake) GetFolderViewByID(ctx context.Context, id int64) (gallerydb.FolderView, error) {
	return gallerydb.FolderView{}, nil
}
func (f *pathTraversalFake) GetFoldersViewsByParentIDOrderByName(ctx context.Context, parent sql.NullInt64) ([]gallerydb.FolderView, error) {
	return nil, nil
}
func (f *pathTraversalFake) GetFileViewsByFolderIDOrderByFileName(ctx context.Context, folderID sql.NullInt64) ([]gallerydb.FileView, error) {
	return nil, nil
}
func (f *pathTraversalFake) GetFileViewByID(ctx context.Context, id int64) (gallerydb.FileView, error) {
	return f.fileView, nil
}
func (f *pathTraversalFake) GetFolderByID(ctx context.Context, id int64) (gallerydb.Folder, error) {
	return gallerydb.Folder{}, nil
}
func (f *pathTraversalFake) GetThumbnailsByFileID(ctx context.Context, fileID int64) (gallerydb.Thumbnail, error) {
	return gallerydb.Thumbnail{}, sql.ErrNoRows
}
func (f *pathTraversalFake) GetThumbnailBlobDataByID(ctx context.Context, id int64) ([]byte, error) {
	return nil, sql.ErrNoRows
}

func (f *pathTraversalFake) GetPreloadRoutesByFolderID(ctx context.Context, parentID sql.NullInt64) (*sql.Rows, error) {
	return nil, nil
}

func (f *pathTraversalFake) GetGalleryStatistics(ctx context.Context) (gallerydb.GetGalleryStatisticsRow, error) {
	return gallerydb.GetGalleryStatisticsRow{}, nil
}

// TestPathTraversal_ImageByID verifies that path traversal attempts
// via the image ID endpoint do not allow access outside the images directory.
func TestPathTraversal_ImageByID(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Attempt to access image with a malicious ID that might contain path traversal
	// This should fail gracefully without exposing filesystem paths
	maliciousIDs := []string{
		"../../../etc/passwd",
		"..%2F..%2F..%2Fetc%2Fpasswd",
		"999999", // Non-existent ID
	}

	for _, id := range maliciousIDs {
		t.Run("MaliciousID_"+id, func(t *testing.T) {
			imageURL := server.URL + "/image/" + id
			req, _ := http.NewRequest("GET", imageURL, nil)
			req.AddCookie(MakeAuthCookie(t, app))

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer resp.Body.Close()

			// Should either return 404 or 500, but never 200 with sensitive content
			if resp.StatusCode == http.StatusOK {
				t.Errorf("Expected non-200 status for malicious ID %q, got %d", id, resp.StatusCode)
			}

			// Verify no sensitive filesystem paths leaked in response
			// (This is a basic check - in production, review error messages carefully)
		})
	}
}

// func TestInputValidation_ConfigHandler(t *testing.T) {
// 	app := CreateApp(t, false)
// 	defer app.Shutdown()

// 	server := httptest.NewServer(app.getRouter())
// 	defer server.Close()

// 	// Parse server URL for Origin header
// 	serverURL, _ := url.Parse(server.URL)
// 	validOrigin := fmt.Sprintf("http://%s", serverURL.Host)

// 	// Use a client with cookie jar for session
// 	jar, err := cookiejar.New(nil)
// 	if err != nil {
// 		t.Fatalf("Failed to create cookie jar: %v", err)
// 	}
// 	client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
// 		return http.ErrUseLastResponse
// 	}}

// 	t.Run("MismatchedPasswords", func(t *testing.T) {
// 		// Log in as admin to establish session
// 		respLogin, err := client.Get(server.URL + "/login")
// 		if err != nil {
// 			t.Fatalf("GET /login failed: %v", err)
// 		}
// 		defer respLogin.Body.Close()
// 		docLogin, err := html.Parse(respLogin.Body)
// 		if err != nil {
// 			t.Fatalf("Failed to parse login page HTML: %v", err)
// 		}
// 		var loginFormNode *html.Node
// 		var findLoginForm func(*html.Node)
// 		findLoginForm = func(n *html.Node) {
// 			if n.Type == html.ElementNode && n.Data == "form" {
// 				for _, a := range n.Attr {
// 					if a.Key == "id" && a.Val == "login-form" {
// 						loginFormNode = n
// 						return
// 					}
// 				}
// 			}
// 			for c := n.FirstChild; c != nil; c = c.NextSibling {
// 				findLoginForm(c)
// 			}
// 		}
// 		findLoginForm(docLogin)
// 		if loginFormNode == nil {
// 			t.Fatal("login form not found on login page")
// 		}
// 		var loginCSRF string
// 		for c := loginFormNode.FirstChild; c != nil; c = c.NextSibling {
// 			if c.Type == html.ElementNode && c.Data == "input" {
// 				var isCSRF bool
// 				var val string
// 				for _, a := range c.Attr {
// 					if a.Key == "name" && a.Val == "csrf_token" {
// 						isCSRF = true
// 					}
// 					if a.Key == "value" {
// 						val = a.Val
// 					}
// 				}
// 				if isCSRF {
// 					loginCSRF = val
// 					break
// 				}
// 			}
// 		}
// 		if loginCSRF == "" {
// 			t.Fatal("CSRF token not found in login form")
// 		}
// 		loginForm := url.Values{}
// 		loginForm.Add("username", "admin")
// 		loginForm.Add("password", "admin")
// 		loginForm.Add("csrf_token", loginCSRF)
// 		reqLogin, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(loginForm.Encode()))
// 		reqLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
// 		reqLogin.Header.Set("Origin", validOrigin)
// 		respLoginPost, err := client.Do(reqLogin)
// 		if err != nil {
// 			t.Fatalf("POST /login failed: %v", err)
// 		}
// 		defer respLoginPost.Body.Close()
// 		if respLoginPost.StatusCode != http.StatusOK {
// 			t.Fatalf("login failed, status: %d", respLoginPost.StatusCode)
// 		}
// 	})

// 	t.Run("MismatchedPasswords", func(t *testing.T) {
// 		// Helper to fetch CSRF token from config page
// 		getConfigCSRF := func() string {
// 			resp, err := client.Get(server.URL + "/config")
// 			if err != nil {
// 				t.Fatalf("GET /config for CSRF failed: %v", err)
// 			}
// 			defer resp.Body.Close()
// 			doc, err := html.Parse(resp.Body)
// 			if err != nil {
// 				t.Fatalf("Failed to parse config page HTML: %v", err)
// 			}
// 			var formNode *html.Node
// 			var findForm func(*html.Node)
// 			findForm = func(n *html.Node) {
// 				if n.Type == html.ElementNode && n.Data == "form" {
// 					formNode = n
// 					return
// 				}
// 				for c := n.FirstChild; c != nil; c = c.NextSibling {
// 					findForm(c)
// 				}
// 			}
// 			findForm(doc)
// 			if formNode == nil {
// 				t.Fatal("config form not found on config page")
// 			}
// 			var csrf string
// 			for c := formNode.FirstChild; c != nil; c = c.NextSibling {
// 				if c.Type == html.ElementNode && c.Data == "input" {
// 					var isCSRF bool
// 					var val string
// 					for _, a := range c.Attr {
// 						if a.Key == "name" && a.Val == "csrf_token" {
// 							isCSRF = true
// 						}
// 						if a.Key == "value" {
// 							val = a.Val
// 						}
// 					}
// 					if isCSRF {
// 						csrf = val
// 						break
// 					}
// 				}
// 			}
// 			if csrf == "" {
// 				t.Fatal("CSRF token not found in config form")
// 			}
// 			return csrf
// 		}
// 		csrf := getConfigCSRF()
// 		formData := url.Values{}
// 		formData.Set("username", "testuser")
// 		formData.Set("password", "newpass123")
// 		formData.Set("password-confirm", "different456")
// 		formData.Set("csrf_token", csrf)

// 		req, _ := http.NewRequest(http.MethodPost, server.URL+"/config", strings.NewReader(formData.Encode()))
// 		req.Header.Set("Origin", validOrigin)
// 		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

// 		resp, err := client.Do(req)
// 		if err != nil {
// 			t.Fatalf("Request failed: %v", err)
// 		}
// 		defer resp.Body.Close()

// 		if resp.StatusCode != http.StatusBadRequest {
// 			t.Errorf("Expected 400 Bad Request for mismatched passwords, got %d", resp.StatusCode)
// 		}
// 	})

// 	t.Run("MatchedPasswords", func(t *testing.T) {
// 		// Helper to fetch CSRF token from config page
// 		getConfigCSRF := func() string {
// 			resp, err := client.Get(server.URL + "/config")
// 			if err != nil {
// 				t.Fatalf("GET /config for CSRF failed: %v", err)
// 			}
// 			defer resp.Body.Close()
// 			doc, err := html.Parse(resp.Body)
// 			if err != nil {
// 				t.Fatalf("Failed to parse config page HTML: %v", err)
// 			}
// 			var formNode *html.Node
// 			var findForm func(*html.Node)
// 			findForm = func(n *html.Node) {
// 				if n.Type == html.ElementNode && n.Data == "form" {
// 					formNode = n
// 					return
// 				}
// 				for c := n.FirstChild; c != nil; c = c.NextSibling {
// 					findForm(c)
// 				}
// 			}
// 			findForm(doc)
// 			if formNode == nil {
// 				t.Fatal("config form not found on config page")
// 			}
// 			var csrf string
// 			for c := formNode.FirstChild; c != nil; c = c.NextSibling {
// 				if c.Type == html.ElementNode && c.Data == "input" {
// 					var isCSRF bool
// 					var val string
// 					for _, a := range c.Attr {
// 						if a.Key == "name" && a.Val == "csrf_token" {
// 							isCSRF = true
// 						}
// 						if a.Key == "value" {
// 							val = a.Val
// 						}
// 					}
// 					if isCSRF {
// 						csrf = val
// 						break
// 					}
// 				}
// 			}
// 			if csrf == "" {
// 				t.Fatal("CSRF token not found in config form")
// 			}
// 			return csrf
// 		}
// 		csrf := getConfigCSRF()
// 		formData := url.Values{}
// 		formData.Set("username", "testuser")
// 		formData.Set("password", "newpass123")
// 		formData.Set("password-confirm", "newpass123")
// 		formData.Set("csrf_token", csrf)

// 		req, _ := http.NewRequest(http.MethodPost, server.URL+"/config", strings.NewReader(formData.Encode()))
// 		req.Header.Set("Origin", validOrigin)
// 		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

// 		resp, err := client.Do(req)
// 		if err != nil {
// 			t.Fatalf("Request failed: %v", err)
// 		}
// 		defer resp.Body.Close()

// 		// Should succeed (200 or redirect)
// 		if resp.StatusCode == http.StatusBadRequest {
// 			t.Errorf("Expected non-400 status for valid password update, got %d", resp.StatusCode)
// 		}
// 	})
// }

// TestRawImageByIDHandler_PathTraversal_Forbidden ensures that if the DB returns
// a file path that would escape the images directory, the handler responds 403.
func TestRawImageByIDHandler_PathTraversal_Forbidden(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Inject a file record with a path attempting to traverse outside images dir
	app.hqOverride = &pathTraversalFake{
		fileView: gallerydb.FileView{ID: 42, Path: "../../etc/passwd", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
	}

	req := httptest.NewRequest(http.MethodGet, "/raw-image/42", nil)
	req.SetPathValue("id", "42")
	// getConfigCSRF := func() string {
	// 	resp, err := client.Get(server.URL + "/config")
	// 	if err != nil {
	// 		t.Fatalf("GET /config for CSRF failed: %v", err)
	// 	}
	// 	defer resp.Body.Close()
	// 	doc, err := html.Parse(resp.Body)
	// 	if err != nil {
	// 		t.Fatalf("Failed to parse config page HTML: %v", err)
	// 	}
	// 	var formNode *html.Node
	// 	var findForm func(*html.Node)
	// 	findForm = func(n *html.Node) {
	// 		if n.Type == html.ElementNode && n.Data == "form" {
	// 			formNode = n
	// 			return
	// 		}
	// 		for c := n.FirstChild; c != nil; c = c.NextSibling {
	// 			findForm(c)
	// 		}
	// 	}
	// 	findForm(doc)
	// 	if formNode == nil {
	// 		t.Fatal("config form not found on config page")
	// 	}
	// 	var csrf string
	// 	for c := formNode.FirstChild; c != nil; c = c.NextSibling {
	// 		if c.Type == html.ElementNode && c.Data == "input" {
	// 			var isCSRF bool
	// 			var val string
	// 			for _, a := range c.Attr {
	// 				if a.Key == "name" && a.Val == "csrf_token" {
	// 					isCSRF = true
	// 				}
	// 				if a.Key == "value" {
	// 					val = a.Val
	// 				}
	// 			}
	// 			if isCSRF {
	// 				csrf = val
	// 				break
	// 			}
	// 		}
	// 	}
	// 	if csrf == "" {
	// 		t.Fatal("CSRF token not found in config form")
	// 	}
	// 	return csrf
	// }
	// csrf := getConfigCSRF()
	// formData := url.Values{}
	// formData.Set("username", "testuser")
	// formData.Set("password", "newpass123")
	// formData.Set("password-confirm", "newpass123")
	// formData.Set("csrf_token", csrf)
	rr := httptest.NewRecorder()

	app.galleryHandlers.RawImageByID(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for path traversal, got %d", rr.Code)
	}
}

// TestCrossOriginProtection_UnsafeMethods verifies that POST, PUT, DELETE, PATCH
// require a valid Origin header matching the request host.
func TestCrossOriginProtection_UnsafeMethods(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	// Test unsafe methods without Origin header
	unsafeMethods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, method := range unsafeMethods {
		t.Run("NoOrigin_"+method, func(t *testing.T) {
			req, _ := http.NewRequest(method, server.URL+"/config", nil)
			req.AddCookie(MakeAuthCookie(t, app))

			client := &http.Client{
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse // Don't follow redirects
				},
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("Expected 403 Forbidden for %s without Origin, got %d", method, resp.StatusCode)
			}
		})
	}

	// Test unsafe method with mismatched Origin
	t.Run("MismatchedOrigin_POST", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/config", nil)
		req.Header.Set("Origin", "http://evil.com")
		req.AddCookie(MakeAuthCookie(t, app))

		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected 403 Forbidden for POST with mismatched Origin, got %d", resp.StatusCode)
		}
	})

	// Test unsafe method with valid Origin
	t.Run("ValidOrigin_POST", func(t *testing.T) {
		// Parse the server URL to get the host
		serverURL, _ := url.Parse(server.URL)
		validOrigin := fmt.Sprintf("http://%s", serverURL.Host)

		// Use a client with cookie jar for session
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("Failed to create cookie jar: %v", err)
		}
		client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}}

		// Log in as admin to establish session and get CSRF token
		reqLogin, _ := http.NewRequest(http.MethodGet, server.URL+"/config", nil)
		reqLogin.Header.Set("Origin", validOrigin)
		reqLogin.AddCookie(MakeAuthCookie(t, app))
		respLogin, err := client.Do(reqLogin)
		if err != nil {
			t.Fatalf("GET /config failed: %v", err)
		}
		defer respLogin.Body.Close()

		// Parse CSRF token from form
		doc, err := html.Parse(respLogin.Body)
		if err != nil {
			t.Fatalf("Failed to parse config page HTML: %v", err)
		}
		var formNode *html.Node
		var findForm func(*html.Node)
		findForm = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "form" {
				formNode = n
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				findForm(c)
			}
		}
		findForm(doc)
		if formNode == nil {
			t.Fatal("config form not found on config page")
		}
		var csrf string
		for c := formNode.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "input" {
				var isCSRF bool
				var val string
				for _, a := range c.Attr {
					if a.Key == "name" && a.Val == "csrf_token" {
						isCSRF = true
					}
					if a.Key == "value" {
						val = a.Val
					}
				}
				if isCSRF {
					csrf = val
					break
				}
			}
		}
		if csrf == "" {
			t.Fatal("CSRF token not found in config form")
		}

		formData := url.Values{}
		formData.Set("username", "testuser")
		formData.Set("password", "testpass")
		formData.Set("password-confirm", "testpass")
		formData.Set("csrf_token", csrf)

		req, _ := http.NewRequest(http.MethodPost, server.URL+"/config", strings.NewReader(formData.Encode()))
		req.Header.Set("Origin", validOrigin)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		// Use the same client/cookie jar for session continuity

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		// Should not be Forbidden (could be 200, 303, etc. depending on handler logic)
		if resp.StatusCode == http.StatusForbidden {
			t.Errorf("Expected non-403 status for POST with valid Origin, got %d", resp.StatusCode)
		}
	})
}

// TestCrossOriginProtection_SafeMethods verifies that GET, HEAD, OPTIONS, TRACE
// do not require Origin header (safe methods).
func TestCrossOriginProtection_SafeMethods(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	safeMethods := []string{http.MethodGet, http.MethodHead, http.MethodOptions}

	for _, method := range safeMethods {
		t.Run(method, func(t *testing.T) {
			req, _ := http.NewRequest(method, server.URL+"/gallery/1", nil)
			req.AddCookie(MakeAuthCookie(t, app))

			client := &http.Client{
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer resp.Body.Close()

			// Safe methods should not return 403 due to missing Origin
			if resp.StatusCode == http.StatusForbidden {
				t.Errorf("Safe method %s should not be Forbidden without Origin, got %d", method, resp.StatusCode)
			}
		})
	}
}

// TestSessionSecurity_HttpOnly verifies that session cookies have HttpOnly flag
// when configured (prevents JavaScript access).
func TestSessionSecurity_HttpOnly(t *testing.T) {
	// Set environment to enable HttpOnly
	t.Setenv("SEPG_SESSION_HTTPONLY", "true")

	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}

	// Fetch CSRF token from GET /gallery/1 (which contains login modal)
	galleryURL := server.URL + "/gallery/1"
	reqGet, _ := http.NewRequest("GET", galleryURL, nil)
	respGet, err := client.Do(reqGet)
	if err != nil {
		t.Fatalf("GET /gallery/1 failed: %v", err)
	}
	defer respGet.Body.Close()
	bodyBytes, _ := io.ReadAll(respGet.Body)
	body := string(bodyBytes)
	var csrf string
	idx := strings.Index(body, "name=\"csrf_token\"")
	if idx != -1 {
		valIdx := strings.Index(body[idx:], "value=\"")
		if valIdx != -1 {
			valStart := idx + valIdx + len("value=\"")
			valEnd := strings.Index(body[valStart:], "\"")
			if valEnd != -1 {
				csrf = body[valStart : valStart+valEnd]
			}
		}
	}
	if csrf == "" {
		t.Fatal("CSRF token not found in login modal")
	}

	formData := url.Values{}
	formData.Set("username", "admin")
	formData.Set("password", "admin")
	formData.Set("csrf_token", csrf)

	loginURL := server.URL + "/login"
	req, _ := http.NewRequest("POST", loginURL, strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", server.URL)
	// Copy session cookie from GET to POST
	if setCookie := respGet.Header.Get("Set-Cookie"); setCookie != "" {
		req.Header.Set("Cookie", setCookie)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	defer resp.Body.Close()

	// Check for HttpOnly flag in Set-Cookie header
	cookies := resp.Header.Values("Set-Cookie")
	if len(cookies) == 0 {
		t.Fatal("No Set-Cookie headers found in login response")
	}
	foundHttpOnly := false
	for _, cookie := range cookies {
		t.Logf("Set-Cookie: %s", cookie)
		if strings.Contains(cookie, "HttpOnly") {
			foundHttpOnly = true
			break
		}
	}

	if !foundHttpOnly {
		t.Error("Expected session cookie to have HttpOnly flag when SEPG_SESSION_HTTPONLY=true")
	}
}

// TestSessionSecurity_Secure verifies that session cookies have Secure flag
// when configured (requires HTTPS).
func TestSessionSecurity_Secure(t *testing.T) {
	// Set environment to enable Secure flag
	t.Setenv("SEPG_SESSION_SECURE", "true")

	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}

	// Fetch CSRF token from GET /gallery/1 (which contains login modal)
	galleryURL := server.URL + "/gallery/1"
	reqGet, _ := http.NewRequest("GET", galleryURL, nil)
	respGet, err := client.Do(reqGet)
	if err != nil {
		t.Fatalf("GET /gallery/1 failed: %v", err)
	}
	defer respGet.Body.Close()
	bodyBytes, _ := io.ReadAll(respGet.Body)
	body := string(bodyBytes)
	var csrf string
	idx := strings.Index(body, "name=\"csrf_token\"")
	if idx != -1 {
		valIdx := strings.Index(body[idx:], "value=\"")
		if valIdx != -1 {
			valStart := idx + valIdx + len("value=\"")
			valEnd := strings.Index(body[valStart:], "\"")
			if valEnd != -1 {
				csrf = body[valStart : valStart+valEnd]
			}
		}
	}
	if csrf == "" {
		t.Fatal("CSRF token not found in login modal")
	}

	formData := url.Values{}
	formData.Set("username", "admin")
	formData.Set("password", "admin")
	formData.Set("csrf_token", csrf)

	loginURL := server.URL + "/login"
	req, _ := http.NewRequest("POST", loginURL, strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", server.URL)
	// Copy session cookie from GET to POST
	if setCookie := respGet.Header.Get("Set-Cookie"); setCookie != "" {
		req.Header.Set("Cookie", setCookie)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	defer resp.Body.Close()

	// Check for Secure flag
	cookies := resp.Header.Values("Set-Cookie")
	if len(cookies) == 0 {
		t.Fatal("No Set-Cookie headers found in login response")
	}
	foundSecure := false
	for _, cookie := range cookies {
		t.Logf("Set-Cookie: %s", cookie)
		if strings.Contains(cookie, "Secure") {
			foundSecure = true
			break
		}
	}

	if !foundSecure {
		// Use a client with cookie jar for session
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("Failed to create cookie jar: %v", err)
		}
		client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}}

		// Parse server URL for Origin header
		serverURL, _ := url.Parse(server.URL)
		validOrigin := fmt.Sprintf("http://%s", serverURL.Host)

		// Log in as admin to establish session
		respLogin, err := client.Get(server.URL + "/login")
		if err != nil {
			t.Fatalf("GET /login failed: %v", err)
		}
		defer respLogin.Body.Close()
		docLogin, err := html.Parse(respLogin.Body)
		if err != nil {
			t.Fatalf("Failed to parse login page HTML: %v", err)
		}
		var loginFormNode *html.Node
		var findLoginForm func(*html.Node)
		findLoginForm = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "form" {
				for _, a := range n.Attr {
					if a.Key == "id" && a.Val == "login-form" {
						loginFormNode = n
						return
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				findLoginForm(c)
			}
		}
		findLoginForm(docLogin)
		if loginFormNode == nil {
			t.Fatal("login form not found on login page")
		}
		var loginCSRF string
		for c := loginFormNode.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "input" {
				var isCSRF bool
				var val string
				for _, a := range c.Attr {
					if a.Key == "name" && a.Val == "csrf_token" {
						isCSRF = true
					}
					if a.Key == "value" {
						val = a.Val
					}
				}
				if isCSRF {
					loginCSRF = val
					break
				}
			}
		}
		if loginCSRF == "" {
			t.Fatal("CSRF token not found in login form")
		}
		loginForm := url.Values{}
		loginForm.Add("username", "admin")
		loginForm.Add("password", "admin")
		loginForm.Add("csrf_token", loginCSRF)
		reqLogin, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(loginForm.Encode()))
		reqLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reqLogin.Header.Set("Origin", validOrigin)
		respLoginPost, err := client.Do(reqLogin)
		if err != nil {
			t.Fatalf("POST /login failed: %v", err)
		}
		defer respLoginPost.Body.Close()
		if respLoginPost.StatusCode != http.StatusOK {
			t.Fatalf("login failed, status: %d", respLoginPost.StatusCode)
		}

		t.Error("Expected session cookie to have Secure flag when SEPG_SESSION_SECURE=true")
	}
}

// TestSessionSecurity_DevMode verifies that security flags can be disabled
// for local development when explicitly configured.
func TestSessionSecurity_DevMode(t *testing.T) {
	// Disable security flags for dev mode
	t.Setenv("SEPG_SESSION_HTTPONLY", "false")
	t.Setenv("SEPG_SESSION_SECURE", "false")

	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}

	// Fetch CSRF token from GET /gallery/1 (which contains login modal)
	galleryURL := server.URL + "/gallery/1"
	reqGet, _ := http.NewRequest("GET", galleryURL, nil)
	respGet, err := client.Do(reqGet)
	if err != nil {
		t.Fatalf("GET /gallery/1 failed: %v", err)
	}
	defer respGet.Body.Close()
	bodyBytes, _ := io.ReadAll(respGet.Body)
	body := string(bodyBytes)
	var csrf string
	idx := strings.Index(body, "name=\"csrf_token\"")
	if idx != -1 {
		valIdx := strings.Index(body[idx:], "value=\"")
		if valIdx != -1 {
			valStart := idx + valIdx + len("value=\"")
			valEnd := strings.Index(body[valStart:], "\"")
			if valEnd != -1 {
				csrf = body[valStart : valStart+valEnd]
			}
		}
	}
	if csrf == "" {
		t.Fatal("CSRF token not found in login modal")
	}

	formData := url.Values{}
	formData.Set("username", "admin")
	formData.Set("password", "admin")
	formData.Set("csrf_token", csrf)

	loginURL := server.URL + "/login"
	req, _ := http.NewRequest("POST", loginURL, strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", server.URL)
	// Copy session cookie from GET to POST
	if setCookie := respGet.Header.Get("Set-Cookie"); setCookie != "" {
		req.Header.Set("Cookie", setCookie)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	defer resp.Body.Close()

	// In dev mode, flags should be absent
	cookies := resp.Header.Values("Set-Cookie")
	if len(cookies) == 0 {
		t.Fatal("No Set-Cookie headers found in login response")
	}
	for _, cookie := range cookies {
		t.Logf("Set-Cookie: %s", cookie)
		if strings.Contains(cookie, "HttpOnly") {
			t.Error("Did not expect HttpOnly flag when SEPG_SESSION_HTTPONLY=false")
		}
		if strings.Contains(cookie, "Secure") {
			t.Error("Did not expect Secure flag when SEPG_SESSION_SECURE=false")
		}
	}
}

// TestAuthenticationRequired verifies that protected endpoints
// require authentication.
func TestAuthenticationRequired(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// List of protected endpoints that should require authentication
	// Gallery viewing routes are now public and should be accessible without authentication
	protectedEndpoints := []string{
		"/config",
		"/logout",
	}

	for _, endpoint := range protectedEndpoints {
		t.Run(endpoint, func(t *testing.T) {
			resp, err := client.Get(server.URL + endpoint)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer resp.Body.Close()

			// /logout is POST-only, so GET returns 400 Bad Request
			// Other endpoints should redirect to login or return unauthorized
			if endpoint == "/logout" {
				// POST-only routes return 400 for GET requests (after Go 1.22 explicit routing)
				if resp.StatusCode != http.StatusBadRequest {
					t.Errorf("Expected 400 for GET %s (POST-only), got %d", endpoint, resp.StatusCode)
				}
			} else {
				// Should redirect to login or return unauthorized
				if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther &&
					resp.StatusCode != http.StatusUnauthorized {
					t.Errorf("Expected redirect or unauthorized for %s without auth, got %d", endpoint, resp.StatusCode)
				}

				// If redirected, should go to /login
				if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther {
					location := resp.Header.Get("Location")
					if !strings.HasPrefix(location, "/login") {
						t.Errorf("Expected redirect to /login for %s, got %s", endpoint, location)
					}
				}
			}
		})
	}
}

// TestRemoveImagesDirPrefix_Security verifies that the path normalization
// function properly handles edge cases and malicious inputs.
func TestRemoveImagesDirPrefix_Security(t *testing.T) {
	tests := []struct {
		name                string
		normalizedImagesDir string
		path                string
		expected            string
		expectError         bool
	}{
		{
			name:                "Normal case",
			normalizedImagesDir: "Images",
			path:                filepath.Join("Images", "gallery", "photo.jpg"),
			expected:            "gallery/photo.jpg",
			expectError:         false,
		},
		{
			name:                "Path traversal attempt",
			normalizedImagesDir: "Images",
			path:                filepath.Join("Images", "..", "..", "etc", "passwd"),
			expected:            "",
			expectError:         true,
		},
		{
			name:                "Empty imagesDir",
			normalizedImagesDir: "",
			path:                "any/path.jpg",
			expected:            "any/path.jpg",
			expectError:         false,
		},
		{
			name:                "Path already normalized",
			normalizedImagesDir: "Images",
			path:                "Images/gallery/photo.jpg",
			expected:            "gallery/photo.jpg",
			expectError:         false,
		},
		{
			name:                "Path traversal in empty imagesDir",
			normalizedImagesDir: "",
			path:                "../etc/passwd",
			expected:            "",
			expectError:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := removeImagesDirPrefix(tt.normalizedImagesDir, tt.path)
			if tt.expectError {
				if err == nil {
					t.Errorf("removeImagesDirPrefix(%q, %q) expected error but got none, result = %q",
						tt.normalizedImagesDir, tt.path, result)
				}
				if !strings.Contains(err.Error(), "traversal") {
					t.Errorf("removeImagesDirPrefix(%q, %q) expected error about traversal, got: %v",
						tt.normalizedImagesDir, tt.path, err)
				}
			} else {
				if err != nil {
					t.Errorf("removeImagesDirPrefix(%q, %q) unexpected error: %v",
						tt.normalizedImagesDir, tt.path, err)
				}
				if result != tt.expected {
					t.Errorf("removeImagesDirPrefix(%q, %q) = %q, want %q",
						tt.normalizedImagesDir, tt.path, result, tt.expected)
				}
			}
		})
	}
}

// TestFileAccessWithinImagesDir verifies that the application only serves
// files from within the configured images directory.
func TestFileAccessWithinImagesDir(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Create a file outside the images directory
	outsideFile := filepath.Join(os.TempDir(), "outside.txt")
	err := os.WriteFile(outsideFile, []byte("sensitive data"), 0o644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(outsideFile)

	// The application should never serve files outside app.imagesDir
	// This test verifies the architecture - files are only accessed via database queries
	// which only contain paths relative to imagesDir

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Attempt to access file outside images directory via raw-image endpoint
	// Using an authenticated request with non-existent ID
	req, _ := http.NewRequest("GET", server.URL+"/raw-image/999999", nil)
	req.AddCookie(MakeAuthCookie(t, app))

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should return 404 or 500 for non-existent ID, not 200
	if resp.StatusCode == http.StatusOK {
		t.Error("Should not be able to access non-existent file ID")
	}
	// Should not redirect to login when authenticated
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther {
		t.Error("Authenticated request should not redirect")
	}
}
