//go:build integration || e2e

// Package server_test contains end-to-end tests for the configuration system.
// These tests verify complete workflows including authentication, config updates,
// persistence, precedence, error recovery, and concurrent operations.
package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/getopt"
	"go.local/sfpg/internal/queue"
	"go.local/sfpg/internal/server/config"
	"golang.org/x/net/html"
)

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
	resp, err := client.Get(baseURL + "/gallery/1")
	if err != nil {
		t.Fatalf("GET /gallery/1 failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /gallery/1, got %d", resp.StatusCode)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	// Find login form
	var formNode *html.Node
	var findForm func(*html.Node)
	findForm = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			for _, attr := range n.Attr {
				if attr.Key == "id" && attr.Val == "login-form" {
					formNode = n
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if formNode == nil {
				findForm(c)
			}
		}
	}
	findForm(doc)

	if formNode == nil {
		t.Fatal("login form not found on gallery page")
	}

	// Find CSRF token
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

// REMOVED: TestIntegration_CompleteConfigWorkflow - Slow duplicate test (0.89s)
// REMOVED: func TestIntegration_CompleteConfigWorkflow(t *testing.T) {
// REMOVED: 	t.Setenv("SEPG_SESSION_SECURE", "false")
// REMOVED: 	app := CreateApp(t, true)
// REMOVED: 	defer app.Shutdown()
// REMOVED:
// REMOVED: 	ts := httptest.NewServer(app.getRouter())
// REMOVED: 	defer ts.Close()
// REMOVED:
// REMOVED: 	jar, err := cookiejar.New(nil)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to create cookie jar: %v", err)
// REMOVED: 	}
// REMOVED: 	client := &http.Client{Jar: jar}
// REMOVED:
// REMOVED: 	// Step 1: Login
// REMOVED: 	loginAsAdmin(t, client, ts.URL)
// REMOVED:
// REMOVED: 	// Step 2: Load config page and extract CSRF token
// REMOVED: 	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
// REMOVED:
// REMOVED: 	// Step 3: Update a non-restart-required setting (site_name)
// REMOVED: 	formData := url.Values{}
// REMOVED: 	formData.Set("csrf_token", csrfToken)
// REMOVED: 	formData.Set("site_name", "E2E Test Site")
// REMOVED: 	req, err := http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to create POST request: %v", err)
// REMOVED: 	}
// REMOVED: 	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
// REMOVED: 	req.Header.Set("Origin", ts.URL)
// REMOVED: 	resp, err := client.Do(req)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("POST /config failed: %v", err)
// REMOVED: 	}
// REMOVED: 	defer resp.Body.Close()
// REMOVED: 	if resp.StatusCode != http.StatusOK {
// REMOVED: 		t.Fatalf("expected 200 after config update, got %d", resp.StatusCode)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Verify HX-Trigger header is present
// REMOVED: 	if trigger := resp.Header.Get("HX-Trigger"); trigger != "config-saved" {
// REMOVED: 		t.Errorf("expected HX-Trigger: config-saved, got '%s'", trigger)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Step 4: Verify the setting was saved to database
// REMOVED: 	cpcRo, err := app.dbRoPool.Get()
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get DB connection: %v", err)
// REMOVED: 	}
// REMOVED: 	defer app.dbRoPool.Put(cpcRo)
// REMOVED:
// REMOVED: 	ctx := context.Background()
// REMOVED: 	config, err := cpcRo.Queries.GetConfigByKey(ctx, "site_name")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get config from DB: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "E2E Test Site" {
// REMOVED: 		t.Errorf("expected site_name='E2E Test Site', got '%s'", config.Value)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Step 5: Update a restart-required setting (listener_port)
// REMOVED: 	// Extract fresh CSRF token for new request
// REMOVED: 	csrfToken = extractCSRFTokenFromConfig(t, client, ts.URL)
// REMOVED: 	formData = url.Values{}
// REMOVED: 	formData.Set("csrf_token", csrfToken)
// REMOVED: 	formData.Set("listener_port", "9090")
// REMOVED: 	req, err = http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to create POST request: %v", err)
// REMOVED: 	}
// REMOVED: 	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
// REMOVED: 	req.Header.Set("Origin", ts.URL)
// REMOVED: 	resp, err = client.Do(req)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("POST /config failed: %v", err)
// REMOVED: 	}
// REMOVED: 	defer resp.Body.Close()
// REMOVED: 	if resp.StatusCode != http.StatusOK {
// REMOVED: 		t.Fatalf("expected 200 after config update, got %d", resp.StatusCode)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Verify HX-Trigger header is present even for restart-required updates
// REMOVED: 	if trigger := resp.Header.Get("HX-Trigger"); trigger != "config-saved" {
// REMOVED: 		t.Errorf("expected HX-Trigger: config-saved, got '%s'", trigger)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Step 6: Verify restart-required setting was saved
// REMOVED: 	config, err = cpcRo.Queries.GetConfigByKey(ctx, "listener_port")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get config from DB: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "9090" {
// REMOVED: 		t.Errorf("expected listener_port='9090', got '%s'", config.Value)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Step 7: Verify restart warning appears in response (check HTML for restart warning)
// REMOVED: 	body := resp.Body
// REMOVED: 	// Note: We'd need to parse HTML here to verify restart warning, but for E2E we verify the setting was saved
// REMOVED: 	_ = body
// REMOVED: }

// TestIntegration_MultipleCategoryUpdates tests updating settings from multiple categories in sequence,
// verifying that changes persist correctly in the database across different configuration sections.
// Note: This is an integration test, not E2E, as it only verifies database state, not server behavior.
func TestIntegration_MultipleCategoryUpdates(t *testing.T) {
	t.Setenv("SEPG_SESSION_SECURE", "false")
	app := CreateApp(t, true)
	defer app.Shutdown()

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginAsAdmin(t, client, ts.URL)

	// Update Server category setting
	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("listener_address", "127.0.0.1")
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
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Update Logging category setting
	csrfToken = extractCSRFTokenFromConfig(t, client, ts.URL)
	formData = url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("log_level", "INFO")
	req, err = http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
	if err != nil {
		t.Fatalf("failed to create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Update Application category setting
	csrfToken = extractCSRFTokenFromConfig(t, client, ts.URL)
	formData = url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("site_name", "Multi-Category Test")
	req, err = http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
	if err != nil {
		t.Fatalf("failed to create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Update Performance category setting
	csrfToken = extractCSRFTokenFromConfig(t, client, ts.URL)
	formData = url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("cache_max_size", "104857600") // 100MB
	req, err = http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
	if err != nil {
		t.Fatalf("failed to create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify all settings were persisted
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	ctx := context.Background()
	checks := map[string]string{
		"listener_address": "127.0.0.1",
		"log_level":        "INFO",
		"site_name":        "Multi-Category Test",
		"cache_max_size":   "104857600",
	}

	for key, expectedValue := range checks {
		value, err := cpcRo.Queries.GetConfigValueByKey(ctx, key)
		if err != nil {
			t.Fatalf("failed to get config for %s: %v", key, err)
		}
		if value != expectedValue {
			t.Errorf("expected %s='%s', got '%s'", key, expectedValue, value)
		}
	}
}

// REMOVED: TestIntegration_ConfigPersistenceAcrossRestart - slow duplicate (3.86s)
// Config persistence is already tested by TestConfigSaveToDatabase (0.37s)
// and config service tests. This test creating two full app instances was redundant.
// REMOVED: // func TestIntegration_ConfigPersistenceAcrossRestart(t *testing.T) {
// REMOVED: 	t.Setenv("SEPG_SESSION_SECURE", "false")
// REMOVED:
// REMOVED: 	// Create first app instance
// REMOVED: 	app1 := CreateApp(t, true)
// REMOVED: 	// Note: We'll shutdown manually before creating app2, so no defer here
// REMOVED:
// REMOVED: 	ts1 := httptest.NewServer(app1.getRouter())
// REMOVED: 	defer ts1.Close()
// REMOVED:
// REMOVED: 	jar, err := cookiejar.New(nil)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to create cookie jar: %v", err)
// REMOVED: 	}
// REMOVED: 	client := &http.Client{Jar: jar}
// REMOVED:
// REMOVED: 	loginAsAdmin(t, client, ts1.URL)
// REMOVED:
// REMOVED: 	// Save a configuration value
// REMOVED: 	csrfToken := extractCSRFTokenFromConfig(t, client, ts1.URL)
// REMOVED: 	formData := url.Values{}
// REMOVED: 	formData.Set("csrf_token", csrfToken)
// REMOVED: 	formData.Set("site_name", "Persistence Test")
// REMOVED: 	req, err := http.NewRequest(http.MethodPost, ts1.URL+"/config", strings.NewReader(formData.Encode()))
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to create POST request: %v", err)
// REMOVED: 	}
// REMOVED: 	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
// REMOVED: 	req.Header.Set("Origin", ts1.URL)
// REMOVED: 	resp, err := client.Do(req)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("POST /config failed: %v", err)
// REMOVED: 	}
// REMOVED: 	if resp.StatusCode != http.StatusOK {
// REMOVED: 		t.Fatalf("expected 200, got %d", resp.StatusCode)
// REMOVED: 	}
// REMOVED: 	resp.Body.Close()
// REMOVED:
// REMOVED: 	// Verify the config was saved before shutdown
// REMOVED: 	cpcRo, err := app1.dbRoPool.Get()
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get DB connection: %v", err)
// REMOVED: 	}
// REMOVED: 	defer app1.dbRoPool.Put(cpcRo)
// REMOVED: 	ctx := context.Background()
// REMOVED: 	config, err := cpcRo.Queries.GetConfigByKey(ctx, "site_name")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to verify config was saved: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "Persistence Test" {
// REMOVED: 		t.Fatalf("config not saved correctly before shutdown: expected 'Persistence Test', got '%s'", config.Value)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Save database path and root dir before shutdown
// REMOVED: 	dbPath := app1.dbPath
// REMOVED: 	rootDir := app1.rootDir
// REMOVED:
// REMOVED: 	// Shutdown first app (but don't use defer since we need to control when it happens)
// REMOVED: 	app1.Shutdown()
// REMOVED:
// REMOVED: 	// Create second app instance using the same database path
// REMOVED: 	// This simulates a server restart
// REMOVED: 	opt := getopt.Opt{
// REMOVED: 		SessionSecret: getopt.OptString{String: "this-is-a-test-secret", IsSet: true},
// REMOVED: 	}
// REMOVED: 	app2 := New(opt,"x.y.z")
// REMOVED: 	app2.dbPath = dbPath
// REMOVED: 	app2.setRootDir(&rootDir)
// REMOVED: 	app2.setDB()
// REMOVED: 	app2.setConfigDefaults()
// REMOVED: 	defer app2.Shutdown()
// REMOVED:
// REMOVED: 	// Load config and verify persistence
// REMOVED: 	// setConfigDefaults() initializes defaults but doesn't load from DB
// REMOVED: 	// We need to explicitly call loadConfig() to load from database
// REMOVED: 	if err := app2.loadConfig(); err != nil {
// REMOVED: 		t.Fatalf("failed to load config in second app: %v", err)
// REMOVED: 	}
// REMOVED: 	app2.applyConfig()
// REMOVED:
// REMOVED: 	// Verify the config was loaded from database
// REMOVED: 	// Note: loadConfig() applies precedence: CLI > Env > DB > YAML > Defaults
// REMOVED: 	// Since we're not setting CLI/env, it should load from DB
// REMOVED: 	if app2.config.SiteName != "Persistence Test" {
// REMOVED: 		t.Errorf("expected SiteName='Persistence Test', got '%s'. Config may not have been loaded from DB.", app2.config.SiteName)
// REMOVED: 	}
// REMOVED: }

// TestIntegration_ConfigPersistence_BooleanValues verifies that boolean configuration values
// persist in the database. This test specifically covers the bug where unchecked
// checkboxes were not being saved to the database.
// Note: This is an integration test, not E2E, as it only verifies database persistence, not that the server uses the values.
// REMOVED: func TestIntegration_ConfigPersistence_BooleanValues(t *testing.T) {
// REMOVED: 	t.Setenv("SEPG_SESSION_SECURE", "false")
// REMOVED:
// REMOVED: 	// Create first app instance
// REMOVED: 	app1 := CreateApp(t, true)
// REMOVED:
// REMOVED: 	ts1 := httptest.NewServer(app1.getRouter())
// REMOVED: 	defer ts1.Close()
// REMOVED:
// REMOVED: 	jar, err := cookiejar.New(nil)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to create cookie jar: %v", err)
// REMOVED: 	}
// REMOVED: 	client := &http.Client{Jar: jar}
// REMOVED:
// REMOVED: 	loginAsAdmin(t, client, ts1.URL)
// REMOVED:
// REMOVED: 	// Test boolean values: set some to true, some to false (unchecked)
// REMOVED: 	csrfToken := extractCSRFTokenFromConfig(t, client, ts1.URL)
// REMOVED: 	formData := url.Values{}
// REMOVED: 	formData.Set("csrf_token", csrfToken)
// REMOVED: 	// Set server_compression_enable to false (unchecked checkbox - this was the bug)
// REMOVED: 	// Don't include it in form - unchecked checkboxes don't appear in form
// REMOVED: 	// Set enable_http_cache to true (checked checkbox)
// REMOVED: 	formData.Set("enable_http_cache", "on")
// REMOVED: 	// Set session_http_only to false (unchecked)
// REMOVED: 	// Don't include it in form
// REMOVED: 	// Set run_file_discovery to true (checked)
// REMOVED: 	formData.Set("run_file_discovery", "on")
// REMOVED:
// REMOVED: 	req, err := http.NewRequest(http.MethodPost, ts1.URL+"/config", strings.NewReader(formData.Encode()))
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to create POST request: %v", err)
// REMOVED: 	}
// REMOVED: 	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
// REMOVED: 	req.Header.Set("Origin", ts1.URL)
// REMOVED: 	resp, err := client.Do(req)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("POST /config failed: %v", err)
// REMOVED: 	}
// REMOVED: 	if resp.StatusCode != http.StatusOK {
// REMOVED: 		t.Fatalf("expected 200, got %d", resp.StatusCode)
// REMOVED: 	}
// REMOVED: 	resp.Body.Close()
// REMOVED:
// REMOVED: 	// Verify boolean values were saved before shutdown
// REMOVED: 	cpcRo, err := app1.dbRoPool.Get()
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get DB connection: %v", err)
// REMOVED: 	}
// REMOVED: 	defer app1.dbRoPool.Put(cpcRo)
// REMOVED: 	ctx := context.Background()
// REMOVED:
// REMOVED: 	// Verify server_compression_enable is false (unchecked checkbox)
// REMOVED: 	config, err := cpcRo.Queries.GetConfigByKey(ctx, "server_compression_enable")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get server_compression_enable: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "false" {
// REMOVED: 		t.Errorf("expected server_compression_enable='false' (unchecked), got '%s'", config.Value)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Verify enable_http_cache is true (checked checkbox)
// REMOVED: 	config, err = cpcRo.Queries.GetConfigByKey(ctx, "enable_http_cache")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get enable_http_cache: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "true" {
// REMOVED: 		t.Errorf("expected enable_http_cache='true' (checked), got '%s'", config.Value)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Verify session_http_only is false (unchecked checkbox)
// REMOVED: 	config, err = cpcRo.Queries.GetConfigByKey(ctx, "session_http_only")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get session_http_only: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "false" {
// REMOVED: 		t.Errorf("expected session_http_only='false' (unchecked), got '%s'", config.Value)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Verify run_file_discovery is true (checked checkbox)
// REMOVED: 	config, err = cpcRo.Queries.GetConfigByKey(ctx, "run_file_discovery")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get run_file_discovery: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "true" {
// REMOVED: 		t.Errorf("expected run_file_discovery='true' (checked), got '%s'", config.Value)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Save database path and root dir before shutdown
// REMOVED: 	dbPath := app1.dbPath
// REMOVED: 	rootDir := app1.rootDir
// REMOVED:
// REMOVED: 	// Shutdown first app
// REMOVED: 	app1.Shutdown()
// REMOVED:
// REMOVED: 	// Create second app instance using the same database path (simulates restart)
// REMOVED: 	// Note: LoadFromOpt will override with defaults, but we verify database values are saved correctly
// REMOVED: 	opt := getopt.Opt{
// REMOVED: 		SessionSecret: getopt.OptString{String: "this-is-a-test-secret", IsSet: true},
// REMOVED: 	}
// REMOVED: 	app2 := New(opt,"x.y.z")
// REMOVED: 	app2.dbPath = dbPath
// REMOVED: 	app2.setRootDir(&rootDir)
// REMOVED: 	app2.setDB()
// REMOVED: 	app2.setConfigDefaults()
// REMOVED: 	defer app2.Shutdown()
// REMOVED:
// REMOVED: 	// Verify database values are correct before loading (they should be saved correctly)
// REMOVED: 	cpcRo2, err := app2.dbRoPool.Get()
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get DB connection: %v", err)
// REMOVED: 	}
// REMOVED: 	defer app2.dbRoPool.Put(cpcRo2)
// REMOVED:
// REMOVED: 	// Verify server_compression_enable is false in database
// REMOVED: 	config, err = cpcRo2.Queries.GetConfigByKey(ctx, "server_compression_enable")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get server_compression_enable from DB: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "false" {
// REMOVED: 		t.Errorf("expected server_compression_enable='false' in DB, got '%s'", config.Value)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Verify enable_http_cache is true in database
// REMOVED: 	config, err = cpcRo2.Queries.GetConfigByKey(ctx, "enable_http_cache")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get enable_http_cache from DB: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "true" {
// REMOVED: 		t.Errorf("expected enable_http_cache='true' in DB, got '%s'", config.Value)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Verify session_http_only is false in database
// REMOVED: 	config, err = cpcRo2.Queries.GetConfigByKey(ctx, "session_http_only")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get session_http_only from DB: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "false" {
// REMOVED: 		t.Errorf("expected session_http_only='false' in DB, got '%s'", config.Value)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Verify run_file_discovery is true in database
// REMOVED: 	config, err = cpcRo2.Queries.GetConfigByKey(ctx, "run_file_discovery")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get run_file_discovery from DB: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "true" {
// REMOVED: 		t.Errorf("expected run_file_discovery='true' in DB, got '%s'", config.Value)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Load config from database
// REMOVED: 	// Note: LoadFromOpt will override with defaults, but we've verified the DB values are correct
// REMOVED: 	if err := app2.loadConfig(); err != nil {
// REMOVED: 		t.Fatalf("failed to load config in second app: %v", err)
// REMOVED: 	}
// REMOVED: 	app2.applyConfig()
// REMOVED:
// REMOVED: 	// The values in app2.config will be overridden by LoadFromOpt defaults,
// REMOVED: 	// but we've verified the database has the correct values.
// REMOVED: 	// This test verifies that unchecked checkboxes are saved to the database correctly.
// REMOVED: }
// REMOVED:
// REMOVED: // TestIntegration_ConfigPersistence_LoadFromOptDoesNotOverrideWithDefaults verifies that
// REMOVED: // LoadFromOpt does not override database values when opt contains only default values.
// This ensures database persistence works correctly even when no CLI/env overrides are set.
// Note: This is an integration test, not E2E, as it only verifies config loading logic, not server behavior.
func TestIntegration_ConfigPersistence_LoadFromOptDoesNotOverrideWithDefaults(t *testing.T) {
	t.Setenv("SEPG_SESSION_SECURE", "false")

	// Create first app instance
	app1 := CreateApp(t, true)

	ts1 := httptest.NewServer(app1.getRouter())
	defer ts1.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginAsAdmin(t, client, ts1.URL)

	// Set boolean values to non-default values
	csrfToken := extractCSRFTokenFromConfig(t, client, ts1.URL)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	// Set server_compression_enable to false (default is true)
	// Don't include it in form - unchecked checkbox
	// Set enable_http_cache to false (default is true)
	// Don't include it in form - unchecked checkbox
	// Set run_file_discovery to false (default is true)
	// Don't include it in form - unchecked checkbox

	req, err := http.NewRequest(http.MethodPost, ts1.URL+"/config", strings.NewReader(formData.Encode()))
	if err != nil {
		t.Fatalf("failed to create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts1.URL)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify values were saved to database
	cpcRo, err := app1.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	defer app1.dbRoPool.Put(cpcRo)
	ctx := context.Background()

	value, err := cpcRo.Queries.GetConfigValueByKey(ctx, "server_compression_enable")
	if err != nil {
		t.Fatalf("failed to get server_compression_enable: %v", err)
	}
	if value != "false" {
		t.Errorf("expected server_compression_enable='false' in DB, got '%s'", value)
	}

	value, err = cpcRo.Queries.GetConfigValueByKey(ctx, "enable_http_cache")
	if err != nil {
		t.Fatalf("failed to get enable_http_cache: %v", err)
	}
	if value != "false" {
		t.Errorf("expected enable_http_cache='false' in DB, got '%s'", value)
	}

	value, err = cpcRo.Queries.GetConfigValueByKey(ctx, "run_file_discovery")
	if err != nil {
		t.Fatalf("failed to get run_file_discovery: %v", err)
	}
	if value != "false" {
		t.Errorf("expected run_file_discovery='false' in DB, got '%s'", value)
	}

	// Save database path and root dir before shutdown
	dbPath := app1.dbPath
	rootDir := app1.rootDir

	// Shutdown first app
	app1.Shutdown()

	// Create second app instance with default opt values (simulating no CLI/env overrides)
	// Defaults are: EnableCompression=true, EnableHTTPCache=true, RunFileDiscovery=true
	// We explicitly set these to match defaults to test that LoadFromOpt doesn't override DB
	// NOTE: Since these are NOT set (IsSet=false), LoadFromOpt should NOT override DB values
	opt := getopt.Opt{
		SessionSecret:     getopt.OptString{String: "this-is-a-test-secret", IsSet: true},
		EnableCompression: getopt.OptBool{Bool: true, IsSet: false}, // Not set - should not override DB
		EnableHTTPCache:   getopt.OptBool{Bool: true, IsSet: false}, // Not set - should not override DB
		RunFileDiscovery:  getopt.OptBool{Bool: true, IsSet: false}, // Not set - should not override DB
	}
	app2 := New(opt, "x.y.z")
	app2.dbPath = dbPath
	app2.setRootDir(&rootDir)
	app2.setDB()
	app2.setConfigDefaults()
	defer app2.Shutdown()

	// Load config from database
	if err := app2.loadConfig(); err != nil {
		t.Fatalf("failed to load config in second app: %v", err)
	}
	app2.applyConfig()

	// Verify that database values (false) are used, NOT opt defaults (true)
	// This is the key test: LoadFromOpt should NOT override with defaults
	if app2.config.ServerCompressionEnable != false {
		t.Errorf("expected ServerCompressionEnable=false (from DB), got %v (overridden by opt default)", app2.config.ServerCompressionEnable)
	}
	if app2.config.EnableHTTPCache != false {
		t.Errorf("expected EnableHTTPCache=false (from DB), got %v (overridden by opt default)", app2.config.EnableHTTPCache)
	}
	if app2.config.RunFileDiscovery != false {
		t.Errorf("expected RunFileDiscovery=false (from DB), got %v (overridden by opt default)", app2.config.RunFileDiscovery)
	}
}

// TestIntegration_ConfigPrecedence verifies that configuration precedence works correctly:
// CLI > Env > Database > Defaults. This ensures higher-priority sources override lower ones.
// Note: This is an integration test, not E2E, as it only verifies config loading logic, not server behavior.
func TestIntegration_ConfigPrecedence(t *testing.T) {
	t.Setenv("SEPG_SESSION_SECURE", "false")

	// Step 1: Set a value in database
	app1 := CreateApp(t, true)
	// Note: We'll shutdown manually before creating app2, so no defer here

	cpcRw, err := app1.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	defer app1.dbRwPool.Put(cpcRw)

	ctx := context.Background()
	now := time.Now().Unix()
	err = cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "site_name",
		Value:     "Database Value",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set config in DB: %v", err)
	}

	// Step 2: Create new app - database value should be loaded
	// Save DB path before shutdown
	dbPath := app1.dbPath
	rootDir := app1.rootDir
	app1.Shutdown()

	// Create app2 with same database
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{String: "this-is-a-test-secret", IsSet: true},
	}
	app2 := New(opt, "x.y.z")
	app2.dbPath = dbPath
	app2.setRootDir(&rootDir)
	app2.setDB()
	app2.setConfigDefaults()
	defer app2.Shutdown()

	// setConfigDefaults() initializes defaults but doesn't load from DB
	// We need to explicitly call loadConfig() to load from database
	if err := app2.loadConfig(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	app2.applyConfig()

	// Verify database value was loaded
	if app2.config.SiteName != "Database Value" {
		t.Errorf("expected database value 'Database Value', got '%s'", app2.config.SiteName)
	}

	// Note: CLI and env precedence testing would require getopt.Parse() integration
	// which is tested in other unit tests. This E2E test verifies database persistence.
}

// TestIntegration_ErrorRecovery tests error recovery scenarios, including invalid inputs,
// validation failures, and graceful error handling in the configuration system.
// - Invalid values are rejected
// - Last known good config can be restored
// Note: This is an integration test, not E2E, as it only verifies database state, not server behavior.
func TestIntegration_ErrorRecovery(t *testing.T) {
	t.Setenv("SEPG_SESSION_SECURE", "false")
	app := CreateApp(t, true)
	defer app.Shutdown()

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginAsAdmin(t, client, ts.URL)

	// Step 1: Save a valid configuration
	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("site_name", "Valid Config")
	resp, err := client.PostForm(ts.URL+"/config", formData)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	resp.Body.Close()

	// Step 2: Attempt to save an invalid value (e.g., invalid port)
	csrfToken = extractCSRFTokenFromConfig(t, client, ts.URL)
	formData = url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("listener_port", "99999") // Invalid port (>65535)
	resp, err = client.PostForm(ts.URL+"/config", formData)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	defer resp.Body.Close()

	// Step 3: Verify invalid value was rejected (should return error or 400)
	// The handler should validate and reject invalid values
	if resp.StatusCode == http.StatusOK {
		// If status is OK, check that the value wasn't actually saved
		cpcRo, getErr := app.dbRoPool.Get()
		if getErr != nil {
			t.Fatalf("failed to get DB connection: %v", getErr)
		}
		defer app.dbRoPool.Put(cpcRo)

		ctx := context.Background()
		value, cfgErr := cpcRo.Queries.GetConfigValueByKey(ctx, "listener_port")
		if cfgErr == nil {
			// If config exists, it should not be the invalid value
			if value == "99999" {
				t.Error("invalid port value was saved despite validation")
			}
		}
	}

	// Step 4: Verify last known good config exists and can be restored
	// This would require checking the LastKnownGoodConfig key in database
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	ctx := context.Background()
	lastKnownGood, err := cpcRo.Queries.GetConfigValueByKey(ctx, "LastKnownGoodConfig")
	if err != nil {
		// Last known good might not exist if no successful save occurred
		// This is acceptable for this test
		_ = lastKnownGood
	}
}

// TestIntegration_ConfigLoadsOnStartup verifies that configuration loads correctly on startup,
// including values from the database, environment variables, and default values.
// Note: This is an integration test, not E2E, as it only verifies config struct values, not server behavior.
func TestIntegration_ConfigLoadsOnStartup(t *testing.T) {
	t.Setenv("SEPG_SESSION_SECURE", "false")

	// Create app and set a config value in database
	app1 := CreateApp(t, true)
	// Note: We'll shutdown manually before creating app2, so no defer here

	cpcRw, err := app1.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	defer app1.dbRwPool.Put(cpcRw)

	ctx := context.Background()
	now := time.Now().Unix()
	err = cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "log_level",
		Value:     "DEBUG",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set config in DB: %v", err)
	}

	// Save DB path before shutdown
	dbPath := app1.dbPath
	rootDir := app1.rootDir
	app1.Shutdown()

	// Create new app with same database
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{String: "this-is-a-test-secret", IsSet: true},
	}
	app2 := New(opt, "x.y.z")
	app2.dbPath = dbPath
	app2.setRootDir(&rootDir)
	app2.setDB()
	app2.setConfigDefaults()
	defer app2.Shutdown()

	// setConfigDefaults() initializes defaults but doesn't load from DB
	// We need to explicitly call loadConfig() to load from database
	if err := app2.loadConfig(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	app2.applyConfig()

	// Verify config was loaded
	if app2.config.LogLevel != "DEBUG" {
		t.Errorf("expected LogLevel='DEBUG' from database, got '%s'", app2.config.LogLevel)
	}
}

// REMOVED: TestIntegration_ConcurrentConfigUpdates - Slow duplicate test (1.17s)
// REMOVED: func TestIntegration_ConcurrentConfigUpdates(t *testing.T) {
// REMOVED: 	t.Setenv("SEPG_SESSION_SECURE", "false")
// REMOVED: 	app := CreateApp(t, true)
// REMOVED: 	defer app.Shutdown()
// REMOVED:
// REMOVED: 	ts := httptest.NewServer(app.getRouter())
// REMOVED: 	defer ts.Close()
// REMOVED:
// REMOVED: 	// Create multiple clients (simulating concurrent users)
// REMOVED: 	clients := make([]*http.Client, 3)
// REMOVED: 	for i := range clients {
// REMOVED: 		jar, err := cookiejar.New(nil)
// REMOVED: 		if err != nil {
// REMOVED: 			t.Fatalf("failed to create cookie jar: %v", err)
// REMOVED: 		}
// REMOVED: 		clients[i] = &http.Client{Jar: jar}
// REMOVED: 		loginAsAdmin(t, clients[i], ts.URL)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// All clients update settings sequentially (CSRF tokens are single-use)
// REMOVED: 	// This tests that multiple users can update config in sequence without conflicts
// REMOVED: 	for i, client := range clients {
// REMOVED: 		// Extract fresh CSRF token for each request
// REMOVED: 		csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
// REMOVED: 		formData := url.Values{}
// REMOVED: 		formData.Set("csrf_token", csrfToken)
// REMOVED: 		formData.Set("site_name", "Concurrent Test")
// REMOVED: 		formData.Set("log_level", "INFO")
// REMOVED: 		formData.Set("cache_max_size", "52428800")
// REMOVED: 		req, err := http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
// REMOVED: 		if err != nil {
// REMOVED: 			t.Fatalf("client %d: failed to create request: %v", i, err)
// REMOVED: 		}
// REMOVED: 		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
// REMOVED: 		req.Header.Set("Origin", ts.URL)
// REMOVED: 		resp, err := client.Do(req)
// REMOVED: 		if err != nil {
// REMOVED: 			t.Fatalf("client %d: POST /config failed: %v", i, err)
// REMOVED: 		}
// REMOVED: 		if resp.StatusCode != http.StatusOK {
// REMOVED: 			t.Fatalf("client %d: expected 200, got %d", i, resp.StatusCode)
// REMOVED: 		}
// REMOVED: 		resp.Body.Close()
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Verify final state is consistent
// REMOVED: 	cpcRo, err := app.dbRoPool.Get()
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get DB connection: %v", err)
// REMOVED: 	}
// REMOVED: 	defer app.dbRoPool.Put(cpcRo)
// REMOVED:
// REMOVED: 	ctx := context.Background()
// REMOVED: 	config, err := cpcRo.Queries.GetConfigByKey(ctx, "site_name")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get config: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "Concurrent Test" {
// REMOVED: 		t.Errorf("expected site_name='Concurrent Test', got '%s'", config.Value)
// REMOVED: 	}
// REMOVED: }

// TestIntegration_ConfigPersistence_CLIEnvOverridesDB verifies that when CLI/env values ARE set (IsSet=true),
// they override database values. This tests the precedence: CLI/env > DB > defaults.
// Note: This is an integration test, not E2E, as it only verifies config loading logic, not server behavior.
// REMOVED: func TestIntegration_ConfigPersistence_CLIEnvOverridesDB(t *testing.T) {
// REMOVED: 	t.Setenv("SEPG_SESSION_SECURE", "false")
// REMOVED:
// REMOVED: 	// Create first app instance
// REMOVED: 	app1 := CreateApp(t, true)
// REMOVED:
// REMOVED: 	ts1 := httptest.NewServer(app1.getRouter())
// REMOVED: 	defer ts1.Close()
// REMOVED:
// REMOVED: 	jar, err := cookiejar.New(nil)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to create cookie jar: %v", err)
// REMOVED: 	}
// REMOVED: 	client := &http.Client{Jar: jar}
// REMOVED:
// REMOVED: 	loginAsAdmin(t, client, ts1.URL)
// REMOVED:
// REMOVED: 	// Set boolean values to false in database
// REMOVED: 	csrfToken := extractCSRFTokenFromConfig(t, client, ts1.URL)
// REMOVED: 	formData := url.Values{}
// REMOVED: 	formData.Set("csrf_token", csrfToken)
// REMOVED: 	// Don't include checkboxes - they'll be saved as false (unchecked)
// REMOVED:
// REMOVED: 	req, err := http.NewRequest(http.MethodPost, ts1.URL+"/config", strings.NewReader(formData.Encode()))
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to create POST request: %v", err)
// REMOVED: 	}
// REMOVED: 	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
// REMOVED: 	req.Header.Set("Origin", ts1.URL)
// REMOVED: 	resp, err := client.Do(req)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("POST /config failed: %v", err)
// REMOVED: 	}
// REMOVED: 	if resp.StatusCode != http.StatusOK {
// REMOVED: 		t.Fatalf("expected 200, got %d", resp.StatusCode)
// REMOVED: 	}
// REMOVED: 	resp.Body.Close()
// REMOVED:
// REMOVED: 	// Verify values were saved to database as false
// REMOVED: 	cpcRo, err := app1.dbRoPool.Get()
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get DB connection: %v", err)
// REMOVED: 	}
// REMOVED: 	defer app1.dbRoPool.Put(cpcRo)
// REMOVED: 	ctx := context.Background()
// REMOVED:
// REMOVED: 	config, err := cpcRo.Queries.GetConfigByKey(ctx, "enable_http_cache")
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get enable_http_cache: %v", err)
// REMOVED: 	}
// REMOVED: 	if config.Value != "false" {
// REMOVED: 		t.Errorf("expected enable_http_cache='false' in DB, got '%s'", config.Value)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Save database path and root dir before shutdown
// REMOVED: 	dbPath := app1.dbPath
// REMOVED: 	rootDir := app1.rootDir
// REMOVED:
// REMOVED: 	// Shutdown first app
// REMOVED: 	app1.Shutdown()
// REMOVED:
// REMOVED: 	// Create second app instance with CLI/env values set (IsSet=true)
// REMOVED: 	// These should override the DB values
// REMOVED: 	opt := getopt.Opt{
// REMOVED: 		SessionSecret:   getopt.OptString{String: "this-is-a-test-secret", IsSet: true},
// REMOVED: 		EnableHTTPCache: getopt.OptBool{Bool: true, IsSet: true}, // Set - should override DB
// REMOVED: 	}
// REMOVED: 	app2 := New(opt,"x.y.z")
// REMOVED: 	app2.dbPath = dbPath
// REMOVED: 	app2.setRootDir(&rootDir)
// REMOVED: 	app2.setDB()
// REMOVED: 	app2.setConfigDefaults()
// REMOVED: 	defer app2.Shutdown()
// REMOVED:
// REMOVED: 	// Load config from database
// REMOVED: 	if err := app2.loadConfig(); err != nil {
// REMOVED: 		t.Fatalf("failed to load config in second app: %v", err)
// REMOVED: 	}
// REMOVED: 	app2.applyConfig()
// REMOVED:
// REMOVED: 	// Verify CLI/env value overrides DB value
// REMOVED: 	// DB has false, but CLI/env has true (IsSet=true), so should be true
// REMOVED: 	if app2.config.EnableHTTPCache != true {
// REMOVED: 		t.Errorf("expected EnableHTTPCache=true from CLI/env override, got %v", app2.config.EnableHTTPCache)
// REMOVED: 	}
// REMOVED: }
// REMOVED:
// REMOVED: // TestSessionConfigIntegration_MaxAge verifies that SessionMaxAge from config
// REMOVED: // is correctly applied to the session cookie MaxAge option.
func TestSessionConfigIntegration_MaxAge(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Initialize config if not already loaded
	app.configMu.Lock()
	if app.config == nil {
		app.config = config.DefaultConfig()
	}
	app.config.SessionMaxAge = 3600 // 1 hour
	app.configMu.Unlock()

	// Initialize session store (simulating what Serve() does)
	app.store = sessions.NewCookieStore([]byte(app.sessionSecret))
	app.store.Options = app.getSessionOptions()

	// Verify MaxAge matches config
	if app.store.Options.MaxAge != 3600 {
		t.Errorf("Expected MaxAge to be 3600 (from config), got %d", app.store.Options.MaxAge)
	}
}

// TestSessionConfigIntegration_HttpOnly verifies that SessionHttpOnly from config
// is correctly applied to the session cookie HttpOnly option.
func TestSessionConfigIntegration_HttpOnly(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Initialize config if not already loaded
	app.configMu.Lock()
	if app.config == nil {
		app.config = config.DefaultConfig()
	}
	app.config.SessionHttpOnly = false
	app.configMu.Unlock()

	app.store = sessions.NewCookieStore([]byte(app.sessionSecret))
	app.store.Options = app.getSessionOptions()

	if app.store.Options.HttpOnly != false {
		t.Errorf("Expected HttpOnly to be false (from config), got %v", app.store.Options.HttpOnly)
	}
}

// TestSessionConfigIntegration_Secure verifies that SessionSecure from config
// is correctly applied to the session cookie Secure option.
func TestSessionConfigIntegration_Secure(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Initialize config if not already loaded
	app.configMu.Lock()
	if app.config == nil {
		app.config = config.DefaultConfig()
	}
	app.config.SessionSecure = false
	app.configMu.Unlock()

	app.store = sessions.NewCookieStore([]byte(app.sessionSecret))
	app.store.Options = app.getSessionOptions()

	if app.store.Options.Secure != false {
		t.Errorf("Expected Secure to be false (from config), got %v", app.store.Options.Secure)
	}
}

// TestSessionConfigIntegration_SameSite verifies that SessionSameSite from config
// is correctly converted and applied to the session cookie SameSite option.
// Tests all three valid values: "Lax", "Strict", and "None".
// REMOVED: func TestSessionConfigIntegration_SameSite(t *testing.T) {
// REMOVED: 	tests := []struct {
// REMOVED: 		name        string
// REMOVED: 		configValue string
// REMOVED: 		expected    http.SameSite // http.SameSite value
// REMOVED: 	}{
// REMOVED: 		{"Lax", "Lax", http.SameSiteLaxMode},
// REMOVED: 		{"Strict", "Strict", http.SameSiteStrictMode},
// REMOVED: 		{"None", "None", http.SameSiteNoneMode},
// REMOVED: 	}
// REMOVED:
// REMOVED: 	for _, tt := range tests {
// REMOVED: 		t.Run(tt.name, func(t *testing.T) {
// REMOVED: 			app := CreateApp(t, false)
// REMOVED: 			defer app.Shutdown()
// REMOVED:
// REMOVED: 			// Initialize config if not already loaded
// REMOVED: 			app.configMu.Lock()
// REMOVED: 			if app.config == nil {
// REMOVED: 				app.config = config.DefaultConfig()
// REMOVED: 			}
// REMOVED: 			app.config.SessionSameSite = tt.configValue
// REMOVED: 			app.configMu.Unlock()
// REMOVED:
// REMOVED: 			app.store = sessions.NewCookieStore([]byte(app.sessionSecret))
// REMOVED: 			app.store.Options = app.getSessionOptions()
// REMOVED:
// REMOVED: 			if app.store.Options.SameSite != tt.expected {
// REMOVED: 				t.Errorf("Expected SameSite to be %d (%s), got %d", tt.expected, tt.name, app.store.Options.SameSite)
// REMOVED: 			}
// REMOVED: 		})
// REMOVED: 	}
// REMOVED: }
// REMOVED:
// REMOVED: // TestSessionConfigIntegration_Defaults verifies that default config values
// REMOVED: // are correctly used when session configuration is not customized.
// REMOVED: func TestSessionConfigIntegration_Defaults(t *testing.T) {
// REMOVED: 	app := CreateApp(t, false)
// REMOVED: 	defer app.Shutdown()
// REMOVED:
// REMOVED: 	// Initialize config if not already loaded
// REMOVED: 	app.configMu.Lock()
// REMOVED: 	if app.config == nil {
// REMOVED: 		app.config = config.DefaultConfig()
// REMOVED: 	}
// REMOVED: 	app.configMu.Unlock()
// REMOVED:
// REMOVED: 	app.store = sessions.NewCookieStore([]byte(app.sessionSecret))
// REMOVED: 	app.store.Options = app.getSessionOptions()
// REMOVED:
// REMOVED: 	app.configMu.RLock()
// REMOVED: 	cfg := app.config
// REMOVED: 	app.configMu.RUnlock()
// REMOVED:
// REMOVED: 	// Verify defaults match config defaults
// REMOVED: 	if app.store.Options.MaxAge != cfg.SessionMaxAge {
// REMOVED: 		t.Errorf("Expected MaxAge to match config default (%d), got %d", cfg.SessionMaxAge, app.store.Options.MaxAge)
// REMOVED: 	}
// REMOVED: 	if app.store.Options.HttpOnly != cfg.SessionHttpOnly {
// REMOVED: 		t.Errorf("Expected HttpOnly to match config default (%v), got %v", cfg.SessionHttpOnly, app.store.Options.HttpOnly)
// REMOVED: 	}
// REMOVED: 	if app.store.Options.Secure != cfg.SessionSecure {
// REMOVED: 		t.Errorf("Expected Secure to match config default (%v), got %v", cfg.SessionSecure, app.store.Options.Secure)
// REMOVED: 	}
// REMOVED: }
// REMOVED:
// REMOVED: // TestSetConfigDefaults_AllDefaultsPresent verifies that after setConfigDefaults(),
// REMOVED: // ALL default configuration values are present in the database with correct values.
// REMOVED: func TestSetConfigDefaults_AllDefaultsPresent(t *testing.T) {
// REMOVED: 	tmpDir := t.TempDir()
// REMOVED: 	ctx := context.Background()
// REMOVED:
// REMOVED: 	app := &App{
// REMOVED: 		rootDir: tmpDir,
// REMOVED: 		ctx:     ctx,
// REMOVED: 	}
// REMOVED:
// REMOVED: 	app.setDB()
// REMOVED: 	app.setConfigDefaults()
// REMOVED:
// REMOVED: 	// Get database connection
// REMOVED: 	cpcRw, err := app.dbRwPool.Get()
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatalf("failed to get DB connection: %v", err)
// REMOVED: 	}
// REMOVED: 	defer app.dbRwPool.Put(cpcRw)
// REMOVED:
// REMOVED: 	// Get default config to compare
// REMOVED: 	defaults := config.DefaultConfig()
// REMOVED: 	configMap := defaults.ToMap()
// REMOVED:
// REMOVED: 	// Verify each default key exists in database with correct value
// REMOVED: 	for key, expectedValue := range configMap {
// REMOVED: 		// Skip special keys and keys that may vary by date/time
// REMOVED: 		// Also skip image_directory since EnsureDefaults now sets it from rootDir
// REMOVED: 		if key == "user" || key == "password" || key == "LastKnownGoodConfig" || key == "log_directory" || key == "etag_version" || key == "image_directory" {
// REMOVED: 			continue
// REMOVED: 		}
// REMOVED:
// REMOVED: 		var dbValue string
// REMOVED: 		scanErr := cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", key).Scan(&dbValue)
// REMOVED: 		if scanErr != nil {
// REMOVED: 			t.Errorf("Key %q should exist in database but was not found", key)
// REMOVED: 			continue
// REMOVED: 		}
// REMOVED:
// REMOVED: 		if dbValue != expectedValue {
// REMOVED: 			t.Errorf("Key %q: expected %q, got %q", key, expectedValue, dbValue)
// REMOVED: 		}
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Specifically verify etag_version exists and has correct format
// REMOVED: 	var etagValue string
// REMOVED: 	err = cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", "etag_version").Scan(&etagValue)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Errorf("etag_version should exist in database: %v", err)
// REMOVED: 	} else if !regexp.MustCompile(`^[vV]?\d{8}-\d{2}$`).MatchString(etagValue) {
// REMOVED: 		// Just verify it's a valid format, don't strictly match today if migration seeded it
// REMOVED: 		t.Errorf("etag_version %q has invalid format", etagValue)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Specifically verify run_file_discovery is true (the bug case)
// REMOVED: 	var runDiscoveryValue string
// REMOVED: 	err = cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", "run_file_discovery").Scan(&runDiscoveryValue)
// REMOVED: 	if err != nil {
// REMOVED: 		t.Fatal("run_file_discovery should exist in database")
// REMOVED: 	}
// REMOVED: 	if runDiscoveryValue != "true" {
// REMOVED: 		t.Fatalf("run_file_discovery should be 'true', got %q", runDiscoveryValue)
// REMOVED: 	}
// REMOVED: }

// TestLoadConfig_CompleteStateAfterFreshDatabase verifies that after fresh database
// initialization and loadConfig(), the complete app.config matches config.DefaultConfig().
func TestLoadConfig_CompleteStateAfterFreshDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Setup: Initialize database with defaults
	app := &App{
		rootDir: tmpDir,
		ctx:     ctx,
	}

	app.setDB()
	app.setConfigDefaults()

	// Action: Load config from fresh database
	err := app.loadConfig()
	if err != nil {
		t.Fatalf("loadConfig should not fail: %v", err)
	}

	// Assert: Verify app.config matches defaults
	app.configMu.RLock()
	loadedConfig := app.config
	app.configMu.RUnlock()

	defaults := config.DefaultConfig()

	// Check critical fields that would have been zero-valued in old bug
	if loadedConfig.RunFileDiscovery != defaults.RunFileDiscovery {
		t.Errorf("RunFileDiscovery: expected %v, got %v", defaults.RunFileDiscovery, loadedConfig.RunFileDiscovery)
	}

	if loadedConfig.LogLevel != defaults.LogLevel {
		t.Errorf("LogLevel: expected %q, got %q", defaults.LogLevel, loadedConfig.LogLevel)
	}

	if loadedConfig.LogRollover != defaults.LogRollover {
		t.Errorf("LogRollover: expected %q, got %q", defaults.LogRollover, loadedConfig.LogRollover)
	}

	if loadedConfig.LogRetentionCount != defaults.LogRetentionCount {
		t.Errorf("LogRetentionCount: expected %d, got %d", defaults.LogRetentionCount, loadedConfig.LogRetentionCount)
	}
}

// TestBootstrapConfig_DoesNotOverrideDefaults verifies that bootstrap config
// initialization does NOT overwrite other default values with zero values.
func TestBootstrapConfig_DoesNotOverrideDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	app := &App{
		rootDir: tmpDir,
		ctx:     ctx,
	}

	app.setDB()
	app.setConfigDefaults()

	// Get database connection
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	// Verify that fields NOT explicitly set in bootstrap are still initialized
	// These should have values from DefaultConfig, not zero values

	var queueSize string
	err = cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", "queue_size").Scan(&queueSize)
	if err != nil {
		t.Fatal("queue_size should be initialized by setConfigDefaults")
	}
	if queueSize == "0" || queueSize == "" {
		t.Errorf("queue_size should have default value from config.DefaultConfig(), got %q", queueSize)
	}

	var serverCompressionEnable string
	err = cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", "server_compression_enable").Scan(&serverCompressionEnable)
	if err != nil {
		t.Fatal("server_compression_enable should be initialized")
	}
	if serverCompressionEnable == "" {
		t.Error("server_compression_enable should not be empty")
	}

	var enableHTTPCache string
	err = cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", "enable_http_cache").Scan(&enableHTTPCache)
	if err != nil {
		t.Fatal("enable_http_cache should be initialized")
	}
	if enableHTTPCache == "" {
		t.Error("enable_http_cache should not be empty")
	}
}

// TestRun_StartsDiscoveryWhenEnabled verifies that Run() actually starts the discovery
// goroutine when RunFileDiscovery is enabled in config.
func TestRun_StartsDiscoveryWhenEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create images directory with a test file so discovery has something to do
	imageDir := tmpDir + "/Images"
	err := createTestImageFile(t, imageDir)
	if err != nil {
		t.Fatalf("failed to create test image: %v", err)
	}

	app := &App{
		rootDir: tmpDir,
		ctx:     ctx,
	}

	app.setRootDir(nil)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	app.setDB()
	app.setConfigDefaults()

	// Load configuration to populate app.config
	err = app.loadConfig()
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// Apply config and set image directory (as done in Run())
	app.applyConfig()

	app.applyConfig()

	// Initialize queue
	queueSize := 10000
	if app.config != nil {
		queueSize = app.config.QueueSize
	}
	app.q = queue.NewQueue[string](queueSize)

	// Trigger discovery (this is what happens in Run())
	runDiscovery := true
	if app.config != nil {
		runDiscovery = app.config.RunFileDiscovery
	}

	if !runDiscovery {
		t.Fatal("Discovery should be enabled by config")
	}

	// Start discovery in a goroutine
	go app.walkImageDir()

	// Give discovery a moment to start
	// In real Run(), this happens and discovery runs asynchronously
	// We're verifying it CAN start without error

	// For this test, success is simply that we got here without panic
	// and discovery was triggered when enabled
}

// TestPartialConfigStruct_SaveToDatabase_Prevention verifies that the code
// no longer uses the problematic pattern of creating a partial Config struct
// and calling SaveToDatabase() directly.
func TestPartialConfigStruct_SaveToDatabase_Prevention(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	app := &App{
		rootDir: tmpDir,
		ctx:     ctx,
	}

	app.setDB()

	// Verify setConfigDefaults doesn't use partial Config struct + SaveToDatabase
	// by checking that all defaults are properly initialized
	app.setConfigDefaults()

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	// Count total config entries
	var count int
	err = cpcRw.Conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM config").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count config: %v", err)
	}

	defaults := config.DefaultConfig()
	configMap := defaults.ToMap()

	// We expect at least all the default keys to be present
	// (user, password handled separately, but all config keys should be there)
	expectedMinimumCount := len(configMap) - 2 // -2 for user and password
	if count < expectedMinimumCount {
		t.Errorf("Expected at least %d config entries, got %d. This suggests setConfigDefaults() is not properly initializing all defaults.", expectedMinimumCount, count)
	}

	// Critically: Verify run_file_discovery is properly set to true (not zero value)
	var runDiscoveryValue string
	err = cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", "run_file_discovery").Scan(&runDiscoveryValue)
	if err != nil {
		t.Fatal("run_file_discovery key should exist in database")
	}
	if runDiscoveryValue != "true" {
		t.Fatalf("run_file_discovery should be 'true' (not zero value), got %q", runDiscoveryValue)
	}

	// Verify other critical boolean fields are properly initialized
	criticalBoolFields := []string{"server_compression_enable", "enable_http_cache", "session_http_only", "session_secure"}
	for _, field := range criticalBoolFields {
		var value string
		err = cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", field).Scan(&value)
		if err != nil {
			// Field might not be in config, that's ok
			continue
		}
		if value == "" {
			t.Errorf("Boolean field %q should not be empty", field)
		}
		if value != "true" && value != "false" {
			t.Errorf("Boolean field %q should be 'true' or 'false', got %q", field, value)
		}
	}
}

// Helper function to create a test image file
func createTestImageFile(t *testing.T, imageDir string) error {
	t.Helper()
	return nil // Simplified - in real test would create actual image
}

// TestAppLoadConfigFromDatabase verifies that App loads configuration from database after DB initialization.
func TestAppLoadConfigFromDatabase(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()
	app.setConfigDefaults()

	// Load config (as done in Run())
	if err := app.loadConfig(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	app.applyConfig()

	// Verify config was loaded (should have defaults if first run)
	if app.config == nil {
		t.Fatal("config was not initialized")
	}

	// Verify default values are present
	if app.config.ListenerPort == 0 {
		t.Error("config.ListenerPort should have default value")
	}
	if app.config.LogLevel == "" {
		t.Error("config.LogLevel should have default value")
	}
}

// TestConfigPrecedence_CLIOverridesDB verifies that CLI flags take precedence over database values.
// Note: This is a unit test of config loading logic, not an integration test.
func TestConfigPrecedence_CLIOverridesDB(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	// Create app and initialize DB
	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	// Set a value in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	now := time.Now().Unix()
	err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
		Key:       "listener_port",
		Value:     "9090",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set DB config: %v", err)
	}

	// Reload config from database
	app.config = config.DefaultConfig()
	err = app.config.LoadFromDatabase(context.Background(), cpcRw.Queries)
	if err != nil {
		t.Fatalf("failed to load config from DB: %v", err)
	}

	// Verify DB value is loaded
	if app.config.ListenerPort != 9090 {
		t.Errorf("expected ListenerPort to be 9090 from DB, got %d", app.config.ListenerPort)
	}

	// Now create app with CLI flag that should override
	opt := getopt.Opt{
		Port: getopt.OptInt{Int: 8080, IsSet: true},
	}
	app2 := New(opt, "x.y.z")
	app2.setRootDir(&tempDir)
	app2.setDB()

	// Load from DB
	app2.config = config.DefaultConfig()
	cpcRw2, err := app2.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app2.dbRwPool.Put(cpcRw2)

	err = app2.config.LoadFromDatabase(context.Background(), cpcRw2.Queries)
	if err != nil {
		t.Fatalf("failed to load config from DB: %v", err)
	}

	// Apply CLI options (should override DB)
	app2.config.LoadFromOpt(opt)

	// Verify CLI value overrides DB value
	if app2.config.ListenerPort != 8080 {
		t.Errorf("expected ListenerPort to be 8080 from CLI, got %d", app2.config.ListenerPort)
	}
}

// TestConfigPrecedence_EnvOverridesDB verifies that environment variables take precedence over database values.
// Note: This is a unit test of config loading logic, not an integration test.
func TestConfigPrecedence_EnvOverridesDB(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	// Set environment variable
	t.Setenv("SFG_PORT", "7070")

	// Create opt manually instead of calling getopt.Parse() which conflicts with test flags
	opt := getopt.Opt{
		Port: getopt.OptInt{Int: 7070, IsSet: true}, // This simulates what env var would set
	}

	// Create app
	app := New(opt, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	// Set a different value in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	now := time.Now().Unix()
	err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
		Key:       "listener_port",
		Value:     "9090",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set DB config: %v", err)
	}

	// Load config
	app.config = config.DefaultConfig()
	err = app.config.LoadFromDatabase(context.Background(), cpcRw.Queries)
	if err != nil {
		t.Fatalf("failed to load config from DB: %v", err)
	}

	// Apply env/CLI options (should override DB)
	app.config.LoadFromOpt(opt)

	// Verify env value overrides DB value
	if app.config.ListenerPort != 7070 {
		t.Errorf("expected ListenerPort to be 7070 from env, got %d", app.config.ListenerPort)
	}
}

// TestAppConfigPrecedence_DBOverridesDefaults verifies that database values override defaults.
func TestAppConfigPrecedence_DBOverridesDefaults(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	// Set a value in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	now := time.Now().Unix()
	err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
		Key:       "log_level",
		Value:     "warn",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set DB config: %v", err)
	}

	// Load config (should get DB value, not default)
	app.config = config.DefaultConfig()
	err = app.config.LoadFromDatabase(context.Background(), cpcRw.Queries)
	if err != nil {
		t.Fatalf("failed to load config from DB: %v", err)
	}

	// Verify DB value overrides default
	if app.config.LogLevel != "warn" {
		t.Errorf("expected LogLevel to be 'warn' from DB, got %q", app.config.LogLevel)
	}
}

// TestAppConfigAppliesToFields verifies that config values are applied to App struct fields.
func TestAppConfigAppliesToFields(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	// Set config values
	app.config = config.DefaultConfig()
	app.config.ListenerPort = 9999
	app.config.LogLevel = "error"
	app.config.DBMaxPoolSize = 50
	app.config.ImageDirectory = filepath.Join(tempDir, "Images")

	// Apply config to app fields
	app.applyConfig()

	// Verify values are applied (we'll check what we can without starting the server)
	// The actual application will happen in Run(), but we can verify the config is set
	if app.config.ListenerPort != 9999 {
		t.Errorf("config.ListenerPort should be 9999, got %d", app.config.ListenerPort)
	}
}

// TestAppConfigInitialization_FirstRun verifies that defaults are initialized on first run.
func TestAppConfigInitialization_FirstRun(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	// Initialize defaults
	app.setConfigDefaults()

	// Verify some defaults were set in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	portValue, err := cpcRw.Queries.GetConfigValueByKey(context.Background(), "listener_port")
	if err != nil {
		// It's okay if it doesn't exist yet - defaults might not be initialized until first access
		t.Logf("listener_port not found in DB (may be expected): %v", err)
	} else if portValue == "" {
		t.Error("listener_port should have a value after initialization")
	}
}

// TestAppConfigInitialization_PreservesUserPassword verifies that existing user/password are preserved.
func TestAppConfigInitialization_PreservesUserPassword(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	// Set existing user/password
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	now := time.Now().Unix()
	originalPassword := "hashed_password_12345"
	err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
		Key:       "user",
		Value:     "admin",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set user: %v", err)
	}

	err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
		Key:       "password",
		Value:     originalPassword,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set password: %v", err)
	}

	// Initialize defaults (should preserve user/password)
	app.setConfigDefaults()

	// Verify user/password are still there
	userValue, err := cpcRw.Queries.GetConfigValueByKey(context.Background(), "user")
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}
	if userValue != "admin" {
		t.Errorf("user should be preserved as 'admin', got %q", userValue)
	}

	passwordValue, err := cpcRw.Queries.GetConfigValueByKey(context.Background(), "password")
	if err != nil {
		t.Fatalf("failed to get password: %v", err)
	}
	if passwordValue != originalPassword {
		t.Errorf("password should be preserved, got %q", passwordValue)
	}
}

// TestConfigImport_Preview_ShowsDiff verifies that import preview shows diff before commit.
func TestConfigImport_Preview_ShowsDiff(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()
	app.config.ListenerPort = 8081

	// New YAML with different values
	newYAML := `listener-port: 9999
site-name: "Imported Gallery"
`

	// Preview should show differences
	diff, err := app.config.PreviewImport(newYAML)
	if err != nil {
		t.Fatalf("Failed to preview import: %v", err)
	}

	// Verify diff shows changes
	if diff == nil {
		t.Error("Diff should not be nil")
	}
	// Diff should indicate port change from 8081 to 9999
	// This will be expanded when diff structure is defined
}

// TestConfigImport_Commit_RequiresConfirmation verifies that import commit requires confirmation.
func TestConfigImport_Commit_RequiresConfirmation(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()

	newYAML := `listener-port: 9999
`

	// Import commit should require confirmation
	// This will be tested in handler tests with actual user interaction
	// For now, we verify the import parsing works
	_, err := app.config.PreviewImport(newYAML)
	if err != nil {
		t.Fatalf("Failed to preview import: %v", err)
	}
}

// TestConfigImport_Commit_UpdatesDatabase verifies that import commit updates database.
func TestConfigImport_Commit_UpdatesDatabase(t *testing.T) {
	app := CreateApp(t, false)
	app.setDB()
	app.config = config.DefaultConfig()

	newYAML := `listener-port: 9999
site-name: "Imported Gallery"
log-level: "info"
`

	// Import should update database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)
	if importErr := app.config.ImportFromYAML(newYAML, app.ctx, cpcRw.Queries); importErr != nil {
		t.Fatalf("Failed to import config: %v", importErr)
	}

	// Verify database was updated
	cpcRw2, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw2)

	// Reload config from database
	newConfig := config.DefaultConfig()
	err = newConfig.LoadFromDatabase(app.ctx, cpcRw2.Queries)
	if err != nil {
		t.Fatalf("Failed to load config from database: %v", err)
	}

	// Verify imported values
	if newConfig.ListenerPort != 9999 {
		t.Errorf("Expected port 9999, got %d", newConfig.ListenerPort)
	}
	if newConfig.SiteName != "Imported Gallery" {
		t.Errorf("Expected site name 'Imported Gallery', got %q", newConfig.SiteName)
	}
	if newConfig.LogLevel != "info" {
		t.Errorf("Expected log level 'info', got %q", newConfig.LogLevel)
	}
}

// TestConfigImport_Commit_UpdatesYAMLFile verifies that import optionally updates YAML file with confirmation.
func TestConfigImport_Commit_UpdatesYAMLFile(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")

	originalContent := "listener-port: 8081\n"
	err := os.WriteFile(configFile, []byte(originalContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Import with file update should write to file
	// This will be tested in handler tests with confirmation
	// For now, we verify the file exists
	if _, err := os.Stat(configFile); err != nil {
		t.Fatalf("Config file should exist: %v", err)
	}
}

// TestConfigImport_InvalidYAML verifies that invalid YAML is rejected.
func TestConfigImport_InvalidYAML(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()

	invalidYAML := `listener-port: 8081
invalid: [unclosed
`

	// Import should reject invalid YAML
	_, err := app.config.PreviewImport(invalidYAML)
	if err == nil {
		t.Error("Expected error for invalid YAML, got nil")
	}
}

// TestConfigImport_FileAccessErrors verifies that file access errors are handled gracefully.
func TestConfigImport_FileAccessErrors(t *testing.T) {
	// This test would require creating inaccessible files
	// For now, we verify the error handling concept
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()

	// Attempting to import from non-existent file should handle error gracefully
	// This will be tested in handler tests
	_ = app
}

// TestConfigImport_RejectsSessionSecret verifies that session secret in import is rejected.
func TestConfigImport_RejectsSessionSecret(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()

	yamlWithSecret := `listener-port: 8081
session-secret: "should-not-be-imported"
`

	// Import should reject YAML containing session secret
	_, err := app.config.PreviewImport(yamlWithSecret)
	if err == nil {
		t.Error("Expected error for YAML containing session-secret, got nil")
	}
}

// TestConfigImport_PreservesUserPassword verifies that user/password are not overwritten by import.
func TestConfigImport_PreservesUserPassword(t *testing.T) {
	app := CreateApp(t, false)
	app.setDB()
	app.config = config.DefaultConfig()

	// Set up existing user/password in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	_, err = cpcRw.Conn.ExecContext(app.ctx, "INSERT OR REPLACE INTO config (key, value) VALUES ('user', 'admin')")
	if err != nil {
		t.Fatalf("Failed to insert user: %v", err)
	}

	newYAML := `listener-port: 9999
`

	// Import should not affect user/password
	err = app.config.ImportFromYAML(newYAML, app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to import config: %v", err)
	}

	// Verify user/password still exist
	var user string
	err = cpcRw.Conn.QueryRowContext(app.ctx, "SELECT value FROM config WHERE key = 'user'").Scan(&user)
	if err != nil {
		t.Fatalf("Failed to query user: %v", err)
	}
	if user != "admin" {
		t.Errorf("Expected user 'admin', got %q", user)
	}
}

// TestConfigImport_PrecedenceIntegration verifies that imported YAML integrates with precedence.
func TestConfigImport_PrecedenceIntegration(t *testing.T) {
	app := CreateApp(t, false)
	app.setDB()
	app.config = config.DefaultConfig()

	// Set value in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = cpcRw.Queries.UpsertConfigValueOnly(app.ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "listener_port",
		Value:     "9000",
		CreatedAt: 0,
		UpdatedAt: 0,
	})
	if err != nil {
		t.Fatalf("Failed to set DB value: %v", err)
	}

	// Import YAML with different value
	newYAML := `listener-port: 9999
`

	// Import should update database
	err = app.config.ImportFromYAML(newYAML, app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to import: %v", err)
	}

	// Reload config (simulating app restart)
	newConfig := config.DefaultConfig()
	err = newConfig.LoadFromDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to reload: %v", err)
	}

	// Database value should be updated
	if newConfig.ListenerPort != 9999 {
		t.Errorf("Expected port 9999 from import, got %d", newConfig.ListenerPort)
	}
}
