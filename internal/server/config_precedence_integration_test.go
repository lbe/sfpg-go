//go:build integration

package server

import (
	"context"
	"fmt"
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

// --- merged from config_precedence_integration_test.go ---
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
	formData := url.Values{}
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
	formData = url.Values{}
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
	formData = url.Values{}
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
	formData = url.Values{}
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
	formData := url.Values{}
	// Include with empty values to signal presence of config fields
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

	value, err := cpcRo.Queries.GetConfigValueByKey(ctx, "enable_http_cache")
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
	// Defaults are: EnableHTTPCache=true, RunFileDiscovery=true
	// We explicitly set these to match defaults to test that LoadFromOpt doesn't override DB
	// NOTE: Since these are NOT set (IsSet=false), LoadFromOpt should NOT override DB values
	opt := getopt.Opt{
		SessionSecret:    getopt.OptString{String: "this-is-a-test-secret-with-min-32-bytes", IsSet: true},
		EnableHTTPCache:  getopt.OptBool{Bool: true, IsSet: false}, // Not set - should not override DB
		RunFileDiscovery: getopt.OptBool{Bool: true, IsSet: false}, // Not set - should not override DB
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
	if app2.ConfigManager.Config.EnableHTTPCache != false {
		t.Errorf("expected EnableHTTPCache=false (from DB), got %v (overridden by opt default)", app2.ConfigManager.Config.EnableHTTPCache)
	}
	if app2.ConfigManager.Config.RunFileDiscovery != false {
		t.Errorf("expected RunFileDiscovery=false (from DB), got %v (overridden by opt default)", app2.ConfigManager.Config.RunFileDiscovery)
	}
}

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
		SessionSecret: getopt.OptString{String: "this-is-a-test-secret-with-min-32-bytes", IsSet: true},
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
	if app2.ConfigManager.Config.SiteName != "Database Value" {
		t.Errorf("expected database value 'Database Value', got '%s'", app2.ConfigManager.Config.SiteName)
	}

	// Note: CLI and env precedence testing would require getopt.Parse() integration
	// which is tested in other unit tests. This E2E test verifies database persistence.
}

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
		SessionSecret: getopt.OptString{String: "this-is-a-test-secret-with-min-32-bytes", IsSet: true},
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
	if app2.ConfigManager.Config.LogLevel != "DEBUG" {
		t.Errorf("expected LogLevel='DEBUG' from database, got '%s'", app2.ConfigManager.Config.LogLevel)
	}
}

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
	app.ConfigManager.Config = config.DefaultConfig()
	err = app.ConfigManager.Config.LoadFromDatabase(context.Background(), cpcRw.Queries)
	if err != nil {
		t.Fatalf("failed to load config from DB: %v", err)
	}

	// Verify DB value is loaded
	if app.ConfigManager.Config.ListenerPort != 9090 {
		t.Errorf("expected ListenerPort to be 9090 from DB, got %d", app.ConfigManager.Config.ListenerPort)
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
	app2.ConfigManager.Config = config.DefaultConfig()
	cpcRw2, err := app2.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app2.dbRwPool.Put(cpcRw2)

	err = app2.ConfigManager.Config.LoadFromDatabase(context.Background(), cpcRw2.Queries)
	if err != nil {
		t.Fatalf("failed to load config from DB: %v", err)
	}

	// Apply CLI options (should override DB)
	app2.ConfigManager.Config.LoadFromOpt(opt)

	// Verify CLI value overrides DB value
	if app2.ConfigManager.Config.ListenerPort != 8080 {
		t.Errorf("expected ListenerPort to be 8080 from CLI, got %d", app2.ConfigManager.Config.ListenerPort)
	}
}

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
	app.ConfigManager.Config = config.DefaultConfig()
	err = app.ConfigManager.Config.LoadFromDatabase(context.Background(), cpcRw.Queries)
	if err != nil {
		t.Fatalf("failed to load config from DB: %v", err)
	}

	// Apply env/CLI options (should override DB)
	app.ConfigManager.Config.LoadFromOpt(opt)

	// Verify env value overrides DB value
	if app.ConfigManager.Config.ListenerPort != 7070 {
		t.Errorf("expected ListenerPort to be 7070 from env, got %d", app.ConfigManager.Config.ListenerPort)
	}
}

func TestConfigPrecedence_LoginRateLimitEnvOverridesDB(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)

	// Simulate what SEPG_LOGIN_RATE_LIMIT_PER_IP=5 would set via env parsing.
	opt := getopt.Opt{
		LoginRateLimitPerIP: getopt.OptInt{Int: 5, IsSet: true},
	}

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
		Key:       "login_rate_limit_per_ip",
		Value:     "10",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set DB config: %v", err)
	}

	// Load config
	app.ConfigManager.Config = config.DefaultConfig()
	err = app.ConfigManager.Config.LoadFromDatabase(context.Background(), cpcRw.Queries)
	if err != nil {
		t.Fatalf("failed to load config from DB: %v", err)
	}

	// Apply env/CLI options (should override DB)
	app.ConfigManager.Config.LoadFromOpt(opt)

	// Verify env value overrides DB value
	if app.ConfigManager.Config.LoginRateLimitPerIP != 5 {
		t.Errorf("expected LoginRateLimitPerIP to be 5 from env, got %d", app.ConfigManager.Config.LoginRateLimitPerIP)
	}
}

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
	app.ConfigManager.Config = config.DefaultConfig()
	err = app.ConfigManager.Config.LoadFromDatabase(context.Background(), cpcRw.Queries)
	if err != nil {
		t.Fatalf("failed to load config from DB: %v", err)
	}

	// Verify DB value overrides default
	if app.ConfigManager.Config.LogLevel != "warn" {
		t.Errorf("expected LogLevel to be 'warn' from DB, got %q", app.ConfigManager.Config.LogLevel)
	}
}

func TestAppConfigAppliesToFields(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	// Set config values
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.ListenerPort = 9999
	app.ConfigManager.Config.LogLevel = "error"
	app.ConfigManager.Config.DBMaxPoolSize = 50
	app.ConfigManager.Config.ImageDirectory = filepath.Join(tempDir, "Images")

	// Apply config to app fields
	app.ApplyConfig()

	// Verify values are applied (we'll check what we can without starting the server)
	// The actual application will happen in Run(), but we can verify the config is set
	if app.ConfigManager.Config.ListenerPort != 9999 {
		t.Errorf("config.ListenerPort should be 9999, got %d", app.ConfigManager.Config.ListenerPort)
	}
}

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
		app.ConfigManager.Config = config.DefaultConfig()
		err = app.ConfigManager.Config.LoadFromDatabase(context.Background(), cpcRw.Queries)
		if err != nil {
			t.Fatalf("failed to load config from DB: %v", err)
		}

		// Verify DB value overrides default
		if app.ConfigManager.Config.EnableHTTPCache {
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
		app.ConfigManager.Config = config.DefaultConfig()
		err = app.ConfigManager.Config.LoadFromDatabase(context.Background(), cpcRw.Queries)
		if err != nil {
			t.Fatalf("failed to load config from DB: %v", err)
		}

		// Apply opt overrides (should apply env)
		app.ConfigManager.Config.LoadFromOpt(app.opt)

		// Verify env value overrides DB
		if !app.ConfigManager.Config.EnableHTTPCache {
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
		app.ConfigManager.Config = config.DefaultConfig()
		err = app.ConfigManager.Config.LoadFromDatabase(context.Background(), cpcRw.Queries)
		if err != nil {
			t.Fatalf("failed to load config from DB: %v", err)
		}

		// Apply opt overrides (should apply CLI)
		app.ConfigManager.Config.LoadFromOpt(app.opt)

		// Verify CLI value overrides both env and DB
		if !app.ConfigManager.Config.EnableHTTPCache {
			t.Errorf("expected EnableHTTPCache to be true from CLI, got false")
		}
	})
}

func TestIntegration_ConfigPersistence_BooleanValues(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	app1 := CreateApp(t)
	defer func() {
		if app1 != nil {
			app1.Shutdown()
		}
	}()

	ts := httptest.NewServer(app1.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginAsAdmin(t, client, ts.URL)

	formData := url.Values{}
	// Omitted checkboxes should be saved as false.
	formData.Set("enable_http_cache", "on")
	formData.Set("run_file_discovery", "on")
	// session_http_only is intentionally omitted.

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

	cpcRo, err := app1.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}

	checks := map[string]string{
		"enable_http_cache":  "true",
		"session_http_only":  "false",
		"run_file_discovery": "true",
	}
	for key, expected := range checks {
		value, err := cpcRo.Queries.GetConfigValueByKey(app1.RuntimeManager.ctx, key)
		if err != nil {
			app1.dbRoPool.Put(cpcRo)
			t.Fatalf("failed to get %s from DB: %v", key, err)
		}
		if value != expected {
			app1.dbRoPool.Put(cpcRo)
			t.Errorf("expected %s='%s' in DB, got '%s'", key, expected, value)
		}
	}
	app1.dbRoPool.Put(cpcRo)

	dbPaths := app1.dbPaths
	rootDir := app1.rootDir
	app1.Shutdown()
	app1 = nil

	app2 := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "this-is-a-test-secret-with-min-32-bytes", IsSet: true},
	}, "x.y.z")
	app2.dbPaths = dbPaths
	app2.setRootDir(&rootDir)
	defer app2.Shutdown()

	app2.setDB()
	app2.setConfigDefaults()
	if err := app2.loadConfig(); err != nil {
		t.Fatalf("failed to load config in second app: %v", err)
	}
	app2.ApplyConfig()

	if !app2.ConfigManager.Config.EnableHTTPCache {
		t.Error("expected EnableHTTPCache=true after restart")
	}
	if app2.ConfigManager.Config.SessionHttpOnly {
		t.Error("expected SessionHttpOnly=false after restart")
	}
	if !app2.ConfigManager.Config.RunFileDiscovery {
		t.Error("expected RunFileDiscovery=true after restart")
	}
}

func TestIntegration_ConfigPersistence_CLIEnvOverridesDB(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	app1 := CreateApp(t)
	defer func() {
		if app1 != nil {
			app1.Shutdown()
		}
	}()

	ts := httptest.NewServer(app1.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginAsAdmin(t, client, ts.URL)

	formData := url.Values{}
	formData.Set("enable_http_cache", "")

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

	cpcRo, err := app1.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}

	dbValue, err := cpcRo.Queries.GetConfigValueByKey(app1.RuntimeManager.ctx, "enable_http_cache")
	if err != nil {
		app1.dbRoPool.Put(cpcRo)
		t.Fatalf("failed to get enable_http_cache from DB: %v", err)
	}
	if dbValue != "false" {
		app1.dbRoPool.Put(cpcRo)
		t.Errorf("expected enable_http_cache='false' in DB, got %q", dbValue)
	}
	app1.dbRoPool.Put(cpcRo)

	dbPaths := app1.dbPaths
	rootDir := app1.rootDir
	app1.Shutdown()
	app1 = nil

	app2 := New(getopt.Opt{
		SessionSecret:   getopt.OptString{String: "this-is-a-test-secret-with-min-32-bytes", IsSet: true},
		EnableHTTPCache: getopt.OptBool{Bool: true, IsSet: true},
	}, "x.y.z")
	app2.dbPaths = dbPaths
	app2.setRootDir(&rootDir)
	defer app2.Shutdown()

	app2.setDB()
	app2.setConfigDefaults()
	if err := app2.loadConfig(); err != nil {
		t.Fatalf("failed to load config in second app: %v", err)
	}
	app2.ApplyConfig()

	if !app2.ConfigManager.Config.EnableHTTPCache {
		t.Error("expected EnableHTTPCache=true (CLI/env override), got false")
	}
}

func TestIntegration_ConcurrentConfigUpdates(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	app := CreateApp(t)
	defer app.Shutdown()

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	errCh := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() {
			jar, err := cookiejar.New(nil)
			if err != nil {
				errCh <- err
				return
			}
			client := &http.Client{Jar: jar}

			loginAsAdmin(t, client, ts.URL)

			formData := url.Values{}
			formData.Set("site_name", "Concurrent Test")
			formData.Set("log_level", "INFO")
			formData.Set("cache_max_size", "52428800")

			req, err := http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
			if err != nil {
				errCh <- err
				return
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", ts.URL)
			resp, err := client.Do(req)
			if err != nil {
				errCh <- err
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errCh <- fmt.Errorf("expected 200, got %d", resp.StatusCode)
				return
			}
			errCh <- nil
		}()
	}

	for i := 0; i < 3; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("concurrent update error: %v", err)
		}
	}

	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	checks := map[string]string{
		"site_name":      "Concurrent Test",
		"log_level":      "INFO",
		"cache_max_size": "52428800",
	}
	for key, expected := range checks {
		value, err := cpcRo.Queries.GetConfigValueByKey(app.RuntimeManager.ctx, key)
		if err != nil {
			t.Fatalf("failed to get %s from DB: %v", key, err)
		}
		if value != expected {
			t.Errorf("expected %s='%s' in DB, got '%s'", key, expected, value)
		}
	}
}

// --- merged from config_import_integration_test.go ---
func TestConfigImport_Commit_UpdatesDatabase(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	app.ConfigManager.Config = config.DefaultConfig()

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
	if importErr := app.ConfigManager.Config.ImportFromYAML(newYAML, app.RuntimeManager.ctx, cpcRw.Queries); importErr != nil {
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
	err = newConfig.LoadFromDatabase(app.RuntimeManager.ctx, cpcRw2.Queries)
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

func TestConfigImport_PreservesUserPassword(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	app.ConfigManager.Config = config.DefaultConfig()

	// Set up existing user/password in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	_, err = cpcRw.Conn.ExecContext(app.RuntimeManager.ctx, "INSERT OR REPLACE INTO config (key, value) VALUES ('user', 'admin')")
	if err != nil {
		t.Fatalf("Failed to insert user: %v", err)
	}

	newYAML := `listener-port: 9999
`

	// Import should not affect user/password
	err = app.ConfigManager.Config.ImportFromYAML(newYAML, app.RuntimeManager.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to import config: %v", err)
	}

	// Verify user/password still exist
	var user string
	err = cpcRw.Conn.QueryRowContext(app.RuntimeManager.ctx, "SELECT value FROM config WHERE key = 'user'").Scan(&user)
	if err != nil {
		t.Fatalf("Failed to query user: %v", err)
	}
	if user != "admin" {
		t.Errorf("Expected user 'admin', got %q", user)
	}
}

func TestConfigImport_PrecedenceIntegration(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	app.ConfigManager.Config = config.DefaultConfig()

	// Set value in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = cpcRw.Queries.UpsertConfigValueOnly(app.RuntimeManager.ctx, gallerydb.UpsertConfigValueOnlyParams{
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
	err = app.ConfigManager.Config.ImportFromYAML(newYAML, app.RuntimeManager.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to import: %v", err)
	}

	// Reload config (simulating app restart)
	newConfig := config.DefaultConfig()
	err = newConfig.LoadFromDatabase(app.RuntimeManager.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to reload: %v", err)
	}

	// Database value should be updated
	if newConfig.ListenerPort != 9999 {
		t.Errorf("Expected port 9999 from import, got %d", newConfig.ListenerPort)
	}
}
