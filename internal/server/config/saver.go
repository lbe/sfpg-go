package config

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// ConfigSaver is an interface for saving configuration values to the database.
// It provides a minimal interface for config persistence operations.
type ConfigSaver interface {
	UpsertConfigValueOnly(ctx context.Context, arg gallerydb.UpsertConfigValueOnlyParams) error
}

// SaveToDatabase saves all configuration values to the database.
// It converts the Config struct to a map and saves each key-value pair.
// Also saves a copy as "LastKnownGoodConfig" for recovery purposes.
// Note: This method calls ExportToYAML which will be moved to config/exporter.go in Task 6.7.
func (c *Config) SaveToDatabase(ctx context.Context, q ConfigSaver) error {
	now := time.Now().Unix()

	// Map of all config keys to their string values
	configMap := c.ToMap()

	for key, value := range configMap {
		// Use UpsertConfigValueOnly for now (metadata will be set separately during initialization)
		err := q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
			Key:       key,
			Value:     value,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("failed to save config key %q: %w", key, err)
		}
	}

	// Save last known good config as YAML string
	yamlContent, err := c.ExportToYAML()
	if err != nil {
		slog.Warn("failed to export YAML for last known good config", "err", err)
		return nil
	}

	err = q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "LastKnownGoodConfig",
		Value:     yamlContent,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Warn("failed to save last known good config", "err", err)
		return nil
	}

	return nil
}

// ToMap converts the Config to a map of key-value pairs for database storage.
func (c *Config) ToMap() map[string]string {
	m := make(map[string]string, len(fields()))
	for _, f := range fields() {
		m[f.dbKey] = f.getDB(c)
	}
	return m
}
