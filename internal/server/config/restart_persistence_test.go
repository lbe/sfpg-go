package config

import (
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// TestRestartPersistence_SaveAndLoadCycle verifies that configuration values
// survive a save-then-load cycle, simulating what happens during a server restart
// where config is saved to the database and then reloaded on startup.
func TestRestartPersistence_SaveAndLoadCycle(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	// Create a config with non-default values
	cfg := DefaultConfig()
	cfg.ListenerPort = 8888
	cfg.SiteName = "Restart Test Gallery"
	cfg.LogLevel = "warn"
	cfg.ServerCompressionEnable = false
	cfg.EnableHTTPCache = true
	cfg.RunFileDiscovery = false

	// Save to database (simulates what happens during a config update)
	err := cfg.SaveToDatabase(ctx, q)
	if err != nil {
		t.Fatalf("SaveToDatabase failed: %v", err)
	}

	// Verify values were persisted in database
	portValue, err := q.GetConfigValueByKey(ctx, "listener_port")
	if err != nil {
		t.Fatalf("failed to get listener_port: %v", err)
	}
	if portValue != "8888" {
		t.Errorf("expected listener_port to be '8888', got %q", portValue)
	}

	siteNameValue, err := q.GetConfigValueByKey(ctx, "site_name")
	if err != nil {
		t.Fatalf("failed to get site_name: %v", err)
	}
	if siteNameValue != "Restart Test Gallery" {
		t.Errorf("expected site_name to be 'Restart Test Gallery', got %q", siteNameValue)
	}

	logLevelValue, err := q.GetConfigValueByKey(ctx, "log_level")
	if err != nil {
		t.Fatalf("failed to get log_level: %v", err)
	}
	if logLevelValue != "warn" {
		t.Errorf("expected log_level to be 'warn', got %q", logLevelValue)
	}

	// Load config from database (simulates what happens on server restart)
	loadedCfg := DefaultConfig()
	err = loadedCfg.LoadFromDatabase(ctx, q)
	if err != nil {
		t.Fatalf("LoadFromDatabase failed: %v", err)
	}

	// Verify values were restored correctly
	if loadedCfg.ListenerPort != 8888 {
		t.Errorf("expected ListenerPort to be 8888 after reload, got %d", loadedCfg.ListenerPort)
	}
	if loadedCfg.SiteName != "Restart Test Gallery" {
		t.Errorf("expected SiteName to be 'Restart Test Gallery' after reload, got %q", loadedCfg.SiteName)
	}
	if loadedCfg.LogLevel != "warn" {
		t.Errorf("expected LogLevel to be 'warn' after reload, got %q", loadedCfg.LogLevel)
	}
	if loadedCfg.ServerCompressionEnable {
		t.Error("expected ServerCompressionEnable to be false after reload")
	}
	if !loadedCfg.EnableHTTPCache {
		t.Error("expected EnableHTTPCache to be true after reload")
	}
	if loadedCfg.RunFileDiscovery {
		t.Error("expected RunFileDiscovery to be false after reload")
	}
}

// TestRestartPersistence_ConfigValueOverridesOpt verifies that config values
// loaded from the database take precedence over default values, matching the
// expected behavior after restart where database-saved config is authoritative.
func TestRestartPersistence_ConfigValueOverridesOpt(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	// Save compression=false and cache=true in the database
	now := time.Now().Unix()
	err := q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "server_compression_enable",
		Value:     "false",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set compression: %v", err)
	}
	err = q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "enable_http_cache",
		Value:     "true",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	// Load from DB
	cfg := DefaultConfig()
	err = cfg.LoadFromDatabase(ctx, q)
	if err != nil {
		t.Fatalf("LoadFromDatabase failed: %v", err)
	}

	// Verify DB values are loaded
	if cfg.ServerCompressionEnable {
		t.Error("expected ServerCompressionEnable to be false from DB")
	}
	if !cfg.EnableHTTPCache {
		t.Error("expected EnableHTTPCache to be true from DB")
	}

	// Simulate opt (startup) values that conflict with DB
	// (e.g., old CLI flags that should NOT override after restart since they are not set)
	// DB values should persist since opt values are not set (IsSet=false)
	// Actual opt override testing is in TestLoadFromOpt
}
