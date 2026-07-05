//go:build integration || e2e

package server

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/server/config"
)

func TestFileAlignment_BootstrapLogsEarlyEvents(t *testing.T) {
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

// TestFileAlignment_ConfigLogsContinuation verifies that when the log directory
// changes, the new log file properly receives subsequent log messages while the
// bootstrap log remains unchanged.

func TestFileAlignment_ConfigLogsContinuation(t *testing.T) {
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
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.LogDirectory = newLogsDir
	app.configMu.Unlock()

	err = app.reloadLoggingFromConfig()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Log a message after config reload
	slog.Info("config phase message")

	// Wait a bit for flush
	time.Sleep(10 * time.Millisecond)

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

// TestFileAlignment_BootstrapFileNotModified verifies that once the log directory
// changes, the bootstrap log file is not modified by subsequent operations.

func TestFileAlignment_BootstrapFileNotModified(t *testing.T) {
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

	// Wait to ensure file times would differ
	time.Sleep(100 * time.Millisecond)

	// Create new logs directory
	newLogsDir := filepath.Join(tmpDir, "logs_prod")
	if mkdirErr := os.MkdirAll(newLogsDir, 0755); mkdirErr != nil {
		t.Fatalf("failed to create new logs dir: %v", mkdirErr)
	}

	// Reload with different directory
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.LogDirectory = newLogsDir
	app.configMu.Unlock()

	err = app.reloadLoggingFromConfig()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Log config message (goes to new file, not bootstrap)
	slog.Info("config only")

	// Wait for logging to flush
	time.Sleep(100 * time.Millisecond)

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

// Helper function to check if a string contains a substring (case-insensitive pattern matching)
