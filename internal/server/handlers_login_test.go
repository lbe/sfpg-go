package server

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/testutil"
)

// ============================================================================
// Main Handler Tests
// ============================================================================

func TestRootRedirectLeadsToGallery(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	server := httptest.NewServer(app.getRouter())
	defer server.Close()

	client := &http.Client{}

	// Test without authentication (gallery is now public)
	req, err := http.NewRequest("GET", server.URL+"/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected final status 200 OK after following redirect, got %d", resp.StatusCode)
	}
}

func TestLoginHandler(t *testing.T) {
	app := CreateApp(t)
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

	// Helper to fetch CSRF token from the uncached /login-form endpoint.
	// The login form is no longer statically rendered in the cached gallery page;
	// it is loaded dynamically via HTMX to avoid stale CSRF tokens.
	getCSRFToken := func() (string, error) {
		resp, err := client.Get(server.URL + "/login-form")
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			return "", err
		}
		// Find the csrf_token input directly in the login form response
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
		findCSRF(doc)
		if csrfToken == "" {
			return "", fmt.Errorf("csrf_token input not found in /login-form response")
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

		req, err := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
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

		// Successful login returns HX-Trigger: auth-changed header
		// The hamburger menu refreshes via GET /hamburger-menu triggered by the event
		if trigger := resp2.Header.Get("HX-Trigger"); trigger != "auth-changed" {
			t.Errorf("Expected HX-Trigger header to be 'auth-changed', got '%s'", trigger)
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

		req, err := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
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
	app := CreateApp(t)
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
		resp, getErr := client.Get(server.URL + "/login-form")
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
	var req *http.Request
	for i := range 3 {
		csrfToken := getCSRFToken()
		form := url.Values{}
		form.Add("username", username)
		form.Add("password", wrongPassword)
		form.Add("csrf_token", csrfToken)

		req, err = http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
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

	req, err = http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
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
	app := CreateApp(t)
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
		resp, getErr := client.Get(server.URL + "/login-form")
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
	var req *http.Request
	for i := range 2 {
		csrfToken := getCSRFToken()
		form := url.Values{}
		form.Add("username", username)
		form.Add("password", "wrongpassword")
		form.Add("csrf_token", csrfToken)

		req, err = http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
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

	req, err = http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
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
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows after successful login, got %v", err)
	}
}

func TestLoginHandler_LockoutExpiration(t *testing.T) {
	app := CreateApp(t)
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
		resp, getErr := client.Get(server.URL + "/login-form")
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

	req, err := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
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

	// Login success returns HX-Trigger: auth-changed (hamburger menu refreshes via /hamburger-menu)
	if trigger := resp.Header.Get("HX-Trigger"); trigger != "auth-changed" {
		t.Errorf("Expected HX-Trigger header to be 'auth-changed', got '%s'", trigger)
	}
}

func TestLoginHandler_LockoutBlocksLogin(t *testing.T) {
	app := CreateApp(t)
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
		resp, getErr := client.Get(server.URL + "/login-form")
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

	req, err := http.NewRequest("POST", server.URL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
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
	app := CreateApp(t)
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
		req, err := http.NewRequest("POST", server.URL+"/logout", strings.NewReader("csrf_token=test-csrf-token-for-consistent-caching"))
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

		// Logout returns HX-Trigger: auth-changed to refresh the hamburger menu
		if trigger := resp.Header.Get("HX-Trigger"); trigger != "auth-changed" {
			t.Errorf("Expected HX-Trigger header to be 'auth-changed', got '%s'", trigger)
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
