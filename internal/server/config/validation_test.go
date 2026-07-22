package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestValidate_InvalidPort(t *testing.T) {
	cfg := DefaultConfig()

	// Test port too low
	cfg.ListenerPort = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for port 0, got nil")
	}

	// Test port too high
	cfg.ListenerPort = 65536
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for port 65536, got nil")
	}

	// Test valid port
	cfg.ListenerPort = 8081
	if err := cfg.Validate(); err != nil {
		t.Errorf("Expected no error for valid port, got: %v", err)
	}
}

// TestValidate_InvalidLogLevel verifies that Validate rejects invalid log levels.

func TestValidate_InvalidLogLevel(t *testing.T) {
	cfg := DefaultConfig()

	// Test invalid log level
	cfg.LogLevel = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for invalid log level, got nil")
	}

	// Test valid log levels
	validLevels := []string{"debug", "info", "warn", "error", "DEBUG", "INFO", "WARN", "ERROR"}
	for _, level := range validLevels {
		cfg.LogLevel = level
		if err := cfg.Validate(); err != nil {
			t.Errorf("Expected no error for log level %q, got: %v", level, err)
		}
	}
}

// TestValidate_InvalidLogRollover verifies that Validate rejects invalid log rollover values.

func TestValidate_InvalidLogRollover(t *testing.T) {
	cfg := DefaultConfig()

	// Test invalid rollover
	cfg.LogRollover = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for invalid log rollover, got nil")
	}

	// Test valid rollovers
	validRollovers := []string{"daily", "weekly", "monthly", "DAILY", "WEEKLY", "MONTHLY"}
	for _, rollover := range validRollovers {
		cfg.LogRollover = rollover
		if err := cfg.Validate(); err != nil {
			t.Errorf("Expected no error for log rollover %q, got: %v", rollover, err)
		}
	}
}

// TestValidate_InvalidLogRetentionCount verifies that Validate rejects invalid retention counts.

func TestValidate_InvalidLogRetentionCount(t *testing.T) {
	cfg := DefaultConfig()

	// Test retention count too low
	cfg.LogRetentionCount = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for retention count 0, got nil")
	}

	// Test valid retention count
	cfg.LogRetentionCount = 7
	if err := cfg.Validate(); err != nil {
		t.Errorf("Expected no error for valid retention count, got: %v", err)
	}
}

// TestValidate_InvalidSessionSameSite verifies that Validate rejects invalid session same-site values.

func TestValidate_InvalidSessionSameSite(t *testing.T) {
	cfg := DefaultConfig()

	// Test invalid same-site
	cfg.SessionSameSite = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for invalid session same-site, got nil")
	}

	// Test valid same-site values
	validSameSite := []string{"Lax", "Strict", "None"}
	for _, sameSite := range validSameSite {
		cfg.SessionSameSite = sameSite
		if err := cfg.Validate(); err != nil {
			t.Errorf("Expected no error for session same-site %q, got: %v", sameSite, err)
		}
	}
}

// TestValidate_NegativeCacheSizes verifies that Validate rejects negative cache sizes.

func TestValidate_NegativeCacheSizes(t *testing.T) {
	cfg := DefaultConfig()

	// Test negative cache max size
	cfg.CacheMaxSize = -1
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for negative cache max size, got nil")
	}

	// Test negative cache max entry size
	cfg.CacheMaxSize = 0 // Reset
	cfg.CacheMaxEntrySize = -1
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for negative cache max entry size, got nil")
	}
}

// TestValidate_InvalidDBPoolSizes verifies that Validate rejects invalid database pool sizes.

func TestValidate_InvalidDBPoolSizes(t *testing.T) {
	cfg := DefaultConfig()

	// Test max pool size too low
	cfg.DBMaxPoolSize = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for max pool size 0, got nil")
	}

	// Test min idle connections negative
	cfg.DBMaxPoolSize = 100
	cfg.DBMinIdleConnections = -1
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for negative min idle connections, got nil")
	}

	// Test min idle exceeds max pool size
	cfg.DBMinIdleConnections = 150
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error when min idle exceeds max pool size, got nil")
	}
}

// TestValidate_InvalidWorkerPoolSizes verifies that Validate rejects invalid worker pool sizes.

func TestValidate_InvalidWorkerPoolSizes(t *testing.T) {
	cfg := DefaultConfig()

	// Test negative worker pool max
	cfg.WorkerPoolMax = -1
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for negative worker pool max, got nil")
	}

	// Test negative worker pool min idle
	cfg.WorkerPoolMax = 10
	cfg.WorkerPoolMinIdle = -1
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for negative worker pool min idle, got nil")
	}

	// Test min idle exceeds max (when both are set)
	cfg.WorkerPoolMax = 10
	cfg.WorkerPoolMinIdle = 15
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error when min idle exceeds max, got nil")
	}

	// Test valid: 0 means auto-calculate
	cfg.WorkerPoolMax = 0
	cfg.WorkerPoolMinIdle = 0
	if err := cfg.Validate(); err != nil {
		t.Errorf("Expected no error for auto-calculate (0), got: %v", err)
	}
}

// TestValidate_InvalidQueueSize verifies that Validate rejects invalid queue sizes.

func TestValidate_InvalidQueueSize(t *testing.T) {
	cfg := DefaultConfig()

	// Test queue size too low
	cfg.QueueSize = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for queue size 0, got nil")
	}

	// Test valid queue size
	cfg.QueueSize = 10000
	if err := cfg.Validate(); err != nil {
		t.Errorf("Expected no error for valid queue size, got: %v", err)
	}
}

// TestValidate_ValidConfig verifies that Validate accepts a fully valid configuration.

func TestValidate_ValidConfig(t *testing.T) {
	cfg := DefaultConfig()

	if err := cfg.Validate(); err != nil {
		t.Errorf("Expected no error for default config, got: %v", err)
	}
}

// TestValidate_MinMaxRelationship verifies that Validate enforces min <= max relationships.

func TestValidate_MinMaxRelationship(t *testing.T) {
	cfg := DefaultConfig()

	// DB pool: min > max
	cfg.DBMaxPoolSize = 10
	cfg.DBMinIdleConnections = 15
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error when DB min idle > max pool size, got nil")
	}

	// Worker pool: min > max (when both set)
	cfg.DBMinIdleConnections = 5 // Reset
	cfg.WorkerPoolMax = 10
	cfg.WorkerPoolMinIdle = 15
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error when worker pool min idle > max, got nil")
	}
}

// TestValidate_CriticalSettings verifies that critical settings are validated.
// Critical settings are those that could break the application if invalid.

func TestValidate_CriticalSettings(t *testing.T) {
	cfg := DefaultConfig()

	// Invalid port (critical - server won't start)
	cfg.ListenerPort = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for critical setting (port), got nil")
	}

	// Invalid DB pool size (critical - database won't work)
	cfg.ListenerPort = 8081 // Reset
	cfg.DBMaxPoolSize = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for critical setting (DB pool size), got nil")
	}
}

// TestValidate_DurationFields verifies that duration fields are handled correctly.
// Note: Duration validation happens during parsing, not in Validate()

func TestValidate_DurationFields(t *testing.T) {
	cfg := DefaultConfig()

	// Valid durations are set during LoadFromYAML or LoadFromDatabase
	// Validate() doesn't check durations, but we verify they're set correctly
	if cfg.CacheMaxTime == 0 {
		t.Error("CacheMaxTime should have a default value")
	}
	if cfg.CacheMaxTime != 30*24*time.Hour {
		t.Errorf("Expected CacheMaxTime to be 30 days, got: %v", cfg.CacheMaxTime)
	}
}

// TestValidateSetting_Comprehensive tests all validation paths in ValidateSetting
// to improve coverage. It covers all config keys with valid and invalid values,
// ensuring proper error messages are returned for invalid inputs.

func TestValidateSetting_Comprehensive(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		name    string
		key     string
		value   string
		wantErr bool
		errMsg  string
	}{
		// listener_port validation
		{
			name:    "valid port",
			key:     "listener_port",
			value:   "8081",
			wantErr: false,
		},
		{
			name:    "port too low",
			key:     "listener_port",
			value:   "0",
			wantErr: true,
			errMsg:  "port must be between 1 and 65535",
		},
		{
			name:    "port too high",
			key:     "listener_port",
			value:   "65536",
			wantErr: true,
			errMsg:  "port must be between 1 and 65535",
		},
		{
			name:    "port invalid format",
			key:     "listener_port",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid port value",
		},
		{
			name:    "port boundary min",
			key:     "listener_port",
			value:   "1",
			wantErr: false,
		},
		{
			name:    "port boundary max",
			key:     "listener_port",
			value:   "65535",
			wantErr: false,
		},
		// log_level validation
		{
			name:    "valid log level debug",
			key:     "log_level",
			value:   "debug",
			wantErr: false,
		},
		{
			name:    "valid log level info",
			key:     "log_level",
			value:   "info",
			wantErr: false,
		},
		{
			name:    "valid log level warn",
			key:     "log_level",
			value:   "warn",
			wantErr: false,
		},
		{
			name:    "valid log level error",
			key:     "log_level",
			value:   "error",
			wantErr: false,
		},
		{
			name:    "log level case insensitive",
			key:     "log_level",
			value:   "DEBUG",
			wantErr: false,
		},
		{
			name:    "invalid log level",
			key:     "log_level",
			value:   "invalid",
			wantErr: true,
			errMsg:  "invalid log level",
		},
		// log_rollover validation
		{
			name:    "valid rollover daily",
			key:     "log_rollover",
			value:   "daily",
			wantErr: false,
		},
		{
			name:    "valid rollover weekly",
			key:     "log_rollover",
			value:   "weekly",
			wantErr: false,
		},
		{
			name:    "valid rollover monthly",
			key:     "log_rollover",
			value:   "monthly",
			wantErr: false,
		},
		{
			name:    "rollover case insensitive",
			key:     "log_rollover",
			value:   "WEEKLY",
			wantErr: false,
		},
		{
			name:    "invalid rollover",
			key:     "log_rollover",
			value:   "invalid",
			wantErr: true,
			errMsg:  "invalid log rollover",
		},
		// log_retention_count validation
		{
			name:    "valid retention count",
			key:     "log_retention_count",
			value:   "7",
			wantErr: false,
		},
		{
			name:    "retention count too low",
			key:     "log_retention_count",
			value:   "0",
			wantErr: true,
			errMsg:  "log retention count must be at least 1",
		},
		{
			name:    "retention count invalid format",
			key:     "log_retention_count",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid log retention count",
		},
		{
			name:    "retention count boundary",
			key:     "log_retention_count",
			value:   "1",
			wantErr: false,
		},
		// session_same_site validation
		{
			name:    "valid same site Lax",
			key:     "session_same_site",
			value:   "Lax",
			wantErr: false,
		},
		{
			name:    "valid same site Strict",
			key:     "session_same_site",
			value:   "Strict",
			wantErr: false,
		},
		{
			name:    "valid same site None",
			key:     "session_same_site",
			value:   "None",
			wantErr: false,
		},
		{
			name:    "invalid same site",
			key:     "session_same_site",
			value:   "invalid",
			wantErr: true,
			errMsg:  "invalid session same-site",
		},
		{
			name:    "same site case sensitive",
			key:     "session_same_site",
			value:   "lax",
			wantErr: true,
			errMsg:  "invalid session same-site",
		},
		// cache_max_size validation
		{
			name:    "valid cache max size",
			key:     "cache_max_size",
			value:   "524288000",
			wantErr: false,
		},
		{
			name:    "cache max size zero",
			key:     "cache_max_size",
			value:   "0",
			wantErr: false,
		},
		{
			name:    "cache max size negative",
			key:     "cache_max_size",
			value:   "-1",
			wantErr: true,
			errMsg:  "cache max size must be non-negative",
		},
		{
			name:    "cache max size invalid format",
			key:     "cache_max_size",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid cache max size",
		},
		// cache_max_entry_size validation
		{
			name:    "valid cache max entry size",
			key:     "cache_max_entry_size",
			value:   "10485760",
			wantErr: false,
		},
		{
			name:    "cache max entry size negative",
			key:     "cache_max_entry_size",
			value:   "-1",
			wantErr: true,
			errMsg:  "cache max entry size must be non-negative",
		},
		// db_max_pool_size validation
		{
			name:    "valid db max pool size",
			key:     "db_max_pool_size",
			value:   "100",
			wantErr: false,
		},
		{
			name:    "db max pool size too low",
			key:     "db_max_pool_size",
			value:   "0",
			wantErr: true,
			errMsg:  "database max pool size must be at least 1",
		},
		{
			name:    "db max pool size invalid format",
			key:     "db_max_pool_size",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid db max pool size",
		},
		{
			name:    "db max pool size boundary",
			key:     "db_max_pool_size",
			value:   "1",
			wantErr: false,
		},
		// db_min_idle_connections validation
		{
			name:    "valid db min idle connections",
			key:     "db_min_idle_connections",
			value:   "10",
			wantErr: false,
		},
		{
			name:    "db min idle connections zero",
			key:     "db_min_idle_connections",
			value:   "0",
			wantErr: false,
		},
		{
			name:    "db min idle connections negative",
			key:     "db_min_idle_connections",
			value:   "-1",
			wantErr: true,
			errMsg:  "database min idle connections must be non-negative",
		},
		{
			name:    "db min idle exceeds max",
			key:     "db_min_idle_connections",
			value:   "200",
			wantErr: false, // cross-field check is handled by Validate(), not ValidateSetting
		},
		{
			name:    "db min idle invalid format",
			key:     "db_min_idle_connections",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid db min idle connections",
		},
		// worker_pool_max validation
		{
			name:    "valid worker pool max",
			key:     "worker_pool_max",
			value:   "10",
			wantErr: false,
		},
		{
			name:    "worker pool max zero",
			key:     "worker_pool_max",
			value:   "0",
			wantErr: false,
		},
		{
			name:    "worker pool max negative",
			key:     "worker_pool_max",
			value:   "-1",
			wantErr: true,
			errMsg:  "worker pool max must be non-negative",
		},
		{
			name:    "worker pool max invalid format",
			key:     "worker_pool_max",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid worker pool max",
		},
		// worker_pool_min_idle validation
		{
			name:    "valid worker pool min idle",
			key:     "worker_pool_min_idle",
			value:   "5",
			wantErr: false,
		},
		{
			name:    "worker pool min idle zero",
			key:     "worker_pool_min_idle",
			value:   "0",
			wantErr: false,
		},
		{
			name:    "worker pool min idle negative",
			key:     "worker_pool_min_idle",
			value:   "-1",
			wantErr: true,
			errMsg:  "worker pool min idle must be non-negative",
		},
		{
			name:    "worker pool min idle exceeds max",
			key:     "worker_pool_min_idle",
			value:   "20",
			wantErr: false, // cross-field check is handled by Validate(), not ValidateSetting
		},
		{
			name:    "worker pool min idle invalid format",
			key:     "worker_pool_min_idle",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid worker pool min idle",
		},
		// queue_size validation
		{
			name:    "valid queue size",
			key:     "queue_size",
			value:   "10000",
			wantErr: false,
		},
		{
			name:    "queue size too low",
			key:     "queue_size",
			value:   "0",
			wantErr: true,
			errMsg:  "queue size must be at least 1",
		},
		{
			name:    "queue size invalid format",
			key:     "queue_size",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid queue size",
		},
		{
			name:    "queue size boundary",
			key:     "queue_size",
			value:   "1",
			wantErr: false,
		},
		// http_cache_body_codec validation
		{
			name:    "valid codec zstd-1",
			key:     "http_cache_body_codec",
			value:   "zstd-1",
			wantErr: false,
		},
		{
			name:    "valid codec gzip-6",
			key:     "http_cache_body_codec",
			value:   "gzip-6",
			wantErr: false,
		},
		{
			name:    "valid codec identity",
			key:     "http_cache_body_codec",
			value:   "identity",
			wantErr: false,
		},
		{
			name:    "invalid codec bogus",
			key:     "http_cache_body_codec",
			value:   "bogus",
			wantErr: true,
			errMsg:  "invalid http cache body codec",
		},
		// unknown key (should not error)
		{
			name:    "unknown key",
			key:     "unknown_setting",
			value:   "any-value",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up config state for dependency checks
			if tt.key == "db_min_idle_connections" && tt.value == "200" {
				cfg.DBMaxPoolSize = 100
			} else if tt.key == "db_min_idle_connections" {
				cfg.DBMaxPoolSize = 100
			}
			if tt.key == "worker_pool_min_idle" && tt.value == "20" {
				cfg.WorkerPoolMax = 10
			} else if tt.key == "worker_pool_min_idle" {
				cfg.WorkerPoolMax = 0
			}

			err := cfg.ValidateSetting(tt.key, tt.value)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateSetting(%q, %q) expected error but got none", tt.key, tt.value)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateSetting(%q, %q) error = %v, want error containing %q", tt.key, tt.value, err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateSetting(%q, %q) unexpected error: %v", tt.key, tt.value, err)
				}
			}

			// Reset config state
			cfg.DBMaxPoolSize = 100
			cfg.WorkerPoolMax = 0
		})
	}
}

// TestSetValueFromString_Comprehensive tests all code paths in SetValueFromString,
// covering all config keys with valid and invalid string values to ensure proper parsing.

func TestSetValueFromString_Comprehensive(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr bool
		errMsg  string
		verify  func(*Config) bool
	}{
		// String fields
		{"listener_address", "listener_address", "127.0.0.1", false, "", func(c *Config) bool { return c.ListenerAddress == "127.0.0.1" }},
		{"log_directory", "log_directory", "/tmp/logs", false, "", func(c *Config) bool { return c.LogDirectory == "/tmp/logs" }},
		{"log_level", "log_level", "info", false, "", func(c *Config) bool { return c.LogLevel == "info" }},
		{"log_rollover", "log_rollover", "daily", false, "", func(c *Config) bool { return c.LogRollover == "daily" }},
		{"site_name", "site_name", "Test Site", false, "", func(c *Config) bool { return c.SiteName == "Test Site" }},
		{"current_theme", "current_theme", "light", false, "", func(c *Config) bool { return c.CurrentTheme == "light" }},
		{"image_directory", "image_directory", "/tmp/images", false, "", func(c *Config) bool { return c.ImageDirectory == "/tmp/images" }},
		{"session_same_site", "session_same_site", "Strict", false, "", func(c *Config) bool { return c.SessionSameSite == "Strict" }},
		// Integer fields
		{"listener_port", "listener_port", "9090", false, "", func(c *Config) bool { return c.ListenerPort == 9090 }},
		{"listener_port_invalid", "listener_port", "not-a-number", true, "invalid port value", nil},
		{"log_retention_count", "log_retention_count", "10", false, "", func(c *Config) bool { return c.LogRetentionCount == 10 }},
		{"log_retention_count_invalid", "log_retention_count", "not-a-number", true, "invalid log retention count", nil},
		{"session_max_age", "session_max_age", "3600", false, "", func(c *Config) bool { return c.SessionMaxAge == 3600 }},
		{"session_max_age_invalid", "session_max_age", "not-a-number", true, "invalid session max age", nil},
		// Boolean fields
		{"session_http_only_true", "session_http_only", "true", false, "", func(c *Config) bool { return c.SessionHttpOnly == true }},
		{"session_http_only_false", "session_http_only", "false", false, "", func(c *Config) bool { return c.SessionHttpOnly == false }},
		{"session_http_only_invalid", "session_http_only", "not-a-bool", true, "invalid session http only", nil},
		{"session_secure_true", "session_secure", "true", false, "", func(c *Config) bool { return c.SessionSecure == true }},
		{"session_secure_false", "session_secure", "false", false, "", func(c *Config) bool { return c.SessionSecure == false }},
		{"enable_http_cache_false", "enable_http_cache", "false", false, "", func(c *Config) bool { return c.EnableHTTPCache == false }},
		{"enable_cache_preload_true", "enable_cache_preload", "true", false, "", func(c *Config) bool { return c.EnableCachePreload == true }},
		{"enable_cache_preload_false", "enable_cache_preload", "false", false, "", func(c *Config) bool { return c.EnableCachePreload == false }},
		{"max_http_cache_entry_insert_per_transaction", "max_http_cache_entry_insert_per_transaction", "25", false, "", func(c *Config) bool { return c.MaxHTTPCacheEntryInsertPerTransaction == 25 }},
		{"max_http_cache_entry_insert_per_transaction_invalid", "max_http_cache_entry_insert_per_transaction", "x", true, "invalid max http cache entry insert per transaction", nil},
		{"run_file_discovery_true", "run_file_discovery", "true", false, "", func(c *Config) bool { return c.RunFileDiscovery == true }},
		// Int64 fields
		{"cache_max_size", "cache_max_size", "524288000", false, "", func(c *Config) bool { return c.CacheMaxSize == 524288000 }},
		{"cache_max_size_invalid", "cache_max_size", "not-a-number", true, "invalid cache max size", nil},
		{"cache_max_entry_size", "cache_max_entry_size", "10485760", false, "", func(c *Config) bool { return c.CacheMaxEntrySize == 10485760 }},
		{"cache_max_entry_size_invalid", "cache_max_entry_size", "not-a-number", true, "invalid cache max entry size", nil},
		// Duration fields
		{"cache_max_time", "cache_max_time", "720h", false, "", func(c *Config) bool { return c.CacheMaxTime == 720*time.Hour }},
		{"cache_max_time_invalid", "cache_max_time", "not-a-duration", true, "invalid cache max time", nil},
		{"cache_cleanup_interval", "cache_cleanup_interval", "5m", false, "", func(c *Config) bool { return c.CacheCleanupInterval == 5*time.Minute }},
		{"cache_cleanup_interval_invalid", "cache_cleanup_interval", "not-a-duration", true, "invalid cache cleanup interval", nil},
		{"db_optimize_interval", "db_optimize_interval", "1h", false, "", func(c *Config) bool { return c.DBOptimizeInterval == 1*time.Hour }},
		{"worker_pool_max_idle_time", "worker_pool_max_idle_time", "10s", false, "", func(c *Config) bool { return c.WorkerPoolMaxIdleTime == 10*time.Second }},
		{"worker_pool_max_idle_time_invalid", "worker_pool_max_idle_time", "not-a-duration", true, "invalid worker pool max idle time", nil},
		// Integer fields (more)
		{"db_max_pool_size", "db_max_pool_size", "100", false, "", func(c *Config) bool { return c.DBMaxPoolSize == 100 }},
		{"db_max_pool_size_invalid", "db_max_pool_size", "not-a-number", true, "invalid db max pool size", nil},
		{"db_min_idle_connections", "db_min_idle_connections", "10", false, "", func(c *Config) bool { return c.DBMinIdleConnections == 10 }},
		{"db_min_idle_connections_invalid", "db_min_idle_connections", "not-a-number", true, "invalid db min idle connections", nil},
		{"worker_pool_max", "worker_pool_max", "20", false, "", func(c *Config) bool { return c.WorkerPoolMax == 20 }},
		{"worker_pool_max_invalid", "worker_pool_max", "not-a-number", true, "invalid worker pool max", nil},
		{"worker_pool_min_idle", "worker_pool_min_idle", "5", false, "", func(c *Config) bool { return c.WorkerPoolMinIdle == 5 }},
		{"worker_pool_min_idle_invalid", "worker_pool_min_idle", "not-a-number", true, "invalid worker pool min idle", nil},
		{"queue_size", "queue_size", "5000", false, "", func(c *Config) bool { return c.QueueSize == 5000 }},
		{"queue_size_invalid", "queue_size", "not-a-number", true, "invalid queue size", nil},
		// JSON array field
		{"themes_valid", "themes", `["dark","light","auto"]`, false, "", func(c *Config) bool {
			return len(c.Themes) == 3 && c.Themes[0] == "dark" && c.Themes[1] == "light" && c.Themes[2] == "auto"
		}},
		{"themes_invalid_json", "themes", "not-json", true, "invalid themes JSON", nil},
		{"themes_empty_array", "themes", "[]", false, "", func(c *Config) bool { return len(c.Themes) == 0 }},
		// Unknown key
		{"unknown_key", "unknown_setting", "any-value", false, "", func(c *Config) bool { return true }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testCfg := DefaultConfig()
			err := testCfg.SetValueFromString(tt.key, tt.value)

			if tt.wantErr {
				if err == nil {
					t.Errorf("SetValueFromString(%q, %q) expected error but got none", tt.key, tt.value)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("SetValueFromString(%q, %q) error = %v, want error containing %q", tt.key, tt.value, err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("SetValueFromString(%q, %q) unexpected error: %v", tt.key, tt.value, err)
				} else if tt.verify != nil && !tt.verify(testCfg) {
					t.Errorf("SetValueFromString(%q, %q) verification failed", tt.key, tt.value)
				}
			}
		})
	}
}

func TestFileExists(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	if !FileExists(file) {
		t.Fatal("expected FileExists to return true for file")
	}
	if FileExists(dir) {
		t.Fatal("expected FileExists to return false for directory")
	}
}

func TestValidateImageDirectory_Empty(t *testing.T) {
	if err := ValidateImageDirectory(""); err == nil {
		t.Fatal("expected ValidateImageDirectory to fail for empty path")
	}
}

func TestValidateGuardrails(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		var cfg *Config
		if got := cfg.ValidateGuardrails(); got != nil {
			t.Errorf("nil.ValidateGuardrails() = %v, want nil", got)
		}
	})

	t.Run("no warnings", func(t *testing.T) {
		cfg := DefaultConfig()
		got := cfg.ValidateGuardrails()
		if len(got) != 0 {
			t.Errorf("DefaultConfig().ValidateGuardrails() = %v, want empty", got)
		}
	})

	t.Run("db_min_idle_gt_db_max_pool", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.DBMinIdleConnections = 20
		cfg.DBMaxPoolSize = 10
		got := cfg.ValidateGuardrails()
		if len(got) != 1 {
			t.Fatalf("got %d warnings, want 1", len(got))
		}
		if got[0].Check != "db_min_idle_gt_db_max_pool" {
			t.Errorf("Check = %q, want %q", got[0].Check, "db_min_idle_gt_db_max_pool")
		}
	})

	t.Run("worker_pool_min_idle_gt_worker_pool_max", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.WorkerPoolMax = 4
		cfg.WorkerPoolMinIdle = 8
		got := cfg.ValidateGuardrails()
		if len(got) != 1 {
			t.Fatalf("got %d warnings, want 1", len(got))
		}
		if got[0].Check != "worker_pool_min_idle_gt_worker_pool_max" {
			t.Errorf("Check = %q, want %q", got[0].Check, "worker_pool_min_idle_gt_worker_pool_max")
		}
	})

	t.Run("session_samesite_none_without_secure", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.SessionSameSite = "None"
		cfg.SessionSecure = false
		got := cfg.ValidateGuardrails()
		if len(got) != 1 {
			t.Fatalf("got %d warnings, want 1", len(got))
		}
		if got[0].Check != "session_samesite_none_without_secure" {
			t.Errorf("Check = %q, want %q", got[0].Check, "session_samesite_none_without_secure")
		}
	})

	t.Run("http_cache_enabled_with_zero_max_size", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.EnableHTTPCache = true
		cfg.CacheMaxSize = 0
		got := cfg.ValidateGuardrails()
		if len(got) != 1 {
			t.Fatalf("got %d warnings, want 1", len(got))
		}
		if got[0].Check != "http_cache_enabled_with_zero_max_size" {
			t.Errorf("Check = %q, want %q", got[0].Check, "http_cache_enabled_with_zero_max_size")
		}
	})

	t.Run("cache_entry_size_exceeds_cache_size", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.EnableHTTPCache = true
		cfg.CacheMaxSize = 100
		cfg.CacheMaxEntrySize = 200
		got := cfg.ValidateGuardrails()
		if len(got) != 1 {
			t.Fatalf("got %d warnings, want 1", len(got))
		}
		if got[0].Check != "cache_entry_size_exceeds_cache_size" {
			t.Errorf("Check = %q, want %q", got[0].Check, "cache_entry_size_exceeds_cache_size")
		}
	})

	t.Run("multiple warnings", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.DBMinIdleConnections = 20
		cfg.DBMaxPoolSize = 10
		cfg.WorkerPoolMax = 4
		cfg.WorkerPoolMinIdle = 8
		got := cfg.ValidateGuardrails()
		checks := make([]string, len(got))
		for i, w := range got {
			checks[i] = w.Check
		}
		wantChecks := []string{
			"db_min_idle_gt_db_max_pool",
			"worker_pool_min_idle_gt_worker_pool_max",
		}
		for _, want := range wantChecks {
			if !slices.Contains(checks, want) {
				t.Errorf("missing expected check %q, got %v", want, checks)
			}
		}
	})
}
