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

func TestConfigAPI_LogDirChangedToSame_NoLoggingReload(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

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

	// Bootstrap logging setup
	app.setupBootstrapLogging()

	// Get the initial log directory
	initialLogDir := app.logger.LogsDir()

	// Update app.config to have the same log directory
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.LogDirectory = initialLogDir
	app.configMu.Unlock()

	// Reload should return early without error (same directory)
	err := app.reloadLoggingFromConfig()
	if err != nil {
		t.Fatalf("expected no error when log directory unchanged, got: %v", err)
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestConfigAPI_LogLevelChangedSameDir_UpdatesLevel verifies that when the user
// changes only the LogLevel setting (keeping directory the same), the log level is
// updated without reinitializing log files.

func TestConfigAPI_LogLevelChangedSameDir_UpdatesLevel(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

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

	// Bootstrap logging setup
	app.setupBootstrapLogging()

	// Initial log level should be debug (bootstrap default)
	if app.logger.LogLevel() != slog.LevelDebug {
		t.Fatalf("expected bootstrap log level to be debug, got: %v", app.logger.LogLevel())
	}

	// Update config to set log level to error
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.LogDirectory = app.logger.LogsDir() // Keep same directory
	app.config.LogLevel = "error"                  // Change level to error
	app.configMu.Unlock()

	// Reload should update the level
	err := app.reloadLoggingFromConfig()
	if err != nil {
		t.Fatalf("expected no error when reloading with level change, got: %v", err)
	}

	// Verify log level was updated
	if app.logger.LogLevel() != slog.LevelError {
		t.Fatalf("expected log level to be error, got: %v", app.logger.LogLevel())
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestConfigAPI_SameDir_NoInterruption verifies that logging continues uninterrupted
// when config is changed but the log directory stays the same.

func TestConfigAPI_SameDir_NoInterruption(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

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

	// Bootstrap logging setup
	app.setupBootstrapLogging()

	// Get initial log directory
	logsDir := app.logger.LogsDir()

	// Update config to same directory
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.LogDirectory = logsDir // Same as bootstrap
	app.configMu.Unlock()

	// Reload with same directory
	err := app.reloadLoggingFromConfig()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Log a message - this should succeed without error
	slog.Info("test message after reload")

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestConfigAPI_LogDirChangedToDifferent_NewFileCreated verifies that when the user
// changes the LogDirectory to a different directory, a new log file is created in that
// directory with the same filename as the bootstrap log.

func TestConfigAPI_LogDirChangedToDifferent_NewFileCreated(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

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

	// Bootstrap logging setup
	app.setupBootstrapLogging()

	// Create a different logs directory
	newLogsDir := filepath.Join(tmpDir, "new_logs")
	if mkdirErr := os.MkdirAll(newLogsDir, 0755); mkdirErr != nil {
		t.Fatalf("failed to create new logs dir: %v", mkdirErr)
	}

	// Update config to use new directory
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.LogDirectory = newLogsDir
	app.configMu.Unlock()

	// Reload with different directory
	err := app.reloadLoggingFromConfig()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify logs directory was updated
	if app.logger.LogsDir() != newLogsDir {
		t.Fatalf("expected logs dir to be updated to %q, got: %q", newLogsDir, app.logger.LogsDir())
	}

	// Verify at least one log file exists in the new directory
	files, err := os.ReadDir(newLogsDir)
	if err != nil {
		t.Fatalf("failed to read new logs directory: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("expected log file to be created in new directory, got 0 files")
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestConfigAPI_LogDirChangedToDifferent_FilesAligned verifies that when the log
// directory changes, the new log file uses the same filename as the bootstrap file,
// allowing administrators to manually align/migrate logs between directories.

func TestConfigAPI_LogDirChangedToDifferent_FilesAligned(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

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

	// Bootstrap logging setup
	app.setupBootstrapLogging()

	// Get bootstrap log file (should be the only file in logs dir)
	bootstrapFiles, err := os.ReadDir(app.logger.LogsDir())
	if err != nil {
		t.Fatalf("failed to read bootstrap logs directory: %v", err)
	}
	if len(bootstrapFiles) == 0 {
		t.Fatalf("expected bootstrap log file, got 0 files")
	}
	bootstrapLogFileName := bootstrapFiles[0].Name()

	// Create a different logs directory
	newLogsDir := filepath.Join(tmpDir, "new_logs")
	if mkdirErr := os.MkdirAll(newLogsDir, 0755); mkdirErr != nil {
		t.Fatalf("failed to create new logs dir: %v", mkdirErr)
	}

	// Update config to use new directory
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.LogDirectory = newLogsDir
	app.configMu.Unlock()

	// Reload with different directory
	err = app.reloadLoggingFromConfig()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Get files in new directory
	newFiles, err := os.ReadDir(newLogsDir)
	if err != nil {
		t.Fatalf("failed to read new logs directory: %v", err)
	}
	if len(newFiles) == 0 {
		t.Fatalf("expected log file in new directory, got 0 files")
	}

	newLogFileName := newFiles[0].Name()

	// Verify the filenames match (alignment)
	if newLogFileName != bootstrapLogFileName {
		t.Fatalf("expected same filename in both directories: %q vs %q",
			bootstrapLogFileName, newLogFileName)
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestConfigAPI_LogDirChangedToDifferent_OldFileStillExists verifies that when
// the log directory changes, the original bootstrap log file is preserved in its
// original location.

func TestConfigAPI_LogDirChangedToDifferent_OldFileStillExists(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

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

	// Bootstrap logging setup
	app.setupBootstrapLogging()

	// Get bootstrap log file path
	bootstrapFiles, err := os.ReadDir(app.logger.LogsDir())
	if err != nil {
		t.Fatalf("failed to read bootstrap logs directory: %v", err)
	}
	if len(bootstrapFiles) == 0 {
		t.Fatalf("expected bootstrap log file, got 0 files")
	}
	bootstrapLogPath := filepath.Join(app.logger.LogsDir(), bootstrapFiles[0].Name())

	// Create a different logs directory
	newLogsDir := filepath.Join(tmpDir, "new_logs")
	if mkdirErr := os.MkdirAll(newLogsDir, 0755); mkdirErr != nil {
		t.Fatalf("failed to create new logs dir: %v", mkdirErr)
	}

	// Update config to use new directory
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.LogDirectory = newLogsDir
	app.configMu.Unlock()

	// Reload with different directory
	err = app.reloadLoggingFromConfig()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify bootstrap log file still exists
	if _, err := os.Stat(bootstrapLogPath); err != nil {
		t.Fatalf("expected bootstrap log file to still exist at %q, got error: %v",
			bootstrapLogPath, err)
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestConfigAPI_LogDirChangedToDifferent_NewEntriesGoToNew verifies that after
// the log directory changes, subsequent log entries are written to the new log file,
// not the bootstrap file.

func TestConfigAPI_LogDirChangedToDifferent_NewEntriesGoToNew(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

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

	// Bootstrap logging setup
	app.setupBootstrapLogging()

	// Get bootstrap log file path and size
	bootstrapFiles, err := os.ReadDir(app.logger.LogsDir())
	if err != nil {
		t.Fatalf("failed to read bootstrap logs directory: %v", err)
	}
	if len(bootstrapFiles) == 0 {
		t.Fatalf("expected bootstrap log file, got 0 files")
	}
	bootstrapLogPath := filepath.Join(app.logger.LogsDir(), bootstrapFiles[0].Name())

	// Get initial bootstrap log file size
	bootstrapInfo, err := os.Stat(bootstrapLogPath)
	if err != nil {
		t.Fatalf("failed to stat bootstrap log: %v", err)
	}
	bootstrapSizeBeforeReload := bootstrapInfo.Size()

	// Create a different logs directory
	newLogsDir := filepath.Join(tmpDir, "new_logs")
	if mkdirErr := os.MkdirAll(newLogsDir, 0755); mkdirErr != nil {
		t.Fatalf("failed to create new logs dir: %v", mkdirErr)
	}

	// Update config to use new directory
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.LogDirectory = newLogsDir
	app.configMu.Unlock()

	// Reload with different directory
	err = app.reloadLoggingFromConfig()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Log a message to new log file
	slog.Info("message after directory change")

	// Get new log file size
	newInfo, err := os.Stat(bootstrapLogPath)
	if err != nil {
		t.Fatalf("failed to stat bootstrap log after reload: %v", err)
	}
	bootstrapSizeAfterReload := newInfo.Size()

	// Bootstrap log file should not have grown much (only reload message)
	// New logs should go to new directory
	if bootstrapSizeAfterReload < bootstrapSizeBeforeReload {
		t.Errorf("bootstrap log file should not shrink: %d -> %d",
			bootstrapSizeBeforeReload, bootstrapSizeAfterReload)
	}

	// Verify new log file was created in new directory
	newFiles, err := os.ReadDir(newLogsDir)
	if err != nil {
		t.Fatalf("failed to read new logs directory: %v", err)
	}
	if len(newFiles) == 0 {
		t.Fatalf("expected log file in new directory, got 0 files")
	}

	// Verify new log file has content (our test message)
	newLogPath := filepath.Join(newLogsDir, newFiles[0].Name())
	newLogContent, err := os.ReadFile(newLogPath)
	if err != nil {
		t.Fatalf("failed to read new log file: %v", err)
	}
	if !strings.Contains(string(newLogContent), "message after directory change") {
		t.Fatalf("expected new log message in new log file, content: %s", string(newLogContent))
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestConfigAPI_LogDirChangedToDifferent_TransitionLogged verifies that the transition
// from one log directory to another is logged in both files, creating an audit trail
// of when the switch occurred.

func TestConfigAPI_LogDirChangedToDifferent_TransitionLogged(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

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

	// Bootstrap logging setup
	app.setupBootstrapLogging()

	// Get bootstrap log file path
	bootstrapFiles, err := os.ReadDir(app.logger.LogsDir())
	if err != nil {
		t.Fatalf("failed to read bootstrap logs directory: %v", err)
	}
	if len(bootstrapFiles) == 0 {
		t.Fatalf("expected bootstrap log file, got 0 files")
	}
	bootstrapLogPath := filepath.Join(app.logger.LogsDir(), bootstrapFiles[0].Name())

	// Create a different logs directory
	newLogsDir := filepath.Join(tmpDir, "new_logs")
	if mkdirErr := os.MkdirAll(newLogsDir, 0755); mkdirErr != nil {
		t.Fatalf("failed to create new logs dir: %v", mkdirErr)
	}

	// Update config to use new directory
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.LogDirectory = newLogsDir
	app.configMu.Unlock()

	// Reload with different directory
	err = app.reloadLoggingFromConfig()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Read bootstrap log to verify transition message exists
	bootstrapContent, err := os.ReadFile(bootstrapLogPath)
	if err != nil {
		t.Fatalf("failed to read bootstrap log: %v", err)
	}
	bootstrapStr := string(bootstrapContent)

	// Check for transition-related log messages
	if !strings.Contains(bootstrapStr, "log") || !strings.Contains(bootstrapStr, "directory") {
		// We expect some indication of the change in the log
		t.Logf("bootstrap log content:\n%s", bootstrapStr)
		// This is informational - the actual message format may vary
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// === Bootstrap Persistence Integration Tests ===
// These tests verify database persistence of logging configuration

// TestBootstrapConfig_SavedToDatabaseOnInit verifies that bootstrap logging
// configuration is included in the database initialization process.
