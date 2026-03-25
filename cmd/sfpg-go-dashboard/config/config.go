// Package config provides configuration parsing for the sfpg-go-dashboard TUI application.
// It supports command-line flags, environment variables, and default values.
package config

import (
	"flag"
	"fmt"
	"os"
	"time"
)

// Config holds all configuration values for the dashboard application.
type Config struct {
	// ServerURL is the base URL of the sfpg-go server.
	ServerURL string
	// Username for authentication (from SFPG_USERNAME env var).
	Username string
	// Password for authentication (from SFPG_PASSWORD env var).
	Password string
	// Refresh is the auto-refresh interval for dashboard data.
	Refresh time.Duration
	// NoRefresh disables auto-refresh when true.
	NoRefresh bool
	// ShowHelp indicates the help message was requested.
	ShowHelp bool
}

// Parse parses command-line flags and environment variables to create a Config.
// It uses flag.NewFlagSet to allow multiple calls in tests.
//
// Precedence (highest to lowest):
//  1. Command-line flags
//  2. Environment variables
//  3. Default values
//
// Environment variables:
//   - SFPG_SERVER: Server URL
//   - SFPG_USERNAME: Username (optional, for automatic login)
//   - SFPG_PASSWORD: Password (optional, for automatic login)
//
// Command-line flags:
//   - -server: Server URL (default: http://localhost:8083)
//   - -refresh: Auto-refresh interval (default: 5s)
//   - -no-refresh: Disable auto-refresh
//   - -help: Show help message
func Parse() *Config {
	return ParseArgs(os.Args[1:])
}

// ParseArgs parses the given arguments and returns a Config.
// This is useful for testing with explicit arguments.
//
// Example:
//
//	cfg := config.ParseArgs([]string{"-server", "http://example.com:8080"})
func ParseArgs(args []string) *Config {
	cfg := &Config{}
	fs := flag.NewFlagSet("sfpg-go-dashboard", flag.ContinueOnError)

	fs.StringVar(&cfg.ServerURL, "server", getEnv("SFPG_SERVER", "http://localhost:8083"), "Server URL")
	fs.DurationVar(&cfg.Refresh, "refresh", 5*time.Second, "Auto-refresh interval")
	fs.BoolVar(&cfg.NoRefresh, "no-refresh", false, "Disable auto-refresh")
	fs.BoolVar(&cfg.ShowHelp, "help", false, "Show help")

	cfg.Username = os.Getenv("SFPG_USERNAME")
	cfg.Password = os.Getenv("SFPG_PASSWORD")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s [options]\n\n", "sfpg-go-dashboard")
		fmt.Fprintln(fs.Output(), "A terminal dashboard for sfpg-go server.")
		fmt.Fprintln(fs.Output(), "Options:")
		fs.PrintDefaults()
		fmt.Fprintln(fs.Output(), "\nEnvironment variables:")
		fmt.Fprintln(fs.Output(), "  SFPG_SERVER   Server URL")
		fmt.Fprintln(fs.Output(), "  SFPG_USERNAME Username (optional)")
		fmt.Fprintln(fs.Output(), "  SFPG_PASSWORD Password (optional)")
	}

	// Silently parse - errors are for --help which we handle elsewhere
	fs.Parse(args)

	return cfg
}

// getEnv returns the value of the environment variable named by the key,
// or the default value if the variable is not set or empty.
func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
