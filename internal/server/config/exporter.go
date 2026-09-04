package config

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ConfigDiff represents the differences between two configurations.
// It contains the current and new YAML representations and a list of changed keys.
type ConfigDiff struct {
	CurrentYAML string
	NewYAML     string
	Changes     []string // List of changed keys
}

// ExportToYAML exports the current configuration to YAML format.
// Excludes sensitive values like session secret.
func (c *Config) ExportToYAML() (string, error) {
	m := make(map[string]any, len(fields()))
	for _, f := range fields() {
		m[f.yamlKey] = f.getYAML(c)
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("failed to marshal config to YAML: %w", err)
	}
	return string(data), nil
}

// ImportFromYAML imports configuration from YAML content and saves to database.
// Validates the YAML and rejects session-secret.
func (c *Config) ImportFromYAML(yamlContent string, ctx context.Context, q ConfigSaver) error {
	imported, err := BuildImportedConfig(c, yamlContent)
	if err != nil {
		return err
	}
	*c = *imported

	// Validate the imported config
	if err := c.Validate(); err != nil {
		return fmt.Errorf("imported config is invalid: %w", err)
	}

	// Save to database
	if err := c.SaveToDatabase(ctx, q); err != nil {
		return fmt.Errorf("failed to save imported config to database: %w", err)
	}

	return nil
}

// RestoreLastKnownGood loads and returns the last known good configuration from the database.
// Returns an error if last known good config is not found or invalid.
// Note: This method only loads and parses the config - it does NOT save it back to the database.
func (c *Config) RestoreLastKnownGood(ctx context.Context, q ConfigQueries) (*Config, error) {
	configs, err := q.GetConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configs: %w", err)
	}

	var lastKnownGoodYAML string
	found := false
	for _, dbConfig := range configs {
		if dbConfig.Key == "LastKnownGoodConfig" {
			lastKnownGoodYAML = dbConfig.Value
			found = true
			break
		}
	}

	if !found || lastKnownGoodYAML == "" {
		return nil, fmt.Errorf("last known good config not found")
	}

	restoredConfig := DefaultConfig()

	var raw map[string]any
	if err := yaml.Unmarshal([]byte(lastKnownGoodYAML), &raw); err != nil {
		return nil, fmt.Errorf("invalid YAML in last known good config: %w", err)
	}

	// Invalid individual values are ignored so restoration continues.
	if applyErr := applyYAMLValues(restoredConfig, raw); applyErr != nil {
		return nil, fmt.Errorf("applying YAML values: %w", applyErr)
	}

	if err := restoredConfig.Validate(); err != nil {
		return nil, fmt.Errorf("restored config is invalid: %w", err)
	}

	return restoredConfig, nil
}

// PreviewImport parses YAML content and returns a diff showing what would change
// if the import were applied. Does not modify the current configuration.
func (c *Config) PreviewImport(yamlContent string) (*ConfigDiff, error) {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(yamlContent), &raw); err != nil {
		return nil, fmt.Errorf("invalid YAML syntax: %w", err)
	}

	if _, ok := raw["session-secret"]; ok {
		return nil, fmt.Errorf("session-secret cannot be imported (memory only)")
	}

	currentYAML, err := c.ExportToYAML()
	if err != nil {
		return nil, fmt.Errorf("failed to export current config: %w", err)
	}

	tempConfig := DefaultConfig()
	if loadErr := tempConfig.LoadFromYAML(); loadErr != nil {
		return nil, fmt.Errorf("loading preview config: %w", loadErr)
	}

	// Invalid individual values are ignored so the preview continues.
	if applyErr := applyYAMLValues(tempConfig, raw); applyErr != nil {
		return nil, fmt.Errorf("applying preview YAML values: %w", applyErr)
	}

	newYAML, err := tempConfig.ExportToYAML()
	if err != nil {
		return nil, fmt.Errorf("failed to export new config: %w", err)
	}

	changes := c.IdentifyChanges(tempConfig)

	return &ConfigDiff{
		CurrentYAML: currentYAML,
		NewYAML:     newYAML,
		Changes:     changes,
	}, nil
}

// GetLastKnownGoodDiff returns a diff showing current config vs last known good config.
// Returns an error if last known good config is not found or cannot be loaded.
func (c *Config) GetLastKnownGoodDiff(ctx context.Context, q ConfigQueries) (*ConfigDiff, error) {
	// Get current YAML
	currentYAML, err := c.ExportToYAML()
	if err != nil {
		return nil, fmt.Errorf("failed to export current config: %w", err)
	}

	// Get last known good config
	restoredConfig, err := c.RestoreLastKnownGood(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("failed to get last known good config: %w", err)
	}

	// Get last known good YAML
	lastKnownGoodYAML, err := restoredConfig.ExportToYAML()
	if err != nil {
		return nil, fmt.Errorf("failed to export last known good config: %w", err)
	}

	// Identify changes
	changes := c.IdentifyChanges(restoredConfig)

	return &ConfigDiff{
		CurrentYAML: currentYAML,
		NewYAML:     lastKnownGoodYAML,
		Changes:     changes,
	}, nil
}

// IdentifyChanges compares two configs and returns a list of changed keys.
func (c *Config) IdentifyChanges(other *Config) []string {
	var keys []string
	for _, f := range fields() {
		if f.getDB(c) != f.getDB(other) {
			keys = append(keys, f.yamlKey)
		}
	}
	return keys
}

// MergeDefaults applies default values to any zero-value non-boolean fields in
// the config. Boolean fields are intentionally excluded because Go's zero value
// for bool (false) is indistinguishable from an explicit false setting; their
// defaults are applied during YAML import where presence can be detected.
func (c *Config) MergeDefaults(defaults *Config) {
	for _, f := range fields() {
		if f.isZero(c) {
			f.setFrom(c, defaults)
		}
	}
}
