package config

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

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

// Error returns the validation failure message.
func (e *ApplyValidationError) Error() string {
	if e == nil || e.err == nil {
		return "configuration validation failed"
	}
	return e.err.Error()
}

// Unwrap returns the underlying validation error.
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

// Error returns the persistence failure message.
func (e *ApplyPersistenceError) Error() string {
	if e == nil || e.err == nil {
		return "failed to persist configuration"
	}
	return e.err.Error()
}

// Unwrap returns the underlying persistence error.
func (e *ApplyPersistenceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// BuildImportedConfig parses and applies YAML content on top of the current
// base config. Fields absent from the imported YAML retain their current
// (live) value. It rejects session-secret because it is memory-only.
func BuildImportedConfig(base *Config, yamlContent string) (*Config, error) {
	if base == nil {
		return nil, fmt.Errorf("base config cannot be nil")
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlContent), &raw); err != nil {
		return nil, fmt.Errorf("invalid YAML syntax: %w", err)
	}

	if _, ok := raw["session-secret"]; ok {
		return nil, fmt.Errorf("session-secret cannot be imported (memory only)")
	}

	candidate := *base
	if err := applyYAMLValues(&candidate, raw); err != nil {
		return nil, err
	}

	return normalizeConfigCandidate(&candidate), nil
}

// ApplyConfig validates, persists, and computes restart requirements for a new configuration.
func ApplyConfig(ctx context.Context, svc ConfigStore, current, candidate *Config) (*ApplyResult, error) {
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

// normalizeConfigCandidate returns a cleaned copy of cfg with trimmed strings
// and normalized file paths. A nil config returns nil.
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

// restartRequiredKeys returns the database key names for settings that differ
// between current and candidate and require a server restart to take effect.
func restartRequiredKeys(current, candidate *Config) []string {
	var keys []string
	for _, f := range fields() {
		if !f.restart {
			continue
		}
		if f.getDB(current) != f.getDB(candidate) {
			keys = append(keys, f.dbKey)
		}
	}
	return keys
}
