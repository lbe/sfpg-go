package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
)

// ConfigQueries is an interface for loading config from database.
// This allows us to work with both Queries and CustomQueries types.
type ConfigQueries interface {
	GetConfigs(ctx context.Context) ([]gallerydb.Config, error)
}

// LoadFromDatabase loads configuration values from the database.
// Only loads values that exist in the database; missing keys keep their current values.
// Ignores metadata columns for now (they'll be used for UI help text later).
func (c *Config) LoadFromDatabase(ctx context.Context, q ConfigQueries) error {
	configs, err := q.GetConfigs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get configs from database: %w", err)
	}

	for _, dbConfig := range configs {
		key := dbConfig.Key
		value := dbConfig.Value

		// Skip special keys like user/password and LastKnownGoodConfig
		if key == "user" || key == "password" || key == "LastKnownGoodConfig" {
			continue
		}

		if err := c.SetValueFromString(key, value); err != nil {
			slog.Warn("failed to set config value from database", "key", key, "value", value, "err", err)
			// Continue loading other values even if one fails
		}
	}

	return nil
}

// FromMap creates a Config from a map of string values.
// This is used for loading from database or other key-value sources.
func FromMap(m map[string]string) (*Config, error) {
	cfg := DefaultConfig()
	for k, v := range m {
		if err := cfg.SetValueFromString(k, v); err != nil {
			return nil, err
		}
	}

	// Apply defaults if key is missing (already handled by DefaultConfig())
	// but ensured for ETag specifically as per plan
	if cfg.ETagVersion == "" {
		cfg.ETagVersion = DefaultConfig().ETagVersion
	}

	return cfg, nil
}

// SetValueFromString sets a config field value from a string representation.
// This is used when loading from database or parsing from YAML.
func (c *Config) SetValueFromString(key, value string) error {
	switch key {
	case "listener_address":
		c.ListenerAddress = value
	case "listener_port":
		port, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid port value %q: %w", value, err)
		}
		c.ListenerPort = port
	case "log_directory":
		c.LogDirectory = value
	case "log_level":
		c.LogLevel = value
	case "log_rollover":
		c.LogRollover = value
	case "log_retention_count":
		count, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid log retention count %q: %w", value, err)
		}
		c.LogRetentionCount = count
	case "site_name":
		c.SiteName = value
	case "themes":
		var themes []string
		if err := json.Unmarshal([]byte(value), &themes); err != nil {
			return fmt.Errorf("invalid themes JSON %q: %w", value, err)
		}
		c.Themes = themes
	case "current_theme":
		c.CurrentTheme = value
	case "image_directory":
		c.ImageDirectory = value
	case "etag_version":
		c.ETagVersion = value
	case "session_max_age":
		age, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid session max age %q: %w", value, err)
		}
		c.SessionMaxAge = age
	case "session_http_only":
		httpOnly, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid session http only %q: %w", value, err)
		}
		c.SessionHttpOnly = httpOnly
	case "session_secure":
		secure, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid session secure %q: %w", value, err)
		}
		c.SessionSecure = secure
	case "session_same_site":
		c.SessionSameSite = value
	case "server_compression_enable":
		enable, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid compression enable %q: %w", value, err)
		}
		c.ServerCompressionEnable = enable
	case "enable_http_cache":
		enable, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid http cache enable %q: %w", value, err)
		}
		c.EnableHTTPCache = enable
	case "cache_max_size":
		size, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid cache max size %q: %w", value, err)
		}
		c.CacheMaxSize = size
	case "cache_max_time":
		duration, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid cache max time %q: %w", value, err)
		}
		c.CacheMaxTime = duration
	case "cache_max_entry_size":
		size, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid cache max entry size %q: %w", value, err)
		}
		c.CacheMaxEntrySize = size
	case "cache_cleanup_interval":
		duration, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid cache cleanup interval %q: %w", value, err)
		}
		c.CacheCleanupInterval = duration
	case "db_max_pool_size":
		size, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid db max pool size %q: %w", value, err)
		}
		c.DBMaxPoolSize = size
	case "db_min_idle_connections":
		count, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid db min idle connections %q: %w", value, err)
		}
		c.DBMinIdleConnections = count
	case "db_optimize_interval":
		duration, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid db optimize interval %q: %w", value, err)
		}
		c.DBOptimizeInterval = duration
	case "worker_pool_max":
		max, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid worker pool max %q: %w", value, err)
		}
		c.WorkerPoolMax = max
	case "worker_pool_min_idle":
		min, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid worker pool min idle %q: %w", value, err)
		}
		c.WorkerPoolMinIdle = min
	case "worker_pool_max_idle_time":
		duration, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid worker pool max idle time %q: %w", value, err)
		}
		c.WorkerPoolMaxIdleTime = duration
	case "queue_size":
		size, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid queue size %q: %w", value, err)
		}
		c.QueueSize = size
	case "enable_cache_preload":
		enable, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid enable cache preload %q: %w", value, err)
		}
		c.EnableCachePreload = enable
	case "max_http_cache_entry_insert_per_transaction":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid max http cache entry insert per transaction %q: %w", value, err)
		}
		c.MaxHTTPCacheEntryInsertPerTransaction = n
	case "run_file_discovery":
		enable, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid run file discovery %q: %w", value, err)
		}
		c.RunFileDiscovery = enable
	default:
		// Unknown key - silently ignore (might be user/password or other legacy keys)
		return nil
	}

	return nil
}

// LoadFromOpt loads configuration values from getopt.Opt (CLI arguments and environment variables).
// This applies the highest precedence configuration source, overriding database and YAML values.
// Only values that were explicitly set (IsSet=true) override the current config.
// This ensures that default/zero values from getopt.Opt do not override database values.
func (c *Config) LoadFromOpt(opt getopt.Opt) {
	if opt.Port.IsSet {
		c.ListenerPort = opt.Port.Int
	}
	if opt.EnableCompression.IsSet {
		c.ServerCompressionEnable = opt.EnableCompression.Bool
	}
	if opt.EnableHTTPCache.IsSet {
		c.EnableHTTPCache = opt.EnableHTTPCache.Bool
	}
	if opt.EnableCachePreload.IsSet {
		c.EnableCachePreload = opt.EnableCachePreload.Bool
	}
	if opt.RunFileDiscovery.IsSet {
		c.RunFileDiscovery = opt.RunFileDiscovery.Bool
	}
	if opt.SessionSecure.IsSet {
		c.SessionSecure = opt.SessionSecure.Bool
	}
	if opt.SessionHttpOnly.IsSet {
		c.SessionHttpOnly = opt.SessionHttpOnly.Bool
	}
	if opt.SessionMaxAge.IsSet {
		c.SessionMaxAge = opt.SessionMaxAge.Int
	}
	if opt.SessionSameSite.IsSet {
		c.SessionSameSite = opt.SessionSameSite.String
	}
	// SessionSecret is not stored in Config (memory only)
}

// LoadFromOptExcluding applies CLI/env values except for fields in the exclude list.
// The exclude list contains config field names that should NOT be overridden (e.g., user-changed fields).
// This supports the use case where CLI values should override unchanged fields, but user changes persist.
func (c *Config) LoadFromOptExcluding(opt getopt.Opt, exclude []string) {
	// Helper to check if a field is in the exclude list
	isExcluded := func(field string) bool {
		return slices.Contains(exclude, field)
	}

	if opt.Port.IsSet && !isExcluded("listener_port") {
		c.ListenerPort = opt.Port.Int
	}
	if opt.EnableCompression.IsSet && !isExcluded("server_compression_enable") {
		c.ServerCompressionEnable = opt.EnableCompression.Bool
	}
	if opt.EnableHTTPCache.IsSet && !isExcluded("enable_http_cache") {
		c.EnableHTTPCache = opt.EnableHTTPCache.Bool
	}
	if opt.EnableCachePreload.IsSet && !isExcluded("enable_cache_preload") {
		c.EnableCachePreload = opt.EnableCachePreload.Bool
	}
	if opt.RunFileDiscovery.IsSet && !isExcluded("run_file_discovery") {
		c.RunFileDiscovery = opt.RunFileDiscovery.Bool
	}
	if opt.SessionSecure.IsSet && !isExcluded("session_secure") {
		c.SessionSecure = opt.SessionSecure.Bool
	}
	if opt.SessionHttpOnly.IsSet && !isExcluded("session_http_only") {
		c.SessionHttpOnly = opt.SessionHttpOnly.Bool
	}
	if opt.SessionMaxAge.IsSet && !isExcluded("session_max_age") {
		c.SessionMaxAge = opt.SessionMaxAge.Int
	}
	if opt.SessionSameSite.IsSet && !isExcluded("session_same_site") {
		c.SessionSameSite = opt.SessionSameSite.String
	}
	// SessionSecret is not stored in Config (memory only)
}

// yamlConfigForConfig holds YAML values for Config struct.
type yamlConfigForConfig struct {
	ListenerAddress                       *string   `yaml:"listener-address"`
	ListenerPort                          *int      `yaml:"listener-port"`
	LogDirectory                          *string   `yaml:"log-directory"`
	LogLevel                              *string   `yaml:"log-level"`
	LogRollover                           *string   `yaml:"log-rollover"`
	LogRetentionCount                     *int      `yaml:"log-retention-count"`
	SiteName                              *string   `yaml:"site-name"`
	Themes                                *[]string `yaml:"themes"`
	CurrentTheme                          *string   `yaml:"current-theme"`
	ImageDirectory                        *string   `yaml:"image-directory"`
	ETagVersion                           *string   `yaml:"etag-version"`
	SessionMaxAge                         *int      `yaml:"session-max-age"`
	SessionHttpOnly                       *bool     `yaml:"session-http-only"`
	SessionSecure                         *bool     `yaml:"session-secure"`
	SessionSameSite                       *string   `yaml:"session-same-site"`
	Compression                           *bool     `yaml:"compression"`
	HTTPCache                             *bool     `yaml:"http-cache"`
	CacheMaxSize                          *int64    `yaml:"cache-max-size"`
	CacheMaxTime                          *string   `yaml:"cache-max-time"`
	CacheMaxEntrySize                     *int64    `yaml:"cache-max-entry-size"`
	CacheCleanupInterval                  *string   `yaml:"cache-cleanup-interval"`
	DBMaxPoolSize                         *int      `yaml:"db-max-pool-size"`
	DBMinIdleConnections                  *int      `yaml:"db-min-idle-connections"`
	DBOptimizeInterval                    *string   `yaml:"db-optimize-interval"`
	WorkerPoolMax                         *int      `yaml:"worker-pool-max"`
	WorkerPoolMinIdle                     *int      `yaml:"worker-pool-min-idle"`
	WorkerPoolMaxIdleTime                 *string   `yaml:"worker-pool-max-idle-time"`
	QueueSize                             *int      `yaml:"queue-size"`
	EnableCachePreload                    *bool     `yaml:"enable-cache-preload"`
	MaxHTTPCacheEntryInsertPerTransaction *int      `yaml:"max-http-cache-entry-insert-per-transaction"`
	Discover                              *bool     `yaml:"discover"`
}

func loadYAMLConfigForConfig(path string) (*yamlConfigForConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var cfg yamlConfigForConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid YAML syntax in %s: %w", path, err)
	}

	return &cfg, nil
}

func applyYAMLConfigToConfig(c *Config, cfg *yamlConfigForConfig) {
	if cfg == nil {
		return
	}

	if cfg.ListenerAddress != nil {
		c.ListenerAddress = *cfg.ListenerAddress
	}
	if cfg.ListenerPort != nil {
		c.ListenerPort = *cfg.ListenerPort
	}
	if cfg.LogDirectory != nil {
		c.LogDirectory = *cfg.LogDirectory
	}
	if cfg.LogLevel != nil {
		c.LogLevel = *cfg.LogLevel
	}
	if cfg.LogRollover != nil {
		c.LogRollover = *cfg.LogRollover
	}
	if cfg.LogRetentionCount != nil {
		c.LogRetentionCount = *cfg.LogRetentionCount
	}
	if cfg.SiteName != nil {
		c.SiteName = *cfg.SiteName
	}
	if cfg.Themes != nil {
		c.Themes = *cfg.Themes
	}
	if cfg.CurrentTheme != nil {
		c.CurrentTheme = *cfg.CurrentTheme
	}
	if cfg.ImageDirectory != nil {
		c.ImageDirectory = *cfg.ImageDirectory
	}
	if cfg.ETagVersion != nil {
		c.ETagVersion = *cfg.ETagVersion
	}
	if cfg.SessionMaxAge != nil {
		c.SessionMaxAge = *cfg.SessionMaxAge
	}
	if cfg.SessionHttpOnly != nil {
		c.SessionHttpOnly = *cfg.SessionHttpOnly
	}
	if cfg.SessionSecure != nil {
		c.SessionSecure = *cfg.SessionSecure
	}
	if cfg.SessionSameSite != nil {
		c.SessionSameSite = *cfg.SessionSameSite
	}
	if cfg.Compression != nil {
		c.ServerCompressionEnable = *cfg.Compression
	}
	if cfg.HTTPCache != nil {
		c.EnableHTTPCache = *cfg.HTTPCache
	}
	if cfg.CacheMaxSize != nil {
		c.CacheMaxSize = *cfg.CacheMaxSize
	}
	if cfg.CacheMaxTime != nil {
		duration, err := time.ParseDuration(*cfg.CacheMaxTime)
		if err == nil {
			c.CacheMaxTime = duration
		} else {
			slog.Warn("invalid cache-max-time duration", "value", *cfg.CacheMaxTime, "err", err)
		}
	}
	if cfg.CacheMaxEntrySize != nil {
		c.CacheMaxEntrySize = *cfg.CacheMaxEntrySize
	}
	if cfg.CacheCleanupInterval != nil {
		duration, err := time.ParseDuration(*cfg.CacheCleanupInterval)
		if err == nil {
			c.CacheCleanupInterval = duration
		} else {
			slog.Warn("invalid cache-cleanup-interval duration", "value", *cfg.CacheCleanupInterval, "err", err)
		}
	}
	if cfg.DBMaxPoolSize != nil {
		c.DBMaxPoolSize = *cfg.DBMaxPoolSize
	}
	if cfg.DBMinIdleConnections != nil {
		c.DBMinIdleConnections = *cfg.DBMinIdleConnections
	}
	if cfg.DBOptimizeInterval != nil {
		duration, err := time.ParseDuration(*cfg.DBOptimizeInterval)
		if err == nil {
			c.DBOptimizeInterval = duration
		} else {
			slog.Warn("invalid db-optimize-interval duration", "value", *cfg.DBOptimizeInterval, "err", err)
		}
	}
	if cfg.WorkerPoolMax != nil {
		c.WorkerPoolMax = *cfg.WorkerPoolMax
	}
	if cfg.WorkerPoolMinIdle != nil {
		c.WorkerPoolMinIdle = *cfg.WorkerPoolMinIdle
	}
	if cfg.WorkerPoolMaxIdleTime != nil {
		duration, err := time.ParseDuration(*cfg.WorkerPoolMaxIdleTime)
		if err == nil {
			c.WorkerPoolMaxIdleTime = duration
		} else {
			slog.Warn("invalid worker-pool-max-idle-time duration", "value", *cfg.WorkerPoolMaxIdleTime, "err", err)
		}
	}
	if cfg.QueueSize != nil {
		c.QueueSize = *cfg.QueueSize
	}
	if cfg.EnableCachePreload != nil {
		c.EnableCachePreload = *cfg.EnableCachePreload
	}
	if cfg.MaxHTTPCacheEntryInsertPerTransaction != nil {
		c.MaxHTTPCacheEntryInsertPerTransaction = *cfg.MaxHTTPCacheEntryInsertPerTransaction
	}
	if cfg.Discover != nil {
		c.RunFileDiscovery = *cfg.Discover
	}
}

// LoadFromYAML loads configuration values from YAML files.
// It loads from platform config dir first (lower precedence), then exe dir (higher precedence).
// Only values present in YAML files are applied; missing keys keep their current values.
func (c *Config) LoadFromYAML() error {
	// Get config file paths (platform first, then exe dir)
	configPaths, err := getopt.FindConfigFiles()
	if err != nil {
		return fmt.Errorf("failed to find config files: %w", err)
	}

	// Load from lowest to highest precedence (platform first, then exe dir)
	for i := len(configPaths) - 1; i >= 0; i-- {
		path := configPaths[i]
		if !FileExists(path) {
			continue
		}

		cfg, err := loadYAMLConfigForConfig(path)
		if err != nil {
			slog.Warn("failed to load YAML config", "path", path, "err", err)
			continue
		}

		applyYAMLConfigToConfig(c, cfg)
	}

	return nil
}
