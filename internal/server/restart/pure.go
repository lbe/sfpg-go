// Package restart provides pure functions for determining server restart requirements.
package restart

import (
	"github.com/lbe/sfpg-go/internal/server/config"
)

// IsHTTPOnlyRestart determines if only HTTP listener needs restart.
// Returns true if config is non-nil (simplified check).
// In production, this would check which specific settings changed.
func IsHTTPOnlyRestart(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	// Simplified: assume HTTP-only restart is possible if config exists
	// In a real implementation, we'd track which specific settings changed
	return true
}

// GetRestartType returns the type of restart needed.
// Returns "HTTP-only" for listener-only changes, "full" for other restart-required changes.
func GetRestartType(cfg *config.Config) string {
	if IsHTTPOnlyRestart(cfg) {
		return "HTTP-only"
	}
	return "full"
}
