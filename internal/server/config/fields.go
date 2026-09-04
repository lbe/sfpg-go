package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
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

// field describes a single typed configuration field. T must be comparable so
// the isZero check can use == against the zero value.
type field[T comparable] struct {
	dbKey      string
	yamlKey    string
	ptr        func(*Config) *T        // returns pointer to the Config field
	parse      func(string) (T, error) // parse + validate; returns typed value
	format     func(T) string          // T → string (for DB storage)
	yamlFmt    func(T) any             // optional: custom YAML formatting. nil = return raw T.
	zeroNever  bool                    // if true, isZero always returns false (used for bools)
	restart    bool
	isCheckbox bool
}

// set parses a string and assigns the result to the Config field.
func (f field[T]) set(c *Config, v string) error {
	val, err := f.parse(v)
	if err != nil {
		return err
	}
	*f.ptr(c) = val
	return nil
}

// getDB formats the field value as a string for database storage.
func (f field[T]) getDB(c *Config) string { return f.format(*f.ptr(c)) }

// getYAML returns the field value for YAML export. If yamlFmt is set it is
// used; otherwise the raw value is returned.
func (f field[T]) getYAML(c *Config) any {
	if f.yamlFmt != nil {
		return f.yamlFmt(*f.ptr(c))
	}
	return *f.ptr(c)
}

// isZero returns true if the field is at its zero value. Bool fields always
// return false because Go's zero value (false) is indistinguishable from an
// explicit false setting.
func (f field[T]) isZero(c *Config) bool {
	if f.zeroNever {
		return false
	}
	var zero T
	return *f.ptr(c) == zero
}

// setFrom copies the field value from src to dst.
func (f field[T]) setFrom(dst, src *Config) { *f.ptr(dst) = *f.ptr(src) }

// toConfigField converts the generic field to a concrete configField suitable
// for the fields() table.
func (f field[T]) toConfigField() configField {
	return configField{
		dbKey:      f.dbKey,
		yamlKey:    f.yamlKey,
		set:        f.set,
		getDB:      f.getDB,
		getYAML:    f.getYAML,
		isZero:     f.isZero,
		restart:    f.restart,
		setFrom:    f.setFrom,
		IsCheckbox: f.isCheckbox,
	}
}

// --- string fields ---

func stringField(dbKey, yamlKey string, ptr func(*Config) *string, validate func(string) (string, error), restart bool) field[string] {
	if validate == nil {
		validate = func(v string) (string, error) { return v, nil }
	}
	return field[string]{
		dbKey:   dbKey,
		yamlKey: yamlKey,
		ptr:     ptr,
		parse:   validate,
		format:  func(v string) string { return v },
		restart: restart,
	}
}

// --- int fields ---

func intField(dbKey, yamlKey string, ptr func(*Config) *int, parse func(string) (int, error), restart bool) field[int] {
	return field[int]{
		dbKey:   dbKey,
		yamlKey: yamlKey,
		ptr:     ptr,
		parse:   parse,
		format:  strconv.Itoa,
		restart: restart,
	}
}

// --- bool fields ---
// name is the human-readable field name used in error messages
// (e.g., "http cache enable" not "enable_http_cache").
func boolField(dbKey, yamlKey, name string, ptr func(*Config) *bool, restart, isCheckbox bool) field[bool] {
	return field[bool]{
		dbKey:      dbKey,
		yamlKey:    yamlKey,
		ptr:        ptr,
		parse:      parseBoolNamed(name),
		format:     strconv.FormatBool,
		zeroNever:  true, // bool zero value is ambiguous
		restart:    restart,
		isCheckbox: isCheckbox,
	}
}

// --- int64 fields ---

func int64Field(dbKey, yamlKey string, ptr func(*Config) *int64, parse func(string) (int64, error), restart bool) field[int64] {
	return field[int64]{
		dbKey:   dbKey,
		yamlKey: yamlKey,
		ptr:     ptr,
		parse:   parse,
		format:  func(v int64) string { return strconv.FormatInt(v, 10) },
		restart: restart,
	}
}

// int64FieldZeroNever is like int64Field but sets zeroNever so isZero always
// returns false. This prevents MergeDefaults from clobbering an explicit 0,
// which is meaningful for fields where 0 is a valid sentinel (e.g. 0 = unlimited).
func int64FieldZeroNever(dbKey, yamlKey string, ptr func(*Config) *int64, parse func(string) (int64, error), restart bool) field[int64] {
	f := int64Field(dbKey, yamlKey, ptr, parse, restart)
	f.zeroNever = true
	return f
}

// --- duration fields ---

func durationField(dbKey, yamlKey, friendlyName string, ptr func(*Config) *time.Duration, restart bool) field[time.Duration] {
	return field[time.Duration]{
		dbKey:   dbKey,
		yamlKey: yamlKey,
		ptr:     ptr,
		parse:   parseDurationNamed(friendlyName, dbKey),
		format:  func(d time.Duration) string { return d.String() },
		yamlFmt: func(d time.Duration) any { return d.String() },
		restart: restart,
	}
}

// --- Parse helper functions ---

// parseBoolNamed returns a parse function for a bool field with the given
// friendly name used in error messages.
func parseBoolNamed(name string) func(string) (bool, error) {
	return func(v string) (bool, error) {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false, fmt.Errorf("invalid %s %q: %w", name, v, err)
		}
		return b, nil
	}
}

// parseIntNamed returns a parse function for an int field with the given
// friendly name used in error messages. No range validation.
func parseIntNamed(name string) func(string) (int, error) {
	return func(v string) (int, error) {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("invalid %s %q: %w", name, v, err)
		}
		return n, nil
	}
}

// parseIntMin returns a parse function for an int field that must be >= min.
// parseName is used in parse errors ("invalid <parseName> ...").
// valName is used in validation errors ("<valName> must be at least ...").
func parseIntMin(parseName, valName string, min int) func(string) (int, error) {
	return func(v string) (int, error) {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("invalid %s %q: %w", parseName, v, err)
		}
		if n < min {
			return 0, fmt.Errorf("%s must be at least %d, got %d", valName, min, n)
		}
		return n, nil
	}
}

// parseIntNonNeg returns a parse function for an int field that must be >= 0.
// parseName is used in parse errors; valName in validation errors.
func parseIntNonNeg(parseName, valName string) func(string) (int, error) {
	return func(v string) (int, error) {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("invalid %s %q: %w", parseName, v, err)
		}
		if n < 0 {
			return 0, fmt.Errorf("%s must be non-negative, got %d", valName, n)
		}
		return n, nil
	}
}

// parsePort parses a port number (1-65535).
func parsePort(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid port value %q: %w", v, err)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port must be between 1 and 65535, got %d", n)
	}
	return n, nil
}

// parseInt64NonNeg returns a parse function for an int64 field that must be >= 0.
func parseInt64NonNeg(name string) func(string) (int64, error) {
	return func(v string) (int64, error) {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid %s %q: %w", name, v, err)
		}
		if n < 0 {
			return 0, fmt.Errorf("%s must be non-negative, got %d", name, n)
		}
		return n, nil
	}
}

// parseDurationNamed returns a parse function for a time.Duration field with
// a friendly name used in error messages.
func parseDurationNamed(friendlyName, dbKey string) func(string) (time.Duration, error) {
	return func(v string) (time.Duration, error) {
		d, err := time.ParseDuration(v)
		if err != nil {
			return 0, fmt.Errorf("invalid %s: %w (%s)", friendlyName, err, dbKey)
		}
		return d, nil
	}
}

// validateHTTPCacheBodyCodec validates an http_cache_body_codec value via the
// cachelite package's registered codec IDs.
func validateHTTPCacheBodyCodec(v string) (string, error) {
	if err := cachelite.ValidateWriteCodecID(v); err != nil {
		return "", fmt.Errorf("invalid http cache body codec: %w", err)
	}
	return v, nil
}

// validateOneOfNamed returns a string validator that checks the (case-insensitive)
// value is in the allowed set. The original value is returned on success.
// name is used in error messages: "invalid <name> %q, must be one of: ..."
func validateOneOfNamed(name string, allowed ...string) func(string) (string, error) {
	valid := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		valid[strings.ToLower(a)] = true
	}
	return func(v string) (string, error) {
		if !valid[strings.ToLower(v)] {
			return "", fmt.Errorf("invalid %s %q, must be one of: %s", name, v, strings.Join(allowed, ", "))
		}
		return v, nil
	}
}

// validateOneOfExactNamed returns a string validator that checks the (exact-case)
// value is in the allowed set. Used for session_same_site which stores exact
// casing.
func validateOneOfExactNamed(name string, allowed ...string) func(string) (string, error) {
	valid := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		valid[a] = true
	}
	return func(v string) (string, error) {
		if !valid[v] {
			return "", fmt.Errorf("invalid %s %q, must be one of: %s", name, v, strings.Join(allowed, ", "))
		}
		return v, nil
	}
}

// parseDBPoolMonitorInterval is the special duration parse for
// db_pool_monitor_interval which also validates non-negative.
func parseDBPoolMonitorInterval(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid db pool monitor interval: %w (%s)", err, "db_pool_monitor_interval")
	}
	if d < 0 {
		return 0, fmt.Errorf("db pool monitor interval must be non-negative, got %v", d)
	}
	return d, nil
}

// themesField returns a configField directly (not via field[T]) because
// []string is not comparable and cannot use the generic field type.
func themesField() configField {
	ptr := func(c *Config) *[]string { return &c.Themes }
	return configField{
		dbKey:   "themes",
		yamlKey: "themes",
		set: func(c *Config, v string) error {
			var themes []string
			if err := json.Unmarshal([]byte(v), &themes); err != nil {
				return fmt.Errorf("invalid themes JSON %q: %w", v, err)
			}
			*ptr(c) = themes
			return nil
		},
		getDB: func(c *Config) string {
			sorted := make([]string, len(*ptr(c)))
			copy(sorted, *ptr(c))
			sort.Strings(sorted)
			b, err := json.Marshal(sorted)
			if err != nil {
				return "[]"
			}
			return string(b)
		},
		getYAML: func(c *Config) any { return *ptr(c) },
		isZero:  func(c *Config) bool { return len(*ptr(c)) == 0 },
		restart: false,
		setFrom: func(dst, src *Config) {
			dst.Themes = make([]string, len(src.Themes))
			copy(dst.Themes, src.Themes)
		},
	}
}

// fields returns the single source of truth for all 38 config fields.
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
			// --- String fields (9) ---
			stringField("listener_address", "listener-address", func(c *Config) *string { return &c.ListenerAddress }, nil, true).toConfigField(),
			stringField("log_directory", "log-directory", func(c *Config) *string { return &c.LogDirectory }, nil, true).toConfigField(),
			stringField("log_level", "log-level", func(c *Config) *string { return &c.LogLevel }, validateOneOfNamed("log level", "debug", "info", "warn", "error"), true).toConfigField(),
			stringField("log_rollover", "log-rollover", func(c *Config) *string { return &c.LogRollover }, validateOneOfNamed("log rollover", "daily", "weekly", "monthly"), true).toConfigField(),
			stringField("site_name", "site-name", func(c *Config) *string { return &c.SiteName }, nil, false).toConfigField(),
			stringField("current_theme", "current-theme", func(c *Config) *string { return &c.CurrentTheme }, nil, false).toConfigField(),
			stringField("image_directory", "image-directory", func(c *Config) *string { return &c.ImageDirectory }, nil, true).toConfigField(),
			stringField("etag_version", "etag-version", func(c *Config) *string { return &c.ETagVersion }, nil, false).toConfigField(),
			stringField("http_cache_body_codec", "http-cache-body-codec", func(c *Config) *string { return &c.HTTPCacheBodyCodec }, validateHTTPCacheBodyCodec, false).toConfigField(),
			stringField("session_same_site", "session-same-site", func(c *Config) *string { return &c.SessionSameSite }, validateOneOfExactNamed("session same-site", "Lax", "Strict", "None"), true).toConfigField(),

			// --- Int fields (11) ---
			intField("listener_port", "listener-port", func(c *Config) *int { return &c.ListenerPort }, parsePort, true).toConfigField(),
			intField("log_retention_count", "log-retention-count", func(c *Config) *int { return &c.LogRetentionCount }, parseIntMin("log retention count", "log retention count", 1), true).toConfigField(),
			intField("session_max_age", "session-max-age", func(c *Config) *int { return &c.SessionMaxAge }, parseIntNamed("session max age"), true).toConfigField(),
			// db_max_pool_size: parse error uses "db max pool size", validation error uses "database max pool size"
			intField("db_max_pool_size", "db-max-pool-size", func(c *Config) *int { return &c.DBMaxPoolSize }, parseIntMin("db max pool size", "database max pool size", 1), true).toConfigField(),
			// db_min_idle_connections: parse error uses "db min idle connections", validation error uses "database min idle connections"
			intField("db_min_idle_connections", "db-min-idle-connections", func(c *Config) *int { return &c.DBMinIdleConnections }, parseIntNonNeg("db min idle connections", "database min idle connections"), true).toConfigField(),
			intField("worker_pool_max", "worker-pool-max", func(c *Config) *int { return &c.WorkerPoolMax }, parseIntNonNeg("worker pool max", "worker pool max"), true).toConfigField(),
			intField("worker_pool_min_idle", "worker-pool-min-idle", func(c *Config) *int { return &c.WorkerPoolMinIdle }, parseIntNonNeg("worker pool min idle", "worker pool min idle"), true).toConfigField(),
			intField("queue_size", "queue-size", func(c *Config) *int { return &c.QueueSize }, parseIntMin("queue size", "queue size", 1), true).toConfigField(),
			intField("max_http_cache_entry_insert_per_transaction", "max-http-cache-entry-insert-per-transaction", func(c *Config) *int { return &c.MaxHTTPCacheEntryInsertPerTransaction }, parseIntNamed("max http cache entry insert per transaction"), false).toConfigField(),
			intField("lockout_duration", "lockout-duration", func(c *Config) *int { return &c.LockoutDuration }, parseIntNamed("lockout duration"), false).toConfigField(),
			intField("lockout_threshold", "lockout-threshold", func(c *Config) *int { return &c.LockoutThreshold }, parseIntMin("lockout threshold", "lockout threshold", 1), false).toConfigField(),
			intField("login_rate_limit_per_ip", "login-rate-limit-per-ip", func(c *Config) *int { return &c.LoginRateLimitPerIP }, parseIntNonNeg("login rate limit per IP", "login rate limit per IP"), false).toConfigField(),
			intField("discovery_queue_max", "discovery-queue-max", func(c *Config) *int { return &c.DiscoveryQueueMax }, parseIntNonNeg("discovery queue max", "discovery queue max"), false).toConfigField(),

			// --- Bool fields (6) ---
			// Note: third arg is human-readable name for error messages, not dbKey.
			boolField("session_http_only", "session-http-only", "session http only", func(c *Config) *bool { return &c.SessionHttpOnly }, true, true).toConfigField(),
			boolField("session_secure", "session-secure", "session secure", func(c *Config) *bool { return &c.SessionSecure }, true, true).toConfigField(),
			boolField("enable_http_cache", "http-cache", "http cache enable", func(c *Config) *bool { return &c.EnableHTTPCache }, true, true).toConfigField(),
			boolField("enable_cache_preload", "enable-cache-preload", "enable cache preload", func(c *Config) *bool { return &c.EnableCachePreload }, false, true).toConfigField(),
			boolField("run_file_discovery", "discover", "run file discovery", func(c *Config) *bool { return &c.RunFileDiscovery }, false, true).toConfigField(),
			boolField("restart_after_discovery", "restart-after-discovery", "restart after discovery", func(c *Config) *bool { return &c.RestartAfterDiscovery }, false, true).toConfigField(),

			// --- Int64 fields (3) ---
			int64Field("cache_max_size", "cache-max-size", func(c *Config) *int64 { return &c.CacheMaxSize }, parseInt64NonNeg("cache max size"), true).toConfigField(),
			int64Field("cache_max_entry_size", "cache-max-entry-size", func(c *Config) *int64 { return &c.CacheMaxEntrySize }, parseInt64NonNeg("cache max entry size"), true).toConfigField(),
			int64FieldZeroNever("dque_max_disk_bytes", "dque-max-disk-bytes", func(c *Config) *int64 { return &c.DQueMaxDiskBytes }, parseInt64NonNeg("dque max disk bytes"), false).toConfigField(),

			// --- Duration fields (5) ---
			durationField("cache_max_time", "cache-max-time", "cache max time", func(c *Config) *time.Duration { return &c.CacheMaxTime }, true).toConfigField(),
			durationField("cache_cleanup_interval", "cache-cleanup-interval", "cache cleanup interval", func(c *Config) *time.Duration { return &c.CacheCleanupInterval }, true).toConfigField(),
			durationField("db_optimize_interval", "db-optimize-interval", "db optimize interval", func(c *Config) *time.Duration { return &c.DBOptimizeInterval }, true).toConfigField(),
			durationField("worker_pool_max_idle_time", "worker-pool-max-idle-time", "worker pool max idle time", func(c *Config) *time.Duration { return &c.WorkerPoolMaxIdleTime }, true).toConfigField(),
			// db_pool_monitor_interval has extra non-negative validation in its parse function.
			{
				dbKey:   "db_pool_monitor_interval",
				yamlKey: "db-pool-monitor-interval",
				set: func(c *Config, v string) error {
					d, err := parseDBPoolMonitorInterval(v)
					if err != nil {
						return err
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

			// --- Themes ([]string, not comparable) ---
			themesField(),
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
