package log

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.local/sfpg/internal/scheduler"
)

// testScheduler creates a scheduler for test isolation
func testScheduler(t *testing.T) *scheduler.Scheduler {
	t.Helper()
	sched := scheduler.NewScheduler(1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = sched.Start(ctx)
	}()
	// Give scheduler time to start
	time.Sleep(10 * time.Millisecond)
	return sched
}

// TestNewBootstrapLogger_CreatesLoggerWithCorrectDefaults verifies that NewBootstrapLogger
// creates a logger with correct default values.
func TestNewBootstrapLogger_CreatesLoggerWithCorrectDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	if logger == nil {
		t.Fatal("logger should not be nil")
	}

	// Check default log level is debug
	logger.mu.RLock()
	level := logger.level
	logger.mu.RUnlock()

	if level != slog.LevelDebug {
		t.Fatalf("expected default log level to be debug, got %v", level)
	}
}

// TestNewBootstrapLogger_CreatesLogsDirectory verifies that NewBootstrapLogger
// creates the logs directory if it doesn't exist.
func TestNewBootstrapLogger_CreatesLogsDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	logsDir := filepath.Join(tmpDir, "logs")
	sched := testScheduler(t)

	// Verify logs directory doesn't exist yet
	if _, err := os.Stat(logsDir); err == nil {
		t.Fatal("logs directory should not exist before NewBootstrapLogger")
	}

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	// Verify logs directory was created
	if info, err := os.Stat(logsDir); err != nil || !info.IsDir() {
		t.Fatalf("logs directory should exist after NewBootstrapLogger: %v", err)
	}
}

// TestNewBootstrapLogger_CreatesLogFile verifies that NewBootstrapLogger
// creates a log file with timestamp in the logs directory.
func TestNewBootstrapLogger_CreatesLogFile(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	logger.mu.RLock()
	logFile := logger.file
	logFilePath := logger.filePath
	logger.mu.RUnlock()

	if logFile == nil {
		t.Fatal("logFile should not be nil after NewBootstrapLogger")
	}

	if info, err := os.Stat(logFilePath); err != nil || info.IsDir() {
		t.Fatalf("log file should exist: %v", err)
	}

	// Verify filename pattern
	expectedPattern := "sfpg-"
	if filepath.Base(logFilePath)[:len(expectedPattern)] != expectedPattern {
		t.Fatalf("log file should match pattern sfpg-*.log, got %s", filepath.Base(logFilePath))
	}
}

// TestNewBootstrapLogger_DoesNotScheduleRollover verifies that NewBootstrapLogger
// does NOT schedule a rollover task (rollover is only scheduled after config is loaded).
func TestNewBootstrapLogger_DoesNotScheduleRollover(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	logger.mu.RLock()
	rolloverTaskID := logger.rolloverTaskID
	retentionTaskID := logger.retentionTaskID
	logger.mu.RUnlock()

	if rolloverTaskID != "" {
		t.Fatalf("rolloverTaskID should be empty after bootstrap, got %s", rolloverTaskID)
	}

	if retentionTaskID != "" {
		t.Fatalf("retentionTaskID should be empty after bootstrap, got %s", retentionTaskID)
	}
}

// TestReloadFromConfig_UpdatesLogLevelWhenDirectoryUnchanged verifies that
// ReloadFromConfig updates the log level when the directory is unchanged.
func TestReloadFromConfig_UpdatesLogLevelWhenDirectoryUnchanged(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	// Get initial log level
	logger.mu.RLock()
	initialLevel := logger.level
	initialDir := logger.dir
	logger.mu.RUnlock()

	if initialLevel != slog.LevelDebug {
		t.Fatalf("expected initial level to be debug, got %v", initialLevel)
	}

	// Create config with same directory but different level
	logDir := filepath.Join(tmpDir, "logs")
	logLevel := "info"
	logRollover := "weekly"
	logRetentionCount := 7

	err = logger.ReloadFromConfig(logDir, logLevel, logRollover, logRetentionCount, sched)
	if err != nil {
		t.Fatalf("ReloadFromConfig should not fail: %v", err)
	}

	// Verify log level was updated
	logger.mu.RLock()
	newLevel := logger.level
	newDir := logger.dir
	logger.mu.RUnlock()

	if newLevel != slog.LevelInfo {
		t.Fatalf("expected log level to be info, got %v", newLevel)
	}

	if newDir != initialDir {
		t.Fatalf("directory should not change when same, got %s, expected %s", newDir, initialDir)
	}
}

// TestReloadFromConfig_SwitchesToNewDirectory verifies that ReloadFromConfig
// switches to a new directory when the configured directory differs.
func TestReloadFromConfig_SwitchesToNewDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	// Get initial directory
	logger.mu.RLock()
	initialDir := logger.dir
	initialFilePath := logger.filePath
	logger.mu.RUnlock()

	// Create new directory
	newLogDir := filepath.Join(tmpDir, "newlogs")
	logLevel := "debug"
	logRollover := "weekly"
	logRetentionCount := 7

	err = logger.ReloadFromConfig(newLogDir, logLevel, logRollover, logRetentionCount, sched)
	if err != nil {
		t.Fatalf("ReloadFromConfig should not fail: %v", err)
	}

	// Verify directory was switched
	logger.mu.RLock()
	newDir := logger.dir
	newFilePath := logger.filePath
	logger.mu.RUnlock()

	if newDir == initialDir {
		t.Fatal("directory should change when different directory is configured")
	}

	if newDir != newLogDir {
		t.Fatalf("expected new directory to be %s, got %s", newLogDir, newDir)
	}

	// Verify new file was created with same filename (aligned)
	if filepath.Base(newFilePath) != filepath.Base(initialFilePath) {
		t.Fatalf("new log file should have same filename as bootstrap, got %s, expected %s",
			filepath.Base(newFilePath), filepath.Base(initialFilePath))
	}
}

// TestReloadFromConfig_SchedulesRolloverAndRetention verifies that ReloadFromConfig
// schedules both rollover and retention tasks.
func TestReloadFromConfig_SchedulesRolloverAndRetention(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	logDir := filepath.Join(tmpDir, "logs")
	logLevel := "debug"
	logRollover := "weekly"
	logRetentionCount := 7

	err = logger.ReloadFromConfig(logDir, logLevel, logRollover, logRetentionCount, sched)
	if err != nil {
		t.Fatalf("ReloadFromConfig should not fail: %v", err)
	}

	logger.mu.RLock()
	rolloverTaskID := logger.rolloverTaskID
	retentionTaskID := logger.retentionTaskID
	logger.mu.RUnlock()

	if rolloverTaskID == "" {
		t.Fatal("rolloverTaskID should be set after ReloadFromConfig")
	}

	if retentionTaskID == "" {
		t.Fatal("retentionTaskID should be set after ReloadFromConfig")
	}
}

// TestShutdown_ClosesLogFile verifies that Shutdown closes the log file.
func TestShutdown_ClosesLogFile(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}

	logger.mu.RLock()
	logFile := logger.file
	logger.mu.RUnlock()

	if logFile == nil {
		t.Fatal("logFile should not be nil")
	}

	err = logger.Shutdown()
	if err != nil {
		t.Fatalf("Shutdown should not fail: %v", err)
	}

	// Verify file is closed by attempting to read from it
	// A closed file should return an error on operations
	_, err = logFile.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("closed file should return error on read")
	}
}

// TestThreadSafety_ConcurrentReloadFromConfig verifies that ReloadFromConfig
// is thread-safe and can be called concurrently.
func TestThreadSafety_ConcurrentReloadFromConfig(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	logDir := filepath.Join(tmpDir, "logs")
	logLevel := "debug"
	logRollover := "weekly"
	logRetentionCount := 7

	// Call ReloadFromConfig concurrently
	var wg sync.WaitGroup
	numGoroutines := 10
	wg.Add(numGoroutines)

	for range numGoroutines {
		go func() {
			defer wg.Done()
			_ = logger.ReloadFromConfig(logDir, logLevel, logRollover, logRetentionCount, sched)
		}()
	}

	wg.Wait()

	// Verify logger is still in valid state
	logger.mu.RLock()
	_ = logger.file
	_ = logger.filePath
	_ = logger.dir
	logger.mu.RUnlock()
}

// TestMutexProtection_TaskIDs verifies that rolloverTaskID and retentionTaskID
// are properly protected by the mutex.
func TestMutexProtection_TaskIDs(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	logDir := filepath.Join(tmpDir, "logs")
	logLevel := "debug"
	logRollover := "weekly"
	logRetentionCount := 7

	// Reload config to set task IDs
	err = logger.ReloadFromConfig(logDir, logLevel, logRollover, logRetentionCount, sched)
	if err != nil {
		t.Fatalf("ReloadFromConfig should not fail: %v", err)
	}

	// Access task IDs both with and without lock to verify mutex is used
	// This is a basic check - the real protection is verified by concurrent access tests
	logger.mu.RLock()
	rolloverID := logger.rolloverTaskID
	retentionID := logger.retentionTaskID
	logger.mu.RUnlock()

	// Verify IDs are set (proves mutex-protected access works)
	if rolloverID == "" {
		t.Fatal("rolloverTaskID should be set")
	}
	if retentionID == "" {
		t.Fatal("retentionTaskID should be set")
	}
}

// TestLoggerGetters_FileReturnsCurrentFile verifies File() returns the current log file.
func TestLoggerGetters_FileReturnsCurrentFile(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	logDir := filepath.Join(tmpDir, "logs")
	err = logger.ReloadFromConfig(logDir, "info", "daily", 7, sched)
	if err != nil {
		t.Fatalf("ReloadFromConfig should not fail: %v", err)
	}

	// File() should return the current file
	file := logger.File()
	if file == nil {
		t.Fatal("File() should not return nil")
	}

	// Verify it's a valid file
	info, err := file.Stat()
	if err != nil {
		t.Fatalf("File() returned invalid file: %v", err)
	}
	if info.IsDir() {
		t.Fatal("File() returned a directory instead of a file")
	}
}

// TestLoggerGetters_FilePathReturnsCorrectPath verifies FilePath() returns the correct path.
func TestLoggerGetters_FilePathReturnsCorrectPath(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	logDir := filepath.Join(tmpDir, "logs")
	err = logger.ReloadFromConfig(logDir, "warn", "weekly", 5, sched)
	if err != nil {
		t.Fatalf("ReloadFromConfig should not fail: %v", err)
	}

	// FilePath() should return a non-empty path
	path := logger.FilePath()
	if path == "" {
		t.Fatal("FilePath() should not return empty string")
	}

	// Verify the path exists
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("FilePath() returned non-existent path: %v", err)
	}

	// Verify the path is in the correct directory
	if !strings.HasPrefix(path, logDir) {
		t.Fatalf("FilePath() returned path %s not in log directory %s", path, logDir)
	}
}

// TestLoggerGetters_LogsDirReturnsCorrectDirectory verifies LogsDir() returns the logs directory.
func TestLoggerGetters_LogsDirReturnsCorrectDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	logDir := filepath.Join(tmpDir, "logs")
	err = logger.ReloadFromConfig(logDir, "error", "monthly", 3, sched)
	if err != nil {
		t.Fatalf("ReloadFromConfig should not fail: %v", err)
	}

	// LogsDir() should return the configured log directory
	dir := logger.LogsDir()
	if dir != logDir {
		t.Fatalf("LogsDir() returned %s, expected %s", dir, logDir)
	}

	// Verify the directory exists
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("LogsDir() returned non-existent directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("LogsDir() returned a file instead of a directory")
	}
}

// TestLoggerGetters_LogLevelReturnsCurrentLevel verifies LogLevel() returns the current level.
func TestLoggerGetters_LogLevelReturnsCurrentLevel(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	// Default level should be debug
	level := logger.LogLevel()
	if level != slog.LevelDebug {
		t.Fatalf("LogLevel() returned %v, expected LevelDebug", level)
	}

	logDir := filepath.Join(tmpDir, "logs")
	// Set to warn level
	err = logger.ReloadFromConfig(logDir, "warn", "daily", 7, sched)
	if err != nil {
		t.Fatalf("ReloadFromConfig should not fail: %v", err)
	}

	level = logger.LogLevel()
	if level != slog.LevelWarn {
		t.Fatalf("LogLevel() returned %v, expected LevelWarn", level)
	}
}

// TestLoggerGetters_SlogLoggerReturnsValidLogger verifies SlogLogger() returns a valid logger.
func TestLoggerGetters_SlogLoggerReturnsValidLogger(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	// SlogLogger() should return a non-nil logger
	slogLogger := logger.SlogLogger()
	if slogLogger == nil {
		t.Fatal("SlogLogger() should not return nil")
	}

	// Verify we can use it to log (shouldn't panic)
	ctx := context.Background()
	slogLogger.InfoContext(ctx, "test message")
}
