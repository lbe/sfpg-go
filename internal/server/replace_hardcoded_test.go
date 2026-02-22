package server

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/getopt"
	"go.local/sfpg/internal/scheduler"
)

// TestAppUsesConfigForLogDirectory verifies that log directory comes from config.
func TestAppUsesConfigForLogDirectory(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.scheduler = scheduler.NewScheduler(1) // Initialize scheduler before setupBootstrapLogging
	app.setupBootstrapLogging()               // Initialize logger before calling reloadLoggingFromConfig
	app.setDB()
	app.setConfigDefaults()

	// Set custom log directory in config
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	// Set config value
	now := time.Now().Unix()
	customLogDir := filepath.Join(tempDir, "custom_logs")
	err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
		Key:       "log_directory",
		Value:     customLogDir,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set log_directory: %v", err)
	}

	// Load config
	if err := app.loadConfig(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Apply config
	app.applyConfig()

	// Reload logging from config to apply the log directory
	if err := app.reloadLoggingFromConfig(); err != nil {
		t.Fatalf("reloadLoggingFromConfig should not fail: %v", err)
	}

	// Verify log directory is from config
	if app.logger.LogsDir() != customLogDir {
		t.Errorf("expected logsDir to be %q from config, got %q", customLogDir, app.logger.LogsDir())
	}
}

// TestAppUsesConfigForImageDirectory verifies that image directory comes from config.
func TestAppUsesConfigForImageDirectory(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()
	app.setConfigDefaults()

	// Set custom image directory in config
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	now := time.Now().Unix()
	customImageDir := filepath.Join(tempDir, "custom_images")
	err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
		Key:       "image_directory",
		Value:     customImageDir,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set image_directory: %v", err)
	}

	// Load config
	if err := app.loadConfig(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Apply config
	app.applyConfig()

	// Set image directory from config (as done in Run())

	// Verify image directory is from config
	if app.imagesDir != customImageDir {
		t.Errorf("expected imagesDir to be %q from config, got %q", customImageDir, app.imagesDir)
	}
}

// TestAppUsesConfigForLogLevel verifies that log level comes from config.
func TestAppUsesConfigForLogLevel(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.scheduler = scheduler.NewScheduler(1) // Initialize scheduler before setupBootstrapLogging
	app.setupBootstrapLogging()               // Initialize logger before calling reloadLoggingFromConfig
	app.setDB()
	app.setConfigDefaults()

	// Set custom log level in config
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	now := time.Now().Unix()
	err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
		Key:       "log_level",
		Value:     "error",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set log_level: %v", err)
	}

	// Load config
	if err := app.loadConfig(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Apply config
	app.applyConfig()

	// Reload logging from config to apply the log level
	if err := app.reloadLoggingFromConfig(); err != nil {
		t.Fatalf("reloadLoggingFromConfig should not fail: %v", err)
	}

	// Verify log level is from config
	if app.logger.LogLevel() != slog.LevelError {
		t.Errorf("expected logLevel to be LevelError from config, got %v", app.logger.LogLevel())
	}
}
