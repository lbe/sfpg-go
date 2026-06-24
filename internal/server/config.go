package server

import (
	"github.com/lbe/sfpg-go/internal/server/config"
)

// Deprecated: Config is a type alias for config.Config.
// Use config.Config directly in new code.
type Config = config.Config

// Deprecated: DefaultConfig returns a Config with all default values.
// Use config.DefaultConfig directly in new code.
func DefaultConfig() *Config {
	return config.DefaultConfig()
}

// Deprecated: ConfigQueries is a type alias for config.ConfigQueries.
// Use config.ConfigQueries directly in new code.
type ConfigQueries = config.ConfigQueries

// Deprecated: ConfigSaver is a type alias for config.ConfigSaver.
// Use config.ConfigSaver directly in new code.
type ConfigSaver = config.ConfigSaver

// Deprecated: ConfigDiff is a type alias for config.ConfigDiff.
// Use config.ConfigDiff directly in new code.
type ConfigDiff = config.ConfigDiff
