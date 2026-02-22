package server

import (
	"github.com/lbe/sfpg-go/internal/server/config"
)

// Config is the main application configuration type.
// It is defined in the config package and re-exported here for backward compatibility.
type Config = config.Config

// DefaultConfig returns a Config with all default values.
func DefaultConfig() *Config {
	return config.DefaultConfig()
}

// ConfigQueries is an alias for config.ConfigQueries to maintain backward compatibility.
type ConfigQueries = config.ConfigQueries

// ConfigSaver is an alias for config.ConfigSaver to maintain backward compatibility.
type ConfigSaver = config.ConfigSaver

// ConfigDiff is an alias for config.ConfigDiff to maintain backward compatibility.
type ConfigDiff = config.ConfigDiff
