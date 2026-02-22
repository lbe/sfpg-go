package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"go.local/sfpg/internal/getopt"
)

// Load handles the full configuration loading precedence:
// Defaults -> Database -> YAML -> CLI/Env
func Load(ctx context.Context, rootDir string, service ConfigService, opt getopt.Opt) (*Config, error) {
	// 1. Start with defaults
	cfg := DefaultConfig()

	// 2. Load from database (if ConfigService is available)
	if service != nil {
		dbConfig, err := service.Load(ctx)
		if err != nil {
			slog.Warn("failed to load config from database via ConfigService", "err", err)
		} else {
			cfg = dbConfig
			defaults := DefaultConfig()
			cfg.MergeDefaults(defaults) // Fill missing database values with code defaults
		}
	}

	// 3. Load from YAML files
	if err := cfg.LoadFromYAML(); err != nil {
		slog.Warn("failed to load config from YAML files", "err", err)
	}

	// 4. Apply CLI/env options
	cfg.LoadFromOpt(opt)

	// 5. Merge defaults for any remaining zero values
	defaults := DefaultConfig()
	cfg.MergeDefaults(defaults)

	// 6. Ensure ImageDirectory is set - it's critical and must be set from rootDir if not configured
	if cfg.ImageDirectory == "" && rootDir != "" {
		cfg.ImageDirectory = filepath.Join(rootDir, "Images")
	}

	return cfg, nil
}

// EnsureDefaults makes sure the database has at least the default config and admin user.
func EnsureDefaults(ctx context.Context, rootDir string, service ConfigService, pool any) {
	if service != nil {
		if err := service.EnsureDefaults(ctx, rootDir); err != nil {
			slog.Error("failed to ensure config defaults", "err", err)
			panic("main - setConfigDefaults")
		}
		return
	}
}

// ValidateImageDirectory checks if the path is valid for use.
func ValidateImageDirectory(path string) error {
	if path == "" {
		return fmt.Errorf("image directory path is empty")
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("image directory does not exist: %q", path)
		}
		return fmt.Errorf("failed to stat image directory %q: %w", path, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("image directory path is not a directory: %q", path)
	}

	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("image directory is not readable: %q: %w", path, err)
	}
	dir.Close()

	return nil
}

// ApplyImageDirectory applies image directory configuration and returns the resolved
// directory values along with any validation error encountered.
func ApplyImageDirectory(imageDirectory string) (string, string, error) {
	if imageDirectory == "" {
		return "", "", nil
	}

	if _, err := os.Stat(imageDirectory); os.IsNotExist(err) {
		if err := os.MkdirAll(imageDirectory, 0o755); err != nil {
			return imageDirectory, filepath.ToSlash(imageDirectory), fmt.Errorf("failed to create image directory %q: %w", imageDirectory, err)
		}
	}

	validateErr := ValidateImageDirectory(imageDirectory)
	return imageDirectory, filepath.ToSlash(imageDirectory), validateErr
}
