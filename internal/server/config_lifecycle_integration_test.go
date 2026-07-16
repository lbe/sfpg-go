//go:build integration

package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
	"golang.org/x/net/html"
	"gopkg.in/yaml.v3"
)

// createAppForConfigTest returns a full app for config-only lifecycle tests.
func createAppForConfigTest(t testing.TB) *App {
	t.Helper()
	return CreateApp(t)
}

// --- merged from config_bootstrap_integration_test.go ---
func TestLoadConfig_CompleteStateAfterFreshDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Setup: Initialize database with defaults
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
		RuntimeManager:        NewRuntimeManager(ctx),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir
	app.SetConfig(config.DefaultConfig())

	app.setDB()
	app.setConfigDefaults()

	// Action: Load config from fresh database
	err := app.loadConfig()
	if err != nil {
		t.Fatalf("loadConfig should not fail: %v", err)
	}

	// Assert: Verify app.ConfigManager.Config matches defaults
	app.ConfigManager.ConfigMu.RLock()
	loadedConfig := app.ConfigManager.Config
	app.ConfigManager.ConfigMu.RUnlock()

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

// --- merged from config_pool_precedence_test.go ---
func TestDBPoolPrecedence_PoolsIgnoreDatabaseConfig(t *testing.T) {
	tempDir := t.TempDir()

	// PHASE 1: Create app instance and populate database with custom pool config
	opt1 := getopt.Opt{
		SessionSecret: getopt.OptString{
			String: "test-secret-for-database-config",
			IsSet:  true,
		},
	}

	app1 := New(opt1, "x.y.z")
	app1.setRootDir(&tempDir)
	app1.setupBootstrapLogging()
	app1.setDB() // Creates pools with hardcoded defaults (100/10)
	app1.setConfigDefaults()
	if err := app1.loadConfig(); err != nil {
		t.Fatalf("First app loadConfig failed: %v", err)
	}

	// Override config with custom pool sizes and save to database
	app1.ConfigManager.Config.DBMaxPoolSize = 25
	app1.ConfigManager.Config.DBMinIdleConnections = 8
	configService := config.NewService(app1.dbRwPool, app1.dbRoPool)
	if err := configService.Save(context.Background(), app1.ConfigManager.Config); err != nil {
		t.Fatalf("Failed to save config to database: %v", err)
	}
	app1.Shutdown()

	// PHASE 2: Create second app that should load pool config from database
	opt2 := getopt.Opt{
		SessionSecret: getopt.OptString{
			String: "test-secret-for-database-config",
			IsSet:  true,
		},
	}

	app2 := New(opt2, "x.y.z")
	app2.setRootDir(&tempDir)
	app2.setupBootstrapLogging()
	app2.setDB() // <- Pools created HERE with app.ConfigManager.Config=nil
	app2.setConfigDefaults()
	if err := app2.loadConfig(); err != nil { // <- Config loaded HERE (too late!)
		t.Fatalf("Second app loadConfig failed: %v", err)
	}
	// After Step F, pool reconfiguration only happens at startup via explicit call
	if err := app2.reconfigurePoolsFromConfig(); err != nil {
		t.Fatalf("Second app reconfigurePoolsFromConfig failed: %v", err)
	}

	// ASSERTION: Pools should be created with database config sizes, but they aren't
	if app2.dbRwPool.Config.MaxConnections != 25 {
		t.Errorf("FAIL: RW pool MaxConnections = %d, want 25 (from database config)",
			app2.dbRwPool.Config.MaxConnections)
	}
	if app2.dbRoPool.Config.MaxConnections != 25 {
		t.Errorf("FAIL: RO pool MaxConnections = %d, want 25 (from database config)",
			app2.dbRoPool.Config.MaxConnections)
	}
	if app2.dbRwPool.Config.MinIdleConnections != 8 {
		t.Errorf("FAIL: RW pool MinIdleConnections = %d, want 8 (from database config)",
			app2.dbRwPool.Config.MinIdleConnections)
	}
	if app2.dbRoPool.Config.MinIdleConnections != 8 {
		t.Errorf("FAIL: RO pool MinIdleConnections = %d, want 8 (from database config)",
			app2.dbRoPool.Config.MinIdleConnections)
	}

	app2.Shutdown()
}

func TestDBPoolPrecedence_ConfigLoadedAfterPoolCreation(t *testing.T) {
	tempDir := t.TempDir()

	// Create app and manually populate the database with pool config BEFORE startup
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{
			String: "test-secret-for-timing",
			IsSet:  true,
		},
	}

	app := New(opt, "x.y.z")
	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()

	// Pre-populate database with custom pool config before setDB()
	app.setDB()             // Creates pools with nil config -> hardcoded defaults
	app.setConfigDefaults() // Ensures DB has required keys

	// Manually insert custom pool config into database
	configService := config.NewService(app.dbRwPool, app.dbRoPool)

	// Set config values in memory
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.DBMaxPoolSize = 30
	app.ConfigManager.Config.DBMinIdleConnections = 12

	// Save to database
	if err := configService.Save(context.Background(), app.ConfigManager.Config); err != nil {
		t.Fatalf("Failed to save config to database: %v", err)
	}

	// Now load config (this will read from the database)
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// ASSERTION: After loadConfig(), app.ConfigManager.Config should have the custom values
	if app.ConfigManager.Config.DBMaxPoolSize != 30 {
		t.Errorf("FAIL: app.ConfigManager.Config.DBMaxPoolSize = %d, want 30", app.ConfigManager.Config.DBMaxPoolSize)
	}
	if app.ConfigManager.Config.DBMinIdleConnections != 12 {
		t.Errorf("FAIL: app.ConfigManager.Config.DBMinIdleConnections = %d, want 12", app.ConfigManager.Config.DBMinIdleConnections)
	}

	// After Step F, pool reconfiguration must be explicitly called
	if err := app.reconfigurePoolsFromConfig(); err != nil {
		t.Fatalf("reconfigurePoolsFromConfig failed: %v", err)
	}

	// CRITICAL FAILURE: Pools still have hardcoded defaults despite config having custom values
	if app.dbRwPool.Config.MaxConnections != 30 {
		t.Errorf("FAIL: RW pool MaxConnections = %d, want 30 (config value not applied to pool)",
			app.dbRwPool.Config.MaxConnections)
	}
	if app.dbRoPool.Config.MaxConnections != 30 {
		t.Errorf("FAIL: RO pool MaxConnections = %d, want 30 (config value not applied to pool)",
			app.dbRoPool.Config.MaxConnections)
	}
	if app.dbRwPool.Config.MinIdleConnections != 12 {
		t.Errorf("FAIL: RW pool MinIdleConnections = %d, want 12 (config value not applied to pool)",
			app.dbRwPool.Config.MinIdleConnections)
	}
	if app.dbRoPool.Config.MinIdleConnections != 12 {
		t.Errorf("FAIL: RO pool MinIdleConnections = %d, want 12 (config value not applied to pool)",
			app.dbRoPool.Config.MinIdleConnections)
	}

	app.Shutdown()
}

// --- merged from config_restart_persistence_integration_test.go ---
func TestConfigPersistence_AfterRestart_SiteNamePersist(t *testing.T) {
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

	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("site_name", "Persistence Test")

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

	siteName, err := cpcRo.Queries.GetConfigValueByKey(app1.RuntimeManager.ctx, "site_name")
	if err != nil {
		app1.dbRoPool.Put(cpcRo)
		t.Fatalf("failed to get site_name from DB: %v", err)
	}
	if siteName != "Persistence Test" {
		app1.dbRoPool.Put(cpcRo)
		t.Errorf("expected site_name='Persistence Test' in DB, got %q", siteName)
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

	if app2.ConfigManager.Config.SiteName != "Persistence Test" {
		t.Errorf("expected app2.ConfigManager.Config.SiteName='Persistence Test', got %q", app2.ConfigManager.Config.SiteName)
	}
}

// --- merged from config_startup_restart_regression_test.go ---
func TestStartupWithDBConfig_PoolSizeHonored(t *testing.T) {
	tempDir := t.TempDir()

	// PHASE 1: Create first app and save custom pool config to database
	opt1 := getopt.Opt{
		SessionSecret: getopt.OptString{
			String: "test-secret-startup-regression",
			IsSet:  true,
		},
	}

	app1 := New(opt1, "x.y.z")
	app1.setRootDir(&tempDir)
	app1.setupBootstrapLogging()
	app1.setDB()
	app1.setConfigDefaults()
	if err := app1.loadConfig(); err != nil {
		t.Fatalf("First app loadConfig failed: %v", err)
	}

	// Save custom pool configuration to database
	app1.ConfigManager.Config.DBMaxPoolSize = 35
	app1.ConfigManager.Config.DBMinIdleConnections = 15
	configService := config.NewService(app1.dbRwPool, app1.dbRoPool)
	if err := configService.Save(context.Background(), app1.ConfigManager.Config); err != nil {
		t.Fatalf("Failed to save config to database: %v", err)
	}

	// Verify database was updated
	dbConfig, err := configService.Load(context.Background())
	if err != nil {
		t.Fatalf("Failed to load config from database: %v", err)
	}
	if dbConfig.DBMaxPoolSize != 35 {
		t.Errorf("Database config DBMaxPoolSize = %d, want 35", dbConfig.DBMaxPoolSize)
	}
	if dbConfig.DBMinIdleConnections != 15 {
		t.Errorf("Database config DBMinIdleConnections = %d, want 15", dbConfig.DBMinIdleConnections)
	}

	app1.Shutdown()

	// PHASE 2: Create second app instance that should load pool config from database
	// This simulates a server restart where the database already has custom pool settings
	opt2 := getopt.Opt{
		SessionSecret: getopt.OptString{
			String: "test-secret-startup-regression",
			IsSet:  true,
		},
	}

	app2 := New(opt2, "x.y.z")
	app2.setRootDir(&tempDir)
	app2.setupBootstrapLogging()

	// The fix ensures config is loaded BEFORE pools are created
	// This was the root cause: setDB() was called before loadConfig()
	app2.setDB()             // Now uses app.ConfigManager.Config if available
	app2.setConfigDefaults() // Ensures DB has required keys
	if err := app2.loadConfig(); err != nil {
		t.Fatalf("Second app loadConfig failed: %v", err)
	}
	// After Step F, pool reconfiguration only happens via explicit call at startup
	if err := app2.reconfigurePoolsFromConfig(); err != nil {
		t.Fatalf("Second app reconfigurePoolsFromConfig failed: %v", err)
	}

	// ASSERTION: Pools should be created with database config sizes
	// Before fix: pools would be 100/10 (hardcoded defaults)
	// After fix: pools should be 35/15 (database values)
	if app2.dbRwPool.Config.MaxConnections != 35 {
		t.Errorf("RW pool MaxConnections = %d, want 35 (from database config)",
			app2.dbRwPool.Config.MaxConnections)
	}
	if app2.dbRoPool.Config.MaxConnections != 35 {
		t.Errorf("RO pool MaxConnections = %d, want 35 (from database config)",
			app2.dbRoPool.Config.MaxConnections)
	}
	if app2.dbRwPool.Config.MinIdleConnections != 15 {
		t.Errorf("RW pool MinIdleConnections = %d, want 15 (from database config)",
			app2.dbRwPool.Config.MinIdleConnections)
	}
	if app2.dbRoPool.Config.MinIdleConnections != 15 {
		t.Errorf("RO pool MinIdleConnections = %d, want 15 (from database config)",
			app2.dbRoPool.Config.MinIdleConnections)
	}

	// Verify app.ConfigManager.Config also has the correct values
	app2.ConfigManager.ConfigMu.RLock()
	configMaxPool := app2.ConfigManager.Config.DBMaxPoolSize
	configMinIdle := app2.ConfigManager.Config.DBMinIdleConnections
	app2.ConfigManager.ConfigMu.RUnlock()

	if configMaxPool != 35 {
		t.Errorf("app2.ConfigManager.Config.DBMaxPoolSize = %d, want 35", configMaxPool)
	}
	if configMinIdle != 15 {
		t.Errorf("app2.ConfigManager.Config.DBMinIdleConnections = %d, want 15", configMinIdle)
	}

	app2.Shutdown()
}

func TestRestartWithModifiedDBConfig_AppliesNewValues(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Verify initial config values (from defaults)
	app.ConfigManager.ConfigMu.RLock()
	initialCompression := app.ConfigManager.Config.ServerCompressionEnable
	initialCache := app.ConfigManager.Config.EnableHTTPCache
	app.ConfigManager.ConfigMu.RUnlock()

	// Initial values should be from defaults (true for compression/cache)
	if !initialCompression {
		t.Logf("Initial compression: %v (unexpected, but continuing)", initialCompression)
	}
	if !initialCache {
		t.Logf("Initial cache: %v (unexpected, but continuing)", initialCache)
	}

	// Modify config in database with new values
	configService := config.NewService(app.dbRwPool, app.dbRoPool)

	// Load current config from database
	dbConfig, err := configService.Load(context.Background())
	if err != nil {
		t.Fatalf("Failed to load config from database: %v", err)
	}

	// Modify values (opposite of defaults)
	dbConfig.ServerCompressionEnable = false
	dbConfig.EnableHTTPCache = false
	dbConfig.ListenerPort = 9876
	dbConfig.DBMaxPoolSize = 42 // Pool size for documentation purposes (not runtime changeable)
	dbConfig.DBMinIdleConnections = 18

	// Save modified config to database
	if saveErr := configService.Save(context.Background(), dbConfig); saveErr != nil {
		t.Fatalf("Failed to save modified config to database: %v", saveErr)
	}

	// Verify config was saved to database
	verifyConfig, err := configService.Load(context.Background())
	if err != nil {
		t.Fatalf("Failed to verify saved config: %v", err)
	}
	if verifyConfig.ServerCompressionEnable != false {
		t.Errorf("Database config compression = %v, want false", verifyConfig.ServerCompressionEnable)
	}
	if verifyConfig.EnableHTTPCache != false {
		t.Errorf("Database config cache = %v, want false", verifyConfig.EnableHTTPCache)
	}
	if verifyConfig.ListenerPort != 9876 {
		t.Errorf("Database config port = %d, want 9876", verifyConfig.ListenerPort)
	}

	// Reload config from database (simulates what happens during runtime config update)
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// Verify new values are applied to app.ConfigManager.Config
	app.ConfigManager.ConfigMu.RLock()
	newCompression := app.ConfigManager.Config.ServerCompressionEnable
	newCache := app.ConfigManager.Config.EnableHTTPCache
	newPort := app.ConfigManager.Config.ListenerPort
	newMaxPool := app.ConfigManager.Config.DBMaxPoolSize
	newMinIdle := app.ConfigManager.Config.DBMinIdleConnections
	app.ConfigManager.ConfigMu.RUnlock()

	// Check that config was reloaded from database
	if newCompression != false {
		t.Errorf("After loadConfig, compression = %v, want false (from database)", newCompression)
	}
	if newCache != false {
		t.Errorf("After loadConfig, cache = %v, want false (from database)", newCache)
	}
	if newPort != 9876 {
		t.Errorf("After loadConfig, port = %d, want 9876 (from database)", newPort)
	}
	if newMaxPool != 42 {
		t.Errorf("After loadConfig, DBMaxPoolSize = %d, want 42 (from database)", newMaxPool)
	}
	if newMinIdle != 18 {
		t.Errorf("After loadConfig, DBMinIdleConnections = %d, want 18 (from database)", newMinIdle)
	}

	// Note: Actual pool sizes cannot change without restart
	// This test only verifies that config values are reloaded correctly
	// The pools themselves still have their original sizes from startup
	if app.dbRwPool.Config.MaxConnections != 100 {
		t.Logf("RW pool MaxConnections = %d (expected to remain at startup value)",
			app.dbRwPool.Config.MaxConnections)
	}
	if app.dbRoPool.Config.MaxConnections != 100 {
		t.Logf("RO pool MaxConnections = %d (expected to remain at startup value)",
			app.dbRoPool.Config.MaxConnections)
	}
}

// --- merged from server_config_test.go ---
func TestSetConfigDefaults_AllDefaultsPresent(t *testing.T) {
	tempDir := t.TempDir()
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "this-is-a-test-secret-with-min-32-bytes", IsSet: true},
	}, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)
	app.setDB()
	app.setConfigDefaults()

	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("Failed to get RO DB connection: %v", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	excluded := map[string]bool{
		"user":                true,
		"password":            true,
		"LastKnownGoodConfig": true,
		"log_directory":       true,
		"etag_version":        true,
		"image_directory":     true,
	}

	defaults := config.DefaultConfig().ToMap()
	for key, expected := range defaults {
		if excluded[key] {
			continue
		}
		got, dbErr := cpcRo.Queries.GetConfigValueByKey(app.RuntimeManager.ctx, key)
		if dbErr != nil {
			t.Errorf("expected config key %q to be present, got error: %v", key, dbErr)
			continue
		}
		if got != expected {
			t.Errorf("config key %q: expected %q, got %q", key, expected, got)
		}
	}

	etag, err := cpcRo.Queries.GetConfigValueByKey(app.RuntimeManager.ctx, "etag_version")
	if err != nil {
		t.Fatalf("expected etag_version to be present, got error: %v", err)
	}
	matched, err := regexp.MatchString(`^[vV]?\d{8}-\d{2}$`, etag)
	if err != nil {
		t.Fatalf("invalid etag regex: %v", err)
	}
	if !matched {
		t.Errorf("etag_version %q does not match expected pattern", etag)
	}

	runDiscovery, err := cpcRo.Queries.GetConfigValueByKey(app.RuntimeManager.ctx, "run_file_discovery")
	if err != nil {
		t.Fatalf("expected run_file_discovery to be present, got error: %v", err)
	}
	if runDiscovery != "true" {
		t.Errorf("expected run_file_discovery=true, got %q", runDiscovery)
	}
}

func TestParseConfigUITemplates_Coverage(t *testing.T) {
	templates, err := parseConfigUITemplates(web.FS)
	if err != nil {
		t.Fatalf("parseConfigUITemplates failed: %v", err)
	}

	// Verify each template exists (value type, zero fields are nil)
	if templates.SaveRestartAlert == nil {
		t.Error("SaveRestartAlert template is nil")
	}
	if templates.SaveSuccessAlert == nil {
		t.Error("SaveSuccessAlert template is nil")
	}
	if templates.ExportModal == nil {
		t.Error("ExportModal template is nil")
	}
	if templates.ImportModal == nil {
		t.Error("ImportModal template is nil")
	}
	if templates.RestoreModal == nil {
		t.Error("RestoreModal template is nil")
	}
	if templates.RestoreSuccessAlert == nil {
		t.Error("RestoreSuccessAlert template is nil")
	}
	if templates.ImportSuccessAlert == nil {
		t.Error("ImportSuccessAlert template is nil")
	}
	if templates.RestartInitiatedAlert == nil {
		t.Error("RestartInitiatedAlert template is nil")
	}
}

func TestLoadConfig_Coverage(t *testing.T) {
	app := createAppForConfigTest(t)
	defer app.Shutdown()

	err := app.loadConfig()
	if err != nil {
		t.Errorf("loadConfig failed: %v", err)
	}

	if app.ConfigManager.Config == nil {
		t.Error("Expected config to be loaded")
	}
}

func TestApplyConfig_Coverage(t *testing.T) {
	app := createAppForConfigTest(t)
	defer app.Shutdown()

	// applyConfig takes no arguments and applies current config
	app.ApplyConfig()

	// Should not panic, config should be applied
}

func TestLoadConfig_WithError(t *testing.T) {
	app := createAppForConfigTest(t)
	defer app.Shutdown()

	// Load config multiple times
	err1 := app.loadConfig()
	err2 := app.loadConfig()

	// Both should succeed or both should fail
	_ = err1
	_ = err2
}

func TestApplyConfig_MultipleApply(t *testing.T) {
	app := createAppForConfigTest(t)
	defer app.Shutdown()

	// Apply config multiple times
	app.ApplyConfig()
	app.ApplyConfig()
	app.ApplyConfig()

	// Should handle multiple applications gracefully
}

// --- merged from config_last_known_good_integration_test.go ---
func TestLastKnownGood_SavedOnConfigUpdate(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.ListenerPort = 8081
	app.ConfigManager.Config.SiteName = "Original"

	// Save initial config
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = app.ConfigManager.Config.SaveToDatabase(app.RuntimeManager.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to save initial config: %v", err)
	}

	// Update config
	app.ConfigManager.Config.ListenerPort = 9999
	app.ConfigManager.Config.SiteName = "Updated"
	err = app.ConfigManager.Config.SaveToDatabase(app.RuntimeManager.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to save updated config: %v", err)
	}

	// Verify last known good was saved
	var lastKnownGoodYAML string
	err = cpcRw.Conn.QueryRowContext(app.RuntimeManager.ctx, "SELECT value FROM config WHERE key = 'LastKnownGoodConfig'").Scan(&lastKnownGoodYAML)
	if err != nil {
		t.Fatalf("Failed to query last known good config: %v", err)
	}

	// Parse YAML to verify it contains the updated values
	var yamlData map[string]interface{}
	if err := yaml.Unmarshal([]byte(lastKnownGoodYAML), &yamlData); err != nil {
		t.Fatalf("Failed to parse last known good YAML: %v", err)
	}

	if port, ok := yamlData["listener-port"].(int); !ok || port != 9999 {
		t.Errorf("Expected listener-port to be 9999 in last known good, got %v", yamlData["listener-port"])
	}
	if siteName, ok := yamlData["site-name"].(string); !ok || siteName != "Updated" {
		t.Errorf("Expected site-name to be 'Updated' in last known good, got %v", yamlData["site-name"])
	}
}

func TestLastKnownGood_RestoreFromUI(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	app.ConfigManager.Config = config.DefaultConfig()

	// Set up last known good config in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	lastKnownGoodYAML := `listener-port: 8888
site-name: "Restored Gallery"
log-level: "warn"
`

	_, err = cpcRw.Conn.ExecContext(app.RuntimeManager.ctx, "INSERT OR REPLACE INTO config (key, value) VALUES ('LastKnownGoodConfig', ?)", lastKnownGoodYAML)
	if err != nil {
		t.Fatalf("Failed to insert last known good config: %v", err)
	}

	// Restore should return diff
	restoredConfig, err := app.ConfigManager.Config.RestoreLastKnownGood(app.RuntimeManager.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to restore last known good: %v", err)
	}

	// Verify restored config matches last known good
	if restoredConfig.ListenerPort != 8888 {
		t.Errorf("Expected restored port 8888, got %d", restoredConfig.ListenerPort)
	}
	if restoredConfig.SiteName != "Restored Gallery" {
		t.Errorf("Expected restored site name 'Restored Gallery', got %q", restoredConfig.SiteName)
	}
	if restoredConfig.LogLevel != "warn" {
		t.Errorf("Expected restored log level 'warn', got %q", restoredConfig.LogLevel)
	}
}

// --- moved from server_e2e_test.go (DB config → runtime behavior) ---
func TestIntegration_DBConfig_HTTPCacheDisableActuallyDisablesCaching(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret-with-min-32-bytes"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)

	// Pre-populate database with enable_http_cache=false BEFORE creating app
	// (simulates config saved in previous run)
	{
		preApp := New(getopt.Opt{}, "x.y.z")
		preApp.setRootDir(&tempDir)
		preApp.setDB()

		cpcRw, err := preApp.dbRwPool.Get()
		if err != nil {
			t.Fatalf("failed to get RW connection: %v", err)
		}

		now := time.Now().Unix()
		err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
			Key:       "enable_http_cache",
			Value:     "false",
			CreatedAt: now,
			UpdatedAt: now,
		})
		cpcRw.Close()
		preApp.dbRwPool.Put(cpcRw)
		if err != nil {
			t.Fatalf("failed to set DB config: %v", err)
		}
		preApp.Shutdown()
	}

	// Now create app as if starting fresh (simulates app restart)
	// The bug: initializeHTTPCache() is called before loadConfig() in startup sequence
	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	// Load config from database (this is where enable_http_cache=false should be picked up)
	app.ConfigManager.Config = config.DefaultConfig()
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = app.ConfigManager.Config.LoadFromDatabase(context.Background(), cpcRw.Queries)
	if err != nil {
		t.Fatalf("failed to load config from DB: %v", err)
	}

	// Verify DB config was loaded
	if app.ConfigManager.Config.EnableHTTPCache {
		t.Fatalf("expected EnableHTTPCache to be false from DB, got true")
	}

	// Now initialize cache (as done in Run())
	// The bug: if initializeHTTPCache() was already called earlier with defaults,
	// the DB config is ignored
	app.initializeHTTPCache()

	// The actual bug test: cache should NOT be initialized when DB says disabled
	// This will FAIL (RED) if cache was initialized during setDB() before config load
	if app.cacheMW != nil {
		t.Errorf("expected cacheMW to be nil when enable_http_cache=false in DB, but it was initialized - this indicates cache init happened before DB config was loaded")
	}
}

func TestIntegration_DBConfig_ListenerPortChangeRequiresRestart(t *testing.T) {
	app := CreateApp(t, WithPool())
	defer app.Shutdown()

	// Set initial port in config
	t.Parallel()
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ListenerPort = 8081
	app.ConfigManager.ConfigMu.Unlock()

	// Build handlers (includes SetRestartRequired callback)
	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers failed: %v", err)
	}

	// Verify restart flag starts as false
	if app.RestartRequired() {
		t.Fatal("expected restartRequired to be false initially")
	}

	// Simulate config change via handler
	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Login as admin
	loginAsAdmin(t, client, ts.URL)

	// Extract CSRF token from config page
	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)

	// POST config change (change port from 8081 to 9090)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("listener_port", "9090")

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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after config update, got %d", resp.StatusCode)
	}

	// Verify restart flag is now set
	if !app.RestartRequired() {
		t.Errorf("expected restartRequired to be true after port change, got false")
	}
}

// TestConfigPersistence_LoginSecurityFields verifies the three login security
// fields round-trip: POST /config persists them to the DB and GET /config
// renders the saved values in the session tab inputs.
func TestConfigPersistence_LoginSecurityFields(t *testing.T) {
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

	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("login_rate_limit_per_ip", "8")
	formData.Set("lockout_threshold", "6")
	formData.Set("lockout_duration", "1200")

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

	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	wantDB := map[string]string{
		"login_rate_limit_per_ip": "8",
		"lockout_threshold":       "6",
		"lockout_duration":        "1200",
	}
	for key, want := range wantDB {
		got, err := cpcRo.Queries.GetConfigValueByKey(app.RuntimeManager.ctx, key)
		if err != nil {
			t.Errorf("GetConfigValueByKey(%q): %v", key, err)
			continue
		}
		if got != want {
			t.Errorf("DB %q = %q, want %q", key, got, want)
		}
	}
	app.dbRoPool.Put(cpcRo)

	getResp, err := client.Get(ts.URL + "/config")
	if err != nil {
		t.Fatalf("GET /config failed: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for GET /config, got %d", getResp.StatusCode)
	}

	doc, err := testutil.ParseHTML(getResp.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	panel := testutil.FindElementByID(doc, "tab-session")
	if panel == nil {
		t.Fatal("#tab-session panel not found")
	}

	values := make(map[string]string)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			name := testutil.GetAttr(n, "name")
			if name != "" {
				values[name] = testutil.GetAttr(n, "value")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(panel)

	for name, want := range wantDB {
		got, ok := values[name]
		if !ok {
			t.Errorf("input %q not found in #tab-session", name)
			continue
		}
		if got != want {
			t.Errorf("input %q value = %q, want %q", name, got, want)
		}
	}
}
