package config

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ApplyResult captures the outcome of applying a config update.
type ApplyResult struct {
	Config              *Config
	RestartRequired     bool
	RestartRequiredKeys []string
}

// ApplyValidationError indicates the candidate config failed validation.
type ApplyValidationError struct {
	err error
}

func (e *ApplyValidationError) Error() string {
	if e == nil || e.err == nil {
		return "configuration validation failed"
	}
	return e.err.Error()
}

func (e *ApplyValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// ApplyPersistenceError indicates the candidate config could not be persisted.
type ApplyPersistenceError struct {
	err error
}

func (e *ApplyPersistenceError) Error() string {
	if e == nil || e.err == nil {
		return "failed to persist configuration"
	}
	return e.err.Error()
}

func (e *ApplyPersistenceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// BuildImportedConfig parses and applies YAML content onto a cloned base config.
// It enforces strict type checks for duration settings used by modal submission.
func BuildImportedConfig(base *Config, yamlContent string) (*Config, error) {
	if base == nil {
		return nil, fmt.Errorf("base config cannot be nil")
	}

	var yamlCfg yamlConfigForConfig
	if err := yaml.Unmarshal([]byte(yamlContent), &yamlCfg); err != nil {
		return nil, fmt.Errorf("invalid YAML syntax: %w", err)
	}

	var configMap map[string]any
	if err := yaml.Unmarshal([]byte(yamlContent), &configMap); err == nil {
		if _, hasSecret := configMap["session-secret"]; hasSecret {
			return nil, fmt.Errorf("session-secret cannot be imported (memory only)")
		}
	}

	if yamlCfg.CacheMaxTime != nil {
		if _, err := time.ParseDuration(*yamlCfg.CacheMaxTime); err != nil {
			return nil, fmt.Errorf("invalid cache max time: %w", err)
		}
	}
	if yamlCfg.CacheCleanupInterval != nil {
		if _, err := time.ParseDuration(*yamlCfg.CacheCleanupInterval); err != nil {
			return nil, fmt.Errorf("invalid cache cleanup interval: %w", err)
		}
	}
	if yamlCfg.DBOptimizeInterval != nil {
		if _, err := time.ParseDuration(*yamlCfg.DBOptimizeInterval); err != nil {
			return nil, fmt.Errorf("invalid db optimize interval: %w", err)
		}
	}
	if yamlCfg.WorkerPoolMaxIdleTime != nil {
		if _, err := time.ParseDuration(*yamlCfg.WorkerPoolMaxIdleTime); err != nil {
			return nil, fmt.Errorf("invalid worker pool max idle time: %w", err)
		}
	}

	candidate := *base
	applyYAMLConfigToConfig(&candidate, &yamlCfg)

	return normalizeConfigCandidate(&candidate), nil
}

// ApplyConfig validates, persists, and computes restart requirements for a new configuration.
func ApplyConfig(ctx context.Context, svc ConfigService, current, candidate *Config) (*ApplyResult, error) {
	if svc == nil {
		return nil, fmt.Errorf("config service cannot be nil")
	}
	if current == nil {
		return nil, fmt.Errorf("current config cannot be nil")
	}
	if candidate == nil {
		return nil, fmt.Errorf("candidate config cannot be nil")
	}

	normalized := normalizeConfigCandidate(candidate)
	if err := svc.Validate(normalized); err != nil {
		return nil, &ApplyValidationError{err: err}
	}

	restartKeys := restartRequiredKeys(current, normalized)
	if err := svc.Save(ctx, normalized); err != nil {
		return nil, &ApplyPersistenceError{err: err}
	}

	return &ApplyResult{
		Config:              normalized,
		RestartRequired:     len(restartKeys) > 0,
		RestartRequiredKeys: restartKeys,
	}, nil
}

func normalizeConfigCandidate(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}

	out := *cfg
	out.ListenerAddress = strings.TrimSpace(out.ListenerAddress)
	out.LogDirectory = normalizePath(out.LogDirectory)
	out.LogLevel = strings.TrimSpace(out.LogLevel)
	out.LogRollover = strings.TrimSpace(out.LogRollover)
	out.SiteName = strings.TrimSpace(out.SiteName)
	out.CurrentTheme = strings.TrimSpace(out.CurrentTheme)
	out.ImageDirectory = normalizePath(out.ImageDirectory)
	out.SessionSameSite = strings.TrimSpace(out.SessionSameSite)

	for i := range out.Themes {
		out.Themes[i] = strings.TrimSpace(out.Themes[i])
	}

	return &out
}

func normalizePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(trimmed)
}

func restartRequiredKeys(current, candidate *Config) []string {
	keys := make([]string, 0, 24)

	if current.ListenerAddress != candidate.ListenerAddress {
		keys = append(keys, "listener_address")
	}
	if current.ListenerPort != candidate.ListenerPort {
		keys = append(keys, "listener_port")
	}
	if current.LogDirectory != candidate.LogDirectory {
		keys = append(keys, "log_directory")
	}
	if current.LogLevel != candidate.LogLevel {
		keys = append(keys, "log_level")
	}
	if current.LogRollover != candidate.LogRollover {
		keys = append(keys, "log_rollover")
	}
	if current.LogRetentionCount != candidate.LogRetentionCount {
		keys = append(keys, "log_retention_count")
	}
	if current.ImageDirectory != candidate.ImageDirectory {
		keys = append(keys, "image_directory")
	}
	if current.ServerCompressionEnable != candidate.ServerCompressionEnable {
		keys = append(keys, "server_compression_enable")
	}
	if current.EnableHTTPCache != candidate.EnableHTTPCache {
		keys = append(keys, "enable_http_cache")
	}
	if current.CacheMaxSize != candidate.CacheMaxSize {
		keys = append(keys, "cache_max_size")
	}
	if current.CacheMaxEntrySize != candidate.CacheMaxEntrySize {
		keys = append(keys, "cache_max_entry_size")
	}
	if current.CacheMaxTime != candidate.CacheMaxTime {
		keys = append(keys, "cache_max_time")
	}
	if current.CacheCleanupInterval != candidate.CacheCleanupInterval {
		keys = append(keys, "cache_cleanup_interval")
	}
	if current.DBMaxPoolSize != candidate.DBMaxPoolSize {
		keys = append(keys, "db_max_pool_size")
	}
	if current.DBMinIdleConnections != candidate.DBMinIdleConnections {
		keys = append(keys, "db_min_idle_connections")
	}
	if current.DBOptimizeInterval != candidate.DBOptimizeInterval {
		keys = append(keys, "db_optimize_interval")
	}
	if current.WorkerPoolMax != candidate.WorkerPoolMax {
		keys = append(keys, "worker_pool_max")
	}
	if current.WorkerPoolMinIdle != candidate.WorkerPoolMinIdle {
		keys = append(keys, "worker_pool_min_idle")
	}
	if current.WorkerPoolMaxIdleTime != candidate.WorkerPoolMaxIdleTime {
		keys = append(keys, "worker_pool_max_idle_time")
	}
	if current.QueueSize != candidate.QueueSize {
		keys = append(keys, "queue_size")
	}
	if current.SessionMaxAge != candidate.SessionMaxAge {
		keys = append(keys, "session_max_age")
	}
	if current.SessionHttpOnly != candidate.SessionHttpOnly {
		keys = append(keys, "session_http_only")
	}
	if current.SessionSecure != candidate.SessionSecure {
		keys = append(keys, "session_secure")
	}
	if current.SessionSameSite != candidate.SessionSameSite {
		keys = append(keys, "session_same_site")
	}

	return keys
}
