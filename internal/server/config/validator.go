package config

import (
	"fmt"
	"strconv"
	"strings"
)

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

	// Validate worker pool (0 means auto, so only validate if set)
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
	switch key {
	case "listener_port":
		port, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid port value %q: %w", value, err)
		}
		if port < 1 || port > 65535 {
			return fmt.Errorf("port must be between 1 and 65535, got %d", port)
		}
	case "log_level":
		validLogLevels := map[string]bool{
			"debug": true,
			"info":  true,
			"warn":  true,
			"error": true,
		}
		if !validLogLevels[strings.ToLower(value)] {
			return fmt.Errorf("invalid log level %q, must be one of: debug, info, warn, error", value)
		}
	case "log_rollover":
		validRollovers := map[string]bool{
			"daily":   true,
			"weekly":  true,
			"monthly": true,
		}
		if !validRollovers[strings.ToLower(value)] {
			return fmt.Errorf("invalid log rollover %q, must be one of: daily, weekly, monthly", value)
		}
	case "log_retention_count":
		count, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid log retention count %q: %w", value, err)
		}
		if count < 1 {
			return fmt.Errorf("log retention count must be at least 1, got %d", count)
		}
	case "session_same_site":
		validSameSite := map[string]bool{
			"Lax":    true,
			"Strict": true,
			"None":   true,
		}
		if !validSameSite[value] {
			return fmt.Errorf("invalid session same-site %q, must be one of: Lax, Strict, None", value)
		}
	case "cache_max_size", "cache_max_entry_size":
		size, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid size value %q: %w", value, err)
		}
		if size < 0 {
			return fmt.Errorf("size must be non-negative, got %d", size)
		}
	case "db_max_pool_size":
		size, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid db max pool size %q: %w", value, err)
		}
		if size < 1 {
			return fmt.Errorf("database max pool size must be at least 1, got %d", size)
		}
	case "db_min_idle_connections":
		count, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid min idle connections value %q: %w", value, err)
		}
		if count < 0 {
			return fmt.Errorf("database min idle connections must be non-negative, got %d", count)
		}
		// Check if it exceeds max pool size (if max is set)
		if c.DBMaxPoolSize > 0 && count > c.DBMaxPoolSize {
			return fmt.Errorf("database min idle connections (%d) cannot exceed max pool size (%d)", count, c.DBMaxPoolSize)
		}
	case "worker_pool_max":
		max, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid worker pool max value %q: %w", value, err)
		}
		if max < 0 {
			return fmt.Errorf("worker pool max must be non-negative, got %d", max)
		}
	case "worker_pool_min_idle":
		min, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid worker pool min idle value %q: %w", value, err)
		}
		if min < 0 {
			return fmt.Errorf("worker pool min idle must be non-negative, got %d", min)
		}
		// Check if it exceeds max (if both are set)
		if c.WorkerPoolMax > 0 && min > 0 && min > c.WorkerPoolMax {
			return fmt.Errorf("worker pool min idle (%d) cannot exceed max (%d)", min, c.WorkerPoolMax)
		}
	case "queue_size":
		size, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid queue size value %q: %w", value, err)
		}
		if size < 1 {
			return fmt.Errorf("queue size must be at least 1, got %d", size)
		}
	default:
		// Unknown keys are not validated (they might be valid but not yet implemented)
		return nil
	}
	return nil
}
