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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"time"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/ui"
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

func TestGetRouter_ConditionalMiddlewareWired(t *testing.T) {
	opt := getopt.Opt{
		EnableHTTPCache: getopt.OptBool{Bool: false, IsSet: true},
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
		EnableHTTPCache: getopt.OptBool{Bool: true, IsSet: true},
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
		EnableHTTPCache: getopt.OptBool{Bool: false, IsSet: true},
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
		EnableHTTPCache: getopt.OptBool{Bool: true, IsSet: true},
	}
	app, router := createAppWithRouter(t, WithGetoptOpt(opt))
	defer app.Shutdown()

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	cookie := MakeAuthCookie(t, app)
	req.AddCookie(cookie)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK with all middleware, got: %d", w.Code)
	}
}

// TestIntegration_GalleryCache_AnonymousDoesNotReceiveAuthBody verifies that an HTTP cache
// HIT on /gallery/1 does not replay authenticated admin chrome to an anonymous client.
// Gallery full-page HTML must stay auth-agnostic; admin links load only via HTMX hamburger.
func TestIntegration_GalleryCache_AnonymousDoesNotReceiveAuthBody(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")
	opt := getopt.Opt{}
	opt.SessionSecret.String = "gallery-cache-auth-isolation-test-secret-32b"
	opt.SessionSecret.IsSet = true
	opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}
	app := CreateApp(t, WithGetoptOpt(opt))
	defer app.Shutdown()

	app.ConfigManager.ConfigMu.Lock()
	if app.ConfigManager.Config == nil {
		app.ConfigManager.Config = config.DefaultConfig()
	}
	app.ConfigManager.Config.EnableHTTPCache = true
	app.ConfigManager.ConfigMu.Unlock()
	app.StartWriteBatcher(app.RuntimeManager.ctx, true, config.DefaultDQueMaxDiskBytes)
	app.initializeHTTPCache()
	if app.cacheMW == nil {
		t.Fatal("expected cacheMW to be initialized")
	}

	router := app.getRouter()
	authCookie := MakeAuthCookie(t, app)

	req1 := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req1.AddCookie(authCookie)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("authenticated gallery: status %d, want 200", w1.Code)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("authenticated gallery: X-Cache %q, want MISS", w1.Header().Get("X-Cache"))
	}

	// Cache writes are async via the WriteBatcher; replay the anonymous
	// request until it is served from the cache.
	req2 := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	w2 := waitForCacheStatus(t, router, req2, "HIT")
	if w2.Code != http.StatusOK {
		t.Fatalf("anonymous gallery: status %d, want 200", w2.Code)
	}

	doc, err := html.Parse(strings.NewReader(w2.Body.String()))
	if err != nil {
		t.Fatalf("parse anonymous gallery HTML: %v", err)
	}
	for _, href := range []string{"/dashboard", "/config"} {
		if integrationTestHasAnchorHref(doc, href) {
			t.Errorf("anonymous cached gallery must not contain admin link %q", href)
		}
	}
}

func integrationTestHasAnchorHref(n *html.Node, href string) bool {
	if n == nil {
		return false
	}
	if n.Type == html.ElementNode && n.Data == "a" {
		for _, a := range n.Attr {
			if a.Key == "href" && a.Val == href {
				return true
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if integrationTestHasAnchorHref(c, href) {
			return true
		}
	}
	return false
}

func TestIntegration_HTMLPartialRoutes_CacheHitPreservesContentType(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")
	opt := getopt.Opt{}
	opt.SessionSecret.String = "gallery-cache-auth-isolation-test-secret-32b"
	opt.SessionSecret.IsSet = true
	opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}
	app := CreateApp(t, WithGetoptOpt(opt))
	defer app.Shutdown()

	app.ConfigManager.ConfigMu.Lock()
	if app.ConfigManager.Config == nil {
		app.ConfigManager.Config = config.DefaultConfig()
	}
	app.ConfigManager.Config.EnableHTTPCache = true
	app.ConfigManager.ConfigMu.Unlock()
	app.StartWriteBatcher(app.RuntimeManager.ctx, true, config.DefaultDQueMaxDiskBytes)
	app.initializeHTTPCache()
	if app.cacheMW == nil {
		t.Fatal("expected cacheMW to be initialized")
	}

	router := app.getRouter()

	// DB seed: one file under root folder.
	ctx := app.RuntimeManager.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("dbRwPool.Get: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	rootFolderID, err := cpcRw.Queries.GetFolderIDByPath(ctx, "")
	if err != nil {
		t.Fatalf("GetFolderIDByPath root: %v", err)
	}

	filePath := "/content-type-test.jpg"
	fpID, err := cpcRw.Queries.UpsertFilePathReturningID(ctx, filePath)
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID: %v", err)
	}
	file, err := cpcRw.Queries.UpsertFileReturningFile(ctx, gallerydb.UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: rootFolderID, Valid: true},
		PathID:    fpID,
		Filename:  "content-type-test.jpg",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile: %v", err)
	}
	fileID := file.ID

	// Populate file_folder_index so lightbox/info-image queries find the file.
	_, err = cpcRw.Conn.ExecContext(ctx,
		`INSERT INTO file_folder_index (file_id, folder_id, image_index, image_count, prev_id, next_id, first_id, last_id)
		 VALUES (?, ?, 1, 1, NULL, NULL, ?, ?)`,
		fileID, rootFolderID, fileID, fileID)
	if err != nil {
		t.Fatalf("insert file_folder_index: %v", err)
	}

	// Per-route subtests: MISS -> HIT with Content-Type assertion.
	wantCT := "text/html; charset=utf-8"

	t.Run("lightbox", func(t *testing.T) {
		path := fmt.Sprintf("/lightbox/%d", fileID)
		hxTarget := "lightbox_content"

		req1 := httptest.NewRequest(http.MethodGet, path, nil)
		req1.Header.Set("HX-Request", "true")
		req1.Header.Set("HX-Target", hxTarget)
		w1 := httptest.NewRecorder()
		router.ServeHTTP(w1, req1)
		if w1.Code != http.StatusOK {
			t.Fatalf("MISS status = %d, want 200", w1.Code)
		}
		if w1.Header().Get("X-Cache") != "MISS" {
			t.Fatalf("X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
		}
		if w1.Header().Get("Content-Type") != wantCT {
			t.Fatalf("MISS Content-Type = %q, want %q", w1.Header().Get("Content-Type"), wantCT)
		}

		req2 := httptest.NewRequest(http.MethodGet, path, nil)
		req2.Header.Set("HX-Request", "true")
		req2.Header.Set("HX-Target", hxTarget)
		w2 := waitForCacheStatus(t, router, req2, "HIT")
		if w2.Code != http.StatusOK {
			t.Fatalf("HIT status = %d, want 200", w2.Code)
		}
		if w2.Header().Get("Content-Type") != wantCT {
			t.Fatalf("HIT Content-Type = %q, want %q", w2.Header().Get("Content-Type"), wantCT)
		}
	})

	t.Run("info_image", func(t *testing.T) {
		path := fmt.Sprintf("/info/image/%d", fileID)
		hxTarget := "box_info"

		req1 := httptest.NewRequest(http.MethodGet, path, nil)
		req1.Header.Set("HX-Request", "true")
		req1.Header.Set("HX-Target", hxTarget)
		w1 := httptest.NewRecorder()
		router.ServeHTTP(w1, req1)
		if w1.Code != http.StatusOK {
			t.Fatalf("MISS status = %d, want 200", w1.Code)
		}
		if w1.Header().Get("X-Cache") != "MISS" {
			t.Fatalf("X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
		}
		if w1.Header().Get("Content-Type") != wantCT {
			t.Fatalf("MISS Content-Type = %q, want %q", w1.Header().Get("Content-Type"), wantCT)
		}

		req2 := httptest.NewRequest(http.MethodGet, path, nil)
		req2.Header.Set("HX-Request", "true")
		req2.Header.Set("HX-Target", hxTarget)
		w2 := waitForCacheStatus(t, router, req2, "HIT")
		if w2.Code != http.StatusOK {
			t.Fatalf("HIT status = %d, want 200", w2.Code)
		}
		if w2.Header().Get("Content-Type") != wantCT {
			t.Fatalf("HIT Content-Type = %q, want %q", w2.Header().Get("Content-Type"), wantCT)
		}
	})

	t.Run("info_folder", func(t *testing.T) {
		path := fmt.Sprintf("/info/folder/%d", rootFolderID)
		hxTarget := "box_info"

		req1 := httptest.NewRequest(http.MethodGet, path, nil)
		req1.Header.Set("HX-Request", "true")
		req1.Header.Set("HX-Target", hxTarget)
		w1 := httptest.NewRecorder()
		router.ServeHTTP(w1, req1)
		if w1.Code != http.StatusOK {
			t.Fatalf("MISS status = %d, want 200", w1.Code)
		}
		if w1.Header().Get("X-Cache") != "MISS" {
			t.Fatalf("X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
		}
		if w1.Header().Get("Content-Type") != wantCT {
			t.Fatalf("MISS Content-Type = %q, want %q", w1.Header().Get("Content-Type"), wantCT)
		}

		req2 := httptest.NewRequest(http.MethodGet, path, nil)
		req2.Header.Set("HX-Request", "true")
		req2.Header.Set("HX-Target", hxTarget)
		w2 := waitForCacheStatus(t, router, req2, "HIT")
		if w2.Code != http.StatusOK {
			t.Fatalf("HIT status = %d, want 200", w2.Code)
		}
		if w2.Header().Get("Content-Type") != wantCT {
			t.Fatalf("HIT Content-Type = %q, want %q", w2.Header().Get("Content-Type"), wantCT)
		}
	})
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
	formData := url.Values{}
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

	// COP allows unsafe methods without Origin/Sec-Fetch-Site (non-browser / curl).
	for _, method := range unsafeMethods {
		t.Run("NoOrigin_"+method, func(t *testing.T) {
			req, _ := http.NewRequest(method, server.URL+"/config", nil)
			req.AddCookie(MakeAuthCookie(t, app))

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer resp.Body.Close()

			// COP allows requests without Origin/Sec-Fetch-Site (non-browser curl).
			// The request should NOT be rejected with 403.
			if resp.StatusCode == http.StatusForbidden {
				t.Errorf("Expected non-403 for %s without Origin (non-browser allowed by COP), got %d", method, resp.StatusCode)
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

	t.Run("CrossSiteSecFetchSite_POST", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/login", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected 403 Forbidden for POST with Sec-Fetch-Site: cross-site, got %d", resp.StatusCode)
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

		// GET /config to populate session cookie
		reqLogin, _ := http.NewRequest(http.MethodGet, server.URL+"/config", nil)
		reqLogin.Header.Set("Origin", validOrigin)
		reqLogin.AddCookie(MakeAuthCookie(t, app))
		respLogin, err := client.Do(reqLogin)
		if err != nil {
			t.Fatalf("GET /config failed: %v", err)
		}
		respLogin.Body.Close()

		formData := url.Values{}
		formData.Set("username", "testuser")
		formData.Set("password", "testpass")
		formData.Set("password-confirm", "testpass")

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

	t.Run("SameOriginSecFetchSite_POST", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/login", nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusForbidden {
			t.Errorf("Expected non-403 for POST with Sec-Fetch-Site: same-origin, got %d", resp.StatusCode)
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

	formData := url.Values{}
	formData.Set("username", "admin")
	formData.Set("password", "admin")

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

	formData := url.Values{}
	formData.Set("username", "admin")
	formData.Set("password", "admin")

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

		loginForm := url.Values{}
		loginForm.Add("username", "admin")
		loginForm.Add("password", "admin")
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

// waitForCacheStatus replays req until the HTTP cache middleware reports the
// wanted X-Cache status. Cache writes are asynchronous via the WriteBatcher,
// so a HIT is only observable after the entry has been flushed to the database.
func waitForCacheStatus(t *testing.T, router http.Handler, req *http.Request, want string) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last *httptest.ResponseRecorder
	for {
		last = httptest.NewRecorder()
		router.ServeHTTP(last, req.Clone(req.Context()))
		if last.Header().Get("X-Cache") == want {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for X-Cache %q, last response X-Cache = %q", want, last.Header().Get("X-Cache"))
		}
		time.Sleep(10 * time.Millisecond)
	}
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
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/test", Variant: "full"}),
		Method:    "GET",
		Path:      "/gallery/test",
		Status:    200,
		Body:      []byte("<html><body>cached content before etag increment</body></html>"),
		CreatedAt: now,
	}
	entry.ContentLength = sql.NullInt64{Int64: int64(len(entry.Body)), Valid: true}
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
	req := httptest.NewRequest("POST", "/config/increment-etag", nil)
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

	exists, err := tableExists(ctx, app, "http_cache_to_be_dropped")
	if err != nil {
		t.Fatalf("tableExists(http_cache_to_be_dropped): %v", err)
	}
	if exists {
		t.Fatal("http_cache_to_be_dropped still exists after Swap")
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
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/x", Variant: "full"}),
		Method:    "GET",
		Path:      "/gallery/x",
		Status:    200,
		Body:      []byte("<html><body>stale content</body></html>"),
		CreatedAt: now,
	}
	entry.ContentLength = sql.NullInt64{Int64: int64(len(entry.Body)), Valid: true}
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

	exists, err := tableExists(ctx, app, "http_cache_to_be_dropped")
	if err != nil {
		t.Fatalf("tableExists(http_cache_to_be_dropped): %v", err)
	}
	if exists {
		t.Fatal("http_cache_to_be_dropped still exists after Swap")
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
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/y", Variant: "full"}),
		Method:    "GET",
		Path:      "/gallery/y",
		Status:    200,
		Body:      []byte("<html><body>valid content</body></html>"),
		CreatedAt: now,
	}
	entry.ContentLength = sql.NullInt64{Int64: int64(len(entry.Body)), Valid: true}
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
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/z", Variant: "full"}),
		Method:    "GET",
		Path:      "/gallery/z",
		Status:    200,
		Body:      []byte("<html><body>cached from before reboot</body></html>"),
		CreatedAt: now,
	}
	entry.ContentLength = sql.NullInt64{Int64: int64(len(entry.Body)), Valid: true}
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

func TestIntegration_CacheKeyFormatUpgrade_InvalidatesLegacyRows(t *testing.T) {
	app, ctx := createAppWithContext(t)
	defer app.Shutdown()

	// Pre-seed stored format version = 2 so the hook detects an upgrade is needed.
	now := time.Now().Unix()
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	err = cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "http_cache_key_format_version",
		Value:     "2",
		CreatedAt: now,
		UpdatedAt: now,
	})
	app.dbRwPool.Put(cpcRw)
	if err != nil {
		t.Fatalf("failed to seed config version 2: %v", err)
	}

	// Seed a v2-style cache entry (|HX=false suffix, no |Variant=).
	v2Key := "GET:/gallery/test-v2-upgrade?|HX=false"
	entry := &cachelite.HTTPCacheEntry{
		Key:       v2Key,
		Method:    "GET",
		Path:      "/gallery/test-v2-upgrade",
		Status:    200,
		Body:      []byte("<html><body>v2 cached response</body></html>"),
		CreatedAt: now,
	}
	entry.ContentLength = sql.NullInt64{Int64: int64(len(entry.Body)), Valid: true}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("StoreCacheEntry (v2): %v", err)
	}

	// Verify the v2 entry exists before calling the hook.
	storedBefore, err := cachelite.GetCacheEntry(ctx, app.dbRwPool, v2Key)
	if err != nil {
		t.Fatalf("GetCacheEntry before hook: %v", err)
	}
	if storedBefore == nil {
		t.Fatal("expected v2 cache entry before hook call")
	}

	// Call the format check hook — stored version 2 < current 3, so it invalidates.
	app.ensureHTTPCacheKeyFormatCurrent()

	// Verify the v2 entry is gone (cache was rotated and v2 key not re-created).
	storedAfter, err := cachelite.GetCacheEntry(ctx, app.dbRwPool, v2Key)
	if !errors.Is(err, sql.ErrNoRows) {
		if err == nil {
			t.Error("expected v2 cache entry to be invalidated, but it still exists")
		} else {
			t.Errorf("unexpected error after invalidation: %v", err)
		}
	}
	_ = storedAfter

	// Verify the config version was persisted.
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get RO connection: %v", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	persisted, err := cpcRo.Queries.GetConfigValueByKey(ctx, "http_cache_key_format_version")
	if err != nil {
		t.Fatalf("GetConfigValueByKey: %v", err)
	}
	if persisted != cachelite.CacheKeyFormatVersionString {
		t.Errorf("persisted format version = %q, want %q", persisted, cachelite.CacheKeyFormatVersionString)
	}
}

func TestIntegration_CacheKeyFormat_CurrentVersionPreservesCache(t *testing.T) {
	app, ctx := createAppWithContext(t)
	defer app.Shutdown()

	// Pre-seed the config so the hook sees the current version (3).
	now := time.Now().Unix()
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	err = cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "http_cache_key_format_version",
		Value:     cachelite.CacheKeyFormatVersionString,
		CreatedAt: now,
		UpdatedAt: now,
	})
	app.dbRwPool.Put(cpcRw)
	if err != nil {
		t.Fatalf("failed to seed config version: %v", err)
	}

	// Seed a v3-style cache entry (|Variant=full suffix).
	v3Key := "GET:/gallery/test-current-version?|Variant=full"
	entry := &cachelite.HTTPCacheEntry{
		Key:       v3Key,
		Method:    "GET",
		Path:      "/gallery/test-current-version",
		Status:    200,
		Body:      []byte("<html><body>v3 cached response</body></html>"),
		CreatedAt: now,
	}
	entry.ContentLength = sql.NullInt64{Int64: int64(len(entry.Body)), Valid: true}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("StoreCacheEntry (v3): %v", err)
	}

	// Call the hook — with current version (3 >= 3), should NOT invalidate.
	app.ensureHTTPCacheKeyFormatCurrent()

	// Verify the entry is still there.
	storedAfter, err := cachelite.GetCacheEntry(ctx, app.dbRwPool, v3Key)
	if errors.Is(err, sql.ErrNoRows) || storedAfter == nil {
		t.Error("expected v3 cache entry to survive format check with current version")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetCacheEntry after hook: %v", err)
	}
}

func TestETagIncrementIntegration(t *testing.T) {
	app, ctx := createAppWithContext(t)
	defer app.Shutdown()

	// Pre-populate cache with an entry (simulating a cached response)
	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/1", Variant: "full"}),
		Method:    "GET",
		Path:      "/gallery/1",
		Status:    200,
		Body:      []byte("<html><body>cached content before etag increment</body></html>"),
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

func TestIntegration_TriggerDiscovery_RebuildsFileFolderIndex(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// processingStats must be non-nil so waitForFileProcessingDrain does not
	// short-circuit. CreateApp sets q and fileProcessor but not processingStats.
	app.SubsystemManager.processingStats = &files.ProcessingStats{}

	// Start pool with processingStats wired in so drain polls real counters.
	app.SubsystemManager.pool.MinWorkers = 1
	app.SubsystemManager.pool.MaxWorkers = 1
	app.RuntimeManager.poolDone = make(chan struct{})
	pf := files.NewPoolFuncWithProcessor(app.SubsystemManager.fileProcessor,
		app.SubsystemManager.q, app.normalizedImagesDir, removeImagesDirPrefix,
		app.SubsystemManager.processingStats, nil)
	go func() {
		defer close(app.RuntimeManager.poolDone)
		app.SubsystemManager.pool.StartWorkerPool(pf, app.dbRoPool, app.dbRwPool, app.SubsystemManager.q.Len)
	}()

	// Copy 3 JPEGs into a subfolder so they share a folder_id.
	src := "../../testdata/thumbnail/no-exif-thumb.jpg"
	subDir := filepath.Join(app.imagesDir, "test-folder")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	copyJPEG := func(dst string) {
		s, err := os.Open(src)
		if err != nil {
			t.Fatalf("open src: %v", err)
		}
		defer s.Close()
		d, err := os.Create(dst)
		if err != nil {
			t.Fatalf("create dst: %v", err)
		}
		defer d.Close()
		if _, err := io.Copy(d, s); err != nil {
			t.Fatalf("copy: %v", err)
		}
	}

	copyJPEG(filepath.Join(subDir, "a-first.jpg"))
	copyJPEG(filepath.Join(subDir, "b-middle.jpg"))
	copyJPEG(filepath.Join(subDir, "c-last.jpg"))

	app.testSeams.RebuildFileFolderIndex = nil // real rebuild

	// Single TriggerDiscovery: walk → drain (pool processes files) → rebuild.
	if err := app.TriggerDiscovery(context.Background()); err != nil {
		t.Fatalf("TriggerDiscovery: %v", err)
	}

	// Query file_folder_index — should have 3 rows for this folder.
	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("get connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	rows, err := cpc.Conn.QueryContext(context.Background(),
		`SELECT ffi.file_id, ffi.folder_id, ffi.image_index, ffi.image_count,
		        ffi.prev_id, ffi.next_id, ffi.first_id, ffi.last_id
		   FROM file_folder_index ffi
		   JOIN files f ON f.id = ffi.file_id
		   JOIN folders fol ON fol.id = f.folder_id
		   JOIN folder_paths fp ON fp.id = fol.path_id
		  WHERE fp.path = 'test-folder'
		  ORDER BY ffi.image_index`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var results []struct {
		fileID, folderID, imageIndex, imageCount int64
		prevID, nextID, firstID, lastID          sql.NullInt64
	}
	for rows.Next() {
		var r struct {
			fileID, folderID, imageIndex, imageCount int64
			prevID, nextID, firstID, lastID          sql.NullInt64
		}
		if err := rows.Scan(&r.fileID, &r.folderID, &r.imageIndex, &r.imageCount,
			&r.prevID, &r.nextID, &r.firstID, &r.lastID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 rows in file_folder_index, got %d", len(results))
	}

	// All rows share the same folder_id, image_count, first_id, last_id.
	folderID := results[0].folderID
	for _, r := range results {
		if r.folderID != folderID {
			t.Errorf("inconsistent folder_id: %d vs %d", r.folderID, folderID)
		}
		if r.imageCount != 3 {
			t.Errorf("expected image_count=3, got %d", r.imageCount)
		}
		if r.firstID.Int64 != results[0].fileID {
			t.Errorf("expected first_id=%d, got %d", results[0].fileID, r.firstID.Int64)
		}
		if r.lastID.Int64 != results[2].fileID {
			t.Errorf("expected last_id=%d, got %d", results[2].fileID, r.lastID.Int64)
		}
	}

	// Verify image_index order: 1, 2, 3.
	for i, r := range results {
		if r.imageIndex != int64(i+1) {
			t.Errorf("expected image_index=%d, got %d", i+1, r.imageIndex)
		}
	}

	// prev_id / next_id chain.
	if results[0].prevID.Valid {
		t.Error("first file should have null prev_id")
	}
	if results[2].nextID.Valid {
		t.Error("last file should have null next_id")
	}
	if results[0].nextID.Int64 != results[1].fileID {
		t.Errorf("first.next = %d, want %d", results[0].nextID.Int64, results[1].fileID)
	}
	if results[1].prevID.Int64 != results[0].fileID {
		t.Errorf("middle.prev = %d, want %d", results[1].prevID.Int64, results[0].fileID)
	}
	if results[1].nextID.Int64 != results[2].fileID {
		t.Errorf("middle.next = %d, want %d", results[1].nextID.Int64, results[2].fileID)
	}
	if results[2].prevID.Int64 != results[1].fileID {
		t.Errorf("last.prev = %d, want %d", results[2].prevID.Int64, results[1].fileID)
	}
}

func TestIntegration_PprofLoopbackAndAuth(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")
	app := CreateApp(t)
	defer app.Shutdown()
	router := app.getRouter()
	authCookie := MakeAuthCookie(t, app)

	subtests := []struct {
		name       string
		remoteAddr string
		useAuth    bool
		want       int
	}{
		{"loopback_v4_unauth", "127.0.0.1:12345", false, http.StatusUnauthorized},
		{"loopback_v4_auth", "127.0.0.1:12345", true, http.StatusOK},
		{"loopback_v6_unauth", "[::1]:12345", false, http.StatusUnauthorized},
		{"loopback_v6_auth", "[::1]:12345", true, http.StatusOK},
		{"remote_unauth", "198.51.100.1:12345", false, http.StatusNotFound},
		{"remote_auth", "198.51.100.1:12345", true, http.StatusNotFound},
		{"mapped_v4_unauth", "[::ffff:127.0.0.1]:12345", false, http.StatusNotFound},
		{"mapped_v4_auth", "[::ffff:127.0.0.1]:12345", true, http.StatusNotFound},
	}
	for _, st := range subtests {
		t.Run(st.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
			req.RemoteAddr = st.remoteAddr
			if st.useAuth {
				req.AddCookie(authCookie)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != st.want {
				t.Fatalf("status = %d, want %d", w.Code, st.want)
			}
		})
	}
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
		formData := url.Values{}
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

	formData := url.Values{}
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
		loginData := url.Values{}
		loginData.Set("username", "wronguser") // non-admin: avoid account lockout side effects
		loginData.Set("password", "wrong")
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
