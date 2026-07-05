package config

import (
	"context"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
)

func TestLoadFromDatabase(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	// Insert some test config values
	now := time.Now().Unix()
	err := q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "listener_port",
		Value:     "9090",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to insert test config: %v", err)
	}

	err = q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "log_level",
		Value:     "info",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to insert test config: %v", err)
	}

	// Load config from database
	cfg := DefaultConfig()
	err = cfg.LoadFromDatabase(ctx, q)
	if err != nil {
		t.Fatalf("failed to load config from database: %v", err)
	}

	// Verify loaded values
	if cfg.ListenerPort != 9090 {
		t.Errorf("expected ListenerPort to be 9090, got %d", cfg.ListenerPort)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected LogLevel to be 'info', got %q", cfg.LogLevel)
	}

	// Verify defaults are still present for unset values
	if cfg.ListenerAddress != "0.0.0.0" {
		t.Errorf("expected ListenerAddress to remain default '0.0.0.0', got %q", cfg.ListenerAddress)
	}
}

// TestLoadFromOpt verifies loading configuration from getopt.Opt.

func TestLoadFromOpt(t *testing.T) {
	opt := getopt.Opt{
		Port:               getopt.OptInt{Int: 9090, IsSet: true},
		EnableCompression:  getopt.OptBool{Bool: true, IsSet: true},
		EnableHTTPCache:    getopt.OptBool{Bool: false, IsSet: true},
		EnableCachePreload: getopt.OptBool{Bool: false, IsSet: true},
		RunFileDiscovery:   getopt.OptBool{Bool: false, IsSet: true},
	}

	cfg := DefaultConfig()
	cfg.LoadFromOpt(opt)

	if cfg.EnableCachePreload {
		t.Errorf("expected EnableCachePreload to be false after LoadFromOpt, got %v", cfg.EnableCachePreload)
	}
	if cfg.ListenerPort != 9090 {
		t.Errorf("expected ListenerPort to be 9090, got %d", cfg.ListenerPort)
	}
	if !cfg.ServerCompressionEnable {
		t.Errorf("expected ServerCompressionEnable to be true, got %v", cfg.ServerCompressionEnable)
	}
	if cfg.EnableHTTPCache {
		t.Errorf("expected EnableHTTPCache to be false, got %v", cfg.EnableHTTPCache)
	}
	if cfg.RunFileDiscovery {
		t.Errorf("expected RunFileDiscovery to be false, got %v", cfg.RunFileDiscovery)
	}
}

// TestLoadFromOpt_SessionOptions verifies session options from getopt.Opt override config defaults

func TestLoadFromOpt_SessionOptions(t *testing.T) {
	opt := getopt.Opt{
		SessionSecure:   getopt.OptBool{Bool: false, IsSet: true},
		SessionHttpOnly: getopt.OptBool{Bool: false, IsSet: true},
		SessionMaxAge:   getopt.OptInt{Int: 7200, IsSet: true},
		SessionSameSite: getopt.OptString{String: "Strict", IsSet: true},
	}

	cfg := DefaultConfig()
	// Default config has SessionSecure=true, SessionHttpOnly=true
	if !cfg.SessionSecure {
		t.Fatal("expected default SessionSecure=true")
	}
	if !cfg.SessionHttpOnly {
		t.Fatal("expected default SessionHttpOnly=true")
	}

	// LoadFromOpt should override with env var values
	cfg.LoadFromOpt(opt)

	if cfg.SessionSecure {
		t.Errorf("expected SessionSecure to be false after LoadFromOpt, got %v", cfg.SessionSecure)
	}
	if cfg.SessionHttpOnly {
		t.Errorf("expected SessionHttpOnly to be false after LoadFromOpt, got %v", cfg.SessionHttpOnly)
	}
	if cfg.SessionMaxAge != 7200 {
		t.Errorf("expected SessionMaxAge=7200 after LoadFromOpt, got %v", cfg.SessionMaxAge)
	}
	if cfg.SessionSameSite != "Strict" {
		t.Errorf("expected SessionSameSite=Strict after LoadFromOpt, got %v", cfg.SessionSameSite)
	}
}

// TestMergeDefaults verifies that MergeDefaults correctly applies defaults to unset values.

func TestMergeDefaults(t *testing.T) {
	cfg := &Config{
		ListenerPort: 9090,
		LogLevel:     "error",
		// Other fields are zero values
	}

	defaults := DefaultConfig()
	cfg.MergeDefaults(defaults)

	// Verify explicitly set values are preserved
	if cfg.ListenerPort != 9090 {
		t.Errorf("expected ListenerPort to remain 9090, got %d", cfg.ListenerPort)
	}
	if cfg.LogLevel != "error" {
		t.Errorf("expected LogLevel to remain 'error', got %q", cfg.LogLevel)
	}

	// Verify zero values are filled with defaults
	if cfg.ListenerAddress != defaults.ListenerAddress {
		t.Errorf("expected ListenerAddress to be default %q, got %q", defaults.ListenerAddress, cfg.ListenerAddress)
	}
	if cfg.LogRetentionCount != defaults.LogRetentionCount {
		t.Errorf("expected LogRetentionCount to be default %d, got %d", defaults.LogRetentionCount, cfg.LogRetentionCount)
	}
}

// TestSaveToDatabase verifies saving configuration to database.

func TestSaveToDatabase(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	cfg := DefaultConfig()
	cfg.ListenerPort = 9090
	cfg.LogLevel = "warn"

	err := cfg.SaveToDatabase(ctx, q)
	if err != nil {
		t.Fatalf("failed to save config to database: %v", err)
	}

	// Verify values were saved
	portValue, err := q.GetConfigValueByKey(ctx, "listener_port")
	if err != nil {
		t.Fatalf("failed to get listener_port from database: %v", err)
	}
	if portValue != "9090" {
		t.Errorf("expected listener_port to be '9090', got %q", portValue)
	}

	logLevelValue, err := q.GetConfigValueByKey(ctx, "log_level")
	if err != nil {
		t.Fatalf("failed to get log_level from database: %v", err)
	}
	if logLevelValue != "warn" {
		t.Errorf("expected log_level to be 'warn', got %q", logLevelValue)
	}
}

// TestLoadFromDatabase_MissingKeys verifies that missing keys are handled gracefully.

func TestLoadFromDatabase_MissingKeys(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	// Don't insert any config values - database is empty
	cfg := DefaultConfig()
	err := cfg.LoadFromDatabase(ctx, q)
	if err != nil {
		t.Fatalf("failed to load config from empty database: %v", err)
	}

	// Verify defaults are still present
	if cfg.ListenerPort != 8081 {
		t.Errorf("expected ListenerPort to remain default 8081, got %d", cfg.ListenerPort)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel to remain default 'debug', got %q", cfg.LogLevel)
	}
}

// TestSaveToDatabase_UpdatesExisting verifies that saving updates existing keys.

func TestSaveToDatabase_UpdatesExisting(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	// Insert initial value
	now := time.Now().Unix()
	err := q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "listener_port",
		Value:     "8080",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to insert initial config: %v", err)
	}

	// Save new value
	cfg := DefaultConfig()
	cfg.ListenerPort = 9090
	err = cfg.SaveToDatabase(ctx, q)
	if err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// Verify value was updated
	portValue, err := q.GetConfigValueByKey(ctx, "listener_port")
	if err != nil {
		t.Fatalf("failed to get listener_port: %v", err)
	}
	if portValue != "9090" {
		t.Errorf("expected listener_port to be updated to '9090', got %q", portValue)
	}
}

// TestTypeConversion verifies that type conversions work correctly.

func TestTypeConversion(t *testing.T) {
	cfg := DefaultConfig()

	// Test string to int conversion
	err := cfg.SetValueFromString("listener_port", "9999")
	if err != nil {
		t.Fatalf("failed to set listener_port: %v", err)
	}
	if cfg.ListenerPort != 9999 {
		t.Errorf("expected ListenerPort to be 9999, got %d", cfg.ListenerPort)
	}

	// Test string to bool conversion
	err = cfg.SetValueFromString("server_compression_enable", "false")
	if err != nil {
		t.Fatalf("failed to set server_compression_enable: %v", err)
	}
	if cfg.ServerCompressionEnable {
		t.Error("expected ServerCompressionEnable to be false")
	}

	// Test string to duration conversion
	err = cfg.SetValueFromString("cache_max_time", "24h")
	if err != nil {
		t.Fatalf("failed to set cache_max_time: %v", err)
	}
	if cfg.CacheMaxTime != 24*time.Hour {
		t.Errorf("expected CacheMaxTime to be 24h, got %v", cfg.CacheMaxTime)
	}

	// Test invalid type conversion
	err = cfg.SetValueFromString("listener_port", "invalid")
	if err == nil {
		t.Error("expected error for invalid port value")
	}
}

// TestJSONSerialization verifies that complex types (arrays, durations) serialize correctly.

func TestIdentifyChanges(t *testing.T) {
	cfg := DefaultConfig()
	other := DefaultConfig()
	other.ListenerAddress = "127.0.0.1"
	other.ListenerPort = 9090
	other.LogDirectory = "/tmp/logs"
	other.LogLevel = "error"
	other.LogRollover = "daily"
	other.LogRetentionCount = cfg.LogRetentionCount + 1
	other.SiteName = "New Name"
	other.CurrentTheme = "light"
	other.ImageDirectory = "/tmp/images"
	other.SessionMaxAge = cfg.SessionMaxAge + 1
	other.SessionHttpOnly = false
	other.SessionSecure = false
	other.SessionSameSite = "Strict"
	other.ServerCompressionEnable = false
	other.EnableHTTPCache = false
	other.CacheMaxSize = cfg.CacheMaxSize + 1
	other.CacheMaxTime = cfg.CacheMaxTime + time.Second
	other.CacheMaxEntrySize = cfg.CacheMaxEntrySize + 1
	other.CacheCleanupInterval = cfg.CacheCleanupInterval + time.Second
	other.DBMaxPoolSize = cfg.DBMaxPoolSize + 1
	other.DBMinIdleConnections = cfg.DBMinIdleConnections + 1
	other.DBOptimizeInterval = cfg.DBOptimizeInterval + time.Second
	other.WorkerPoolMax = cfg.WorkerPoolMax + 1
	other.WorkerPoolMinIdle = cfg.WorkerPoolMinIdle + 1
	other.WorkerPoolMaxIdleTime = cfg.WorkerPoolMaxIdleTime + time.Second
	other.QueueSize = cfg.QueueSize + 1
	other.EnableCachePreload = false
	other.RunFileDiscovery = false
	changes := cfg.IdentifyChanges(other)
	if !contains(changes, "log-level") || !contains(changes, "listener-port") || !contains(changes, "log-directory") {
		t.Fatalf("expected key changes, got %v", changes)
	}
}

func TestSaveToDatabase_ErrorPaths(t *testing.T) {
	cfg := DefaultConfig()

	t.Run("returns error when upsert fails", func(t *testing.T) {
		saver := &mockSaver{failKey: "listener_port"}
		err := cfg.SaveToDatabase(context.Background(), saver)
		if err == nil {
			t.Fatal("expected error when upsert fails")
		}
	})

	t.Run("ignores last known good save error", func(t *testing.T) {
		saver := &mockSaver{failKey: "LastKnownGoodConfig"}
		if err := cfg.SaveToDatabase(context.Background(), saver); err != nil {
			t.Fatalf("expected SaveToDatabase to succeed, got %v", err)
		}

		found := false
		for _, call := range saver.calls {
			if call.Key == "LastKnownGoodConfig" {
				found = true
			}
		}
		if !found {
			t.Fatal("expected LastKnownGoodConfig to be saved")
		}
	})
}
