package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
)

// ConfigQueries is an interface for loading config from database.
// This allows us to work with both Queries and CustomQueries types.
type ConfigQueries interface {
	GetConfigs(ctx context.Context) ([]gallerydb.Config, error)
}

// LoadFromDatabase loads configuration values from the database.
// Only loads values that exist in the database; missing keys keep their current values.
// Also populates HelpText and ExampleValues maps from each row's metadata columns.
func (c *Config) LoadFromDatabase(ctx context.Context, q ConfigQueries) error {
	configs, err := q.GetConfigs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get configs from database: %w", err)
	}

	if len(configs) > 0 {
		c.HelpText = make(map[string]string, len(configs))
		c.ExampleValues = make(map[string]string, len(configs))
	}

	for _, dbConfig := range configs {
		key := dbConfig.Key
		value := dbConfig.Value

		// Populate metadata maps from each row's help_text / example_value columns.
		if dbConfig.HelpText.Valid {
			c.HelpText[key] = dbConfig.HelpText.String
		}
		if dbConfig.ExampleValue.Valid {
			c.ExampleValues[key] = dbConfig.ExampleValue.String
		}

		// Skip special keys like user/password and LastKnownGoodConfig
		if key == "user" || key == "password" || key == "LastKnownGoodConfig" {
			continue
		}

		if err := c.SetValueFromString(key, value); err != nil {
			slog.Warn("failed to set config value from database", "key", key, "value", value, "err", err)
			// Continue loading other values even if one fails
		}
	}

	return nil
}

// FromMap creates a Config from a map of string values.
// This is used for loading from database or other key-value sources.
func FromMap(m map[string]string) (*Config, error) {
	cfg := DefaultConfig()
	for k, v := range m {
		if err := cfg.SetValueFromString(k, v); err != nil {
			return nil, err
		}
	}

	// Apply defaults if key is missing (already handled by DefaultConfig())
	// but ensured for ETag specifically as per plan
	if cfg.ETagVersion == "" {
		cfg.ETagVersion = DefaultConfig().ETagVersion
	}

	return cfg, nil
}

// SetValueFromString sets a config field value from a string representation.
// This is used when loading from database or parsing from YAML.
func (c *Config) SetValueFromString(key, value string) error {
	for _, f := range fields() {
		if f.dbKey == key {
			return f.set(c, value)
		}
	}
	// Unknown key - silently ignore (might be user/password or other legacy keys)
	return nil
}

// LoadFromOpt loads configuration values from getopt.Opt (CLI arguments and environment variables).
// This applies the highest precedence configuration source, overriding database and YAML values.
// Only values that were explicitly set (IsSet=true) override the current config.
// This ensures that default/zero values from getopt.Opt do not override database values.
func (c *Config) LoadFromOpt(opt getopt.Opt) {
	c.loadFromOpt(opt, nil)
}

// LoadFromOptExcluding applies CLI/env values except for fields in the exclude list.
// The exclude list contains config field names that should NOT be overridden (e.g., user-changed fields).
// This supports the use case where CLI values should override unchanged fields, but user changes persist.
func (c *Config) LoadFromOptExcluding(opt getopt.Opt, exclude []string) {
	c.loadFromOpt(opt, exclude)
}

// cliRoute maps a getopt.Opt field to a config dbKey and extracts a string
// value when the CLI flag is set. The dbKey must exist in the fields()
// registry — this is enforced by TestLoadFromOptDBKeysExistInFields.
type cliRoute struct {
	dbKey string
	val   func(getopt.Opt) (string, bool)
}

// cliRoutes defines which CLI/env flags map to config fields. Adding a new
// CLI-settable field requires an entry here AND a matching entry in fields().
var cliRoutes = []cliRoute{
	{"listener_port", func(o getopt.Opt) (string, bool) { return strconv.Itoa(o.Port.Int), o.Port.IsSet }},
	{"enable_http_cache", func(o getopt.Opt) (string, bool) {
		return strconv.FormatBool(o.EnableHTTPCache.Bool), o.EnableHTTPCache.IsSet
	}},
	{"enable_cache_preload", func(o getopt.Opt) (string, bool) {
		return strconv.FormatBool(o.EnableCachePreload.Bool), o.EnableCachePreload.IsSet
	}},
	{"run_file_discovery", func(o getopt.Opt) (string, bool) {
		return strconv.FormatBool(o.RunFileDiscovery.Bool), o.RunFileDiscovery.IsSet
	}},
	{"session_secure", func(o getopt.Opt) (string, bool) {
		return strconv.FormatBool(o.SessionSecure.Bool), o.SessionSecure.IsSet
	}},
	{"session_http_only", func(o getopt.Opt) (string, bool) {
		return strconv.FormatBool(o.SessionHttpOnly.Bool), o.SessionHttpOnly.IsSet
	}},
	{"session_max_age", func(o getopt.Opt) (string, bool) { return strconv.Itoa(o.SessionMaxAge.Int), o.SessionMaxAge.IsSet }},
	{"session_same_site", func(o getopt.Opt) (string, bool) { return o.SessionSameSite.String, o.SessionSameSite.IsSet }},
	{"login_rate_limit_per_ip", func(o getopt.Opt) (string, bool) {
		return strconv.Itoa(o.LoginRateLimitPerIP.Int), o.LoginRateLimitPerIP.IsSet
	}},
}

// loadFromOpt applies explicitly set CLI/environment values to c. Fields whose
// database key appears in exclude are skipped. A nil exclude slice skips nothing.
// Values are applied through the fields() registry so that dbKey strings are
// validated against the single source of truth.
func (c *Config) loadFromOpt(opt getopt.Opt, exclude []string) {
	// Build a lookup from the fields registry to validate all dbKeys.
	fieldsByKey := make(map[string]configField, len(fields()))
	for _, f := range fields() {
		fieldsByKey[f.dbKey] = f
	}

	for _, r := range cliRoutes {
		if slices.Contains(exclude, r.dbKey) {
			continue
		}
		v, ok := r.val(opt)
		if !ok {
			continue
		}
		if f, ok := fieldsByKey[r.dbKey]; ok {
			if err := f.set(c, v); err != nil {
				slog.Warn("failed to set config value from CLI", "key", r.dbKey, "err", err)
			}
		}
	}
	// SessionSecret is not stored in Config (memory only)
}

// yamlFieldsMap builds a yamlKey → *configField lookup from fields().
func yamlFieldsMap() map[string]*configField {
	fieldsList := fields()
	m := make(map[string]*configField, len(fieldsList))
	for i := range fieldsList {
		if fieldsList[i].yamlKey != "" {
			m[fieldsList[i].yamlKey] = &fieldsList[i]
		}
	}
	return m
}

// yamlValueToSetString converts a yaml.v3-decoded interface{} value to a
// string suitable for configField.set(). Returns an error for nil values.
func yamlValueToSetString(v interface{}) (string, error) {
	switch val := v.(type) {
	case nil:
		return "", fmt.Errorf("value is null, expected a valid value")
	case string:
		return val, nil
	case int:
		return strconv.Itoa(val), nil
	case bool:
		return strconv.FormatBool(val), nil
	case []interface{}:
		b, err := json.Marshal(val)
		if err != nil {
			return "", fmt.Errorf("failed to marshal YAML sequence: %w", err)
		}
		return string(b), nil
	default:
		return "", fmt.Errorf("unsupported YAML value type %T", v)
	}
}

// applyYAMLValues applies values from a generic YAML map to c, using
// fields() by yamlKey. Fields absent from fields() are silently skipped.
// Invalid values for known fields return an error.
func applyYAMLValues(c *Config, raw map[string]interface{}) error {
	fieldByYAML := yamlFieldsMap()
	for yamlKey, rawVal := range raw {
		f, ok := fieldByYAML[yamlKey]
		if !ok {
			continue
		}
		strVal, err := yamlValueToSetString(rawVal)
		if err != nil {
			return fmt.Errorf("yaml key %q: %w", yamlKey, err)
		}
		if err := f.set(c, strVal); err != nil {
			return fmt.Errorf("yaml key %q: %w", yamlKey, err)
		}
	}
	return nil
}

// LoadFromYAML loads configuration values from YAML files.
// It loads from platform config dir first (lower precedence), then exe dir (higher precedence).
// Only values present in YAML files are applied; missing keys keep their current values.
func (c *Config) LoadFromYAML() error {
	configPaths, err := getopt.FindConfigFiles()
	if err != nil {
		return fmt.Errorf("failed to find config files: %w", err)
	}

	for i := len(configPaths) - 1; i >= 0; i-- {
		path := configPaths[i]
		if !FileExists(path) {
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("failed to read YAML config", "path", path, "err", err)
			continue
		}

		var raw map[string]interface{}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			slog.Warn("invalid YAML syntax in config", "path", path, "err", err)
			continue
		}

		if err := applyYAMLValues(c, raw); err != nil {
			slog.Warn("failed to apply YAML config", "path", path, "err", err)
			continue
		}
	}

	return nil
}
