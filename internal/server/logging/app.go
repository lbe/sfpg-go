package logging

import (
	"fmt"
	"log/slog"

	"github.com/lbe/sfpg-go/internal/log"
	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/config"
)

// SetupBootstrap initializes minimal logging for early startup phase.
func SetupBootstrap(rootDir string, sch *scheduler.Scheduler, version string) (*log.Logger, error) {
	logger, err := log.NewBootstrapLogger(rootDir, sch, version)
	if err != nil {
		return nil, fmt.Errorf("failed to setup bootstrap logging: %w", err)
	}

	// Echo profiler status if active
	if dir := profiler.Dir(); dir != "" {
		if mode := profiler.Mode(); mode != "" {
			slog.Info("Profiler", "mode", mode, "dir", dir)
		}
	}

	return logger, nil
}

// Reload reinitializes logging based on configuration.
func Reload(logger *log.Logger, cfg *config.Config, sch *scheduler.Scheduler) error {
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}
	// Delegate to logger
	return logger.ReloadFromConfig(cfg.LogDirectory, cfg.LogLevel, cfg.LogRollover, cfg.LogRetentionCount, sch)
}
