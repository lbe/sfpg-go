package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// configField describes a single configuration field for the table-driven
// refactor. It holds the database key, YAML key, closures for getting,
// setting, and copying the field's value, plus form presentation metadata
// (IsCheckbox).
type configField struct {
	dbKey      string                          // "listener_port"
	yamlKey    string                          // "listener-port"
	set        func(c *Config, v string) error // parse+validate+assign
	getDB      func(c *Config) string          // typed->string (for DB)
	getYAML    func(c *Config) any             // typed->native (for YAML)
	isZero     func(c *Config) bool            // for MergeDefaults
	restart    bool                            // requires restart
	setFrom    func(dst, src *Config)          // copy src's value to dst
	IsCheckbox bool                            // HTML checkbox field
}

// durationFriendlyNames maps YAML duration keys to human-readable field names
// used in error messages.
var durationFriendlyNames = map[string]string{
	"cache-max-time":            "cache max time",
	"cache-cleanup-interval":    "cache cleanup interval",
	"db-optimize-interval":      "db optimize interval",
	"worker-pool-max-idle-time": "worker pool max idle time",
	"db-pool-monitor-interval":  "db pool monitor interval",
}

// fields returns the single source of truth for all 32 config fields.
// The table is the sole definition used by ToMap, SetValueFromString,
// ExportToYAML, IdentifyChanges, restartRequiredKeys, MergeDefaults,
// and RecoverFromCorruption.
var (
	fieldsOnce  sync.Once
	fieldsCache []configField
)

func fields() []configField {
	fieldsOnce.Do(func() {
		fieldsCache = []configField{
			// --- String fields ---
			{
				dbKey:   "listener_address",
				yamlKey: "listener-address",
				set:     func(c *Config, v string) error { c.ListenerAddress = v; return nil },
				getDB:   func(c *Config) string { return c.ListenerAddress },
				getYAML: func(c *Config) any { return c.ListenerAddress },
				isZero:  func(c *Config) bool { return c.ListenerAddress == "" },
				restart: true,
				setFrom: func(dst, src *Config) { dst.ListenerAddress = src.ListenerAddress },
			},
			{
				dbKey:   "log_directory",
				yamlKey: "log-directory",
				set:     func(c *Config, v string) error { c.LogDirectory = v; return nil },
				getDB:   func(c *Config) string { return c.LogDirectory },
				getYAML: func(c *Config) any { return c.LogDirectory },
				isZero:  func(c *Config) bool { return c.LogDirectory == "" },
				restart: true,
				setFrom: func(dst, src *Config) { dst.LogDirectory = src.LogDirectory },
			},
			{
				dbKey:   "log_level",
				yamlKey: "log-level",
				set: func(c *Config, v string) error {
					valid := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
					if !valid[strings.ToLower(v)] {
						return fmt.Errorf("invalid log level %q, must be one of: debug, info, warn, error", v)
					}
					c.LogLevel = v
					return nil
				},
				getDB:   func(c *Config) string { return c.LogLevel },
				getYAML: func(c *Config) any { return c.LogLevel },
				isZero:  func(c *Config) bool { return c.LogLevel == "" },
				restart: true,
				setFrom: func(dst, src *Config) { dst.LogLevel = src.LogLevel },
			},
			{
				dbKey:   "log_rollover",
				yamlKey: "log-rollover",
				set: func(c *Config, v string) error {
					valid := map[string]bool{"daily": true, "weekly": true, "monthly": true}
					if !valid[strings.ToLower(v)] {
						return fmt.Errorf("invalid log rollover %q, must be one of: daily, weekly, monthly", v)
					}
					c.LogRollover = v
					return nil
				},
				getDB:   func(c *Config) string { return c.LogRollover },
				getYAML: func(c *Config) any { return c.LogRollover },
				isZero:  func(c *Config) bool { return c.LogRollover == "" },
				restart: true,
				setFrom: func(dst, src *Config) { dst.LogRollover = src.LogRollover },
			},
			{
				dbKey:   "site_name",
				yamlKey: "site-name",
				set:     func(c *Config, v string) error { c.SiteName = v; return nil },
				getDB:   func(c *Config) string { return c.SiteName },
				getYAML: func(c *Config) any { return c.SiteName },
				isZero:  func(c *Config) bool { return c.SiteName == "" },
				restart: false,
				setFrom: func(dst, src *Config) { dst.SiteName = src.SiteName },
			},
			{
				dbKey:   "current_theme",
				yamlKey: "current-theme",
				set:     func(c *Config, v string) error { c.CurrentTheme = v; return nil },
				getDB:   func(c *Config) string { return c.CurrentTheme },
				getYAML: func(c *Config) any { return c.CurrentTheme },
				isZero:  func(c *Config) bool { return c.CurrentTheme == "" },
				restart: false,
				setFrom: func(dst, src *Config) { dst.CurrentTheme = src.CurrentTheme },
			},
			{
				dbKey:   "image_directory",
				yamlKey: "image-directory",
				set:     func(c *Config, v string) error { c.ImageDirectory = v; return nil },
				getDB:   func(c *Config) string { return c.ImageDirectory },
				getYAML: func(c *Config) any { return c.ImageDirectory },
				isZero:  func(c *Config) bool { return c.ImageDirectory == "" },
				restart: true,
				setFrom: func(dst, src *Config) { dst.ImageDirectory = src.ImageDirectory },
			},
			{
				dbKey:   "etag_version",
				yamlKey: "etag-version",
				set:     func(c *Config, v string) error { c.ETagVersion = v; return nil },
				getDB:   func(c *Config) string { return c.ETagVersion },
				getYAML: func(c *Config) any { return c.ETagVersion },
				isZero:  func(c *Config) bool { return c.ETagVersion == "" },
				restart: false,
				setFrom: func(dst, src *Config) { dst.ETagVersion = src.ETagVersion },
			},
			{
				dbKey:   "session_same_site",
				yamlKey: "session-same-site",
				set: func(c *Config, v string) error {
					valid := map[string]bool{"Lax": true, "Strict": true, "None": true}
					if !valid[v] {
						return fmt.Errorf("invalid session same-site %q, must be one of: Lax, Strict, None", v)
					}
					c.SessionSameSite = v
					return nil
				},
				getDB:   func(c *Config) string { return c.SessionSameSite },
				getYAML: func(c *Config) any { return c.SessionSameSite },
				isZero:  func(c *Config) bool { return c.SessionSameSite == "" },
				restart: true,
				setFrom: func(dst, src *Config) { dst.SessionSameSite = src.SessionSameSite },
			},
			// --- Int fields ---
			{
				dbKey:   "listener_port",
				yamlKey: "listener-port",
				set: func(c *Config, v string) error {
					n, err := strconv.Atoi(v)
					if err != nil {
						return fmt.Errorf("invalid port value %q: %w", v, err)
					}
					if n < 1 || n > 65535 {
						return fmt.Errorf("port must be between 1 and 65535, got %d", n)
					}
					c.ListenerPort = n
					return nil
				},
				getDB:   func(c *Config) string { return strconv.Itoa(c.ListenerPort) },
				getYAML: func(c *Config) any { return c.ListenerPort },
				isZero:  func(c *Config) bool { return c.ListenerPort == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.ListenerPort = src.ListenerPort },
			},
			{
				dbKey:   "log_retention_count",
				yamlKey: "log-retention-count",
				set: func(c *Config, v string) error {
					n, err := strconv.Atoi(v)
					if err != nil {
						return fmt.Errorf("invalid log retention count %q: %w", v, err)
					}
					if n < 1 {
						return fmt.Errorf("log retention count must be at least 1, got %d", n)
					}
					c.LogRetentionCount = n
					return nil
				},
				getDB:   func(c *Config) string { return strconv.Itoa(c.LogRetentionCount) },
				getYAML: func(c *Config) any { return c.LogRetentionCount },
				isZero:  func(c *Config) bool { return c.LogRetentionCount == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.LogRetentionCount = src.LogRetentionCount },
			},
			{
				dbKey:   "session_max_age",
				yamlKey: "session-max-age",
				set: func(c *Config, v string) error {
					n, err := strconv.Atoi(v)
					if err != nil {
						return fmt.Errorf("invalid session max age %q: %w", v, err)
					}
					c.SessionMaxAge = n
					return nil
				},
				getDB:   func(c *Config) string { return strconv.Itoa(c.SessionMaxAge) },
				getYAML: func(c *Config) any { return c.SessionMaxAge },
				isZero:  func(c *Config) bool { return c.SessionMaxAge == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.SessionMaxAge = src.SessionMaxAge },
			},
			{
				dbKey:   "db_max_pool_size",
				yamlKey: "db-max-pool-size",
				set: func(c *Config, v string) error {
					n, err := strconv.Atoi(v)
					if err != nil {
						return fmt.Errorf("invalid db max pool size %q: %w", v, err)
					}
					if n < 1 {
						return fmt.Errorf("database max pool size must be at least 1, got %d", n)
					}
					c.DBMaxPoolSize = n
					return nil
				},
				getDB:   func(c *Config) string { return strconv.Itoa(c.DBMaxPoolSize) },
				getYAML: func(c *Config) any { return c.DBMaxPoolSize },
				isZero:  func(c *Config) bool { return c.DBMaxPoolSize == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.DBMaxPoolSize = src.DBMaxPoolSize },
			},
			{
				dbKey:   "db_min_idle_connections",
				yamlKey: "db-min-idle-connections",
				set: func(c *Config, v string) error {
					n, err := strconv.Atoi(v)
					if err != nil {
						return fmt.Errorf("invalid db min idle connections %q: %w", v, err)
					}
					if n < 0 {
						return fmt.Errorf("database min idle connections must be non-negative, got %d", n)
					}
					c.DBMinIdleConnections = n
					return nil
				},
				getDB:   func(c *Config) string { return strconv.Itoa(c.DBMinIdleConnections) },
				getYAML: func(c *Config) any { return c.DBMinIdleConnections },
				isZero:  func(c *Config) bool { return c.DBMinIdleConnections == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.DBMinIdleConnections = src.DBMinIdleConnections },
			},
			{
				dbKey:   "worker_pool_max",
				yamlKey: "worker-pool-max",
				set: func(c *Config, v string) error {
					n, err := strconv.Atoi(v)
					if err != nil {
						return fmt.Errorf("invalid worker pool max %q: %w", v, err)
					}
					if n < 0 {
						return fmt.Errorf("worker pool max must be non-negative, got %d", n)
					}
					c.WorkerPoolMax = n
					return nil
				},
				getDB:   func(c *Config) string { return strconv.Itoa(c.WorkerPoolMax) },
				getYAML: func(c *Config) any { return c.WorkerPoolMax },
				isZero:  func(c *Config) bool { return c.WorkerPoolMax == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.WorkerPoolMax = src.WorkerPoolMax },
			},
			{
				dbKey:   "worker_pool_min_idle",
				yamlKey: "worker-pool-min-idle",
				set: func(c *Config, v string) error {
					n, err := strconv.Atoi(v)
					if err != nil {
						return fmt.Errorf("invalid worker pool min idle %q: %w", v, err)
					}
					if n < 0 {
						return fmt.Errorf("worker pool min idle must be non-negative, got %d", n)
					}
					c.WorkerPoolMinIdle = n
					return nil
				},
				getDB:   func(c *Config) string { return strconv.Itoa(c.WorkerPoolMinIdle) },
				getYAML: func(c *Config) any { return c.WorkerPoolMinIdle },
				isZero:  func(c *Config) bool { return c.WorkerPoolMinIdle == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.WorkerPoolMinIdle = src.WorkerPoolMinIdle },
			},
			{
				dbKey:   "queue_size",
				yamlKey: "queue-size",
				set: func(c *Config, v string) error {
					n, err := strconv.Atoi(v)
					if err != nil {
						return fmt.Errorf("invalid queue size %q: %w", v, err)
					}
					if n < 1 {
						return fmt.Errorf("queue size must be at least 1, got %d", n)
					}
					c.QueueSize = n
					return nil
				},
				getDB:   func(c *Config) string { return strconv.Itoa(c.QueueSize) },
				getYAML: func(c *Config) any { return c.QueueSize },
				isZero:  func(c *Config) bool { return c.QueueSize == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.QueueSize = src.QueueSize },
			},
			{
				dbKey:   "max_http_cache_entry_insert_per_transaction",
				yamlKey: "max-http-cache-entry-insert-per-transaction",
				set: func(c *Config, v string) error {
					n, err := strconv.Atoi(v)
					if err != nil {
						return fmt.Errorf("invalid max http cache entry insert per transaction %q: %w", v, err)
					}
					c.MaxHTTPCacheEntryInsertPerTransaction = n
					return nil
				},
				getDB:   func(c *Config) string { return strconv.Itoa(c.MaxHTTPCacheEntryInsertPerTransaction) },
				getYAML: func(c *Config) any { return c.MaxHTTPCacheEntryInsertPerTransaction },
				isZero:  func(c *Config) bool { return c.MaxHTTPCacheEntryInsertPerTransaction == 0 },
				restart: false,
				setFrom: func(dst, src *Config) {
					dst.MaxHTTPCacheEntryInsertPerTransaction = src.MaxHTTPCacheEntryInsertPerTransaction
				},
			},
			// --- LockoutDuration (int, seconds) ---
			{
				dbKey:   "lockout_duration",
				yamlKey: "lockout-duration",
				set: func(c *Config, v string) error {
					n, err := strconv.Atoi(v)
					if err != nil {
						return fmt.Errorf("invalid lockout duration %q: %w", v, err)
					}
					c.LockoutDuration = n
					return nil
				},
				getDB:   func(c *Config) string { return strconv.Itoa(c.LockoutDuration) },
				getYAML: func(c *Config) any { return c.LockoutDuration },
				isZero:  func(c *Config) bool { return c.LockoutDuration == 0 },
				restart: false,
				setFrom: func(dst, src *Config) { dst.LockoutDuration = src.LockoutDuration },
			},
			// --- Bool fields (isZero always returns false) ---
			{
				dbKey:      "session_http_only",
				yamlKey:    "session-http-only",
				IsCheckbox: true,
				set: func(c *Config, v string) error {
					b, err := strconv.ParseBool(v)
					if err != nil {
						return fmt.Errorf("invalid session http only %q: %w", v, err)
					}
					c.SessionHttpOnly = b
					return nil
				},
				getDB:   func(c *Config) string { return strconv.FormatBool(c.SessionHttpOnly) },
				getYAML: func(c *Config) any { return c.SessionHttpOnly },
				isZero:  func(c *Config) bool { return false },
				restart: true,
				setFrom: func(dst, src *Config) { dst.SessionHttpOnly = src.SessionHttpOnly },
			},
			{
				dbKey:      "session_secure",
				yamlKey:    "session-secure",
				IsCheckbox: true,
				set: func(c *Config, v string) error {
					b, err := strconv.ParseBool(v)
					if err != nil {
						return fmt.Errorf("invalid session secure %q: %w", v, err)
					}
					c.SessionSecure = b
					return nil
				},
				getDB:   func(c *Config) string { return strconv.FormatBool(c.SessionSecure) },
				getYAML: func(c *Config) any { return c.SessionSecure },
				isZero:  func(c *Config) bool { return false },
				restart: true,
				setFrom: func(dst, src *Config) { dst.SessionSecure = src.SessionSecure },
			},
			{
				dbKey:      "server_compression_enable",
				yamlKey:    "compression",
				IsCheckbox: true,
				set: func(c *Config, v string) error {
					b, err := strconv.ParseBool(v)
					if err != nil {
						return fmt.Errorf("invalid compression enable %q: %w", v, err)
					}
					c.ServerCompressionEnable = b
					return nil
				},
				getDB:   func(c *Config) string { return strconv.FormatBool(c.ServerCompressionEnable) },
				getYAML: func(c *Config) any { return c.ServerCompressionEnable },
				isZero:  func(c *Config) bool { return false },
				restart: true,
				setFrom: func(dst, src *Config) { dst.ServerCompressionEnable = src.ServerCompressionEnable },
			},
			{
				dbKey:      "enable_http_cache",
				yamlKey:    "http-cache",
				IsCheckbox: true,
				set: func(c *Config, v string) error {
					b, err := strconv.ParseBool(v)
					if err != nil {
						return fmt.Errorf("invalid http cache enable %q: %w", v, err)
					}
					c.EnableHTTPCache = b
					return nil
				},
				getDB:   func(c *Config) string { return strconv.FormatBool(c.EnableHTTPCache) },
				getYAML: func(c *Config) any { return c.EnableHTTPCache },
				isZero:  func(c *Config) bool { return false },
				restart: true,
				setFrom: func(dst, src *Config) { dst.EnableHTTPCache = src.EnableHTTPCache },
			},
			{
				dbKey:      "enable_cache_preload",
				yamlKey:    "enable-cache-preload",
				IsCheckbox: true,
				set: func(c *Config, v string) error {
					b, err := strconv.ParseBool(v)
					if err != nil {
						return fmt.Errorf("invalid enable cache preload %q: %w", v, err)
					}
					c.EnableCachePreload = b
					return nil
				},
				getDB:   func(c *Config) string { return strconv.FormatBool(c.EnableCachePreload) },
				getYAML: func(c *Config) any { return c.EnableCachePreload },
				isZero:  func(c *Config) bool { return false },
				restart: false,
				setFrom: func(dst, src *Config) { dst.EnableCachePreload = src.EnableCachePreload },
			},
			{
				dbKey:      "run_file_discovery",
				yamlKey:    "discover",
				IsCheckbox: true,
				set: func(c *Config, v string) error {
					b, err := strconv.ParseBool(v)
					if err != nil {
						return fmt.Errorf("invalid run file discovery %q: %w", v, err)
					}
					c.RunFileDiscovery = b
					return nil
				},
				getDB:   func(c *Config) string { return strconv.FormatBool(c.RunFileDiscovery) },
				getYAML: func(c *Config) any { return c.RunFileDiscovery },
				isZero:  func(c *Config) bool { return false },
				restart: false,
				setFrom: func(dst, src *Config) { dst.RunFileDiscovery = src.RunFileDiscovery },
			},
			// --- Int64 fields ---
			{
				dbKey:   "cache_max_size",
				yamlKey: "cache-max-size",
				set: func(c *Config, v string) error {
					n, err := strconv.ParseInt(v, 10, 64)
					if err != nil {
						return fmt.Errorf("invalid cache max size %q: %w", v, err)
					}
					if n < 0 {
						return fmt.Errorf("cache max size must be non-negative, got %d", n)
					}
					c.CacheMaxSize = n
					return nil
				},
				getDB:   func(c *Config) string { return strconv.FormatInt(c.CacheMaxSize, 10) },
				getYAML: func(c *Config) any { return c.CacheMaxSize },
				isZero:  func(c *Config) bool { return c.CacheMaxSize == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.CacheMaxSize = src.CacheMaxSize },
			},
			{
				dbKey:   "cache_max_entry_size",
				yamlKey: "cache-max-entry-size",
				set: func(c *Config, v string) error {
					n, err := strconv.ParseInt(v, 10, 64)
					if err != nil {
						return fmt.Errorf("invalid cache max entry size %q: %w", v, err)
					}
					if n < 0 {
						return fmt.Errorf("cache max entry size must be non-negative, got %d", n)
					}
					c.CacheMaxEntrySize = n
					return nil
				},
				getDB:   func(c *Config) string { return strconv.FormatInt(c.CacheMaxEntrySize, 10) },
				getYAML: func(c *Config) any { return c.CacheMaxEntrySize },
				isZero:  func(c *Config) bool { return c.CacheMaxEntrySize == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.CacheMaxEntrySize = src.CacheMaxEntrySize },
			},
			// --- time.Duration fields ---
			{
				dbKey:   "cache_max_time",
				yamlKey: "cache-max-time",
				set: func(c *Config, v string) error {
					d, err := time.ParseDuration(v)
					if err != nil {
						friendly, ok := durationFriendlyNames["cache-max-time"]
						if !ok {
							friendly = "cache-max-time"
						}
						return fmt.Errorf("invalid %s: %w (%s)", friendly, err, "cache_max_time")
					}
					c.CacheMaxTime = d
					return nil
				},
				getDB:   func(c *Config) string { return c.CacheMaxTime.String() },
				getYAML: func(c *Config) any { return c.CacheMaxTime.String() },
				isZero:  func(c *Config) bool { return c.CacheMaxTime == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.CacheMaxTime = src.CacheMaxTime },
			},
			{
				dbKey:   "cache_cleanup_interval",
				yamlKey: "cache-cleanup-interval",
				set: func(c *Config, v string) error {
					d, err := time.ParseDuration(v)
					if err != nil {
						friendly, ok := durationFriendlyNames["cache-cleanup-interval"]
						if !ok {
							friendly = "cache-cleanup-interval"
						}
						return fmt.Errorf("invalid %s: %w (%s)", friendly, err, "cache_cleanup_interval")
					}
					c.CacheCleanupInterval = d
					return nil
				},
				getDB:   func(c *Config) string { return c.CacheCleanupInterval.String() },
				getYAML: func(c *Config) any { return c.CacheCleanupInterval.String() },
				isZero:  func(c *Config) bool { return c.CacheCleanupInterval == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.CacheCleanupInterval = src.CacheCleanupInterval },
			},
			{
				dbKey:   "db_optimize_interval",
				yamlKey: "db-optimize-interval",
				set: func(c *Config, v string) error {
					d, err := time.ParseDuration(v)
					if err != nil {
						friendly, ok := durationFriendlyNames["db-optimize-interval"]
						if !ok {
							friendly = "db-optimize-interval"
						}
						return fmt.Errorf("invalid %s: %w (%s)", friendly, err, "db_optimize_interval")
					}
					c.DBOptimizeInterval = d
					return nil
				},
				getDB:   func(c *Config) string { return c.DBOptimizeInterval.String() },
				getYAML: func(c *Config) any { return c.DBOptimizeInterval.String() },
				isZero:  func(c *Config) bool { return c.DBOptimizeInterval == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.DBOptimizeInterval = src.DBOptimizeInterval },
			},
			{
				dbKey:   "worker_pool_max_idle_time",
				yamlKey: "worker-pool-max-idle-time",
				set: func(c *Config, v string) error {
					d, err := time.ParseDuration(v)
					if err != nil {
						friendly, ok := durationFriendlyNames["worker-pool-max-idle-time"]
						if !ok {
							friendly = "worker-pool-max-idle-time"
						}
						return fmt.Errorf("invalid %s: %w (%s)", friendly, err, "worker_pool_max_idle_time")
					}
					c.WorkerPoolMaxIdleTime = d
					return nil
				},
				getDB:   func(c *Config) string { return c.WorkerPoolMaxIdleTime.String() },
				getYAML: func(c *Config) any { return c.WorkerPoolMaxIdleTime.String() },
				isZero:  func(c *Config) bool { return c.WorkerPoolMaxIdleTime == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.WorkerPoolMaxIdleTime = src.WorkerPoolMaxIdleTime },
			},
			{
				dbKey:   "db_pool_monitor_interval",
				yamlKey: "db-pool-monitor-interval",
				set: func(c *Config, v string) error {
					d, err := time.ParseDuration(v)
					if err != nil {
						friendly, ok := durationFriendlyNames["db-pool-monitor-interval"]
						if !ok {
							friendly = "db-pool-monitor-interval"
						}
						return fmt.Errorf("invalid %s: %w (%s)", friendly, err, "db_pool_monitor_interval")
					}
					if d < 0 {
						return fmt.Errorf("db pool monitor interval must be non-negative, got %v", d)
					}
					c.DBPoolMonitorInterval = d
					return nil
				},
				getDB:   func(c *Config) string { return c.DBPoolMonitorInterval.String() },
				getYAML: func(c *Config) any { return c.DBPoolMonitorInterval.String() },
				isZero:  func(c *Config) bool { return c.DBPoolMonitorInterval == 0 },
				restart: true,
				setFrom: func(dst, src *Config) { dst.DBPoolMonitorInterval = src.DBPoolMonitorInterval },
			},
			// --- Themes ([]string) ---
			{
				dbKey:   "themes",
				yamlKey: "themes",
				set: func(c *Config, v string) error {
					var themes []string
					if err := json.Unmarshal([]byte(v), &themes); err != nil {
						return fmt.Errorf("invalid themes JSON %q: %w", v, err)
					}
					c.Themes = themes
					return nil
				},
				getDB: func(c *Config) string {
					sorted := make([]string, len(c.Themes))
					copy(sorted, c.Themes)
					sort.Strings(sorted)
					b, err := json.Marshal(sorted)
					if err != nil {
						return "[]"
					}
					return string(b)
				},
				getYAML: func(c *Config) any { return c.Themes },
				isZero:  func(c *Config) bool { return len(c.Themes) == 0 },
				restart: false,
				setFrom: func(dst, src *Config) {
					dst.Themes = make([]string, len(src.Themes))
					copy(dst.Themes, src.Themes)
				},
			},
		}
	})
	return fieldsCache
}

// FieldInfo exposes a config field's key, setter, and form presentation
// metadata to external packages (primarily the handlers package).
type FieldInfo struct {
	DBKey      string
	Set        func(c *Config, v string) error
	IsCheckbox bool // render as HTML checkbox
}

var (
	fieldsInfoOnce  sync.Once
	fieldsInfoCache []FieldInfo
)

// Fields returns metadata for all config fields, for use by handler packages.
func Fields() []FieldInfo {
	fieldsInfoOnce.Do(func() {
		result := make([]FieldInfo, 0, len(fields()))
		for _, f := range fields() {
			result = append(result, FieldInfo{
				DBKey:      f.dbKey,
				Set:        f.set,
				IsCheckbox: f.IsCheckbox,
			})
		}
		fieldsInfoCache = result
	})
	return fieldsInfoCache
}
