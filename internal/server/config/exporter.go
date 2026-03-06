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
	// Create a map of config values for YAML export
	configMap := make(map[string]any)

	// Server settings
	configMap["listener-address"] = c.ListenerAddress
	configMap["listener-port"] = c.ListenerPort

	// Logging settings
	configMap["log-directory"] = c.LogDirectory
	configMap["log-level"] = c.LogLevel
	configMap["log-rollover"] = c.LogRollover
	configMap["log-retention-count"] = c.LogRetentionCount

	// Application settings
	configMap["site-name"] = c.SiteName
	configMap["themes"] = c.Themes
	configMap["current-theme"] = c.CurrentTheme
	configMap["image-directory"] = c.ImageDirectory
	configMap["etag-version"] = c.ETagVersion

	// Session settings
	configMap["session-max-age"] = c.SessionMaxAge
	configMap["session-http-only"] = c.SessionHttpOnly
	configMap["session-secure"] = c.SessionSecure
	configMap["session-same-site"] = c.SessionSameSite

	// Performance settings
	configMap["compression"] = c.ServerCompressionEnable
	configMap["http-cache"] = c.EnableHTTPCache
	configMap["cache-max-size"] = c.CacheMaxSize
	configMap["cache-max-time"] = c.CacheMaxTime.String()
	configMap["cache-max-entry-size"] = c.CacheMaxEntrySize
	configMap["cache-cleanup-interval"] = c.CacheCleanupInterval.String()
	configMap["db-max-pool-size"] = c.DBMaxPoolSize
	configMap["db-min-idle-connections"] = c.DBMinIdleConnections
	configMap["db-optimize-interval"] = c.DBOptimizeInterval.String()
	configMap["worker-pool-max"] = c.WorkerPoolMax
	configMap["worker-pool-min-idle"] = c.WorkerPoolMinIdle
	configMap["worker-pool-max-idle-time"] = c.WorkerPoolMaxIdleTime.String()
	configMap["queue-size"] = c.QueueSize
	configMap["enable-cache-preload"] = c.EnableCachePreload
	configMap["max-http-cache-entry-insert-per-transaction"] = c.MaxHTTPCacheEntryInsertPerTransaction

	// Discovery settings
	configMap["discover"] = c.RunFileDiscovery

	// Note: session-secret is intentionally excluded

	// Marshal to YAML
	data, err := yaml.Marshal(configMap)
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

	// Find LastKnownGoodConfig
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

	// Parse YAML and create new Config (without saving - this is just for restoration)
	restoredConfig := DefaultConfig()

	// Parse YAML directly without using ImportFromYAML (which would try to save)
	var yamlConfig yamlConfigForConfig
	if err := yaml.Unmarshal([]byte(lastKnownGoodYAML), &yamlConfig); err != nil {
		return nil, fmt.Errorf("invalid YAML in last known good config: %w", err)
	}

	// Apply YAML to config
	applyYAMLConfigToConfig(restoredConfig, &yamlConfig)

	// Validate the restored config
	if err := restoredConfig.Validate(); err != nil {
		return nil, fmt.Errorf("restored config is invalid: %w", err)
	}

	return restoredConfig, nil
}

// PreviewImport parses YAML content and returns a diff showing what would change
// if the import were applied. Does not modify the current configuration.
func (c *Config) PreviewImport(yamlContent string) (*ConfigDiff, error) {
	// Parse the new YAML
	var newConfigMap map[string]any
	if err := yaml.Unmarshal([]byte(yamlContent), &newConfigMap); err != nil {
		return nil, fmt.Errorf("invalid YAML syntax: %w", err)
	}

	// Check for session-secret (should be rejected)
	if _, hasSecret := newConfigMap["session-secret"]; hasSecret {
		return nil, fmt.Errorf("session-secret cannot be imported (memory only)")
	}

	// Generate current YAML for comparison
	currentYAML, err := c.ExportToYAML()
	if err != nil {
		return nil, fmt.Errorf("failed to export current config: %w", err)
	}

	// Create a temporary config to parse new YAML
	tempConfig := DefaultConfig()
	tempConfig.LoadFromYAML() // Load current YAML if exists

	// Parse new YAML into temp config
	var newYAMLConfig yamlConfigForConfig
	if unmarshalErr := yaml.Unmarshal([]byte(yamlContent), &newYAMLConfig); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", unmarshalErr)
	}
	applyYAMLConfigToConfig(tempConfig, &newYAMLConfig)

	// Generate new YAML
	newYAML, err := tempConfig.ExportToYAML()
	if err != nil {
		return nil, fmt.Errorf("failed to export new config: %w", err)
	}

	// Identify changed keys
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
	var changes []string

	if c.ListenerAddress != other.ListenerAddress {
		changes = append(changes, "listener-address")
	}
	if c.ListenerPort != other.ListenerPort {
		changes = append(changes, "listener-port")
	}
	if c.LogDirectory != other.LogDirectory {
		changes = append(changes, "log-directory")
	}
	if c.LogLevel != other.LogLevel {
		changes = append(changes, "log-level")
	}
	if c.LogRollover != other.LogRollover {
		changes = append(changes, "log-rollover")
	}
	if c.LogRetentionCount != other.LogRetentionCount {
		changes = append(changes, "log-retention-count")
	}
	if c.SiteName != other.SiteName {
		changes = append(changes, "site-name")
	}
	// Note: Themes comparison would need slice comparison
	if c.CurrentTheme != other.CurrentTheme {
		changes = append(changes, "current-theme")
	}
	if c.ImageDirectory != other.ImageDirectory {
		changes = append(changes, "image-directory")
	}
	if c.SessionMaxAge != other.SessionMaxAge {
		changes = append(changes, "session-max-age")
	}
	if c.SessionHttpOnly != other.SessionHttpOnly {
		changes = append(changes, "session-http-only")
	}
	if c.SessionSecure != other.SessionSecure {
		changes = append(changes, "session-secure")
	}
	if c.SessionSameSite != other.SessionSameSite {
		changes = append(changes, "session-same-site")
	}
	if c.ServerCompressionEnable != other.ServerCompressionEnable {
		changes = append(changes, "compression")
	}
	if c.EnableHTTPCache != other.EnableHTTPCache {
		changes = append(changes, "http-cache")
	}
	if c.CacheMaxSize != other.CacheMaxSize {
		changes = append(changes, "cache-max-size")
	}
	if c.CacheMaxTime != other.CacheMaxTime {
		changes = append(changes, "cache-max-time")
	}
	if c.CacheMaxEntrySize != other.CacheMaxEntrySize {
		changes = append(changes, "cache-max-entry-size")
	}
	if c.CacheCleanupInterval != other.CacheCleanupInterval {
		changes = append(changes, "cache-cleanup-interval")
	}
	if c.DBMaxPoolSize != other.DBMaxPoolSize {
		changes = append(changes, "db-max-pool-size")
	}
	if c.DBMinIdleConnections != other.DBMinIdleConnections {
		changes = append(changes, "db-min-idle-connections")
	}
	if c.DBOptimizeInterval != other.DBOptimizeInterval {
		changes = append(changes, "db-optimize-interval")
	}
	if c.WorkerPoolMax != other.WorkerPoolMax {
		changes = append(changes, "worker-pool-max")
	}
	if c.WorkerPoolMinIdle != other.WorkerPoolMinIdle {
		changes = append(changes, "worker-pool-min-idle")
	}
	if c.WorkerPoolMaxIdleTime != other.WorkerPoolMaxIdleTime {
		changes = append(changes, "worker-pool-max-idle-time")
	}
	if c.QueueSize != other.QueueSize {
		changes = append(changes, "queue-size")
	}
	if c.EnableCachePreload != other.EnableCachePreload {
		changes = append(changes, "enable-cache-preload")
	}
	if c.MaxHTTPCacheEntryInsertPerTransaction != other.MaxHTTPCacheEntryInsertPerTransaction {
		changes = append(changes, "max-http-cache-entry-insert-per-transaction")
	}
	if c.RunFileDiscovery != other.RunFileDiscovery {
		changes = append(changes, "discover")
	}

	return changes
}

// MergeDefaults applies default values to any zero-value fields in the config.
// This is useful when loading partial configuration from database or YAML.
func (c *Config) MergeDefaults(defaults *Config) {
	if c.ListenerAddress == "" {
		c.ListenerAddress = defaults.ListenerAddress
	}
	if c.ListenerPort == 0 {
		c.ListenerPort = defaults.ListenerPort
	}
	if c.LogDirectory == "" {
		c.LogDirectory = defaults.LogDirectory
	}
	if c.LogLevel == "" {
		c.LogLevel = defaults.LogLevel
	}
	if c.LogRollover == "" {
		c.LogRollover = defaults.LogRollover
	}
	if c.LogRetentionCount == 0 {
		c.LogRetentionCount = defaults.LogRetentionCount
	}
	if c.SiteName == "" {
		c.SiteName = defaults.SiteName
	}
	if len(c.Themes) == 0 {
		c.Themes = defaults.Themes
	}
	if c.CurrentTheme == "" {
		c.CurrentTheme = defaults.CurrentTheme
	}
	if c.ImageDirectory == "" {
		c.ImageDirectory = defaults.ImageDirectory
	}
	if c.SessionMaxAge == 0 {
		c.SessionMaxAge = defaults.SessionMaxAge
	}
	if !c.SessionHttpOnly && !defaults.SessionHttpOnly {
		// Only set if defaults is true (HttpOnly defaults to true)
		c.SessionHttpOnly = defaults.SessionHttpOnly
	}
	if !c.SessionSecure && !defaults.SessionSecure {
		// Only set if defaults is true (Secure defaults to true)
		c.SessionSecure = defaults.SessionSecure
	}
	if c.SessionSameSite == "" {
		c.SessionSameSite = defaults.SessionSameSite
	}
	if !c.ServerCompressionEnable && !defaults.ServerCompressionEnable {
		c.ServerCompressionEnable = defaults.ServerCompressionEnable
	}
	if !c.EnableHTTPCache && !defaults.EnableHTTPCache {
		c.EnableHTTPCache = defaults.EnableHTTPCache
	}
	if c.CacheMaxSize == 0 {
		c.CacheMaxSize = defaults.CacheMaxSize
	}
	if c.CacheMaxTime == 0 {
		c.CacheMaxTime = defaults.CacheMaxTime
	}
	if c.CacheMaxEntrySize == 0 {
		c.CacheMaxEntrySize = defaults.CacheMaxEntrySize
	}
	if c.CacheCleanupInterval == 0 {
		c.CacheCleanupInterval = defaults.CacheCleanupInterval
	}
	if c.DBMaxPoolSize == 0 {
		c.DBMaxPoolSize = defaults.DBMaxPoolSize
	}
	if c.DBMinIdleConnections == 0 {
		c.DBMinIdleConnections = defaults.DBMinIdleConnections
	}
	if c.DBOptimizeInterval == 0 {
		c.DBOptimizeInterval = defaults.DBOptimizeInterval
	}
	if c.WorkerPoolMax == 0 {
		c.WorkerPoolMax = defaults.WorkerPoolMax
	}
	if c.WorkerPoolMinIdle == 0 {
		c.WorkerPoolMinIdle = defaults.WorkerPoolMinIdle
	}
	if c.WorkerPoolMaxIdleTime == 0 {
		c.WorkerPoolMaxIdleTime = defaults.WorkerPoolMaxIdleTime
	}
	if c.QueueSize == 0 {
		c.QueueSize = defaults.QueueSize
	}
	if !c.EnableCachePreload && !defaults.EnableCachePreload {
		c.EnableCachePreload = defaults.EnableCachePreload
	}
	if c.MaxHTTPCacheEntryInsertPerTransaction == 0 {
		c.MaxHTTPCacheEntryInsertPerTransaction = defaults.MaxHTTPCacheEntryInsertPerTransaction
	}
	if !c.RunFileDiscovery && !defaults.RunFileDiscovery {
		c.RunFileDiscovery = defaults.RunFileDiscovery
	}
}
