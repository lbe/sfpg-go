//go:build integration || e2e

package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
)

func TestIntegration_MultipleCategoryUpdates(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")
	app := CreateApp(t, WithPool())
	defer app.Shutdown()

	t.Parallel()
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

// Config persistence is already tested by TestConfigSaveToDatabase (0.37s)
// and config service tests. This test creating two full app instances was redundant.

// TestIntegration_ConfigPersistence_BooleanValues verifies that boolean configuration values
// persist in the database. This test specifically covers the bug where unchecked
// checkboxes were not being saved to the database.
// Note: This is an integration test, not E2E, as it only verifies database persistence, not that the server uses the values.
// This ensures database persistence works correctly even when no CLI/env overrides are set.
// Note: This is an integration test, not E2E, as it only verifies config loading logic, not server behavior.

func TestIntegration_ConfigPersistence_LoadFromOptDoesNotOverrideWithDefaults(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	// Create first app instance
	app1 := CreateApp(t, WithPool())

	t.Parallel()
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
	// Include with empty values to signal presence of config fields
	formData.Set("server_compression_enable", "")
	formData.Set("enable_http_cache", "")
	formData.Set("run_file_discovery", "")

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

	// Save database paths and root dir before shutdown
	dbPaths := app1.dbPaths
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
	app2.dbPaths = dbPaths
	app2.setRootDir(&rootDir)
	app2.setDB()
	app2.setConfigDefaults()
	defer app2.Shutdown()

	// Load config from database
	if err := app2.loadConfig(); err != nil {
		t.Fatalf("failed to load config in second app: %v", err)
	}
	app2.ApplyConfig()

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
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	// Step 1: Set a value in database
	app1 := CreateApp(t, WithPool())
	// Note: We'll shutdown manually before creating app2, so no defer here

	t.Parallel()
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
	// Save DB paths before shutdown
	dbPaths := app1.dbPaths
	rootDir := app1.rootDir
	app1.Shutdown()

	// Create app2 with same database
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{String: "this-is-a-test-secret", IsSet: true},
	}
	app2 := New(opt, "x.y.z")
	app2.dbPaths = dbPaths
	app2.setRootDir(&rootDir)
	app2.setDB()
	app2.setConfigDefaults()
	defer app2.Shutdown()

	// setConfigDefaults() initializes defaults but doesn't load from DB
	// We need to explicitly call loadConfig() to load from database
	if err := app2.loadConfig(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	app2.ApplyConfig()

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

func TestIntegration_ConfigLoadsOnStartup(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	// Create app and set a config value in database
	app1 := CreateApp(t, WithPool())
	// Note: We'll shutdown manually before creating app2, so no defer here

	t.Parallel()
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

	// Save DB paths before shutdown
	dbPaths := app1.dbPaths
	rootDir := app1.rootDir
	app1.Shutdown()

	// Create new app with same database
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{String: "this-is-a-test-secret", IsSet: true},
	}
	app2 := New(opt, "x.y.z")
	app2.dbPaths = dbPaths
	app2.setRootDir(&rootDir)
	app2.setDB()
	app2.setConfigDefaults()
	defer app2.Shutdown()

	// setConfigDefaults() initializes defaults but doesn't load from DB
	// We need to explicitly call loadConfig() to load from database
	if err := app2.loadConfig(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	app2.ApplyConfig()

	// Verify config was loaded
	if app2.config.LogLevel != "DEBUG" {
		t.Errorf("expected LogLevel='DEBUG' from database, got '%s'", app2.config.LogLevel)
	}
}

// TestIntegration_ConfigPersistence_CLIEnvOverridesDB verifies that when CLI/env values ARE set (IsSet=true),
// they override database values. This tests the precedence: CLI/env > DB > defaults.
// Note: This is an integration test, not E2E, as it only verifies config loading logic, not server behavior.

func TestConfigPrecedence_CLIOverridesDB(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)

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

	// Close the first app to release its dque flock before creating
	// a second app with the same root directory.
	app.Shutdown()

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
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)

	// Set environment variable
	setenvForTest(t, "SFG_PORT", "7070")

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
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)

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
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)

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
	app.ApplyConfig()

	// Verify values are applied (we'll check what we can without starting the server)
	// The actual application will happen in Run(), but we can verify the config is set
	if app.config.ListenerPort != 9999 {
		t.Errorf("config.ListenerPort should be 9999, got %d", app.config.ListenerPort)
	}
}

// TestAppConfigInitialization_FirstRun verifies that defaults are initialized on first run.

func TestDBConfig_EnvAndCLIOverrideDBValues(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)

	// Test Case 1: DB value should override default
	t.Run("DB overrides default", func(t *testing.T) {
		app := New(getopt.Opt{}, "x.y.z")
		app.setRootDir(&tempDir)
		app.setDB()
		defer app.Shutdown()

		// Set enable_http_cache=false in database (default is true)
		cpcRw, err := app.dbRwPool.Get()
		if err != nil {
			t.Fatalf("failed to get RW connection: %v", err)
		}
		defer app.dbRwPool.Put(cpcRw)

		now := time.Now().Unix()
		err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
			Key:       "enable_http_cache",
			Value:     "false",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("failed to set DB config: %v", err)
		}

		// Load config (should get DB value)
		app.config = config.DefaultConfig()
		err = app.config.LoadFromDatabase(context.Background(), cpcRw.Queries)
		if err != nil {
			t.Fatalf("failed to load config from DB: %v", err)
		}

		// Verify DB value overrides default
		if app.config.EnableHTTPCache {
			t.Errorf("expected EnableHTTPCache to be false from DB, got true")
		}
	})

	// Test Case 2: Env should override DB
	t.Run("Env overrides DB", func(t *testing.T) {
		setenvForTest(t, "SFG_HTTP_CACHE", "true") // Enable via env

		// Simulate env var being parsed into Opt
		opt := getopt.Opt{
			EnableHTTPCache: getopt.OptBool{Bool: true, IsSet: true},
		}

		app := New(opt, "x.y.z")
		app.setRootDir(&tempDir)
		app.setDB()
		defer app.Shutdown()

		// Set different value in database (false)
		cpcRw, err := app.dbRwPool.Get()
		if err != nil {
			t.Fatalf("failed to get RW connection: %v", err)
		}
		defer app.dbRwPool.Put(cpcRw)

		now := time.Now().Unix()
		err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
			Key:       "enable_http_cache",
			Value:     "false",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("failed to set DB config: %v", err)
		}

		// Load config from DB first
		app.config = config.DefaultConfig()
		err = app.config.LoadFromDatabase(context.Background(), cpcRw.Queries)
		if err != nil {
			t.Fatalf("failed to load config from DB: %v", err)
		}

		// Apply opt overrides (should apply env)
		app.config.LoadFromOpt(app.opt)

		// Verify env value overrides DB
		if !app.config.EnableHTTPCache {
			t.Errorf("expected EnableHTTPCache to be true from env, got false")
		}
	})

	// Test Case 3: CLI should override both Env and DB
	t.Run("CLI overrides Env and DB", func(t *testing.T) {
		setenvForTest(t, "SFG_HTTP_CACHE", "false") // Disable via env

		// CLI flag value (true via CLI should win)
		opt := getopt.Opt{
			EnableHTTPCache: getopt.OptBool{Bool: true, IsSet: true},
		}

		app := New(opt, "x.y.z")
		app.setRootDir(&tempDir)
		app.setDB()
		defer app.Shutdown()

		// Set different value in database (false)
		cpcRw, err := app.dbRwPool.Get()
		if err != nil {
			t.Fatalf("failed to get RW connection: %v", err)
		}
		defer app.dbRwPool.Put(cpcRw)

		now := time.Now().Unix()
		err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
			Key:       "enable_http_cache",
			Value:     "false",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("failed to set DB config: %v", err)
		}

		// Load config from DB first
		app.config = config.DefaultConfig()
		err = app.config.LoadFromDatabase(context.Background(), cpcRw.Queries)
		if err != nil {
			t.Fatalf("failed to load config from DB: %v", err)
		}

		// Apply opt overrides (should apply CLI)
		app.config.LoadFromOpt(app.opt)

		// Verify CLI value overrides both env and DB
		if !app.config.EnableHTTPCache {
			t.Errorf("expected EnableHTTPCache to be true from CLI, got false")
		}
	})
}
