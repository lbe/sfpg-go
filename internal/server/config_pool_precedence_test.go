package server

import (
	"context"
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
)

// TestDBPoolPrecedence_PoolsIgnoreDatabaseConfig verifies that pools created during
// app initialization DO NOT respect DB pool configuration values stored in the database.
//
// BEHAVIOR: When the database is pre-populated with custom DBMaxPoolSize and DBMinIdleConnections,
// those values should be used to configure the connection pools. Currently, this fails because
// pools are created BEFORE the database config is loaded.
//
// EXPECTED: Pools are created with custom sizes from database config (25/8)
// ACTUAL (RED): Pools are created with hardcoded defaults (100/10) because setDB() is called
//
//	before app.loadConfig() in the startup sequence
//
// ROOT CAUSE: internal/server/app.go line 600 calls setDB() with app.config=nil, which causes
//
//	database.Setup to use hardcoded defaults. The config is loaded at line 635,
//	but by then the pools already exist and cannot be reconfigured.
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
	app1.config.DBMaxPoolSize = 25
	app1.config.DBMinIdleConnections = 8
	configService := config.NewService(app1.dbRwPool, app1.dbRoPool)
	if err := configService.Save(context.Background(), app1.config); err != nil {
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
	app2.setDB() // <- Pools created HERE with app.config=nil
	app2.setConfigDefaults()
	if err := app2.loadConfig(); err != nil { // <- Config loaded HERE (too late!)
		t.Fatalf("Second app loadConfig failed: %v", err)
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

// TestDBPoolPrecedence_ConfigLoadedAfterPoolCreation verifies that the config loading happens
// too late in the startup sequence to affect pool configuration.
//
// BEHAVIOR: The startup sequence should load configuration before creating connection pools,
// so that pool size settings are honored. Currently, the sequence is wrong.
//
// EXPECTED (after fix): Both app.config and pools reflect custom sizes
// ACTUAL (RED): app.config gets the custom values, but pools were already created
//
//	with hardcoded defaults and cannot be reconfigured
//
// This test demonstrates the timing issue: app.config is loaded AFTER setDB(),
// making it impossible for pool configuration to take effect.
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
	app.config = config.DefaultConfig()
	app.config.DBMaxPoolSize = 30
	app.config.DBMinIdleConnections = 12

	// Save to database
	if err := configService.Save(context.Background(), app.config); err != nil {
		t.Fatalf("Failed to save config to database: %v", err)
	}

	// Now load config (this will read from the database)
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// ASSERTION: After loadConfig(), app.config should have the custom values
	if app.config.DBMaxPoolSize != 30 {
		t.Errorf("FAIL: app.config.DBMaxPoolSize = %d, want 30", app.config.DBMaxPoolSize)
	}
	if app.config.DBMinIdleConnections != 12 {
		t.Errorf("FAIL: app.config.DBMinIdleConnections = %d, want 12", app.config.DBMinIdleConnections)
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
