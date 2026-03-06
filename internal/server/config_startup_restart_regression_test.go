//go:build integration || e2e

// Package server contains regression tests for config startup and restart behaviors.
// These tests verify that configuration values saved in the database are properly
// applied during app startup and when configuration is reloaded at runtime.
package server

import (
	"context"
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
)

// TestStartupWithDBConfig_PoolSizeHonored verifies that when pool configuration
// is saved in the database, a new app instance honors those values during startup.
//
// This is a regression test for the pool precedence bug where pools were created
// with hardcoded defaults (100/10) instead of database values because setDB() was
// called before loadConfig() in the startup sequence.
//
// Test Flow:
// 1. Create first app instance
// 2. Save custom pool sizes to database (35 max, 15 min idle)
// 3. Shutdown first app
// 4. Create second app instance using same database
// 5. Verify pools are configured with database values, not defaults
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
	app1.config.DBMaxPoolSize = 35
	app1.config.DBMinIdleConnections = 15
	configService := config.NewService(app1.dbRwPool, app1.dbRoPool)
	if err := configService.Save(context.Background(), app1.config); err != nil {
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
	app2.setDB()             // Now uses app.config if available
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

	// Verify app.config also has the correct values
	app2.configMu.RLock()
	configMaxPool := app2.config.DBMaxPoolSize
	configMinIdle := app2.config.DBMinIdleConnections
	app2.configMu.RUnlock()

	if configMaxPool != 35 {
		t.Errorf("app2.config.DBMaxPoolSize = %d, want 35", configMaxPool)
	}
	if configMinIdle != 15 {
		t.Errorf("app2.config.DBMinIdleConnections = %d, want 15", configMinIdle)
	}

	app2.Shutdown()
}

// TestRestartWithModifiedDBConfig_AppliesNewValues verifies that when configuration
// is modified in the database and loadConfig() is called, the new values are applied
// to the app's in-memory config state.
//
// This test covers runtime config reload scenarios (not pool reconfiguration, which
// requires full restart). It ensures that the config loading mechanism properly reads
// and applies database values.
//
// Test Flow:
// 1. Create app instance with default config
// 2. Verify initial config values
// 3. Modify config in database (change compression, cache, port settings)
// 4. Call loadConfig() to reload from database
// 5. Verify new values are applied to app.config
//
// Note: Pool sizes cannot be changed at runtime without full restart, but other
// config values (compression, cache, session settings) should be reloadable.
func TestRestartWithModifiedDBConfig_AppliesNewValues(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Verify initial config values (from defaults)
	app.configMu.RLock()
	initialCompression := app.config.ServerCompressionEnable
	initialCache := app.config.EnableHTTPCache
	app.configMu.RUnlock()

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
	if err := configService.Save(context.Background(), dbConfig); err != nil {
		t.Fatalf("Failed to save modified config to database: %v", err)
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

	// Verify new values are applied to app.config
	app.configMu.RLock()
	newCompression := app.config.ServerCompressionEnable
	newCache := app.config.EnableHTTPCache
	newPort := app.config.ListenerPort
	newMaxPool := app.config.DBMaxPoolSize
	newMinIdle := app.config.DBMinIdleConnections
	app.configMu.RUnlock()

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
