package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"go.local/sfpg/internal/gallerydb"
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
		// Log warning but don't fail the save if YAML export fails
		return nil
	}

	err = q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "LastKnownGoodConfig",
		Value:     yamlContent,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		// Log warning but don't fail the save if last known good save fails
		return nil
	}

	return nil
}

// ToMap converts the Config to a map of key-value pairs for database storage.
func (c *Config) ToMap() map[string]string {
	m := make(map[string]string)

	m["listener_address"] = c.ListenerAddress
	m["listener_port"] = strconv.Itoa(c.ListenerPort)
	m["log_directory"] = c.LogDirectory
	m["log_level"] = c.LogLevel
	m["log_rollover"] = c.LogRollover
	m["log_retention_count"] = strconv.Itoa(c.LogRetentionCount)
	m["site_name"] = c.SiteName
	themesJSON, _ := json.Marshal(c.Themes)
	m["themes"] = string(themesJSON)
	m["current_theme"] = c.CurrentTheme
	m["image_directory"] = c.ImageDirectory
	m["etag_version"] = c.ETagVersion
	m["session_max_age"] = strconv.Itoa(c.SessionMaxAge)
	m["session_http_only"] = strconv.FormatBool(c.SessionHttpOnly)
	m["session_secure"] = strconv.FormatBool(c.SessionSecure)
	m["session_same_site"] = c.SessionSameSite
	m["server_compression_enable"] = strconv.FormatBool(c.ServerCompressionEnable)
	m["enable_http_cache"] = strconv.FormatBool(c.EnableHTTPCache)
	m["cache_max_size"] = strconv.FormatInt(c.CacheMaxSize, 10)
	m["cache_max_time"] = c.CacheMaxTime.String()
	m["cache_max_entry_size"] = strconv.FormatInt(c.CacheMaxEntrySize, 10)
	m["cache_cleanup_interval"] = c.CacheCleanupInterval.String()
	m["db_max_pool_size"] = strconv.Itoa(c.DBMaxPoolSize)
	m["db_min_idle_connections"] = strconv.Itoa(c.DBMinIdleConnections)
	m["db_optimize_interval"] = c.DBOptimizeInterval.String()
	m["worker_pool_max"] = strconv.Itoa(c.WorkerPoolMax)
	m["worker_pool_min_idle"] = strconv.Itoa(c.WorkerPoolMinIdle)
	m["worker_pool_max_idle_time"] = c.WorkerPoolMaxIdleTime.String()
	m["queue_size"] = strconv.Itoa(c.QueueSize)
	m["enable_cache_preload"] = strconv.FormatBool(c.EnableCachePreload)
	m["max_http_cache_entry_insert_per_transaction"] = strconv.Itoa(c.MaxHTTPCacheEntryInsertPerTransaction)
	m["run_file_discovery"] = strconv.FormatBool(c.RunFileDiscovery)

	return m
}
