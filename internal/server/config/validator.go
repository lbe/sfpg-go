package config

import (
	"fmt"
	"strings"
)

// GuardrailWarning represents a non-fatal configuration anomaly that should be
// visible at startup because effective runtime behavior may differ from the
// configured value(s).
type GuardrailWarning struct {
	Check      string
	Configured string
	Effective  string
	Hint       string
}

// Validate validates all configuration values and returns an error if any are invalid.
func (c *Config) Validate() error {
	// Validate port
	if c.ListenerPort < 1 || c.ListenerPort > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", c.ListenerPort)
	}

	// Validate log level
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[strings.ToLower(c.LogLevel)] {
		return fmt.Errorf("invalid log level %q, must be one of: debug, info, warn, error", c.LogLevel)
	}

	// Validate log rollover
	validRollovers := map[string]bool{
		"daily":   true,
		"weekly":  true,
		"monthly": true,
	}
	if !validRollovers[strings.ToLower(c.LogRollover)] {
		return fmt.Errorf("invalid log rollover %q, must be one of: daily, weekly, monthly", c.LogRollover)
	}

	// Validate log retention count
	if c.LogRetentionCount < 1 {
		return fmt.Errorf("log retention count must be at least 1, got %d", c.LogRetentionCount)
	}

	// Validate session same-site
	validSameSite := map[string]bool{
		"Lax":    true,
		"Strict": true,
		"None":   true,
	}
	if !validSameSite[c.SessionSameSite] {
		return fmt.Errorf("invalid session same-site %q, must be one of: Lax, Strict, None", c.SessionSameSite)
	}

	// Validate cache sizes
	if c.CacheMaxSize < 0 {
		return fmt.Errorf("cache max size must be non-negative, got %d", c.CacheMaxSize)
	}
	if c.CacheMaxEntrySize < 0 {
		return fmt.Errorf("cache max entry size must be non-negative, got %d", c.CacheMaxEntrySize)
	}

	// Validate database pool sizes
	if c.DBMaxPoolSize < 1 {
		return fmt.Errorf("database max pool size must be at least 1, got %d", c.DBMaxPoolSize)
	}
	if c.DBMinIdleConnections < 0 {
		return fmt.Errorf("database min idle connections must be non-negative, got %d", c.DBMinIdleConnections)
	}
	if c.DBMinIdleConnections > c.DBMaxPoolSize {
		return fmt.Errorf("database min idle connections (%d) cannot exceed max pool size (%d)", c.DBMinIdleConnections, c.DBMaxPoolSize)
	}

	// Validate db pool monitor interval
	if c.DBPoolMonitorInterval < 0 {
		return fmt.Errorf("db pool monitor interval must be non-negative, got %v", c.DBPoolMonitorInterval)
	}

	// Validate worker pool (max 0 means auto; min 0 means no idle workers;
	// only reject negatives or min > max when both are set)
	if c.WorkerPoolMax < 0 {
		return fmt.Errorf("worker pool max must be non-negative, got %d", c.WorkerPoolMax)
	}
	if c.WorkerPoolMinIdle < 0 {
		return fmt.Errorf("worker pool min idle must be non-negative, got %d", c.WorkerPoolMinIdle)
	}
	if c.WorkerPoolMax > 0 && c.WorkerPoolMinIdle > 0 && c.WorkerPoolMinIdle > c.WorkerPoolMax {
		return fmt.Errorf("worker pool min idle (%d) cannot exceed max (%d)", c.WorkerPoolMinIdle, c.WorkerPoolMax)
	}

	// Validate queue size
	if c.QueueSize < 1 {
		return fmt.Errorf("queue size must be at least 1, got %d", c.QueueSize)
	}

	return nil
}

// ValidateSetting validates a single configuration setting by key and value.
// Returns an error if the value is invalid for that setting.
func (c *Config) ValidateSetting(key, value string) error {
	for _, f := range fields() {
		if f.dbKey == key {
			tmp := *c
			return f.set(&tmp, value)
		}
	}
	return nil // unknown keys are not validated
}

// ValidateGuardrails returns non-fatal warnings for dangerous or contradictory
// configuration combinations. These warnings are intended for startup
// diagnostics and operator visibility.
func (c *Config) ValidateGuardrails() []GuardrailWarning {
	if c == nil {
		return nil
	}

	warnings := make([]GuardrailWarning, 0)

	if c.DBMinIdleConnections > c.DBMaxPoolSize {
		warnings = append(warnings, GuardrailWarning{
			Check:      "db_min_idle_gt_db_max_pool",
			Configured: fmt.Sprintf("db_min_idle_connections=%d, db_max_pool_size=%d", c.DBMinIdleConnections, c.DBMaxPoolSize),
			Effective:  "database pool reconfiguration can fail and prior pool settings may remain active",
			Hint:       "Set db_min_idle_connections <= db_max_pool_size and restart or reload configuration.",
		})
	}

	if c.WorkerPoolMax > 0 && c.WorkerPoolMinIdle > 0 && c.WorkerPoolMinIdle > c.WorkerPoolMax {
		warnings = append(warnings, GuardrailWarning{
			Check:      "worker_pool_min_idle_gt_worker_pool_max",
			Configured: fmt.Sprintf("worker_pool_min_idle=%d, worker_pool_max=%d", c.WorkerPoolMinIdle, c.WorkerPoolMax),
			Effective:  "worker pool initialization can use unexpected limits or fail",
			Hint:       "Set worker_pool_min_idle <= worker_pool_max. worker_pool_max=0 still auto-sizes max; worker_pool_min_idle=0 means no idle workers.",
		})
	}

	if c.SessionSameSite == "None" && !c.SessionSecure {
		warnings = append(warnings, GuardrailWarning{
			Check:      "session_samesite_none_without_secure",
			Configured: fmt.Sprintf("session_same_site=%q, session_secure=%t", c.SessionSameSite, c.SessionSecure),
			Effective:  "browsers may reject the session cookie, causing repeated logins",
			Hint:       "Set session_secure=true when using session_same_site=None.",
		})
	}

	if c.EnableHTTPCache && c.CacheMaxSize == 0 {
		warnings = append(warnings, GuardrailWarning{
			Check:      "http_cache_enabled_with_zero_max_size",
			Configured: fmt.Sprintf("enable_http_cache=%t, cache_max_size=%d", c.EnableHTTPCache, c.CacheMaxSize),
			Effective:  "HTTP cache behaves as effectively disabled because no storage budget is available",
			Hint:       "Set cache_max_size to a positive value (for example 524288000 for 500MB) or disable HTTP cache explicitly.",
		})
	}

	if c.EnableHTTPCache && c.CacheMaxSize > 0 && c.CacheMaxEntrySize > c.CacheMaxSize {
		warnings = append(warnings, GuardrailWarning{
			Check:      "cache_entry_size_exceeds_cache_size",
			Configured: fmt.Sprintf("cache_max_entry_size=%d, cache_max_size=%d", c.CacheMaxEntrySize, c.CacheMaxSize),
			Effective:  "large responses can be skipped from cache because one entry exceeds total cache budget",
			Hint:       "Set cache_max_entry_size <= cache_max_size.",
		})
	}

	return warnings
}
