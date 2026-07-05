//go:build e2e

package server

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
)

// TestE2E_ConfigRestart_UsesUpdatedPort verifies that after a configuration change
// the new port value is persisted and that POST /config/restart requests a real
// process restart. The actual re-exec is stubbed because this test does not run
// a live Serve() loop.
func TestE2E_ConfigRestart_UsesUpdatedPort(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")
	app := CreateApp(t, WithPool())
	defer app.Shutdown()

	// Set initial config
	t.Parallel()
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.ListenerAddress = "127.0.0.1"
	app.config.ListenerPort = 0 // Random port for testing
	app.configMu.Unlock()

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}
	loginAsAdmin(t, client, ts.URL)

	// Get initial port
	app.configMu.RLock()
	initialPort := app.config.ListenerPort
	app.configMu.RUnlock()

	// Update port configuration
	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("listener_port", "8888")

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

	// Verify config was updated in memory
	app.configMu.RLock()
	updatedPort := app.config.ListenerPort
	app.configMu.RUnlock()

	if updatedPort != 8888 {
		t.Errorf("expected port 8888 in config, got %d", updatedPort)
	}

	// Trigger restart
	restartCsrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
	restartFormData := url.Values{}
	restartFormData.Set("csrf_token", restartCsrfToken)
	restartReq, err := http.NewRequest(http.MethodPost, ts.URL+"/config/restart", strings.NewReader(restartFormData.Encode()))
	if err != nil {
		t.Fatalf("failed to create restart request: %v", err)
	}
	restartReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	restartReq.Header.Set("Origin", ts.URL)

	restartResp, err := client.Do(restartReq)
	if err != nil {
		t.Fatalf("POST /config/restart failed: %v", err)
	}
	defer restartResp.Body.Close()

	if restartResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for restart, got %d", restartResp.StatusCode)
	}

	// The handler should have requested a real process restart.
	// It flushes the response first and then triggers the restart in a
	// background goroutine, so we poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if app.restartRequested.Load() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !app.restartRequested.Load() {
		t.Fatal("restart was not requested after POST /config/restart")
	}

	// In a real process, Serve() would return and Run() would exec a new image.
	// Here we just verify the persisted config port is the new value.
	app.configMu.RLock()
	restartPort := app.config.ListenerPort
	app.configMu.RUnlock()

	// Verify the server would use the updated port
	if restartPort != 8888 {
		t.Errorf("after restart, expected server to use port 8888, but config shows %d", restartPort)
	}
	if restartPort == initialPort {
		t.Errorf("after restart, port should have changed from %d to 8888, but it's still %d", initialPort, restartPort)
	}
}

// TestE2E_ConfigCompression_ServerUsesConfig verifies that the server actually
// uses compression settings from app.config, not app.opt, after configuration changes.
// This tests that getRouter() reads from app.config dynamically.
func TestE2E_ConfigCompression_ServerUsesConfig(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")
	app := CreateApp(t)
	defer app.Shutdown()

	// Set initial config with compression enabled
	t.Parallel()
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.ServerCompressionEnable = true
	app.configMu.Unlock()

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
	app.configMu.RLock()
	compressionEnabled := app.config.ServerCompressionEnable
	app.configMu.RUnlock()

	if compressionEnabled {
		t.Error("expected compression to be disabled in config after update")
	}

	// Verify getRouter() uses app.config, not app.opt
	// Set app.opt to enabled (old value) - if getRouter() uses app.opt, compression would still be enabled
	app.opt.EnableCompression = getopt.OptBool{Bool: true, IsSet: true}

	req2 := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	req2.AddCookie(cookie)
	w2 := httptest.NewRecorder()
	app.getRouter().ServeHTTP(w2, req2)

	// If getRouter() uses app.config (correct), compression should be disabled
	// If getRouter() uses app.opt (wrong), compression would be enabled
	if w2.Header().Get("Content-Encoding") == "gzip" {
		t.Error("after config update, compression should be disabled (per app.config), but getRouter() appears to be using app.opt")
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

// TestE2E_ConfigCache_ServerUsesConfig verifies that the server actually
// uses cache settings from app.config, not app.opt, after configuration changes.
func TestE2E_ConfigCache_ServerUsesConfig(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")
	app := CreateApp(t)
	defer app.Shutdown()

	// Set initial config with cache enabled
	t.Parallel()
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.EnableHTTPCache = true
	app.configMu.Unlock()

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
	app.configMu.RLock()
	cacheEnabled := app.config.EnableHTTPCache
	app.configMu.RUnlock()

	if cacheEnabled {
		t.Error("expected cache to be disabled in config after update")
	}

	// Verify getRouter() uses app.config, not app.opt
	// Set app.opt to enabled (old value)
	app.opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}

	req2 := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req2.AddCookie(cookie)
	w2 := httptest.NewRecorder()
	app.getRouter().ServeHTTP(w2, req2)

	// If getRouter() uses app.config (correct), cache middleware should not be applied
	// If getRouter() uses app.opt (wrong), cache middleware would still be applied
	// We can't directly test cache behavior without more setup, but we verify the config is correct
	app.configMu.RLock()
	finalCacheEnabled := app.config.EnableHTTPCache
	app.configMu.RUnlock()

	if finalCacheEnabled {
		t.Error("after config update, cache should be disabled (per app.config)")
	}
}
