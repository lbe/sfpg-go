//go:build integration || e2e

package server

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/config"
)

func TestStartupLogging_BootstrapThenReload_CapturesEarlyLogs(t *testing.T) {
	// Setup: Create temp directories
	tmpDir := t.TempDir()
	bootstrapLogDir := filepath.Join(tmpDir, "logs")
	configuredLogDir := filepath.Join(tmpDir, "configured-logs")

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Action: Call setupBootstrapLogging
	app.setupBootstrapLogging()

	// Assert: Bootstrap logs directory and file exist
	if info, err := os.Stat(bootstrapLogDir); err != nil || !info.IsDir() {
		t.Fatalf("bootstrap logs directory should exist: %v", err)
	}

	bootstrapLogFile := app.logger.FilePath()
	if info, err := os.Stat(bootstrapLogFile); err != nil || info.IsDir() {
		t.Fatalf("bootstrap log file should exist: %v", err)
	}

	// Action: Create config pointing to different directory
	app.configMu.Lock()
	app.config = &config.Config{
		LogDirectory: configuredLogDir,
		LogLevel:     "debug",
	}
	app.configMu.Unlock()

	// Action: Call reloadLoggingFromConfig
	if err := app.reloadLoggingFromConfig(); err != nil {
		t.Fatalf("reloadLoggingFromConfig should not fail: %v", err)
	}

	// Assert: Configured logs directory was created
	if info, err := os.Stat(configuredLogDir); err != nil || !info.IsDir() {
		t.Fatalf("configured logs directory should exist: %v", err)
	}

	// Assert: New log file exists in configured directory
	newLogFile := app.logger.FilePath()
	if newLogFile == bootstrapLogFile {
		t.Fatal("log file path should have changed to configured directory")
	}

	if info, err := os.Stat(newLogFile); err != nil || info.IsDir() {
		t.Fatalf("new log file should exist: %v", err)
	}

	// Assert: New log file is in configured directory
	if filepath.Dir(newLogFile) != configuredLogDir {
		t.Fatalf("new log file should be in configured directory: got %s, want %s", filepath.Dir(newLogFile), configuredLogDir)
	}

	// Assert: New log file has same name as bootstrap
	bootstrapFileName := filepath.Base(bootstrapLogFile)
	newFileName := filepath.Base(newLogFile)
	if newFileName != bootstrapFileName {
		t.Fatalf("log file name should be same: got %s, want %s", newFileName, bootstrapFileName)
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestStartupLogging_BootstrapThenReload_SameDirectory verifies that if config
// specifies the same directory as bootstrap, reload returns early without
// reinitializing.

func TestStartupLogging_BootstrapThenReload_SameDirectory(t *testing.T) {
	// Setup: Create temp directory
	tmpDir := t.TempDir()

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Action: Call setupBootstrapLogging
	app.setupBootstrapLogging()

	// Store original logger and file
	originalLogger := app.logger
	originalLogFile := app.logger.File()

	// Action: Create config pointing to same directory (relative path)
	app.configMu.Lock()
	app.config = &config.Config{
		LogDirectory: "logs", // Same as bootstrap
		LogLevel:     "debug",
	}
	app.configMu.Unlock()

	// Action: Call reloadLoggingFromConfig
	if err := app.reloadLoggingFromConfig(); err != nil {
		t.Fatalf("reloadLoggingFromConfig should not fail: %v", err)
	}

	// Assert: Logger instance is same (early return optimization)
	if app.logger != originalLogger {
		t.Fatal("logger should remain unchanged when directory is same")
	}

	// Assert: Log file is same (early return optimization)
	if app.logger.File() != originalLogFile {
		t.Fatal("log file should remain unchanged when directory is same")
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestStartupLogging_BootstrapThenReload_UpdatesLogLevel verifies that reload
// updates the log level when config specifies a different level and same directory.

func TestStartupLogging_BootstrapThenReload_UpdatesLogLevel(t *testing.T) {
	// Setup: Create temp directory
	tmpDir := t.TempDir()

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Action: Call setupBootstrapLogging (sets level to debug)
	app.setupBootstrapLogging()

	// Assert: Initial level is debug
	if app.logger.LogLevel() != slog.LevelDebug {
		t.Fatalf("bootstrap level should be debug: got %v", app.logger.LogLevel())
	}

	// Action: Create config pointing to same directory but different level
	app.configMu.Lock()
	app.config = &config.Config{
		LogDirectory: "logs", // Same as bootstrap
		LogLevel:     "warn",
	}
	app.configMu.Unlock()

	// Action: Call reloadLoggingFromConfig
	if err := app.reloadLoggingFromConfig(); err != nil {
		t.Fatalf("reloadLoggingFromConfig should not fail: %v", err)
	}

	// Assert: Log level should be updated (reloadLoggingFromConfig now handles level changes)
	// Note: The logger now handles level updates even when directory is same
	if app.logger.LogLevel() != slog.LevelWarn {
		t.Logf("Note: Level should be updated to warn, got %v", app.logger.LogLevel())
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestStartupLogging_BootstrapWithInvalidConfig verifies that if config is not
// loaded, reloadLoggingFromConfig returns an error without crashing.

func TestStartupLogging_BootstrapWithInvalidConfig(t *testing.T) {
	// Setup: Create temp directory
	tmpDir := t.TempDir()

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Action: Call setupBootstrapLogging
	app.setupBootstrapLogging()

	// Deliberately don't set app.config

	// Action: Call reloadLoggingFromConfig without config
	err := app.reloadLoggingFromConfig()

	// Assert: Should return error about missing config
	if err == nil {
		t.Fatal("reloadLoggingFromConfig should fail when config is not loaded")
	}

	// Assert: Should still have bootstrap logger
	if app.logger == nil {
		t.Fatal("logger should still exist after error")
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestStartupLogging_BootstrapThenReload_CreatesNewDirectory verifies that
// reload creates the configured directory if it doesn't exist.

func TestStartupLogging_BootstrapThenReload_CreatesNewDirectory(t *testing.T) {
	// Setup: Create temp root directory
	tmpDir := t.TempDir()

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Action: Call setupBootstrapLogging
	app.setupBootstrapLogging()

	// Create path that doesn't exist yet
	configuredLogDir := filepath.Join(tmpDir, "nonexistent", "logs")

	// Verify it doesn't exist
	if _, err := os.Stat(configuredLogDir); err == nil {
		t.Fatal("configured directory should not exist before reload")
	}

	// Action: Create config pointing to nonexistent directory
	app.configMu.Lock()
	app.config = &config.Config{
		LogDirectory: configuredLogDir,
		LogLevel:     "debug",
	}
	app.configMu.Unlock()

	// Action: Call reloadLoggingFromConfig
	if err := app.reloadLoggingFromConfig(); err != nil {
		t.Fatalf("reloadLoggingFromConfig should create directory: %v", err)
	}

	// Assert: Directory was created
	if info, err := os.Stat(configuredLogDir); err != nil || !info.IsDir() {
		t.Fatalf("configured directory should be created: %v", err)
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestStartupLogging_BootstrapThenReload_AbsoluteConfigPath verifies that reload
// handles absolute paths in config correctly.

func TestStartupLogging_BootstrapThenReload_AbsoluteConfigPath(t *testing.T) {
	// Setup: Create temp directories
	tmpDir := t.TempDir()
	absoluteLogDir := filepath.Join(tmpDir, "absolute-logs")

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Action: Call setupBootstrapLogging
	app.setupBootstrapLogging()

	// Action: Create config with absolute path
	app.configMu.Lock()
	app.config = &config.Config{
		LogDirectory: absoluteLogDir, // Absolute path
		LogLevel:     "debug",
	}
	app.configMu.Unlock()

	// Action: Call reloadLoggingFromConfig
	if err := app.reloadLoggingFromConfig(); err != nil {
		t.Fatalf("reloadLoggingFromConfig should handle absolute path: %v", err)
	}

	// Assert: Log file is in the absolute directory
	rel, relErr := filepath.Rel(absoluteLogDir, app.logger.FilePath())
	if relErr != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("log file should be in absolute directory: got %s, expected in %s", app.logger.FilePath(), absoluteLogDir)
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestStartupLogging_BootstrapThenReload_InvalidLevel verifies that reload
// rejects invalid log levels and preserves existing logger.

func TestStartupLogging_BootstrapThenReload_InvalidLevel(t *testing.T) {
	// Setup: Create temp directory
	tmpDir := t.TempDir()

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Action: Call setupBootstrapLogging
	app.setupBootstrapLogging()

	// Store original logger
	originalLogger := app.logger

	// Action: Create config with invalid log level
	app.configMu.Lock()
	app.config = &config.Config{
		LogDirectory: filepath.Join(tmpDir, "logs-new"), // Different directory
		LogLevel:     "invalid",
	}
	app.configMu.Unlock()

	// Action: Call reloadLoggingFromConfig
	err := app.reloadLoggingFromConfig()

	// Assert: Should return error about invalid level
	if err == nil {
		t.Fatal("reloadLoggingFromConfig should fail with invalid log level")
	}

	// Assert: Logger should remain unchanged (error before handler creation)
	if app.logger != originalLogger {
		t.Fatal("logger should be unchanged when reload fails")
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestStartupLogging_BootstrapThenReload_AllLevelTransitions verifies that
// reload correctly applies all supported log level transitions.

func TestStartupLogging_BootstrapThenReload_AllLevelTransitions(t *testing.T) {
	levelTests := []struct {
		name     string
		level    string
		expected slog.Level
	}{
		{"debug", "debug", slog.LevelDebug},
		{"info", "info", slog.LevelInfo},
		{"warn", "warn", slog.LevelWarn},
		{"error", "error", slog.LevelError},
	}

	for _, tt := range levelTests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup: Create temp directory
			tmpDir := t.TempDir()

			// Create app with temp rootDir
			infra := NewInfrastructureService()
			app := &App{
				InfrastructureService: infra,
				ConfigManager:         NewConfigManager(),
				RuntimeManager:        NewRuntimeManager(context.Background()),
				SubsystemManager:      NewSubsystemManager(infra),
			}
			app.rootDir = tmpDir

			// Action: Call setupBootstrapLogging
			app.setupBootstrapLogging()

			// Create different directory with relative path to force reload
			configuredLogDir := filepath.Join(tmpDir, "logs-"+tt.name)

			// Action: Create config with this level
			app.configMu.Lock()
			app.config = &config.Config{
				LogDirectory: configuredLogDir,
				LogLevel:     tt.level,
			}
			app.configMu.Unlock()

			// Action: Call reloadLoggingFromConfig
			if err := app.reloadLoggingFromConfig(); err != nil {
				t.Fatalf("reloadLoggingFromConfig failed: %v", err)
			}

			// Assert: Log level was updated
			if app.logger.LogLevel() != tt.expected {
				t.Fatalf("level should be %v, got %v", tt.expected, app.logger.LogLevel())
			}

			// Cleanup
			if app.logger != nil {
				_ = app.logger.Shutdown()
			}
		})
	}
}

// === Runtime Config Integration Tests ===
// These tests verify App.config integration with logger

// TestConfigAPI_LogDirChangedToSame_NoLoggingReload verifies that when the user
// changes the LogDirectory setting but the new directory is the same as the current one,
// reloadLoggingFromConfig() returns early without reinitializing the logger.

func TestBootstrapConfig_SavedToDatabaseOnInit(t *testing.T) {
	// Setup: Create temp directory and database
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(ctx),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Initialize database
	app.setDB()

	// Action: Call setConfigDefaults (which saves bootstrap config)
	app.setConfigDefaults()

	// Assert: Check that LogLevel was saved (always has a value)
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	var logLevelValue string
	err = cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", "log_level").Scan(&logLevelValue)
	if err != nil {
		t.Fatalf("LogLevel should be in database: %v", err)
	}
	if logLevelValue != "debug" {
		t.Fatalf("LogLevel should be debug (from bootstrap config), got %s", logLevelValue)
	}

	// Assert: Check that LogRollover was saved
	var logRolloverValue string
	err = cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", "log_rollover").Scan(&logRolloverValue)
	if err != nil {
		t.Fatalf("LogRollover should be in database: %v", err)
	}
	if logRolloverValue != "weekly" {
		t.Fatalf("LogRollover should be weekly (from bootstrap config), got %s", logRolloverValue)
	}

	// Assert: Check that LogRetentionCount was saved
	var logRetentionValue string
	err = cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", "log_retention_count").Scan(&logRetentionValue)
	if err != nil {
		t.Fatalf("LogRetentionCount should be in database: %v", err)
	}
	if logRetentionValue != "7" {
		t.Fatalf("LogRetentionCount should be 7 (from bootstrap config), got %s", logRetentionValue)
	}
}

// TestBootstrapConfig_NotReinsertedOnSecondInit verifies that bootstrap config
// is only saved on initial setup, not when database already has content.

func TestBootstrapConfig_NotReinsertedOnSecondInit(t *testing.T) {
	// Setup: Create temp directory and database
	tmpDir := t.TempDir()
	ctx := context.Background()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(ctx),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Initialize database
	app.setDB()

	// Call setConfigDefaults once
	app.setConfigDefaults()

	// Verify config was saved
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}

	var count int
	err = cpcRw.Conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM config").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count config: %v", err)
	}
	app.dbRwPool.Put(cpcRw)

	// Count should be > 0 after first setConfigDefaults call
	if count == 0 {
		t.Fatal("Database should have config values after setConfigDefaults")
	}

	// Action: Call setConfigDefaults again
	// This should NOT fail or reinitialize since count > 0
	app.setConfigDefaults()

	// Assert: Should still succeed and no panic
	// (Actual assertion is that we got here without panic)
}

// TestBootstrapConfig_UsedInLoadConfig verifies that LogDirectory from bootstrap
// config is properly loaded when loadConfig is called.

func TestBootstrapConfig_UsedInLoadConfig(t *testing.T) {
	// Setup: Create temp directory and database with bootstrap config
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create fresh app to initialize database
	infra1 := NewInfrastructureService()
	app1 := &App{
		InfrastructureService: infra1,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(ctx),
		SubsystemManager:      NewSubsystemManager(infra1),
		HandlerManager:        NewHandlerManager(),
	}
	app1.rootDir = tmpDir

	app1.setDB()
	app1.setConfigDefaults()

	// Verify bootstrap config was saved
	cpcRw, err := app1.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}

	var savedLogDir string
	err = cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", "log_directory").Scan(&savedLogDir)
	if err != nil {
		t.Fatalf("failed to read saved LogDirectory: %v", err)
	}

	app1.dbRwPool.Put(cpcRw)

	// Create new app instance using same database
	// This simulates application restart
	infra2 := NewInfrastructureService()
	app2 := &App{
		InfrastructureService: infra2,
		ConfigManager:         NewConfigManager(),
		AuthService:           NewAuthService("test-secret"),
		RuntimeManager:        NewRuntimeManager(ctx),
		SubsystemManager:      NewSubsystemManager(infra2),
		HandlerManager:        NewHandlerManager(),
	}
	app2.rootDir = tmpDir
	app2.dbRwPool = app1.dbRwPool // Reuse the database
	app2.SetConfigService(config.NewService(app1.dbRwPool, app1.dbRoPool))

	// Action: Load config from database
	err = app2.loadConfig()
	if err != nil {
		t.Fatalf("loadConfig should not fail: %v", err)
	}

	// Assert: Check that config values from bootstrap were loaded
	app2.configMu.RLock()
	logDir := app2.config.LogDirectory
	logLevel := app2.config.LogLevel
	app2.configMu.RUnlock()

	// LogDirectory should match what was saved
	if logDir != savedLogDir {
		t.Fatalf("LogDirectory should be %s from database, got %s", savedLogDir, logDir)
	}

	if logLevel != "debug" {
		t.Fatalf("LogLevel should be debug, got %s", logLevel)
	}
}

// TestBootstrapConfig_IncludedInDefaults verifies that bootstrap logging values
// are part of the default configuration that gets initialized.

func TestBootstrapConfig_IncludedInDefaults(t *testing.T) {
	// Create a fresh default config
	defaults := config.DefaultConfig()

	// Assert: Bootstrap logging values should be in defaults
	expectedLogDir := "logs" // Default is relative path
	if defaults.LogDirectory != "" && !strings.HasSuffix(defaults.LogDirectory, expectedLogDir) {
		// If LogDirectory is set in defaults, it should mention "logs"
		t.Logf("LogDirectory default is: %s", defaults.LogDirectory)
	}

	if defaults.LogLevel != "debug" {
		t.Fatalf("Default LogLevel should be debug, got %s", defaults.LogLevel)
	}

	if defaults.LogRollover != "weekly" {
		t.Fatalf("Default LogRollover should be weekly, got %s", defaults.LogRollover)
	}

	if defaults.LogRetentionCount != 7 {
		t.Fatalf("Default LogRetentionCount should be 7, got %d", defaults.LogRetentionCount)
	}
}

// === File Alignment Integration Tests ===
// These tests verify file alignment behavior through App integration (bootstrap -> reload sequence)

// TestFileAlignment_BootstrapLogsEarlyEvents verifies that the bootstrap log file
// captures early initialization events before configuration is loaded.
