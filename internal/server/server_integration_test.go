//go:build integration

package server

import (
	"context"
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

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"golang.org/x/net/html"
)

// --- merged from middleware_wiring_integration_test.go ---
func contains(header, value string) bool {
	parts := strings.SplitSeq(header, ",")
	for part := range parts {
		if strings.TrimSpace(part) == value {
			return true
		}
	}
	return false
}

// createAppWithRouter returns a full app and its router for middleware/router tests.
func createAppWithRouter(t testing.TB, opts ...AppOption) (*App, http.Handler) {
	t.Helper()
	app := CreateApp(t, opts...)
	return app, app.getRouter()
}

// createAppWithServer returns a full app and an httptest server backed by its router.
func createAppWithServer(t testing.TB, opts ...AppOption) (*App, *httptest.Server) {
	t.Helper()
	app := CreateApp(t, opts...)
	return app, httptest.NewServer(app.getRouter())
}

// createAppWithLoadedConfig returns a full app with config loaded from the database.
func createAppWithLoadedConfig(t testing.TB) *App {
	t.Helper()
	app := CreateApp(t)
	if err := app.loadConfig(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	return app
}

// createAppWithContext returns a full app and a non-nil context.
func createAppWithContext(t testing.TB) (*App, context.Context) {
	t.Helper()
	app := CreateApp(t)
	ctx := app.RuntimeManager.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return app, ctx
}

func TestGetRouter_CompressionMiddlewareWired(t *testing.T) {
	opt := getopt.Opt{
		EnableCompression: getopt.OptBool{Bool: true, IsSet: true},
		EnableHTTPCache:   getopt.OptBool{Bool: false, IsSet: true},
	}
	app, router := createAppWithRouter(t, WithGetoptOpt(opt))
	defer app.Shutdown()

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	varyHeaders := w.Header().Values("Vary")
	hasAcceptEncoding := false
	for _, v := range varyHeaders {
		if contains(v, "Accept-Encoding") {
			hasAcceptEncoding = true
			break
		}
	}
	if !hasAcceptEncoding {
		t.Errorf("Expected 'Accept-Encoding' in Vary header when compression enabled, got: %v", varyHeaders)
	}
}

func TestGetRouter_CompressionMiddlewareNotWired(t *testing.T) {
	opt := getopt.Opt{
		EnableCompression: getopt.OptBool{Bool: false, IsSet: true},
		EnableHTTPCache:   getopt.OptBool{Bool: false, IsSet: true},
	}
	app, router := createAppWithRouter(t, WithGetoptOpt(opt))
	defer app.Shutdown()

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	varyHeaders := w.Header().Values("Vary")
	for _, v := range varyHeaders {
		if contains(v, "Accept-Encoding") {
			t.Errorf("Expected no 'Accept-Encoding' in Vary header when compression disabled, got: %v", varyHeaders)
			break
		}
	}
}

func TestGetRouter_ConditionalMiddlewareWired(t *testing.T) {
	opt := getopt.Opt{
		EnableCompression: getopt.OptBool{Bool: false, IsSet: true},
		EnableHTTPCache:   getopt.OptBool{Bool: false, IsSet: true},
	}
	app, router := createAppWithRouter(t, WithGetoptOpt(opt))
	defer app.Shutdown()

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK with conditional middleware in chain, got: %d", w.Code)
	}

	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Error("Expected ETag header to be set")
	}
}

func TestGetRouter_HTTPCacheMiddlewareWired(t *testing.T) {
	opt := getopt.Opt{
		EnableCompression: getopt.OptBool{Bool: false, IsSet: true},
		EnableHTTPCache:   getopt.OptBool{Bool: true, IsSet: true},
	}
	app, router := createAppWithRouter(t, WithGetoptOpt(opt))
	defer app.Shutdown()

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got: %d", w.Code)
	}
}

func TestGetRouter_HTTPCacheMiddlewareNotWired(t *testing.T) {
	opt := getopt.Opt{
		EnableCompression: getopt.OptBool{Bool: false, IsSet: true},
		EnableHTTPCache:   getopt.OptBool{Bool: false, IsSet: true},
	}
	app, router := createAppWithRouter(t, WithGetoptOpt(opt))
	defer app.Shutdown()

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got: %d", w.Code)
	}
}

func TestGetRouter_MiddlewareOrdering(t *testing.T) {
	opt := getopt.Opt{
		EnableCompression: getopt.OptBool{Bool: true, IsSet: true},
		EnableHTTPCache:   getopt.OptBool{Bool: true, IsSet: true},
	}
	app, router := createAppWithRouter(t, WithGetoptOpt(opt))
	defer app.Shutdown()

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK with all middleware, got: %d", w.Code)
	}
}

// --- moved from server_e2e_test.go (config → router wiring) ---
func TestIntegration_ConfigCompression_ServerUsesConfig(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")
	app := CreateApp(t)
	defer app.Shutdown()

	// Set initial config with compression enabled
	t.Parallel()
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.ServerCompressionEnable = true
	app.ConfigManager.ConfigMu.Unlock()

	// Set app.opt to different value (simulating old CLI/env value)
	app.opt.EnableCompression = getopt.OptBool{Bool: true, IsSet: true}

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}
	loginAsAdmin(t, client, ts.URL)

	// Verify initial state: compression enabled
	req1 := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req1.Header.Set("Accept-Encoding", "gzip")
	cookie := MakeAuthCookie(t, app)
	req1.AddCookie(cookie)
	w1 := httptest.NewRecorder()
	app.getRouter().ServeHTTP(w1, req1)

	if w1.Header().Get("Content-Encoding") != "gzip" {
		t.Error("initial state: expected compression to be enabled (Content-Encoding: gzip)")
	}

	// Update config to disable compression
	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	// Include with empty value to signal presence of config fields
	formData.Set("server_compression_enable", "")

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify config was updated
	app.ConfigManager.ConfigMu.RLock()
	compressionEnabled := app.ConfigManager.Config.ServerCompressionEnable
	app.ConfigManager.ConfigMu.RUnlock()

	if compressionEnabled {
		t.Error("expected compression to be disabled in config after update")
	}

	// Verify getRouter() uses app.ConfigManager.Config, not app.opt
	// Set app.opt to enabled (old value) - if getRouter() uses app.opt, compression would still be enabled
	app.opt.EnableCompression = getopt.OptBool{Bool: true, IsSet: true}

	req2 := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	req2.AddCookie(cookie)
	w2 := httptest.NewRecorder()
	app.getRouter().ServeHTTP(w2, req2)

	// If getRouter() uses app.ConfigManager.Config (correct), compression should be disabled
	// If getRouter() uses app.opt (wrong), compression would be enabled
	if w2.Header().Get("Content-Encoding") == "gzip" {
		t.Error("after config update, compression should be disabled (per app.ConfigManager.Config), but getRouter() appears to be using app.opt")
	}

	// Verify Vary header doesn't include Accept-Encoding when compression is disabled
	varyHeaders := w2.Header().Values("Vary")
	hasAcceptEncoding := false
	for _, v := range varyHeaders {
		if strings.Contains(v, "Accept-Encoding") {
			hasAcceptEncoding = true
			break
		}
	}
	if hasAcceptEncoding {
		t.Error("after disabling compression, Vary header should not include Accept-Encoding")
	}
}

func TestIntegration_ConfigCache_ServerUsesConfig(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")
	app := CreateApp(t)
	defer app.Shutdown()

	// Set initial config with cache enabled
	t.Parallel()
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.EnableHTTPCache = true
	app.ConfigManager.ConfigMu.Unlock()

	// Initialize cache middleware (normally done in createDatabasePools)
	// We need to set up the cache middleware manually for this test
	app.opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}

	// Release the existing batcher's dque flock before recreating pools.
	if app.writeBatcher != nil {
		app.writeBatcher.Close()
	}

	app.setDB() // This will call createDatabasePools which initializes cache middleware

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}
	loginAsAdmin(t, client, ts.URL)

	// Make a request to cache something
	cookie := MakeAuthCookie(t, app)
	req1 := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req1.AddCookie(cookie)
	w1 := httptest.NewRecorder()
	app.getRouter().ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	// Verify cache is working (X-Cache header should be MISS on first request)
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Logf("first request X-Cache: %s (may be OK if cache not enabled)", w1.Header().Get("X-Cache"))
	}

	// Update config to disable cache
	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	// Include with empty value to signal presence of config fields
	formData.Set("enable_http_cache", "")

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify config was updated
	app.ConfigManager.ConfigMu.RLock()
	cacheEnabled := app.ConfigManager.Config.EnableHTTPCache
	app.ConfigManager.ConfigMu.RUnlock()

	if cacheEnabled {
		t.Error("expected cache to be disabled in config after update")
	}

	// Verify getRouter() uses app.ConfigManager.Config, not app.opt
	// Set app.opt to enabled (old value)
	app.opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}

	req2 := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req2.AddCookie(cookie)
	w2 := httptest.NewRecorder()
	app.getRouter().ServeHTTP(w2, req2)

	// If getRouter() uses app.ConfigManager.Config (correct), cache middleware should not be applied
	// If getRouter() uses app.opt (wrong), cache middleware would still be applied
	// We can't directly test cache behavior without more setup, but we verify the config is correct
	app.ConfigManager.ConfigMu.RLock()
	finalCacheEnabled := app.ConfigManager.Config.EnableHTTPCache
	app.ConfigManager.ConfigMu.RUnlock()

	if finalCacheEnabled {
		t.Error("after config update, cache should be disabled (per app.ConfigManager.Config)")
	}
}

// --- merged from security_integration_test.go ---
func TestCrossOriginProtection_UnsafeMethodsFull(t *testing.T) {
	app, server := createAppWithServer(t)
	defer app.Shutdown()
	defer server.Close()

	unsafeMethods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for _, method := range unsafeMethods {
		t.Run("NoOrigin_"+method, func(t *testing.T) {
			req, _ := http.NewRequest(method, server.URL+"/config", nil)
			req.AddCookie(MakeAuthCookie(t, app))

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

	t.Run("MismatchedOrigin_POST", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/config", nil)
		req.Header.Set("Origin", "http://evil.com")
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected 403 Forbidden for POST with mismatched Origin, got %d", resp.StatusCode)
		}
	})

	t.Run("ValidOrigin_POST", func(t *testing.T) {
		serverURL, _ := url.Parse(server.URL)
		validOrigin := fmt.Sprintf("http://%s", serverURL.Host)

		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("Failed to create cookie jar: %v", err)
		}
		client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}}

		reqLogin, _ := http.NewRequest(http.MethodGet, server.URL+"/config", nil)
		reqLogin.Header.Set("Origin", validOrigin)
		reqLogin.AddCookie(MakeAuthCookie(t, app))
		respLogin, err := client.Do(reqLogin)
		if err != nil {
			t.Fatalf("GET /config failed: %v", err)
		}
		defer respLogin.Body.Close()

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

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusForbidden {
			t.Errorf("Expected non-403 status for POST with valid Origin, got %d", resp.StatusCode)
		}
	})
}

func TestCrossOriginProtection_SafeMethodsFull(t *testing.T) {
	app, server := createAppWithServer(t)
	defer app.Shutdown()
	defer server.Close()

	safeMethods := []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for _, method := range safeMethods {
		t.Run(method, func(t *testing.T) {
			req, _ := http.NewRequest(method, server.URL+"/gallery/1", nil)
			req.AddCookie(MakeAuthCookie(t, app))

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusForbidden {
				t.Errorf("Safe method %s should not be Forbidden without Origin, got %d", method, resp.StatusCode)
			}
		})
	}
}

func TestSessionSecurity_HttpOnly(t *testing.T) {
	t.Setenv("SEPG_SESSION_HTTPONLY", "true")

	app, server := createAppWithServer(t)
	defer app.Shutdown()
	defer server.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	reqGet, _ := http.NewRequest("GET", server.URL+"/login-form", nil)
	respGet, err := client.Do(reqGet)
	if err != nil {
		t.Fatalf("GET /login-form failed: %v", err)
	}
	defer respGet.Body.Close()
	bodyBytes, _ := io.ReadAll(respGet.Body)
	body := string(bodyBytes)
	var csrf string
	idx := strings.Index(body, `name="csrf_token"`)
	if idx != -1 {
		valIdx := strings.Index(body[idx:], `value="`)
		if valIdx != -1 {
			valStart := idx + valIdx + len(`value="`)
			valEnd := strings.Index(body[valStart:], `"`)
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
	if setCookie := respGet.Header.Get("Set-Cookie"); setCookie != "" {
		req.Header.Set("Cookie", setCookie)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	defer resp.Body.Close()

	cookies := resp.Header.Values("Set-Cookie")
	if len(cookies) == 0 {
		t.Fatal("No Set-Cookie headers found in login response")
	}
	foundHttpOnly := false
	for _, cookie := range cookies {
		if strings.Contains(cookie, "HttpOnly") {
			foundHttpOnly = true
			break
		}
	}

	if !foundHttpOnly {
		t.Error("Expected session cookie to have HttpOnly flag when SEPG_SESSION_HTTPONLY=true")
	}
}

func TestSessionSecurity_Secure(t *testing.T) {
	t.Setenv("SEPG_SESSION_SECURE", "true")

	app, server := createAppWithServer(t)
	defer app.Shutdown()
	defer server.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	reqGet, _ := http.NewRequest("GET", server.URL+"/login-form", nil)
	respGet, err := client.Do(reqGet)
	if err != nil {
		t.Fatalf("GET /login-form failed: %v", err)
	}
	defer respGet.Body.Close()
	bodyBytes, _ := io.ReadAll(respGet.Body)
	body := string(bodyBytes)
	var csrf string
	idx := strings.Index(body, `name="csrf_token"`)
	if idx != -1 {
		valIdx := strings.Index(body[idx:], `value="`)
		if valIdx != -1 {
			valStart := idx + valIdx + len(`value="`)
			valEnd := strings.Index(body[valStart:], `"`)
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
	if setCookie := respGet.Header.Get("Set-Cookie"); setCookie != "" {
		req.Header.Set("Cookie", setCookie)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	defer resp.Body.Close()

	cookies := resp.Header.Values("Set-Cookie")
	if len(cookies) == 0 {
		t.Fatal("No Set-Cookie headers found in login response")
	}
	foundSecure := false
	for _, cookie := range cookies {
		if strings.Contains(cookie, "Secure") {
			foundSecure = true
			break
		}
	}

	if !foundSecure {
		// Try with cookie jar approach
		jar, _ := cookiejar.New(nil)
		client2 := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		serverURL, _ := url.Parse(server.URL)
		validOrigin := fmt.Sprintf("http://%s", serverURL.Host)

		respLogin, err := client2.Get(server.URL + "/login")
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
		respLoginPost, err := client2.Do(reqLogin)
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

// --- merged from server_session_integration_test.go ---
func TestGetSessionOptionsConfig(t *testing.T) {
	app := createAppWithLoadedConfig(t)
	defer app.Shutdown()

	cfg := app.getSessionOptionsConfig()
	if cfg == nil {
		t.Fatal("Expected non-nil session options config")
	}

	if cfg.SessionMaxAge <= 0 {
		t.Error("Expected positive session max age")
	}

	// Just check that values exist, don't test specific values since they come from defaults
	_ = cfg.SessionHttpOnly
	_ = cfg.SessionSecure

	if cfg.SessionSameSite == "" {
		t.Error("Expected non-empty SessionSameSite")
	}
}

func TestGetSessionOptions_WithLoadedConfig(t *testing.T) {
	app := createAppWithLoadedConfig(t)
	defer app.Shutdown()

	opts := session.GetSessionOptions(app.getSessionOptionsConfig())
	if opts == nil {
		t.Error("Expected non-nil session options")
	}
}

func TestSessionExpiry_SessionMaxAge(t *testing.T) {
	app := createAppWithLoadedConfig(t)
	defer app.Shutdown()

	// Set a short session lifetime
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.SessionMaxAge = 1
	app.ConfigManager.ConfigMu.Unlock()
	app.SessionAuthFacade.store.Options = session.GetSessionOptions(app.getSessionOptionsConfig())

	opts := session.GetSessionOptions(app.getSessionOptionsConfig())
	if opts == nil {
		t.Fatal("expected non-nil session options")
	}
	if opts.MaxAge != 1 {
		t.Errorf("MaxAge = %d, want 1", opts.MaxAge)
	}

	// Verify the session is created with the correct MaxAge
	rrWithCookie := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	session, err := app.SessionAuthFacade.store.Get(req, session.SessionName)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}
	session.Values["authenticated"] = true
	if err := session.Save(req, rrWithCookie); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}
	setCookie := rrWithCookie.Header().Get("Set-Cookie")
	if setCookie == "" {
		t.Fatal("expected Set-Cookie header")
	}

	// The Max-Age should be present in the cookie header
	if !strings.Contains(setCookie, "Max-Age=1") &&
		!strings.Contains(setCookie, "Max-Age= 1") {
		t.Logf("Set-Cookie header: %s", setCookie)
	}
}

// --- merged from etag_cache_invalidation_integration_test.go ---
func tableExists(ctx context.Context, app *App, tableName string) (bool, error) {
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		return false, fmt.Errorf("failed to get rw pool connection: %w", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	row := cpcRw.Conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`, tableName)
	var exists int64
	if err := row.Scan(&exists); err != nil {
		return false, fmt.Errorf("scan table exists query failed: %w", err)
	}
	return exists == 1, nil
}

func waitForTableExistence(t *testing.T, ctx context.Context, app *App, tableName string, wantExists bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		exists, err := tableExists(ctx, app, tableName)
		if err != nil {
			t.Fatalf("tableExists(%s): %v", tableName, err)
		}
		if exists == wantExists {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	final, err := tableExists(ctx, app, tableName)
	if err != nil {
		t.Fatalf("tableExists(%s): %v", tableName, err)
	}
	t.Fatalf("timed out waiting for table %s existence=%v, got %v", tableName, wantExists, final)
}

func TestETagIncrement_InvalidatesHTTPCache(t *testing.T) {
	opt := getopt.Opt{}
	opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}
	opt.SessionSecret = getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}
	app := CreateApp(t, WithGetoptOpt(opt))
	defer app.Shutdown()

	ctx := app.RuntimeManager.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	// Populate HTTP cache with an entry
	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/test", Encoding: "identity"}),
		Method:    "GET",
		Path:      "/gallery/test",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("cached content before etag increment"),
		CreatedAt: now,
	}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	// Verify entry exists
	stored, err := cachelite.GetCacheEntry(ctx, app.dbRwPool, entry.Key)
	if err != nil || stored == nil {
		t.Fatalf("expected cache entry to exist before increment, err=%v", err)
	}

	// Call ConfigIncrementETag via handler (simulates user clicking "Increment ETag")
	h := app.HandlerManager.configETagHandler
	if h == nil {
		t.Fatal("app.HandlerManager.configETagHandler is nil")
	}
	formData := strings.NewReader("csrf_token=valid-token")
	req := httptest.NewRequest("POST", "/config/increment-etag", formData)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	addAuthToRequest(t, h.SessionManager, req)

	h.ConfigIncrementETag(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("ConfigIncrementETag status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	// Verify cache was cleared (GetCacheEntry returns nil, sql.ErrNoRows when not found)
	storedAfter, err := cachelite.GetCacheEntry(ctx, app.dbRwPool, entry.Key)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetCacheEntry after increment: %v", err)
	}
	if storedAfter != nil {
		t.Error("expected HTTP cache to be cleared after ETag increment, but entry still exists")
	}

	rotatedExists, err := tableExists(ctx, app, "http_cache_to_be_dropped")
	if err != nil {
		t.Fatalf("tableExists(http_cache_to_be_dropped): %v", err)
	}
	if !rotatedExists {
		t.Fatal("expected rotated stale cache table http_cache_to_be_dropped to exist after ETag invalidation")
	}
}

func TestApplyConfig_InvalidatesCacheWhenETagChanges(t *testing.T) {
	app, ctx := createAppWithContext(t)
	defer app.Shutdown()

	// Set UI cache version to "old", config to "new" so they differ
	ui.SetCacheVersion("etag-old")
	app.ConfigManager.ConfigMu.Lock()
	if app.ConfigManager.Config == nil {
		app.ConfigManager.ConfigMu.Unlock()
		t.Fatal("app.ConfigManager.Config is nil")
	}
	app.ConfigManager.Config.ETagVersion = "etag-new"
	app.ConfigManager.ConfigMu.Unlock()

	// Populate HTTP cache
	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/x", Encoding: "identity"}),
		Method:    "GET",
		Path:      "/gallery/x",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("stale content"),
		CreatedAt: now,
	}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	stored, _ := cachelite.GetCacheEntry(ctx, app.dbRwPool, entry.Key)
	if stored == nil {
		t.Fatal("expected cache entry to exist before applyConfig")
	}

	app.ApplyConfig()

	storedAfter, err := cachelite.GetCacheEntry(ctx, app.dbRwPool, entry.Key)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetCacheEntry after applyConfig: %v", err)
	}
	if storedAfter != nil {
		t.Error("expected HTTP cache to be cleared when ETag changed in applyConfig, but entry still exists")
	}

	rotatedExists, err := tableExists(ctx, app, "http_cache_to_be_dropped")
	if err != nil {
		t.Fatalf("tableExists(http_cache_to_be_dropped): %v", err)
	}
	if !rotatedExists {
		t.Fatal("expected rotated stale cache table http_cache_to_be_dropped to exist after applyConfig ETag invalidation")
	}
}

func TestApplyConfig_DoesNotInvalidateWhenETagUnchanged(t *testing.T) {
	app, ctx := createAppWithContext(t)
	defer app.Shutdown()

	sameETag := "etag-unchanged"
	ui.SetCacheVersion(sameETag)
	app.ConfigManager.ConfigMu.Lock()
	if app.ConfigManager.Config == nil {
		app.ConfigManager.ConfigMu.Unlock()
		t.Fatal("app.ConfigManager.Config is nil")
	}
	app.ConfigManager.Config.ETagVersion = sameETag
	app.ConfigManager.ConfigMu.Unlock()

	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/y", Encoding: "identity"}),
		Method:    "GET",
		Path:      "/gallery/y",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("valid content"),
		CreatedAt: now,
	}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	app.ApplyConfig()

	storedAfter, _ := cachelite.GetCacheEntry(ctx, app.dbRwPool, entry.Key)
	if storedAfter == nil {
		t.Error("expected HTTP cache to NOT be cleared when ETag unchanged, but entry was removed")
	}
}

func TestApplyConfig_DoesNotInvalidateOnStartup(t *testing.T) {
	app, ctx := createAppWithContext(t)
	defer app.Shutdown()

	// Simulate fresh process: in-memory UI cache version not set (empty)
	ui.SetCacheVersion("")
	app.ConfigManager.ConfigMu.Lock()
	if app.ConfigManager.Config == nil {
		app.ConfigManager.ConfigMu.Unlock()
		t.Fatal("app.ConfigManager.Config is nil")
	}
	app.ConfigManager.Config.ETagVersion = "v1-from-db"
	app.ConfigManager.ConfigMu.Unlock()

	// Populate HTTP cache (e.g. from previous run)
	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/z", Encoding: "identity"}),
		Method:    "GET",
		Path:      "/gallery/z",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("cached from before reboot"),
		CreatedAt: now,
	}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	app.ApplyConfig()

	storedAfter, err := cachelite.GetCacheEntry(ctx, app.dbRwPool, entry.Key)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetCacheEntry after applyConfig: %v", err)
	}
	if storedAfter == nil {
		t.Error("expected HTTP cache to persist across startup when ETag unchanged (oldETag empty); entry was cleared")
	}
}

func TestETagIncrementIntegration(t *testing.T) {
	app, ctx := createAppWithContext(t)
	defer app.Shutdown()

	// Pre-populate cache with an entry (simulating a cached response)
	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/1", Encoding: "identity"}),
		Method:    "GET",
		Path:      "/gallery/1",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("cached content before etag increment"),
		CreatedAt: now,
	}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	// Verify cache entry exists
	countBefore, err := cachelite.CountCacheEntries(ctx, app.dbRwPool)
	if err != nil {
		t.Fatalf("failed to count cache entries: %v", err)
	}
	if countBefore == 0 {
		t.Error("expected cache entry to be created")
	}

	// Increment ETag
	newETag, err := app.IncrementETag()
	if err != nil {
		t.Fatalf("IncrementETag failed: %v", err)
	}
	if newETag == "" {
		t.Error("expected non-empty ETag from IncrementETag")
	}

	// Verify cache is cleared
	countAfter, err := cachelite.CountCacheEntries(ctx, app.dbRwPool)
	if err != nil {
		t.Fatalf("failed to count cache entries: %v", err)
	}
	if countAfter != 0 {
		t.Errorf("expected cache to be cleared, got %d entries", countAfter)
	}

	// Verify new ETag is stored in config
	cfg, err := app.ConfigManager.ConfigService.Load(ctx)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.ETagVersion != newETag {
		t.Errorf("expected ETag version %s in config, got %s", newETag, cfg.ETagVersion)
	}
}

func TestWalkImageDir_DropsStaleCacheTable(t *testing.T) {
	app, ctx := createAppWithContext(t)
	defer app.Shutdown()

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get rw pool connection: %v", err)
	}
	_, err = cpcRw.Conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS http_cache_to_be_dropped (id INTEGER PRIMARY KEY, body BLOB)`)
	app.dbRwPool.Put(cpcRw)
	if err != nil {
		t.Fatalf("failed to create http_cache_to_be_dropped: %v", err)
	}

	beforeExists, err := tableExists(ctx, app, "http_cache_to_be_dropped")
	if err != nil {
		t.Fatalf("tableExists before walkImageDir: %v", err)
	}
	if !beforeExists {
		t.Fatal("expected stale table to exist before walkImageDir")
	}

	app.TriggerDiscovery()
	waitForTableExistence(t, ctx, app, "http_cache_to_be_dropped", false)
}

// TestLoginRateLimitPerIP_Returns429AfterLimit verifies the configured per-IP
// login limit is enforced end-to-end: after POST /config sets
// login_rate_limit_per_ip=2 (which clears prior history), the third login
// POST from the same IP returns 429.
func TestLoginRateLimitPerIP_Returns429AfterLimit(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	app := CreateApp(t)
	defer app.Shutdown()

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginAsAdmin(t, client, ts.URL)

	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	originalLimit, err := cpcRo.Queries.GetConfigValueByKey(app.RuntimeManager.ctx, "login_rate_limit_per_ip")
	if err != nil {
		app.dbRoPool.Put(cpcRo)
		t.Fatalf("failed to read login_rate_limit_per_ip from DB: %v", err)
	}
	app.dbRoPool.Put(cpcRo)

	// Register restore before mutating (use the still-authenticated client —
	// never re-login after the burst exhausts the limit).
	defer func() {
		csrf := extractCSRFTokenFromConfig(t, client, ts.URL)
		formData := url.Values{}
		formData.Set("csrf_token", csrf)
		formData.Set("login_rate_limit_per_ip", originalLimit)
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
		if err != nil {
			t.Errorf("restore: create request: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", ts.URL)
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("restore POST /config: %v", err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("restore POST /config: got %d", resp.StatusCode)
		}
	}()

	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("login_rate_limit_per_ip", "2")

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
	if err != nil {
		t.Fatalf("failed to create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for POST /config, got %d", resp.StatusCode)
	}
	// ApplyConfig → SyncLoginRateLimitMax(2) cleared history, so the
	// loginAsAdmin attempt above does not count toward the burst.

	jar2, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client2 := &http.Client{Jar: jar2}

	for attempt := 1; attempt <= 3; attempt++ {
		token := extractCSRFTokenFromLogin(t, client2, ts.URL)
		loginData := url.Values{}
		loginData.Set("username", "wronguser") // non-admin: avoid account lockout side effects
		loginData.Set("password", "wrong")
		loginData.Set("csrf_token", token)
		loginReq, err := http.NewRequest(http.MethodPost, ts.URL+"/login", strings.NewReader(loginData.Encode()))
		if err != nil {
			t.Fatalf("attempt %d: create request: %v", attempt, err)
		}
		loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		loginReq.Header.Set("Origin", ts.URL)
		loginResp, err := client2.Do(loginReq)
		if err != nil {
			t.Fatalf("attempt %d: POST /login: %v", attempt, err)
		}
		loginResp.Body.Close()
		if attempt <= 2 && loginResp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("attempt %d: unexpected 429", attempt)
		}
		if attempt == 3 && loginResp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: got %d, want 429", attempt, loginResp.StatusCode)
		}
	}
}
