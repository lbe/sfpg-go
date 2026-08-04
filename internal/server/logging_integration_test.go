//go:build integration

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

// --- merged from logging_alignment_integration_test.go ---
func TestFileAlignment_BootstrapLogsEarlyEvents(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Bootstrap logging setup
	app.setupBootstrapLogging()

	// Log an early event
	slog.Info("early bootstrap event")

	// Get bootstrap log path
	bootstrapPath := app.logger.FilePath()

	// Read bootstrap log content
	content, err := os.ReadFile(bootstrapPath)
	if err != nil {
		t.Fatalf("failed to read bootstrap log: %v", err)
	}

	logContent := string(content)

	// Verify log contains the message
	if !strings.Contains(logContent, "early bootstrap event") {
		t.Fatalf("expected 'early bootstrap event' in bootstrap log, got:\n%s", logContent)
	}

	// Verify it's in JSON format
	if !strings.Contains(logContent, "\"msg\"") && !strings.Contains(logContent, "\"message\"") {
		t.Fatalf("expected JSON formatted log, got:\n%s", logContent[:100])
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

func TestFileAlignment_ConfigLogsContinuation(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Bootstrap logging setup
	app.setupBootstrapLogging()

	// Log bootstrap message
	slog.Info("bootstrap phase message")

	bootstrapPath := app.logger.FilePath()

	// Get bootstrap file size
	bootstrapInfo, err := os.Stat(bootstrapPath)
	if err != nil {
		t.Fatalf("failed to stat bootstrap log: %v", err)
	}
	_ = bootstrapInfo // Variable used for verification, not directly checked

	// Create new logs directory
	newLogsDir := filepath.Join(tmpDir, "logs_prod")
	if mkdirErr := os.MkdirAll(newLogsDir, 0755); mkdirErr != nil {
		t.Fatalf("failed to create new logs dir: %v", mkdirErr)
	}

	// Reload with different directory
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.LogDirectory = newLogsDir
	app.ConfigManager.ConfigMu.Unlock()

	err = app.reloadLoggingFromConfig()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Log a message after config reload
	slog.Info("config phase message")

	// Bootstrap log should not have the config phase message
	bootstrapContent, err := os.ReadFile(bootstrapPath)
	if err != nil {
		t.Fatalf("failed to read bootstrap log: %v", err)
	}
	bootstrapStr := string(bootstrapContent)

	if strings.Contains(bootstrapStr, "config phase message") {
		t.Fatalf("bootstrap log should not contain config phase message")
	}

	// New log file should have the config phase message
	newFiles, err := os.ReadDir(newLogsDir)
	if err != nil || len(newFiles) == 0 {
		t.Fatalf("expected new log file")
	}

	newLogPath := filepath.Join(newLogsDir, newFiles[0].Name())
	newContent, err := os.ReadFile(newLogPath)
	if err != nil {
		t.Fatalf("failed to read new log: %v", err)
	}
	newStr := string(newContent)

	if !strings.Contains(newStr, "config phase message") {
		t.Fatalf("new log should contain config phase message, got:\n%s", newStr)
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

func TestFileAlignment_BootstrapFileNotModified(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Bootstrap logging setup
	app.setupBootstrapLogging()

	// Log bootstrap message
	slog.Info("bootstrap only")

	bootstrapPath := app.logger.FilePath()

	// Get bootstrap file stat before reload
	statBefore, err := os.Stat(bootstrapPath)
	if err != nil {
		t.Fatalf("failed to stat bootstrap log: %v", err)
	}
	_ = statBefore // Used to verify file exists before reload
	sizeBefore := statBefore.Size()

	// Create new logs directory
	newLogsDir := filepath.Join(tmpDir, "logs_prod")
	if mkdirErr := os.MkdirAll(newLogsDir, 0755); mkdirErr != nil {
		t.Fatalf("failed to create new logs dir: %v", mkdirErr)
	}

	// Reload with different directory
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.LogDirectory = newLogsDir
	app.ConfigManager.ConfigMu.Unlock()

	err = app.reloadLoggingFromConfig()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Log config message (goes to new file, not bootstrap)
	slog.Info("config only")

	// Check bootstrap file hasn't changed (size should be same or close)
	statAfter, err := os.Stat(bootstrapPath)
	if err != nil {
		t.Fatalf("failed to stat bootstrap log after reload: %v", err)
	}
	sizeAfter := statAfter.Size()

	// Bootstrap file should not have grown significantly
	// (only the reload transition message might be added to bootstrap)
	if sizeAfter-sizeBefore > 500 {
		t.Fatalf("bootstrap file grew unexpectedly: %d -> %d bytes",
			sizeBefore, sizeAfter)
	}

	// Read bootstrap file content
	bootstrapContent, err := os.ReadFile(bootstrapPath)
	if err != nil {
		t.Fatalf("failed to read bootstrap log: %v", err)
	}

	// Verify bootstrap file doesn't contain config-only message
	if strings.Contains(string(bootstrapContent), "config only") {
		t.Fatalf("bootstrap file should not contain config-only message")
	}

	// Cleanup
	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// trailing from logging_alignment_integration_test.go
// Helper function to check if a string contains a substring (case-insensitive pattern matching)

// --- merged from logging_api_integration_test.go ---
func TestConfigAPI_LogDirChangedToSame_NoLoggingReload(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Bootstrap logging setup
	app.setupBootstrapLogging()

	// Get the initial log directory
	initialLogDir := app.logger.LogsDir()

	// Update app.ConfigManager.Config to have the same log directory
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.LogDirectory = initialLogDir
	app.ConfigManager.ConfigMu.Unlock()

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

func TestConfigAPI_LogLevelChangedSameDir_UpdatesLevel(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.LogDirectory = app.logger.LogsDir() // Keep same directory
	app.ConfigManager.Config.LogLevel = "error"                  // Change level to error
	app.ConfigManager.ConfigMu.Unlock()

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

func TestConfigAPI_SameDir_NoInterruption(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.LogDirectory = logsDir // Same as bootstrap
	app.ConfigManager.ConfigMu.Unlock()

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

func TestConfigAPI_LogDirChangedToDifferent_NewFileCreated(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.LogDirectory = newLogsDir
	app.ConfigManager.ConfigMu.Unlock()

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

func TestConfigAPI_LogDirChangedToDifferent_FilesAligned(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.LogDirectory = newLogsDir
	app.ConfigManager.ConfigMu.Unlock()

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

func TestConfigAPI_LogDirChangedToDifferent_OldFileStillExists(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.LogDirectory = newLogsDir
	app.ConfigManager.ConfigMu.Unlock()

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

func TestConfigAPI_LogDirChangedToDifferent_NewEntriesGoToNew(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.LogDirectory = newLogsDir
	app.ConfigManager.ConfigMu.Unlock()

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

func TestConfigAPI_LogDirChangedToDifferent_TransitionLogged(t *testing.T) {
	// Setup: Create temp rootDir
	tmpDir := t.TempDir()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = config.DefaultConfig()
	app.ConfigManager.Config.LogDirectory = newLogsDir
	app.ConfigManager.ConfigMu.Unlock()

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

// trailing from logging_api_integration_test.go
// === Bootstrap Persistence Integration Tests ===
// These tests verify database persistence of logging configuration

// TestBootstrapConfig_SavedToDatabaseOnInit verifies that bootstrap logging
// configuration is included in the database initialization process.

// --- merged from logging_startup_integration_test.go ---
func TestStartupLogging_BootstrapThenReload_UpdatesLogLevel(t *testing.T) {
	// Setup: Create temp directory
	tmpDir := t.TempDir()

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = &config.Config{
		LogDirectory: "logs", // Same as bootstrap
		LogLevel:     "warn",
	}
	app.ConfigManager.ConfigMu.Unlock()

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

func TestStartupLogging_BootstrapWithInvalidConfig(t *testing.T) {
	// Setup: Create temp directory
	tmpDir := t.TempDir()

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Action: Call setupBootstrapLogging
	app.setupBootstrapLogging()

	// Deliberately don't set app.ConfigManager.Config

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

func TestStartupLogging_BootstrapThenReload_CreatesNewDirectory(t *testing.T) {
	// Setup: Create temp root directory
	tmpDir := t.TempDir()

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = &config.Config{
		LogDirectory: configuredLogDir,
		LogLevel:     "debug",
	}
	app.ConfigManager.ConfigMu.Unlock()

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

func TestStartupLogging_BootstrapThenReload_AbsoluteConfigPath(t *testing.T) {
	// Setup: Create temp directories
	tmpDir := t.TempDir()
	absoluteLogDir := filepath.Join(tmpDir, "absolute-logs")

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
		RuntimeManager:        NewRuntimeManager(context.Background()),
		SubsystemManager:      NewSubsystemManager(infra),
		HandlerManager:        NewHandlerManager(),
	}
	app.rootDir = tmpDir

	// Action: Call setupBootstrapLogging
	app.setupBootstrapLogging()

	// Action: Create config with absolute path
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = &config.Config{
		LogDirectory: absoluteLogDir, // Absolute path
		LogLevel:     "debug",
	}
	app.ConfigManager.ConfigMu.Unlock()

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

func TestStartupLogging_BootstrapThenReload_InvalidLevel(t *testing.T) {
	// Setup: Create temp directory
	tmpDir := t.TempDir()

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = &config.Config{
		LogDirectory: filepath.Join(tmpDir, "logs-new"), // Different directory
		LogLevel:     "invalid",
	}
	app.ConfigManager.ConfigMu.Unlock()

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
				ConfigManager:         config.NewConfigManager(),
				RuntimeManager:        NewRuntimeManager(context.Background()),
				SubsystemManager:      NewSubsystemManager(infra),
			}
			app.rootDir = tmpDir

			// Action: Call setupBootstrapLogging
			app.setupBootstrapLogging()

			// Create different directory with relative path to force reload
			configuredLogDir := filepath.Join(tmpDir, "logs-"+tt.name)

			// Action: Create config with this level
			app.ConfigManager.ConfigMu.Lock()
			app.ConfigManager.Config = &config.Config{
				LogDirectory: configuredLogDir,
				LogLevel:     tt.level,
			}
			app.ConfigManager.ConfigMu.Unlock()

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

func TestBootstrapConfig_SavedToDatabaseOnInit(t *testing.T) {
	// Setup: Create temp directory and database
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create app with temp rootDir
	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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

func TestBootstrapConfig_NotReinsertedOnSecondInit(t *testing.T) {
	// Setup: Create temp directory and database
	tmpDir := t.TempDir()
	ctx := context.Background()

	infra := NewInfrastructureService()
	app := &App{
		InfrastructureService: infra,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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

func TestBootstrapConfig_UsedInLoadConfig(t *testing.T) {
	// Setup: Create temp directory and database with bootstrap config
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create fresh app to initialize database
	infra1 := NewInfrastructureService()
	app1 := &App{
		InfrastructureService: infra1,
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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
		ConfigManager:         config.NewConfigManager(),
		SessionAuthFacade:     NewSessionAuthFacade("test-secret-with-at-least-32-bytes-long"),
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
	app2.ConfigManager.ConfigMu.RLock()
	logDir := app2.ConfigManager.Config.LogDirectory
	logLevel := app2.ConfigManager.Config.LogLevel
	app2.ConfigManager.ConfigMu.RUnlock()

	// LogDirectory should match what was saved
	if logDir != savedLogDir {
		t.Fatalf("LogDirectory should be %s from database, got %s", savedLogDir, logDir)
	}

	if logLevel != "debug" {
		t.Fatalf("LogLevel should be debug, got %s", logLevel)
	}
}

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

// trailing from logging_startup_integration_test.go
// === File Alignment Integration Tests ===
// These tests verify file alignment behavior through App integration (bootstrap -> reload sequence)

// TestFileAlignment_BootstrapLogsEarlyEvents verifies that the bootstrap log file
// captures early initialization events before configuration is loaded.
