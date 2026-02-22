package server

import (
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
	"time"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
)

// ============================================================================
// Main Handler Tests
// ============================================================================

// TestRootRedirectLeadsToGallery ensures that a request to "/" ultimately
// serves the gallery page (redirects to /gallery/1 and returns 200).
// Gallery routes are now public, so authentication is not required.
func TestRootRedirectLeadsToGallery(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	client := &http.Client{}

	// Test without authentication (gallery is now public)
	req, _ := http.NewRequest("GET", server.URL+"/", nil)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected final status 200 OK after following redirect, got %d", resp.StatusCode)
	}
}

func TestRootHandler(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	// Use a client that does not follow redirects
	client := &http.Client{
		CheckRedirect: func( /*req*/ _ *http.Request /*via*/, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	t.Run("Unauthenticated access to root", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/")
		if err != nil {
			t.Fatalf("client.Get failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusFound {
			t.Errorf("Expected status code %d, got %d", http.StatusFound, resp.StatusCode)
		}

		location, err := resp.Location()
		if err != nil {
			t.Fatalf("Failed to get redirect location: %v", err)
		}

		if location.Path != "/gallery/1" {
			t.Errorf("Expected redirect to /gallery/1, got %s", location.Path)
		}
	})

	t.Run("Authenticated access to root", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/", nil)
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("client.Do failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusFound {
			t.Errorf("Expected status code %d, got %d", http.StatusFound, resp.StatusCode)
		}

		location, err := resp.Location()
		if err != nil {
			t.Fatalf("Failed to get redirect location: %v", err)
		}

		if location.Path != "/gallery/1" {
			t.Errorf("Expected redirect to /gallery/1*, got %s", location.String())
		}
	})

	t.Run("Unrecognized path", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/unrecognized/path", nil)
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("client.Do failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected status code %d, got %d", http.StatusBadRequest, resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}
		expectedBody := "400 Bad Request\n"
		if string(body) != expectedBody {
			t.Errorf("Expected body %q, got %q", expectedBody, string(body))
		}
	})
}

func TestLoginHandler(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	// Create a client with cookie jar to maintain session across requests
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("Failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	t.Run("GET login returns Bad Request (POST-only route)", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/login")
		if err != nil {
			t.Fatalf("GET /login failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("Expected status 400 Bad Request, got %d", resp.StatusCode)
		}
	})

	// Helper to fetch CSRF token from gallery page (which contains login modal)
	getCSRFToken := func() (string, error) {
		resp, err := client.Get(server.URL + "/gallery/1")
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			return "", err
		}
		formNode := findElementByID(doc, "login-form")
		if formNode == nil {
			return "", fmt.Errorf("login form not found in gallery page")
		}
		// Find the CSRF token input (recursive search for nested elements)
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
			return "", fmt.Errorf("csrf_token input not found")
		}
		return csrfToken, nil
	}

	t.Run("POST login success", func(t *testing.T) {
		csrfToken, err := getCSRFToken()
		if err != nil {
			t.Fatalf("Failed to get CSRF token: %v", err)
		}
		form := url.Values{}
		form.Add("username", "admin")
		form.Add("password", "admin")
		form.Add("csrf_token", csrfToken)

		req, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", server.URL)
		req.Header.Set("HX-Request", "true") // Simulate HTMX request

		resp2, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /login failed: %v", err)
		}
		defer func() { _ = resp2.Body.Close() }()

		if resp2.StatusCode != http.StatusOK {
			t.Errorf("Expected status OK (200), got %d", resp2.StatusCode)
		}

		// Successful login returns hamburger menu HTML with hx-swap-oob for HTMX
		doc, err := testutil.ParseHTML(resp2.Body)
		if err != nil {
			t.Fatalf("Failed to parse HTML response: %v", err)
		}

		// Verify OOB swap is present
		var hasOOBSwap bool
		var checkOOB func(*html.Node)
		checkOOB = func(n *html.Node) {
			if n.Type == html.ElementNode {
				for _, attr := range n.Attr {
					if attr.Key == "hx-swap-oob" && attr.Val == "true" {
						hasOOBSwap = true
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				checkOOB(c)
			}
		}
		checkOOB(doc)
		if !hasOOBSwap {
			t.Error("Expected hx-swap-oob='true' for HTMX out-of-band swap")
		}

		// Verify hamburger menu element exists
		menuItems := findElementByID(doc, "hamburger-menu-items")
		if menuItems == nil {
			t.Error("Expected id='hamburger-menu-items' element in response")
		}

		// Verify menu shows authenticated options
		configLink := testutil.FindElement(doc, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a" && testutil.GetAttr(n, "aria-label") == "Configuration"
		})
		if configLink == nil {
			t.Error("Expected Configuration link in authenticated menu")
		}
		logoutLabel := testutil.FindElement(doc, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "label" && testutil.GetAttr(n, "aria-label") == "Logout"
		})
		if logoutLabel == nil {
			t.Error("Expected Logout label in authenticated menu")
		}

		// Verify HX-Trigger header is present for frontend event handling
		if trigger := resp2.Header.Get("HX-Trigger"); trigger != "login-success" {
			t.Errorf("Expected HX-Trigger header to be 'login-success', got '%s'", trigger)
		}
	})

	t.Run("POST login failure", func(t *testing.T) {
		csrfToken, err := getCSRFToken()
		if err != nil {
			t.Fatalf("Failed to get CSRF token: %v", err)
		}
		form := url.Values{}
		form.Add("username", "admin")
		form.Add("password", "wrongpassword")
		form.Add("csrf_token", csrfToken)

		req, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", server.URL)

		resp2, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /login failed: %v", err)
		}
		defer func() { _ = resp2.Body.Close() }()

		if resp2.StatusCode != http.StatusOK {
			t.Errorf("Expected status OK, got %d", resp2.StatusCode)
		}

		doc2, err := testutil.ParseHTML(resp2.Body)
		if err != nil {
			t.Fatalf("Failed to parse HTML: %v", err)
		}

		errorDiv := findElementByID(doc2, "login-error-message")
		if errorDiv == nil {
			t.Fatal("Could not find login error message div, which indicates a failed login did not render the error element.")
		}

		// Verify the error message has actual content (not empty)
		errorText := getTextContent(errorDiv)
		if errorText == "" {
			t.Fatal("Error message div exists but has no content - user won't see the error")
		}
		if errorText != "Invalid credentials" && errorText != "Account locked. Please try again later." {
			t.Errorf("Expected error message to be an auth error, got: %q", errorText)
		}

		// Verify the login form is returned (not empty response like success case)
		loginForm := findElementByID(doc2, "login-form")
		if loginForm == nil {
			t.Fatal("Login form not found in error response - modal won't stay open")
		}
	})
}

func TestLoginHandler_LockoutAfterThreeFailures(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("Failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Helper to get CSRF token from gallery page (which contains login modal)
	getCSRFToken := func() string {
		resp, getErr := client.Get(server.URL + "/gallery/1")
		if getErr != nil {
			t.Fatalf("Failed to get gallery page: %v", getErr)
		}
		defer resp.Body.Close()
		doc, parseErr := testutil.ParseHTML(resp.Body)
		if parseErr != nil {
			t.Fatalf("Failed to parse HTML: %v", parseErr)
		}
		formNode := findElementByID(doc, "login-form")
		if formNode == nil {
			t.Fatal("login form not found in gallery page")
		}
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
			t.Fatal("CSRF token not found")
		}
		return csrfToken
	}

	username := "admin"
	wrongPassword := "wrongpassword"

	// Make 3 failed login attempts
	for i := range 3 {
		csrfToken := getCSRFToken()
		form := url.Values{}
		form.Add("username", username)
		form.Add("password", wrongPassword)
		form.Add("csrf_token", csrfToken)

		req, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", server.URL)

		resp, doErr := client.Do(req)
		if doErr != nil {
			t.Fatalf("POST /login failed (attempt %d): %v", i+1, doErr)
		}
		resp.Body.Close()

		// Verify error message is shown
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status OK on attempt %d, got %d", i+1, resp.StatusCode)
		}
	}

	// Verify account is locked in database
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get RO DB connection: %v", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	attempt, err := cpcRo.Queries.GetLoginAttempt(app.ctx, username)
	if err != nil {
		t.Fatalf("GetLoginAttempt failed: %v", err)
	}
	if attempt.FailedAttempts != 3 {
		t.Errorf("expected failed_attempts 3, got %d", attempt.FailedAttempts)
	}
	if !attempt.LockedUntil.Valid {
		t.Error("expected locked_until to be set after 3 failures, but it was NULL")
	}
	// Verify lockout is approximately 1 hour from now (allow 5 second tolerance)
	now := time.Now().Unix()
	expectedLockout := now + 3600
	if attempt.LockedUntil.Int64 < expectedLockout-5 || attempt.LockedUntil.Int64 > expectedLockout+5 {
		t.Errorf("expected locked_until to be approximately %d (1 hour from now), got %d", expectedLockout, attempt.LockedUntil.Int64)
	}

	// 4th attempt should be blocked (even with correct password)
	csrfToken := getCSRFToken()
	form := url.Values{}
	form.Add("username", username)
	form.Add("password", "admin") // Correct password
	form.Add("csrf_token", csrfToken)

	req, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", server.URL)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /login failed (4th attempt): %v", err)
	}
	defer resp.Body.Close()

	// Should show account locked error, not redirect
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK for locked account, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	doc, err := testutil.ParseHTML(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("Failed to parse HTML response: %v", err)
	}

	// Look for error message in login form
	loginForm := findElementByID(doc, "login-form")
	if loginForm == nil {
		t.Fatal("Login form not found in response")
	}

	// Check for error message element
	errorMsg := findElementByID(doc, "login-error-message")
	if errorMsg == nil {
		t.Fatal("Login error message not found in response")
	}
	text := strings.TrimSpace(getTextContent(errorMsg))
	if text != "Account locked. Please try again later." {
		t.Errorf("Expected account locked error message, got %q", text)
	}
}

func TestLoginHandler_ClearAttemptsOnSuccess(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("Failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Helper to get CSRF token from gallery page (which contains login modal)
	getCSRFToken := func() string {
		resp, getErr := client.Get(server.URL + "/gallery/1")
		if getErr != nil {
			t.Fatalf("Failed to get gallery page: %v", getErr)
		}
		defer resp.Body.Close()
		doc, parseErr := testutil.ParseHTML(resp.Body)
		if parseErr != nil {
			t.Fatalf("Failed to parse HTML: %v", parseErr)
		}
		formNode := findElementByID(doc, "login-form")
		if formNode == nil {
			t.Fatal("login form not found in gallery page")
		}
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
			t.Fatal("CSRF token not found")
		}
		return csrfToken
	}

	username := "admin"

	// Make 2 failed login attempts
	for i := range 2 {
		csrfToken := getCSRFToken()
		form := url.Values{}
		form.Add("username", username)
		form.Add("password", "wrongpassword")
		form.Add("csrf_token", csrfToken)

		req, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", server.URL)

		resp, doErr := client.Do(req)
		if doErr != nil {
			t.Fatalf("POST /login failed (attempt %d): %v", i+1, doErr)
		}
		resp.Body.Close()
	}

	// Verify failed attempts are recorded
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get RO DB connection: %v", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	attempt, err := cpcRo.Queries.GetLoginAttempt(app.ctx, username)
	if err != nil {
		t.Fatalf("GetLoginAttempt failed: %v", err)
	}
	if attempt.FailedAttempts != 2 {
		t.Errorf("expected failed_attempts 2, got %d", attempt.FailedAttempts)
	}

	// Make successful login
	csrfToken := getCSRFToken()
	form := url.Values{}
	form.Add("username", username)
	form.Add("password", "admin")
	form.Add("csrf_token", csrfToken)

	req, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", server.URL)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /login (success) failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK on successful login, got %d", resp.StatusCode)
	}

	// Verify attempts are cleared
	_, err = cpcRo.Queries.GetLoginAttempt(app.ctx, username)
	if err == nil {
		t.Error("expected login_attempts record to be deleted after successful login, but it still exists")
	} else if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows after successful login, got %v", err)
	}
}

func TestLoginHandler_LockoutExpiration(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	username := "admin"

	// Create a locked account with expired lockout (locked_until in the past)
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	pastTime := time.Now().Unix() - 7200 // 2 hours ago
	err = cpcRw.Queries.UpsertLoginAttempt(app.ctx, gallerydb.UpsertLoginAttemptParams{
		Username:       username,
		FailedAttempts: 3,
		LastAttemptAt:  pastTime,
		LockedUntil:    sql.NullInt64{Int64: pastTime, Valid: true},
	})
	if err != nil {
		t.Fatalf("UpsertLoginAttempt failed: %v", err)
	}

	// Attempt login - should succeed because lockout expired
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("Failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Helper to get CSRF token from gallery page (which contains login modal)
	getCSRFToken := func() string {
		resp, getErr := client.Get(server.URL + "/gallery/1")
		if getErr != nil {
			t.Fatalf("Failed to get gallery page: %v", getErr)
		}
		defer resp.Body.Close()
		doc, parseErr := testutil.ParseHTML(resp.Body)
		if parseErr != nil {
			t.Fatalf("Failed to parse HTML: %v", parseErr)
		}
		formNode := findElementByID(doc, "login-form")
		if formNode == nil {
			t.Fatal("login form not found in gallery page")
		}
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
			t.Fatal("CSRF token not found")
		}
		return csrfToken
	}

	csrfToken := getCSRFToken()
	form := url.Values{}
	form.Add("username", username)
	form.Add("password", "admin")
	form.Add("csrf_token", csrfToken)

	req, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", server.URL)

	resp, doErr := client.Do(req)
	if doErr != nil {
		t.Fatalf("POST /login failed: %v", doErr)
	}
	defer resp.Body.Close()

	// Should succeed (lockout expired)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK for expired lockout, got %d", resp.StatusCode)
	}

	// Success returns hamburger menu HTML with hx-swap-oob
	doc, err := testutil.ParseHTML(resp.Body)
	if err != nil {
		t.Fatalf("Failed to parse HTML response: %v", err)
	}

	// Verify OOB swap is present
	var hasOOBSwap bool
	var checkOOB func(*html.Node)
	checkOOB = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for _, attr := range n.Attr {
				if attr.Key == "hx-swap-oob" && attr.Val == "true" {
					hasOOBSwap = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			checkOOB(c)
		}
	}
	checkOOB(doc)
	if !hasOOBSwap {
		t.Error("Expected hx-swap-oob='true' in response")
	}

	// Verify hamburger menu element exists
	menuItems := findElementByID(doc, "hamburger-menu-items")
	if menuItems == nil {
		t.Error("Expected id='hamburger-menu-items' element in response")
	}
}

func TestLoginHandler_LockoutBlocksLogin(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	username := "admin"

	// Create a locked account with active lockout
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	futureTime := time.Now().Unix() + 3600 // 1 hour from now
	err = cpcRw.Queries.UpsertLoginAttempt(app.ctx, gallerydb.UpsertLoginAttemptParams{
		Username:       username,
		FailedAttempts: 3,
		LastAttemptAt:  time.Now().Unix(),
		LockedUntil:    sql.NullInt64{Int64: futureTime, Valid: true},
	})
	if err != nil {
		t.Fatalf("UpsertLoginAttempt failed: %v", err)
	}

	// Attempt login with correct password - should be blocked
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("Failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Helper to get CSRF token from gallery page (which contains login modal)
	getCSRFToken := func() string {
		resp, getErr := client.Get(server.URL + "/gallery/1")
		if getErr != nil {
			t.Fatalf("Failed to get gallery page: %v", getErr)
		}
		defer resp.Body.Close()
		doc, parseErr := testutil.ParseHTML(resp.Body)
		if parseErr != nil {
			t.Fatalf("Failed to parse HTML: %v", parseErr)
		}
		formNode := findElementByID(doc, "login-form")
		if formNode == nil {
			t.Fatal("login form not found in gallery page")
		}
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
			t.Fatal("CSRF token not found")
		}
		return csrfToken
	}

	csrfToken := getCSRFToken()
	form := url.Values{}
	form.Add("username", username)
	form.Add("password", "admin") // Correct password
	form.Add("csrf_token", csrfToken)

	req, _ := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", server.URL)

	resp, doErr := client.Do(req)
	if doErr != nil {
		t.Fatalf("POST /login failed: %v", doErr)
	}
	defer resp.Body.Close()

	// Should show account locked error, not redirect (even with correct password)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK for locked account, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	doc, parseErr := testutil.ParseHTML(strings.NewReader(string(body)))
	if parseErr != nil {
		t.Fatalf("Failed to parse HTML response: %v", parseErr)
	}

	// Look for error message in login form
	loginForm := findElementByID(doc, "login-form")
	if loginForm == nil {
		t.Fatal("Login form not found in response")
	}

	// Check for error message element
	errorMsg := findElementByID(doc, "login-error-message")
	if errorMsg == nil {
		t.Fatal("Login error message not found in response")
	}
	text := strings.TrimSpace(getTextContent(errorMsg))
	if text != "Account locked. Please try again later." {
		t.Errorf("Expected account locked error message, got %q", text)
	}

	// Verify no redirect (account is locked)
	location := resp.Header.Get("Location")
	if location == "/" {
		t.Error("Expected no redirect when account is locked, but got Location: /")
	}
}

func TestLogoutHandler(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	t.Run("POST logout", func(t *testing.T) {
		// Use a client that doesn't follow redirects so we can check the Location header
		noRedirectClient := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		req, _ := http.NewRequest("POST", server.URL+"/logout", nil)
		req.AddCookie(MakeAuthCookie(t, app)) // Authenticate the request
		req.Header.Set("Origin", server.URL)
		req.Header.Set("HX-Request", "true") // Simulate HTMX request (logout form uses HTMX)

		resp, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatalf("POST /logout failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status OK (200) for HTMX request, got %d", resp.StatusCode)
		}

		// Verify session cookie is actually cleared (MaxAge=-1)
		cookies := resp.Cookies()
		var sessionCookie *http.Cookie
		for _, c := range cookies {
			if c.Name == "session-name" {
				sessionCookie = c
				break
			}
		}
		if sessionCookie == nil {
			t.Error("Expected Set-Cookie header with session-name to clear the cookie")
		} else if sessionCookie.MaxAge != -1 {
			t.Errorf("Expected MaxAge=-1 to clear cookie, got MaxAge=%d", sessionCookie.MaxAge)
		}

		// Verify OOB swap is returned to update menu with correct structure
		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to parse HTML response: %v", err)
		}

		// Verify hamburger menu element with OOB swap
		menuItems := findElementByID(doc, "hamburger-menu-items")
		if menuItems == nil {
			t.Error("Expected id='hamburger-menu-items' in OOB swap response")
		}

		// Verify OOB swap attribute
		if menuItems != nil {
			hasOOB := false
			for _, attr := range menuItems.Attr {
				if attr.Key == "hx-swap-oob" && attr.Val == "true" {
					hasOOB = true
				}
			}
			if !hasOOB {
				t.Error("Expected hx-swap-oob='true' on hamburger menu element")
			}

			// Check for ul element inside menu
			var hasUL bool
			var checkUL func(*html.Node)
			checkUL = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "ul" {
					hasUL = true
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					checkUL(c)
				}
			}
			checkUL(menuItems)
			if !hasUL {
				t.Error("Expected <ul> element in hamburger menu structure")
			}

			// Check for li elements
			var hasLI bool
			var checkLI func(*html.Node)
			checkLI = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "li" {
					hasLI = true
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					checkLI(c)
				}
			}
			checkLI(menuItems)
			if !hasLI {
				t.Error("Expected <li> elements in hamburger menu structure")
			}

			// Check for login label
			var hasLoginLabel bool
			var checkLabel func(*html.Node)
			checkLabel = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "label" {
					for _, attr := range n.Attr {
						if attr.Key == "for" && attr.Val == "login_modal" {
							hasLoginLabel = true
						}
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					checkLabel(c)
				}
			}
			checkLabel(doc)
			if !hasLoginLabel {
				t.Error("Expected login_modal label in OOB swap response after logout")
			}

			loginLabel := testutil.FindElement(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "label" && testutil.GetAttr(n, "aria-label") == "Login"
			})
			if loginLabel == nil {
				t.Error("Expected Login label in OOB swap response")
			}
			logoutLabel := testutil.FindElement(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "label" && testutil.GetAttr(n, "aria-label") == "Logout"
			})
			if logoutLabel != nil {
				t.Error("Should not contain Logout label after logout")
			}
		}
	})
}

// getElementTextByID finds an element by ID and returns its text content.
func getElementTextByID(n *html.Node, id string) (string, bool) {
	node := findElementByID(n, id)
	if node == nil {
		return "", false
	}
	// Text is in the FirstChild of the span
	if node.FirstChild != nil && node.FirstChild.Type == html.TextNode {
		return node.FirstChild.Data, true
	}
	return "", false
}

// findElementByID traverses the HTML node tree to find an element by its ID.
func findElementByID(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode {
		for _, a := range n.Attr {
			if a.Key == "id" && a.Val == id {
				return n
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := findElementByID(c, id); result != nil {
			return result
		}
	}
	return nil
}

// getAttribute is a helper to find an attribute value by key.
func getAttribute(n *html.Node, key string) (string, bool) {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val, true
		}
	}
	return "", false
}

// TestRefactoredGalleryHandlerByID tests the future ID-based gallery handler.
func TestRefactoredGalleryHandlerByID(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	// Get the router from the app
	router := app.getRouter()

	// Create a test server
	server := httptest.NewServer(router)
	defer server.Close()

	// Create a root folder for testing
	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	pathID, err := cpc.Queries.UpsertFolderPathReturningID(app.ctx, "/test-folder")
	if err != nil {
		t.Fatalf("failed to insert folder path: %v", err)
	}
	folder, err := cpc.Queries.UpsertFolderReturningFolder(app.ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Valid: false},
		PathID:    pathID,
		Name:      "test-folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to insert folder: %v", err)
	}

	t.Run("unauthenticated", func(t *testing.T) {
		client := &http.Client{}

		// Make a request to the test server
		resp, err := client.Get(server.URL + fmt.Sprintf("/gallery/%d", folder.ID))
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		// Gallery routes are now public, expect 200 OK
		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to parse response body: %v", err)
		}

		if findElementByID(doc, "boxgallery") == nil {
			t.Error("response body does not contain the gallery container")
		}
	})

	t.Run("authenticated", func(t *testing.T) {
		client := &http.Client{}
		req, _ := http.NewRequest("GET", server.URL+fmt.Sprintf("/gallery/%d", folder.ID), nil)
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		// Full page response must Vary on HX-Request and HX-Target so the browser does not reuse a cached partial for a full page request.
		varyStr := strings.Join(resp.Header.Values("Vary"), ", ")
		if !strings.Contains(varyStr, "HX-Request") || !strings.Contains(varyStr, "HX-Target") {
			t.Errorf("full page gallery response must Vary on HX-Request and HX-Target, got Vary: %q", varyStr)
		}

		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to parse response body: %v", err)
		}

		if findElementByID(doc, "boxgallery") == nil {
			t.Error("response body does not contain the gallery container")
		}
	})

	t.Run("authenticated htmx partial", func(t *testing.T) {
		client := &http.Client{}
		req, _ := http.NewRequest("GET", server.URL+fmt.Sprintf("/gallery/%d", folder.ID), nil)
		req.AddCookie(MakeAuthCookie(t, app))
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", "gallery-content")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		// Read raw response body to check if it contains full HTML layout
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}
		bodyStr := string(body)

		// Parse HTML for further validation
		doc, err := testutil.ParseHTML(strings.NewReader(bodyStr))
		if err != nil {
			t.Fatalf("Failed to parse HTML response: %v", err)
		}

		// Verify response is a partial (no Doctype node)
		var hasDoctype bool
		var findDoctype func(*html.Node)
		findDoctype = func(n *html.Node) {
			if n.Type == html.DoctypeNode {
				hasDoctype = true
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				findDoctype(c)
			}
		}
		findDoctype(doc)
		if hasDoctype {
			t.Error("response should be a partial (no DOCTYPE), but contained a doctype node")
		}

		// Check for gallery container by ID
		if findElementByID(doc, "boxgallery") == nil {
			t.Error("response body does not contain the gallery container (id='boxgallery')")
		}

		// Partial response must use Cache-Control: no-store so the browser does not cache or bfcache it.
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("partial gallery response must have Cache-Control: no-store, got %q", cc)
		}
	})

	t.Run("authenticated htmx partial with oob breadcrumbs", func(t *testing.T) {
		client := &http.Client{}
		req, _ := http.NewRequest("GET", server.URL+fmt.Sprintf("/gallery/%d", folder.ID), nil)
		req.AddCookie(MakeAuthCookie(t, app))
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", "gallery-content")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		doc, err := testutil.ParseHTML(strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("Failed to parse HTML response: %v", err)
		}

		// Check for hx-swap-oob attributes
		var foundOOBImageCount, foundOOBBreadcrumbs bool
		var checkOOB func(*html.Node)
		checkOOB = func(n *html.Node) {
			if n.Type == html.ElementNode {
				for _, a := range n.Attr {
					if a.Key == "hx-swap-oob" {
						if a.Val == "true" {
							foundOOBImageCount = true
						}
						if a.Val == "innerHTML:#breadcrumbs-container" {
							foundOOBBreadcrumbs = true
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				checkOOB(c)
			}
		}
		checkOOB(doc)

		if !foundOOBImageCount {
			t.Error("response body does not contain oob image-count swap")
		}

		if !foundOOBBreadcrumbs {
			t.Error("response body does not contain oob breadcrumbs swap directive")
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		client := &http.Client{}
		req, _ := http.NewRequest("GET", server.URL+"/gallery/99999", nil)
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusNotFound)
		}
	})
}

// TestRefactoredImageHandlerByID tests the future ID-based image handler.
func TestRefactoredImageHandlerByID(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	// Get the router from the app
	router := app.getRouter()

	// Create a test server
	server := httptest.NewServer(router)
	defer server.Close()

	// Create a folder and file for testing
	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	file, err := importer.UpsertPathChain(app.ctx, "/test-folder/test-image.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}

	t.Run("authenticated cache-busting", func(t *testing.T) {
		client := &http.Client{}
		req, _ := http.NewRequest("GET", server.URL+fmt.Sprintf("/image/%d", file.ID), nil)
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to parse HTML: %v", err)
		}

		imgPathPrefix := fmt.Sprintf("/raw-image/%d?v=", file.ID)
		var found bool
		var imgSrcs []string
		var f func(*html.Node)
		f = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "img" {
				for _, a := range n.Attr {
					if a.Key == "src" {
						imgSrcs = append(imgSrcs, a.Val)
						if strings.HasPrefix(a.Val, imgPathPrefix) {
							found = true
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				f(c)
			}
		}
		f(doc)
		if !found {
			t.Fatalf("did not find <img> element with src starting with %q in response. Found srcs: %v", imgPathPrefix, imgSrcs)
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		client := &http.Client{}
		// Make a request to the test server
		resp, err := client.Get(server.URL + fmt.Sprintf("/image/%d", file.ID))
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		// Image routes are now public, expect 200 OK
		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		if findElementByID(doc, "imageContainer") == nil {
			t.Error("response body does not contain the image container")
		}
	})

	t.Run("authenticated", func(t *testing.T) {
		client := &http.Client{}
		req, _ := http.NewRequest("GET", server.URL+fmt.Sprintf("/image/%d", file.ID), nil)
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		if findElementByID(doc, "imageContainer") == nil {
			t.Error("response body does not contain the image container")
		}
	})
}

func TestRefactoredRawImageByIDHandler(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	file, err := importer.UpsertPathChain(app.ctx, "/test-folder/raw-image.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}

	// Create the physical file
	imageContent := []byte("dummy image data")
	imagePath := filepath.Join(app.imagesDir, "/test-folder/raw-image.jpg")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("failed to create test image directory: %v", err)
	}
	if err := os.WriteFile(imagePath, imageContent, 0o644); err != nil {
		t.Fatalf("failed to write test image file: %v", err)
	}

	t.Run("unauthenticated", func(t *testing.T) {
		client := &http.Client{}
		// No cache-buster
		u, _ := url.Parse(server.URL)
		u.Path = fmt.Sprintf("/raw-image/%d", file.ID)
		req, _ := http.NewRequest("GET", u.String(), nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		// Raw-image routes are now public, expect 200 OK (or appropriate status for file not found)
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("handler returned wrong status code: got %v want %v or %v", resp.StatusCode, http.StatusOK, http.StatusNotFound)
		}
		// With cache-buster
		q := u.Query()
		q.Set("v", "12345")
		u.RawQuery = q.Encode()
		req2, _ := http.NewRequest("GET", u.String(), nil)
		resp2, err := client.Do(req2)
		if err != nil {
			t.Fatalf("Failed to make request to test server (cache-buster): %v", err)
		}
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode != http.StatusOK && resp2.StatusCode != http.StatusNotFound && resp2.StatusCode != http.StatusInternalServerError {
			t.Errorf("handler (cache-buster) returned wrong status code: got %v want %v or %v", resp2.StatusCode, http.StatusOK, http.StatusNotFound)
		}
	})

	t.Run("authenticated", func(t *testing.T) {
		client := &http.Client{}
		// No cache-buster
		u, _ := url.Parse(server.URL)
		u.Path = fmt.Sprintf("/raw-image/%d", file.ID)
		req, _ := http.NewRequest("GET", u.String(), nil)
		req.AddCookie(MakeAuthCookie(t, app))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}
		if resp.Header.Get("Content-Type") != "image/jpeg" {
			t.Errorf("wrong content type: got %s want image/jpeg", resp.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}
		if string(body) != string(imageContent) {
			t.Errorf("response body mismatch: got %s want %s", string(body), string(imageContent))
		}
		// With cache-buster
		q := u.Query()
		q.Set("v", "12345")
		u.RawQuery = q.Encode()
		req2, _ := http.NewRequest("GET", u.String(), nil)
		req2.AddCookie(MakeAuthCookie(t, app))
		resp2, err := client.Do(req2)
		if err != nil {
			t.Fatalf("Failed to make request to test server (cache-buster): %v", err)
		}
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode != http.StatusOK {
			t.Errorf("handler (cache-buster) returned wrong status code: got %v want %v", resp2.StatusCode, http.StatusOK)
		}
		if resp2.Header.Get("Content-Type") != "image/jpeg" {
			t.Errorf("wrong content type (cache-buster): got %s want image/jpeg", resp2.Header.Get("Content-Type"))
		}
		body2, err := io.ReadAll(resp2.Body)
		if err != nil {
			t.Fatalf("Failed to read response body (cache-buster): %v", err)
		}
		if string(body2) != string(imageContent) {
			t.Errorf("response body mismatch (cache-buster): got %s want %s", string(body2), string(imageContent))
		}
	})
}

func TestRefactoredThumbnailByIDHandler(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	file, err := importer.UpsertPathChain(app.ctx, "/test-folder/thumb-image.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}

	thumbContent := []byte("dummy thumb data")
	_, err = files.UpsertThumbnail(app.ctx, cpc, file.ID, thumbContent)
	if err != nil {
		t.Fatalf("failed to upsert thumbnail: %v", err)
	}

	t.Run("authenticated", func(t *testing.T) {
		client := &http.Client{}
		req, _ := http.NewRequest("GET", server.URL+fmt.Sprintf("/thumbnail/file/%d", file.ID), nil)
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		if string(body) != string(thumbContent) {
			t.Errorf("response body mismatch: got %s want %s", string(body), string(thumbContent))
		}
	})

	t.Run("authenticated cache-busting", func(t *testing.T) {
		client := &http.Client{}
		req, _ := http.NewRequest("GET", server.URL+fmt.Sprintf("/gallery/%d", file.FolderID.Int64), nil)
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to parse HTML: %v", err)
		}

		thumbPath := fmt.Sprintf("/thumbnail/file/%d?v=", file.ID)
		var found bool
		var imgSrcs []string
		var f func(*html.Node)
		f = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "img" {
				for _, a := range n.Attr {
					if a.Key == "src" {
						imgSrcs = append(imgSrcs, a.Val)
						if strings.HasPrefix(a.Val, thumbPath) {
							found = true
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				f(c)
			}
		}
		f(doc)
		if !found {
			t.Fatalf("did not find <img> element with src starting with %q in response. Found srcs: %v", thumbPath, imgSrcs)
		}
	})
}

func TestRefactoredFolderThumbnailByIDHandler(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	file, err := importer.UpsertPathChain(app.ctx, "/tile-folder/tile-image.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}

	thumbContent := []byte("dummy tile data")
	_, err = files.UpsertThumbnail(app.ctx, cpc, file.ID, thumbContent)
	if err != nil {
		t.Fatalf("failed to upsert thumbnail: %v", err)
	}

	// Assign thumbnail to folder
	tx, err := cpc.Conn.BeginTx(app.ctx, nil)
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }() // Rollback if anything goes wrong
	qtx := cpc.Queries.WithTx(tx)
	err = qtx.UpdateFolderTileId(app.ctx, gallerydb.UpdateFolderTileIdParams{
		TileID: sql.NullInt64{Int64: file.ID, Valid: true},
		ID:     file.FolderID.Int64,
	})
	if err != nil {
		t.Fatalf("failed to update folder tile id: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit transaction: %v", err)
	}

	t.Run("authenticated", func(t *testing.T) {
		client := &http.Client{}
		req, _ := http.NewRequest("GET", server.URL+fmt.Sprintf("/thumbnail/folder/%d", file.FolderID.Int64), nil)
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		if string(body) != string(thumbContent) {
			t.Errorf("response body mismatch: got %s want %s", string(body), string(thumbContent))
		}
	})

	t.Run("authenticated cache-busting", func(t *testing.T) {
		client := &http.Client{}
		cacheBuster := time.Now().Unix()
		url := server.URL + fmt.Sprintf("/thumbnail/folder/%d?v=%d", file.FolderID.Int64, cacheBuster)
		req, _ := http.NewRequest("GET", url, nil)
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		if string(body) != string(thumbContent) {
			t.Errorf("response body mismatch: got %s want %s", string(body), string(thumbContent))
		}
	})
}

// REMOVED: func TestGalleryByIDHandler_SortOrder(t *testing.T) {
// REMOVED: 	app := CreateApp(t, false)
// REMOVED: 	defer app.Shutdown()
// REMOVED:
// REMOVED: 	// Generate test files in app.imagesDir
// REMOVED: 	filePaths := []string{
// REMOVED: 		"FileA.gif",
// REMOVED: 		"FileB.png",
// REMOVED: 		"FileC.jpg",
// REMOVED: 		"FolderB/FileB.jpg",
// REMOVED: 		"FolderA/FileA.jpg",
// REMOVED: 	}
// REMOVED:
// REMOVED: 	err := gentestfiles.CreateTestFiles(app.imagesDir, filePaths)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("file to create test files: %v", err)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	router := app.getRouter()
// REMOVED: 	server := httptest.NewServer(router)
// REMOVED: 	defer server.Close()
// REMOVED:
// REMOVED: 	cpc, err := app.dbRwPool.Get()
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get db connection: %v", err)
// REMOVED: 	}
// REMOVED: 	defer app.dbRwPool.Put(cpc)
// REMOVED:
// REMOVED: 	importer := gallerylib.Importer{Q: cpc.Queries}
// REMOVED:
// REMOVED: 	for _, fp := range filePaths {
// REMOVED: 		_, err2 := importer.UpsertPathChain(app.ctx, fp, 0, 0, "", 0, 0, 0, "image/jpeg")
// REMOVED: 		if err2 != nil {
// REMOVED: 			t.Fatalf("failed to upsert path chain: %v", err2)
// REMOVED: 		}
// REMOVED: 	}
// REMOVED:
// REMOVED: 	waitForWorkersToBeIdle(app, t)
// REMOVED:
// REMOVED: 	// Get the ID of the parent folder
// REMOVED: 	sortTestFolder, err := cpc.Queries.GetFolderByPath(app.ctx, "")
// REMOVED: 	if err != nil {
// REMOVED: 		folderPaths, _ := cpc.Queries.GetFolderPaths(app.ctx)
// REMOVED: 		t.Logf("Folder paths in DB: %v", folderPaths)
// REMOVED: 		t.Fatalf("Failed to get folder by path '%s': %v", app.imagesDir, err)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// t.Run("tempTest", func(t *testing.T) {
// REMOVED: 	// 	req := httptest.NewRequest("GET", "/gallery/1", nil)
// REMOVED: 	// 	req.SetPathValue("id", "1")
// REMOVED: 	// 	w := httptest.NewRecorder()
// REMOVED: 	//
// REMOVED: 	// 	// Call YOUR handler function directly (not a live endpoint)
// REMOVED: 	// 	app.galleryByIDHandler(w, req)
// REMOVED: 	//
// REMOVED: 	// 	body := w.Body.String()
// REMOVED: 	// 	fmt.Println(body) // For debugging
// REMOVED: 	//
// REMOVED: 	// 	_ = body // Use body for debugging if needed
// REMOVED: 	// })
// REMOVED:
// REMOVED: 	t.Run("authenticated - correct sort order", func(t *testing.T) {
// REMOVED: 		client := &http.Client{}
// REMOVED: 		req, _ := http.NewRequest("GET", server.URL+fmt.Sprintf("/gallery/%d", sortTestFolder.ID), nil)
// REMOVED: 		req.AddCookie(MakeAuthCookie(t, app))
// REMOVED:
// REMOVED: 		resp, err := client.Do(req)
// REMOVED: 		if err != nil {
// REMOVED: 			t.Fatalf("Failed to make request to test server: %v", err)
// REMOVED: 		}
// REMOVED: 		defer func() { _ = resp.Body.Close() }()
// REMOVED:
// REMOVED: 		if resp.StatusCode != http.StatusOK {
// REMOVED: 			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
// REMOVED: 		}
// REMOVED:
// REMOVED: 		// Verify Content-Type header is set correctly
// REMOVED: 		contentType := resp.Header.Get("Content-Type")
// REMOVED: 		if contentType != "text/html; charset=utf-8" {
// REMOVED: 			t.Errorf("Expected Content-Type 'text/html; charset=utf-8', got %q", contentType)
// REMOVED: 		}
// REMOVED:
// REMOVED: 		doc, err := html.Parse(resp.Body)
// REMOVED: 		if err != nil {
// REMOVED: 			t.Fatalf("Failed to parse response body: %v", err)
// REMOVED: 		}
// REMOVED:
// REMOVED: 		boxGallery := findElementByID(doc, "boxgallery")
// REMOVED: 		if boxGallery == nil {
// REMOVED: 			t.Fatal("could not find boxgallery element")
// REMOVED: 		}
// REMOVED:
// REMOVED: 		var actualDispNames []string
// REMOVED: 		// Each tile is a div with role="listitem"
// REMOVED: 		for c := boxGallery.FirstChild; c != nil; c = c.NextSibling {
// REMOVED: 			if c.Type == html.ElementNode && c.Data == "div" {
// REMOVED: 				role, ok := getAttribute(c, "role")
// REMOVED: 				if ok && role == "listitem" {
// REMOVED: 					// Find the <a> element inside the listitem div
// REMOVED: 					aNode := findElementByTag(c, "a")
// REMOVED: 					if aNode != nil {
// REMOVED: 						ariaLabel, ariaOk := getAttribute(aNode, "aria-label")
// REMOVED: 						if ariaOk && strings.HasPrefix(ariaLabel, "View ") {
// REMOVED: 							actualDispNames = append(actualDispNames, strings.TrimPrefix(ariaLabel, "View "))
// REMOVED: 						}
// REMOVED: 					}
// REMOVED: 				}
// REMOVED: 			}
// REMOVED: 		}
// REMOVED:
// REMOVED: 		expectedDispNames := []string{
// REMOVED: 			"📁︎ FolderA",
// REMOVED: 			"📁︎ FolderB",
// REMOVED: 			"FileA.gif",
// REMOVED: 			"FileB.png",
// REMOVED: 			"FileC.jpg",
// REMOVED: 		}
// REMOVED:
// REMOVED: 		if !reflect.DeepEqual(actualDispNames, expectedDispNames) {
// REMOVED: 			t.Errorf("sort order mismatch:\nGot:  %v\nWant: %v", actualDispNames, expectedDispNames)
// REMOVED: 		}
// REMOVED: 	})
// REMOVED: }

// TestGalleryByIDHandler_SetsAllRequiredHeaders verifies that galleryByIDHandler
// sets all required response headers correctly when compression is enabled.
// It validates Content-Type, ETag, Cache-Control, Last-Modified,
// Content-Encoding, and Vary headers through an integration test.
func TestGalleryByIDHandler_SetsAllRequiredHeaders(t *testing.T) {
	// Ensure default session flags for handler tests
	// Don't set environment variables - rely on CreateAppWithOpt defaults
	app := CreateAppWithOpt(t, false, getopt.Opt{EnableCompression: getopt.OptBool{Bool: true, IsSet: true}})
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	// Ensure there is at least one gallery (root folder exists by default from migrations)
	client := &http.Client{}
	req, _ := http.NewRequest("GET", server.URL+"/gallery/1", nil)
	req.AddCookie(MakeAuthCookie(t, app))
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request to test server: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
	}

	// Verify all required headers are set correctly
	contentType := resp.Header.Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("expected Content-Type 'text/html; charset=utf-8', got %q", contentType)
	}

	if ce := resp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("expected Content-Encoding 'gzip', got %q", ce)
	}

	if etag := resp.Header.Get("ETag"); etag == "" {
		t.Errorf("expected ETag header to be set, got empty")
	}

	cacheControl := resp.Header.Get("Cache-Control")
	if cacheControl != "public, max-age=2592000" {
		t.Errorf("expected Cache-Control 'public, max-age=2592000', got %q", cacheControl)
	}

	if lastMod := resp.Header.Get("Last-Modified"); lastMod == "" {
		t.Errorf("expected Last-Modified header to be set, got empty")
	} else if _, err := http.ParseTime(lastMod); err != nil {
		t.Errorf("Last-Modified header is not valid HTTP date format: %v", err)
	}

	if vary := resp.Header.Get("Vary"); vary == "" {
		t.Errorf("expected Vary header to be set (from compression middleware), got empty")
	}
}

func TestLightboxLooping(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	file1, err := importer.UpsertPathChain(app.ctx, "/loop-test/image1.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}
	file2, err := importer.UpsertPathChain(app.ctx, "/loop-test/image2.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}
	// Ensure order by filename
	if file1.Filename > file2.Filename {
		file1, file2 = file2, file1
	}

	t.Run("cache-busting on all lightbox controls", func(t *testing.T) {
		// Test both the first and last image for all relevant controls
		for _, tc := range []struct {
			label string
			id    int64
			prev  int64
			next  int64
			first int64
			last  int64
		}{
			{"first", file1.ID, file2.ID, file2.ID, file1.ID, file2.ID},
			{"last", file2.ID, file1.ID, file1.ID, file1.ID, file2.ID},
		} {
			req, _ := http.NewRequest("GET", server.URL+fmt.Sprintf("/lightbox/%d", tc.id), nil)
			req.AddCookie(MakeAuthCookie(t, app))
			resp, err := (&http.Client{}).Do(req)
			if err != nil {
				t.Fatalf("Failed to make request to test server: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
			}
			doc, err := testutil.ParseHTML(resp.Body)
			if err != nil {
				t.Fatalf("Failed to parse response body: %v", err)
			}
			// Helper to check hx-get by id
			checkBtn := func(id, want string) {
				btn := findElementByID(doc, id)
				if btn == nil {
					t.Fatalf("Could not find %s in lightbox response", id)
				}
				got, ok := getAttribute(btn, "hx-get")
				if !ok {
					t.Fatalf("%s is missing hx-get attribute", id)
				}
				if got != want {
					t.Errorf("%s has wrong hx-get: got %q, want %q", id, got, want)
				}
			}
			// Navigation buttons
			checkBtn("lightbox-first-btn", fmt.Sprintf("/lightbox/%d?v=%s", tc.first, ui.GetCacheVersion()))
			checkBtn("lightbox-prev-btn", fmt.Sprintf("/lightbox/%d?v=%s", tc.prev, ui.GetCacheVersion()))
			checkBtn("lightbox-next-btn", fmt.Sprintf("/lightbox/%d?v=%s", tc.next, ui.GetCacheVersion()))
			checkBtn("lightbox-last-btn", fmt.Sprintf("/lightbox/%d?v=%s", tc.last, ui.GetCacheVersion()))

			// Info box (hx-get on #lightbox-ui)
			lightboxUI := findElementByID(doc, "lightbox-ui")
			if lightboxUI == nil {
				t.Fatal("Could not find lightbox-ui in response")
			}
			infoGet, ok := getAttribute(lightboxUI, "hx-get")
			if !ok {
				t.Fatal("lightbox-ui missing hx-get attribute for info box")
			}
			wantInfo := fmt.Sprintf("/info/image/%d?v=%s", tc.id, ui.GetCacheVersion())
			if infoGet != wantInfo {
				t.Errorf("lightbox-ui info box hx-get wrong: got %q, want %q", infoGet, wantInfo)
			}

			// Navigation overlays: find all <a> with hx-get
			var overlayCount int
			var f func(*html.Node)
			f = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "a" {
					if v, ok := getAttribute(n, "hx-get"); ok {
						overlayCount++
						// Should be /lightbox/<id>?v=ui.GetCacheVersion()
						if !strings.HasPrefix(v, "/lightbox/") || !strings.Contains(v, "?v=") {
							t.Errorf("overlay <a> hx-get malformed: %q", v)
						}
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					f(c)
				}
			}
			f(doc)
			if overlayCount == 0 {
				t.Error("No navigation overlay <a> elements with hx-get found")
			}
		}
	})
}

func TestInfoBoxFolderHandler(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	// 1. Setup data
	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	// Create a folder with 2 subfolders, 3 images, 1 non-image file
	_, err = importer.UpsertPathChain(app.ctx, "/info-test/sub1/file.txt", 0, 0, "", 0, 0, 0, "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	_, err = importer.UpsertPathChain(app.ctx, "/info-test/sub2/file.txt", 0, 0, "", 0, 0, 0, "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	_, err = importer.UpsertPathChain(app.ctx, "/info-test/image1.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	_, err = importer.UpsertPathChain(app.ctx, "/info-test/image2.png", 0, 0, "", 0, 0, 0, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	_, err = importer.UpsertPathChain(app.ctx, "/info-test/image3.gif", 0, 0, "", 0, 0, 0, "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	_, err = importer.UpsertPathChain(app.ctx, "/info-test/document.pdf", 0, 0, "", 0, 0, 0, "application/pdf")
	if err != nil {
		t.Fatal(err)
	}

	// Get the ID of the parent folder
	testFolder, err := cpc.Queries.GetFolderByPath(app.ctx, "/info-test")
	if err != nil {
		t.Fatalf("Failed to get test folder: %v", err)
	}

	// 2. Make request
	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+fmt.Sprintf("/info/folder/%d", testFolder.ID), nil)
	req.AddCookie(MakeAuthCookie(t, app))

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 3. Assertions
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	doc, err := testutil.ParseHTML(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Check specific fields by ID
	checks := []struct {
		id       string
		expected string
	}{
		{"folder-dir-count", "2"},
		{"folder-image-count", "3"},
		{"folder-file-count", "1"},
	}

	for _, check := range checks {
		text, found := getElementTextByID(doc, check.id)
		if !found {
			t.Errorf("Element with ID %q not found", check.id)
			continue
		}
		if text != check.expected {
			t.Errorf("For element #%s, expected text %q, got %q", check.id, check.expected, text)
		}
	}
}

func TestInfoBoxImageHandler(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	// 1. Setup data
	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	file, err := importer.UpsertPathChain(app.ctx, "/info-test/image1.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}

	// 2. Add mock EXIF and IPTC data
	mockExif := gallerydb.UpsertExifParams{
		FileID:       file.ID,
		CameraMake:   sql.NullString{String: "TestMake", Valid: true},
		CameraModel:  sql.NullString{String: "TestModel", Valid: true},
		LensModel:    sql.NullString{Valid: false},
		Latitude:     sql.NullFloat64{Float64: 34.94304, Valid: true},
		Longitude:    sql.NullFloat64{Float64: -109.77774666667, Valid: true},
		Altitude:     sql.NullFloat64{Valid: false},
		CaptureDate:  sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		Iso:          sql.NullInt64{Int64: 400, Valid: true},
		ShutterSpeed: sql.NullString{String: "1/250", Valid: true},
		Aperture:     sql.NullString{String: "f8.0", Valid: true},
		FocalLength:  sql.NullString{String: "50.0", Valid: true},
		Orientation:  sql.NullInt64{Valid: false},
	}
	err = cpc.Queries.UpsertExif(app.ctx, mockExif)
	if err != nil {
		t.Fatalf("Failed to insert mock EXIF data: %v", err)
	}

	mockIptc := gallerydb.UpsertIPTCParams{
		FileID: file.ID,

		Creator: sql.NullString{String: "Test Author", Valid: true},

		Copyright: sql.NullString{String: "Test Copyright", Valid: true},
	}

	err = cpc.Queries.UpsertIPTC(app.ctx, mockIptc)
	if err != nil {
		t.Fatalf("Failed to insert mock IPTC data: %v", err)
	}

	// 3. Make request
	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+fmt.Sprintf("/info/image/%d", file.ID), nil)
	req.AddCookie(MakeAuthCookie(t, app))

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 4. Assertions
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status OK, got %d. Body: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	doc, err := testutil.ParseHTML(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Check specific fields by ID
	checks := []struct {
		id       string
		expected string
	}{
		{"image-filename", file.Filename},
		{"image-file-size", fmt.Sprintf("%d", file.SizeBytes.Int64)},
		{"image-index", "1"},
		{"image-count", "1"},
		{"exif-camera-make", "TestMake"},
		{"exif-camera-model", "TestModel"},
		{"exif-iso", "400"},
		{"exif-aperture", "f8.0"},
		{"exif-focal-length", "50.0"},
		// {"iptc-creator", "Test Author"},
		// {"iptc-copyright", "Test Copyright"},
	}

	for _, check := range checks {
		text, found := getElementTextByID(doc, check.id)
		if !found {
			t.Errorf("Element with ID %q not found", check.id)
			continue
		}
		if strings.TrimSpace(text) != check.expected {
			t.Errorf("For element #%s, expected %q, got %q", check.id, check.expected, text)
		}
	}
}

func TestInfoFolderCacheBusting(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond)
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	folder, err := importer.UpsertPathChain(app.ctx, "/cachebust-folder/file.txt", 0, 0, "", 0, 0, 0, "text/plain")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}

	url := server.URL + "/info/folder/" + fmt.Sprint(folder.FolderID.Int64) + "?v=" + ui.GetCacheVersion()
	req, _ := http.NewRequest("GET", url, nil)
	req.AddCookie(MakeAuthCookie(t, app))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request to test server: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
	}
	// Optionally, parse the response body for further cache-busting references if needed
}

// ============================================================================
// HX-Push-URL Regression Tests
// ============================================================================

func TestHXPushURLRegression(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond)
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	// Create test folder and image
	importer := gallerylib.Importer{Q: cpc.Queries}
	file, err := importer.UpsertPathChain(app.ctx, "/test-gallery/test-image.jpg", 100, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to import test file: %v", err)
	}
	folderID := file.FolderID.Int64
	fileID := file.ID

	// Get current version
	version := app.GetETagVersion()

	authCookie := MakeAuthCookie(t, app)

	tests := []struct {
		name     string
		path     string
		expected string // empty means handler must NOT set HX-Push-URL (lightbox/info so back/j works)
	}{
		{
			name:     "Gallery",
			path:     fmt.Sprintf("/gallery/%d", folderID),
			expected: fmt.Sprintf("/gallery/%d?v=%s", folderID, version),
		},
		{
			name:     "Image",
			path:     fmt.Sprintf("/image/%d", fileID),
			expected: fmt.Sprintf("/image/%d?v=%s", fileID, version),
		},
		{
			name:     "Lightbox",
			path:     fmt.Sprintf("/lightbox/%d", fileID),
			expected: "", // must not push URL so back/j after close goes to previous folder
		},
		{
			name:     "Info Folder",
			path:     fmt.Sprintf("/info/folder/%d", folderID),
			expected: "", // must not push URL (info is partial; lightbox loads /info/image and must not change URL)
		},
		{
			name:     "Info Image",
			path:     fmt.Sprintf("/info/image/%d", fileID),
			expected: "", // must not push URL so opening lightbox does not change URL; back/j works after close
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", server.URL+tt.path, nil)
			req.AddCookie(authCookie)
			// Header should be present even for non-HTMX requests (safe)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s: request failed: %v", tt.name, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s: expected status 200, got %d", tt.name, resp.StatusCode)
			}

			pushURL := resp.Header.Get("HX-Push-URL")
			if pushURL != tt.expected {
				t.Errorf("%s: expected HX-Push-URL %q, got %q", tt.name, tt.expected, pushURL)
			}
		})
	}
}

// ============================================================================
// Config Export/Import Handler Tests
// ============================================================================

// ============================================================================
// Config Restore/Restart Handler Tests
// ============================================================================
