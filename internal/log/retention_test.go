package log

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFindLogFiles_FindsAllLogFiles verifies that findLogFiles finds all
// log files matching the pattern sfpg-*.log.
func TestFindLogFiles_FindsAllLogFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create log files
	logFiles := []string{
		"sfpg-2025-01-01_00-00-00.log",
		"sfpg-2025-01-02_00-00-00.log",
		"sfpg-2025-01-03_00-00-00.log",
		"not-a-log-file.txt",
		"other-file.log", // Doesn't match pattern
	}

	for _, fileName := range logFiles {
		filePath := filepath.Join(tmpDir, fileName)
		f, err := os.Create(filePath)
		if err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
		f.Close()
	}

	// Find log files
	foundFiles, err := findLogFiles(tmpDir)
	if err != nil {
		t.Fatalf("findLogFiles should not fail: %v", err)
	}

	// Should find 3 log files (matching sfpg-*.log pattern)
	if len(foundFiles) != 3 {
		t.Fatalf("expected 3 log files, found %d", len(foundFiles))
	}

	// Verify all found files match the pattern
	for _, file := range foundFiles {
		name := file.Name()
		if len(name) < 5 || name[:5] != "sfpg-" || filepath.Ext(name) != ".log" {
			t.Fatalf("found file does not match pattern: %s", name)
		}
	}
}

// TestCleanupOldLogs_KeepsCorrectCount verifies that CleanupOldLogs keeps
// the correct number of files based on retention count.
func TestCleanupOldLogs_KeepsCorrectCount(t *testing.T) {
	tmpDir := t.TempDir()
	activeFilePath := filepath.Join(tmpDir, "sfpg-2025-01-10_00-00-00.log")

	// Create active file
	activeFile, err := os.Create(activeFilePath)
	if err != nil {
		t.Fatalf("failed to create active file: %v", err)
	}
	activeFile.Close()

	// Create old log files (10 files)
	for i := 1; i <= 10; i++ {
		oldFile := filepath.Join(tmpDir, fmt.Sprintf("sfpg-2025-01-%02d_00-00-00.log", i))
		f, fileErr := os.Create(oldFile)
		if fileErr != nil {
			t.Fatalf("failed to create old log file: %v", fileErr)
		}
		f.Close()
		// Set modification time to make them older
		modTime := time.Now().Add(-time.Duration(i) * time.Hour)
		os.Chtimes(oldFile, modTime, modTime)
	}

	retentionCount := 7

	// Run cleanup
	err = CleanupOldLogs(tmpDir, activeFilePath, retentionCount)
	if err != nil {
		t.Fatalf("CleanupOldLogs should not fail: %v", err)
	}

	// Verify only retentionCount files remain (plus active file)
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read directory: %v", err)
	}

	logFileCount := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".log" {
			logFileCount++
		}
	}

	// Should have retentionCount old files + 1 active file
	expectedCount := retentionCount + 1
	if logFileCount != expectedCount {
		t.Fatalf("expected %d log files, found %d", expectedCount, logFileCount)
	}

	// Verify active file still exists
	if _, err := os.Stat(activeFilePath); err != nil {
		t.Fatalf("active file should not be deleted: %v", err)
	}
}

// TestCleanupOldLogs_NeverDeletesActiveFile verifies that CleanupOldLogs
// never deletes the active log file, even if it's the oldest.
func TestCleanupOldLogs_NeverDeletesActiveFile(t *testing.T) {
	tmpDir := t.TempDir()
	activeFilePath := filepath.Join(tmpDir, "sfpg-2025-01-01_00-00-00.log") // Oldest file

	// Create active file (oldest)
	activeFile, err := os.Create(activeFilePath)
	if err != nil {
		t.Fatalf("failed to create active file: %v", err)
	}
	activeFile.Close()
	oldTime := time.Now().Add(-24 * time.Hour)
	os.Chtimes(activeFilePath, oldTime, oldTime)

	// Create newer log files
	for i := 2; i <= 10; i++ {
		newFile := filepath.Join(tmpDir, fmt.Sprintf("sfpg-2025-01-%02d_00-00-00.log", i))
		f, fileErr := os.Create(newFile)
		if fileErr != nil {
			t.Fatalf("failed to create log file: %v", fileErr)
		}
		f.Close()
	}

	retentionCount := 7

	// Run cleanup
	err = CleanupOldLogs(tmpDir, activeFilePath, retentionCount)
	if err != nil {
		t.Fatalf("CleanupOldLogs should not fail: %v", err)
	}

	// Verify active file still exists
	if _, err := os.Stat(activeFilePath); err != nil {
		t.Fatalf("active file should not be deleted even if it's the oldest: %v", err)
	}
}

// TestCleanupOldLogs_NoFiles verifies that CleanupOldLogs handles the case
// when there are no log files gracefully.
func TestCleanupOldLogs_NoFiles(t *testing.T) {
	tmpDir := t.TempDir()
	activeFilePath := filepath.Join(tmpDir, "sfpg-2025-01-01_00-00-00.log")

	// Create active file only
	activeFile, err := os.Create(activeFilePath)
	if err != nil {
		t.Fatalf("failed to create active file: %v", err)
	}
	activeFile.Close()

	retentionCount := 7

	// Run cleanup (should not fail)
	err = CleanupOldLogs(tmpDir, activeFilePath, retentionCount)
	if err != nil {
		t.Fatalf("CleanupOldLogs should not fail when no old files exist: %v", err)
	}

	// Verify active file still exists
	if _, err := os.Stat(activeFilePath); err != nil {
		t.Fatalf("active file should still exist: %v", err)
	}
}

// TestCleanupOldLogs_FewerFilesThanRetentionCount verifies that CleanupOldLogs
// handles the case when there are fewer files than the retention count.
func TestCleanupOldLogs_FewerFilesThanRetentionCount(t *testing.T) {
	tmpDir := t.TempDir()
	activeFilePath := filepath.Join(tmpDir, "sfpg-2025-01-05_00-00-00.log")

	// Create active file
	activeFile, err := os.Create(activeFilePath)
	if err != nil {
		t.Fatalf("failed to create active file: %v", err)
	}
	activeFile.Close()

	// Create only 3 old log files (less than retention count of 7)
	for i := 1; i <= 3; i++ {
		oldFile := filepath.Join(tmpDir, fmt.Sprintf("sfpg-2025-01-%02d_00-00-00.log", i))
		f, fileErr := os.Create(oldFile)
		if fileErr != nil {
			t.Fatalf("failed to create old log file: %v", fileErr)
		}
		f.Close()
	}

	retentionCount := 7

	// Run cleanup (should not delete any files)
	if cleanErr := CleanupOldLogs(tmpDir, activeFilePath, retentionCount); cleanErr != nil {
		t.Fatalf("CleanupOldLogs should not fail: %v", cleanErr)
	}

	// Verify all files still exist (3 old + 1 active = 4)
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read directory: %v", err)
	}

	logFileCount := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".log" {
			logFileCount++
		}
	}

	expectedCount := 4
	if logFileCount != expectedCount {
		t.Fatalf("expected %d log files (all should be kept), found %d", expectedCount, logFileCount)
	}
}

// TestCleanupOldLogs_SortingByModificationTime verifies that CleanupOldLogs
// sorts files by modification time (newest first) and keeps the most recent ones.
func TestCleanupOldLogs_SortingByModificationTime(t *testing.T) {
	tmpDir := t.TempDir()
	activeFilePath := filepath.Join(tmpDir, "sfpg-2025-01-10_00-00-00.log")

	// Create active file
	activeFile, err := os.Create(activeFilePath)
	if err != nil {
		t.Fatalf("failed to create active file: %v", err)
	}
	activeFile.Close()

	// Create old log files with different modification times
	// File 1: oldest (should be deleted)
	file1 := filepath.Join(tmpDir, "sfpg-2025-01-01_00-00-00.log")
	f1, _ := os.Create(file1)
	f1.Close()
	os.Chtimes(file1, time.Now().Add(-10*time.Hour), time.Now().Add(-10*time.Hour))

	// File 2: newer (should be kept)
	file2 := filepath.Join(tmpDir, "sfpg-2025-01-02_00-00-00.log")
	f2, _ := os.Create(file2)
	f2.Close()
	os.Chtimes(file2, time.Now().Add(-5*time.Hour), time.Now().Add(-5*time.Hour))

	// File 3: newest (should be kept)
	file3 := filepath.Join(tmpDir, "sfpg-2025-01-03_00-00-00.log")
	f3, _ := os.Create(file3)
	f3.Close()
	os.Chtimes(file3, time.Now().Add(-1*time.Hour), time.Now().Add(-1*time.Hour))

	retentionCount := 2

	// Run cleanup
	err = CleanupOldLogs(tmpDir, activeFilePath, retentionCount)
	if err != nil {
		t.Fatalf("CleanupOldLogs should not fail: %v", err)
	}

	// Verify file1 (oldest) was deleted
	if _, err := os.Stat(file1); err == nil {
		t.Fatal("oldest file should have been deleted")
	}

	// Verify file2 and file3 (newest) were kept
	if _, err := os.Stat(file2); err != nil {
		t.Fatal("newer file should have been kept")
	}
	if _, err := os.Stat(file3); err != nil {
		t.Fatal("newest file should have been kept")
	}
}

// TestRetentionTask_Run_ExecutesRetentionCleanup verifies that RetentionTask.Run()
// executes retention cleanup correctly.
func TestRetentionTask_Run_ExecutesRetentionCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer logger.Shutdown()

	// Create multiple old log files
	logsDir := filepath.Join(tmpDir, "logs")
	for i := 1; i <= 10; i++ {
		oldFile := filepath.Join(logsDir, fmt.Sprintf("sfpg-2025-01-%02d_00-00-00.log", i))
		f, fileErr := os.Create(oldFile)
		if fileErr != nil {
			t.Fatalf("failed to create test log file: %v", fileErr)
		}
		f.Close()
		os.Chtimes(oldFile, time.Now().Add(-time.Duration(i)*time.Hour), time.Now().Add(-time.Duration(i)*time.Hour))
	}

	logRetentionCount := 7

	// Create retention task
	task := &RetentionTask{
		logger:            logger,
		logRetentionCount: logRetentionCount,
	}

	// Execute retention cleanup
	err = task.Run(context.Background())
	if err != nil {
		t.Fatalf("RetentionTask.Run should not fail: %v", err)
	}

	// Verify retention cleanup ran (should keep only 7 most recent + current)
	logger.mu.RLock()
	activeFilePath := logger.filePath
	logger.mu.RUnlock()

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

	// Verify active file still exists
	if _, err := os.Stat(activeFilePath); err != nil {
		t.Fatalf("active file should not be deleted: %v", err)
	}
}
