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

func TestLoadFromOpt_LoginRateLimitPerIP(t *testing.T) {
	opt := getopt.Opt{LoginRateLimitPerIP: getopt.OptInt{Int: 5, IsSet: true}}
	cfg := DefaultConfig()
	cfg.LoadFromOpt(opt)
	if cfg.LoginRateLimitPerIP != 5 {
		t.Fatalf("got %d", cfg.LoginRateLimitPerIP)
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

func TestConfigHelp_DatabaseSchema_HelpTextColumn(t *testing.T) {
	db, _, ctx := setupTestDB(t)
	defer db.Close()

	var columnExists bool
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) > 0
		FROM pragma_table_info('config')
		WHERE name = 'help_text'
	`).Scan(&columnExists)
	if err != nil {
		t.Fatalf("failed to query table info: %v", err)
	}

	if !columnExists {
		t.Error("help_text column does not exist in config table")
	}
}

func TestConfigHelp_DatabaseSchema_ExampleValueColumn(t *testing.T) {
	db, _, ctx := setupTestDB(t)
	defer db.Close()

	var columnExists bool
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) > 0
		FROM pragma_table_info('config')
		WHERE name = 'example_value'
	`).Scan(&columnExists)
	if err != nil {
		t.Fatalf("failed to query table info: %v", err)
	}

	if !columnExists {
		t.Error("example_value column does not exist in config table")
	}
}

func TestConfigHelp_RetrieveHelpText(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	// Insert a config setting with help text
	_, err := db.ExecContext(ctx, `
		INSERT INTO config (key, value, help_text, example_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, UNIXEPOCH('now'), UNIXEPOCH('now'))
	`, "test_setting", "test_value", "This is help text for the test setting", "example_value")
	if err != nil {
		t.Fatalf("Failed to insert test config: %v", err)
	}

	// Retrieve help text via queries
	_, err = q.GetConfigValueByKey(ctx, "test_setting")
	if err != nil {
		t.Fatalf("GetConfigValueByKey failed: %v", err)
	}

	// Now query directly for the metadata columns
	var helpText, exampleValue string
	err = db.QueryRowContext(ctx, `
		SELECT help_text, example_value
		FROM config
		WHERE key = ?
	`, "test_setting").Scan(&helpText, &exampleValue)
	if err != nil {
		t.Fatalf("Failed to retrieve help text: %v", err)
	}

	if helpText != "This is help text for the test setting" {
		t.Errorf("Expected help text 'This is help text for the test setting', got '%s'", helpText)
	}

	if exampleValue != "example_value" {
		t.Errorf("Expected example value 'example_value', got '%s'", exampleValue)
	}
}

// TestIdentifyChanges_AllFieldTypes tests that IdentifyChanges detects changes in every
// config field individually, providing per-field failure isolation beyond the
// combined TestIdentifyChanges test.
func TestIdentifyChanges_AllFieldTypes(t *testing.T) {
	cfg1 := DefaultConfig()

	tests := []struct {
		name     string
		modify   func(*Config)
		expected string
	}{
		{"listener_address", func(c *Config) { c.ListenerAddress = "1.2.3.4" }, "listener-address"},
		{"listener_port", func(c *Config) { c.ListenerPort = 9999 }, "listener-port"},
		{"log_directory", func(c *Config) { c.LogDirectory = "/tmp/logs" }, "log-directory"},
		{"log_level", func(c *Config) { c.LogLevel = "warn" }, "log-level"},
		{"log_rollover", func(c *Config) { c.LogRollover = "daily" }, "log-rollover"},
		{"log_retention_count", func(c *Config) { c.LogRetentionCount = 10 }, "log-retention-count"},
		{"site_name", func(c *Config) { c.SiteName = "New Site" }, "site-name"},
		{"current_theme", func(c *Config) { c.CurrentTheme = "light" }, "current-theme"},
		{"image_directory", func(c *Config) { c.ImageDirectory = "/tmp/images" }, "image-directory"},
		{"session_max_age", func(c *Config) { c.SessionMaxAge = 3600 }, "session-max-age"},
		{"session_http_only", func(c *Config) { c.SessionHttpOnly = false }, "session-http-only"},
		{"session_secure", func(c *Config) { c.SessionSecure = false }, "session-secure"},
		{"session_same_site", func(c *Config) { c.SessionSameSite = "Strict" }, "session-same-site"},

		{"enable_http_cache", func(c *Config) { c.EnableHTTPCache = false }, "http-cache"},
		{"cache_max_size", func(c *Config) { c.CacheMaxSize = 1000000 }, "cache-max-size"},
		{"cache_max_time", func(c *Config) { c.CacheMaxTime = 1 * time.Hour }, "cache-max-time"},
		{"cache_max_entry_size", func(c *Config) { c.CacheMaxEntrySize = 5000000 }, "cache-max-entry-size"},
		{"cache_cleanup_interval", func(c *Config) { c.CacheCleanupInterval = 10 * time.Minute }, "cache-cleanup-interval"},
		{"db_max_pool_size", func(c *Config) { c.DBMaxPoolSize = 50 }, "db-max-pool-size"},
		{"db_min_idle_connections", func(c *Config) { c.DBMinIdleConnections = 5 }, "db-min-idle-connections"},
		{"db_optimize_interval", func(c *Config) { c.DBOptimizeInterval = 2 * time.Hour }, "db-optimize-interval"},
		{"db_pool_monitor_interval", func(c *Config) { c.DBPoolMonitorInterval = 30 * time.Second }, "db-pool-monitor-interval"},
		{"worker_pool_max", func(c *Config) { c.WorkerPoolMax = 20 }, "worker-pool-max"},
		{"worker_pool_min_idle", func(c *Config) { c.WorkerPoolMinIdle = 5 }, "worker-pool-min-idle"},
		{"worker_pool_max_idle_time", func(c *Config) { c.WorkerPoolMaxIdleTime = 20 * time.Second }, "worker-pool-max-idle-time"},
		{"queue_size", func(c *Config) { c.QueueSize = 5000 }, "queue-size"},
		{"run_file_discovery", func(c *Config) { c.RunFileDiscovery = false }, "discover"},
		{"restart_after_discovery", func(c *Config) { c.RestartAfterDiscovery = true }, "restart-after-discovery"},
		{"enable_cache_preload", func(c *Config) { c.EnableCachePreload = false }, "enable-cache-preload"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			other := DefaultConfig()
			tt.modify(other)
			changes := cfg1.IdentifyChanges(other)
			if !contains(changes, tt.expected) {
				t.Errorf("IdentifyChanges should detect %q, got changes: %v", tt.expected, changes)
			}
		})
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
