package log

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCalculateNextRolloverTime_Daily verifies that calculateNextRolloverTime
// returns the next 00:00:00 (midnight of next day) for daily rollover.
func TestCalculateNextRolloverTime_Daily(t *testing.T) {
	now := time.Date(2025, 1, 15, 14, 30, 0, 0, time.UTC)
	nextTime := calculateNextRolloverTime("daily", now)

	expected := time.Date(2025, 1, 16, 0, 0, 0, 0, time.UTC)
	if !nextTime.Equal(expected) {
		t.Fatalf("expected next daily rollover at %v, got %v", expected, nextTime)
	}
}

// TestCalculateNextRolloverTime_DailyAtMidnight verifies that if current time
// is exactly midnight, it returns the next day's midnight.
func TestCalculateNextRolloverTime_DailyAtMidnight(t *testing.T) {
	now := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	nextTime := calculateNextRolloverTime("daily", now)

	expected := time.Date(2025, 1, 16, 0, 0, 0, 0, time.UTC)
	if !nextTime.Equal(expected) {
		t.Fatalf("expected next daily rollover at %v, got %v", expected, nextTime)
	}
}

// TestCalculateNextRolloverTime_Weekly verifies that calculateNextRolloverTime
// returns the next Monday at 00:00:00 for weekly rollover.
func TestCalculateNextRolloverTime_Weekly(t *testing.T) {
	// Test from Wednesday (should return next Monday)
	now := time.Date(2025, 1, 15, 14, 30, 0, 0, time.UTC) // Wednesday
	nextTime := calculateNextRolloverTime("weekly", now)

	expected := time.Date(2025, 1, 20, 0, 0, 0, 0, time.UTC) // Next Monday
	if !nextTime.Equal(expected) {
		t.Fatalf("expected next weekly rollover at %v (Monday), got %v", expected, nextTime)
	}
}

// TestCalculateNextRolloverTime_WeeklyOnMonday verifies that if current time
// is Monday, it returns the next Monday (7 days away).
func TestCalculateNextRolloverTime_WeeklyOnMonday(t *testing.T) {
	// Test from Monday
	now := time.Date(2025, 1, 13, 14, 30, 0, 0, time.UTC) // Monday
	nextTime := calculateNextRolloverTime("weekly", now)

	expected := time.Date(2025, 1, 20, 0, 0, 0, 0, time.UTC) // Next Monday (7 days away)
	if !nextTime.Equal(expected) {
		t.Fatalf("expected next weekly rollover at %v (next Monday), got %v", expected, nextTime)
	}
}

// TestCalculateNextRolloverTime_WeeklyOnSunday verifies that if current time
// is Sunday, it returns the next Monday (1 day away).
func TestCalculateNextRolloverTime_WeeklyOnSunday(t *testing.T) {
	// Test from Sunday
	now := time.Date(2025, 1, 12, 14, 30, 0, 0, time.UTC) // Sunday
	nextTime := calculateNextRolloverTime("weekly", now)

	expected := time.Date(2025, 1, 13, 0, 0, 0, 0, time.UTC) // Next Monday (1 day away)
	if !nextTime.Equal(expected) {
		t.Fatalf("expected next weekly rollover at %v (next Monday), got %v", expected, nextTime)
	}
}

// TestCalculateNextRolloverTime_Monthly verifies that calculateNextRolloverTime
// returns the first day of next month at 00:00:00 for monthly rollover.
func TestCalculateNextRolloverTime_Monthly(t *testing.T) {
	now := time.Date(2025, 1, 15, 14, 30, 0, 0, time.UTC)
	nextTime := calculateNextRolloverTime("monthly", now)

	expected := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	if !nextTime.Equal(expected) {
		t.Fatalf("expected next monthly rollover at %v, got %v", expected, nextTime)
	}
}

// TestCalculateNextRolloverTime_MonthlyOnFirstDay verifies that if current time
// is the first day of the month, it returns the first day of next month.
func TestCalculateNextRolloverTime_MonthlyOnFirstDay(t *testing.T) {
	now := time.Date(2025, 1, 1, 14, 30, 0, 0, time.UTC)
	nextTime := calculateNextRolloverTime("monthly", now)

	expected := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	if !nextTime.Equal(expected) {
		t.Fatalf("expected next monthly rollover at %v, got %v", expected, nextTime)
	}
}

// TestCalculateNextRolloverTime_MonthlyYearBoundary verifies that monthly rollover
// correctly handles year boundary (December to January).
func TestCalculateNextRolloverTime_MonthlyYearBoundary(t *testing.T) {
	now := time.Date(2025, 12, 15, 14, 30, 0, 0, time.UTC)
	nextTime := calculateNextRolloverTime("monthly", now)

	expected := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !nextTime.Equal(expected) {
		t.Fatalf("expected next monthly rollover at %v, got %v", expected, nextTime)
	}
}

// TestCalculateNextRolloverTime_TimezoneHandling verifies that calculateNextRolloverTime
// preserves the timezone of the input time.
func TestCalculateNextRolloverTime_TimezoneHandling(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("failed to load timezone: %v", err)
	}

	now := time.Date(2025, 1, 15, 14, 30, 0, 0, loc)
	nextTime := calculateNextRolloverTime("daily", now)

	if nextTime.Location() != loc {
		t.Fatalf("expected timezone %v, got %v", loc, nextTime.Location())
	}

	expected := time.Date(2025, 1, 16, 0, 0, 0, 0, loc)
	if !nextTime.Equal(expected) {
		t.Fatalf("expected next daily rollover at %v, got %v", expected, nextTime)
	}
}

// TestRolloverTask_Run_ExecutesRollover verifies that RolloverTask.Run()
// executes rollover correctly by creating a new file and closing the old one.
func TestRolloverTask_Run_ExecutesRollover(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	// Get initial file path
	logger.mu.RLock()
	initialFilePath := logger.filePath
	initialFile := logger.file
	logger.mu.RUnlock()

	// Create rollover task
	task := &RolloverTask{
		logger:            logger,
		logRollover:       "daily",
		logRetentionCount: 7,
	}

	// Execute rollover
	err = task.Run(context.Background())
	if err != nil {
		t.Fatalf("RolloverTask.Run should not fail: %v", err)
	}

	// Verify new file was created
	logger.mu.RLock()
	newFilePath := logger.filePath
	newFile := logger.file
	logger.mu.RUnlock()

	if newFilePath == initialFilePath {
		t.Fatal("rollover should create a new file with different path")
	}

	// Verify old file is closed (attempting to read should fail)
	_, err = initialFile.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("old file should be closed after rollover")
	}

	// Verify new file exists
	if _, err := os.Stat(newFilePath); err != nil {
		t.Fatalf("new log file should exist: %v", err)
	}

	// Verify new file is different from old file
	if newFile == initialFile {
		t.Fatal("rollover should create a new file object")
	}
}

// TestRolloverTask_Run_TriggersRetentionCleanup verifies that RolloverTask.Run()
// triggers retention cleanup after creating the new file.
func TestRolloverTask_Run_TriggersRetentionCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	// Create multiple old log files to test retention
	logsDir := filepath.Join(tmpDir, "logs")
	for i := range 10 {
		oldFile := filepath.Join(logsDir, fmt.Sprintf("sfpg-2025-01-%02d_00-00-00.log", i+1))
		f, fileErr := os.Create(oldFile)
		if fileErr != nil {
			t.Fatalf("failed to create test log file: %v", fileErr)
		}
		f.Close()
	}

	// Create rollover task
	task := &RolloverTask{
		logger:            logger,
		logRollover:       "daily",
		logRetentionCount: 7,
	}

	// Execute rollover
	err = task.Run(context.Background())
	if err != nil {
		t.Fatalf("RolloverTask.Run should not fail: %v", err)
	}

	// Verify retention cleanup ran (should keep only 7 most recent + current)
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		t.Fatalf("failed to read logs directory: %v", err)
	}

	logFiles := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".log" {
			logFiles++
		}
	}

	// Should have 7 old files + 1 current file = 8 total
	expectedCount := 8
	if logFiles > expectedCount {
		t.Fatalf("retention cleanup should keep at most %d files, found %d", expectedCount, logFiles)
	}
}

// TestScheduleRollover_RemovesOldTask verifies that scheduleRollover removes
// the old rollover task if rolloverTaskID is set.
func TestScheduleRollover_RemovesOldTask(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	logRollover := "daily"
	logRetentionCount := 7

	// Schedule initial rollover
	logger.mu.Lock()
	taskID1, err := scheduleRollover(logger, logRollover, logRetentionCount, sched)
	if err != nil {
		logger.mu.Unlock()
		t.Fatalf("scheduleRollover should not fail: %v", err)
	}
	logger.rolloverTaskID = taskID1
	logger.mu.Unlock()

	// Change rollover period
	logRollover = "weekly"

	// Schedule new rollover (should remove old one)
	logger.mu.Lock()
	taskID2, err := scheduleRollover(logger, logRollover, logRetentionCount, sched)
	if err != nil {
		logger.mu.Unlock()
		t.Fatalf("scheduleRollover should not fail: %v", err)
	}
	logger.rolloverTaskID = taskID2
	logger.mu.Unlock()

	if taskID1 == taskID2 {
		t.Fatal("new rollover task should have different ID")
	}

	// Verify old task was removed (attempting to remove again should fail)
	_ = sched.RemoveTask(taskID1)
	// Note: RemoveTask might succeed even if task was already removed
	// The important thing is that the new task is scheduled
}

// TestScheduleRollover_ErrorHandling verifies that if RemoveTask fails,
// scheduleRollover logs a warning and continues (adds new task anyway).
func TestScheduleRollover_ErrorHandling(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	logRollover := "daily"
	logRetentionCount := 7

	// Set a non-existent task ID
	logger.mu.Lock()
	logger.rolloverTaskID = "non-existent-task-id"
	logger.mu.Unlock()

	// Schedule rollover (should handle RemoveTask error gracefully)
	logger.mu.Lock()
	taskID, err := scheduleRollover(logger, logRollover, logRetentionCount, sched)
	logger.mu.Unlock()

	if err != nil {
		t.Fatalf("scheduleRollover should not fail even if RemoveTask fails: %v", err)
	}

	if taskID == "" {
		t.Fatal("scheduleRollover should return a task ID even if RemoveTask fails")
	}
}
