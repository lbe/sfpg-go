// Package config provides configuration parsing for the sfpg-go-dashboard TUI.
//
// Configuration can be provided via command-line flags or environment variables.
// Environment variables take precedence over defaults, and flags take precedence
// over environment variables.
//
// # Environment Variables
//
//	SFPG_SERVER   - Server URL (default: http://localhost:8083)
//	SFPG_USERNAME - Username for authentication (optional)
//	SFPG_PASSWORD - Password for authentication (optional)
//
// # Command-Line Flags
//
//	-server      Server URL (default: http://localhost:8083)
//	-refresh     Auto-refresh interval (default: 5s)
//	-no-refresh  Disable auto-refresh
//	-help        Show help message
//
// # Example
//
//	cfg := config.Parse()
//	if cfg.Username != "" && cfg.Password != "" {
//	    // automatic login available
//	}
package config
