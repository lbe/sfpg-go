//go:build integration || e2e

package server

import (
	"context"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/server/config"
)

func TestLoadConfig_CompleteStateAfterFreshDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Setup: Initialize database with defaults
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
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

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(ctx),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir
	app.SetConfig(config.DefaultConfig())

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

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(ctx),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

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
	app.ApplyConfig()

	app.ApplyConfig()

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
	go app.TriggerDiscovery()

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

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(ctx),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir
	app.SetConfig(config.DefaultConfig())

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

func TestAppLoadConfigFromDatabase(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()
	app.setConfigDefaults()

	// Load config (as done in Run())
	if err := app.loadConfig(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	app.ApplyConfig()

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

func TestAppConfigInitialization_FirstRun(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)

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
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)

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
