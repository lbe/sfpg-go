package log

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/scheduler"
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
	defer func() {
		if sErr := logger.Shutdown(); sErr != nil {
			t.Fatal(sErr)
		}
	}()

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

// mockDirEntry is a test double for os.DirEntry.
type mockDirEntry struct {
	name    string
	isDir   bool
	infoErr error
}

func (m mockDirEntry) Name() string               { return m.name }
func (m mockDirEntry) IsDir() bool                { return m.isDir }
func (m mockDirEntry) Type() os.FileMode          { return 0 }
func (m mockDirEntry) Info() (os.FileInfo, error) { return &mockFileInfo{name: m.name}, m.infoErr }

// mockFileInfo is a test double for os.FileInfo.
type mockFileInfo struct{ name string }

func (m *mockFileInfo) Name() string       { return m.name }
func (m *mockFileInfo) Size() int64        { return 0 }
func (m *mockFileInfo) Mode() os.FileMode  { return 0 }
func (m *mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m *mockFileInfo) IsDir() bool        { return false }
func (m *mockFileInfo) Sys() any           { return nil }

// TestFindLogFiles_MissingPaths exercises error and skip paths in findLogFiles.
func TestFindLogFiles_MissingPaths(t *testing.T) {
	originalReadDir := osReadDir
	t.Cleanup(func() { osReadDir = originalReadDir })

	tests := []struct {
		name       string
		entries    []os.DirEntry
		readDirErr error
		wantErr    bool
		wantNames  []string
	}{
		{
			name:       "read dir error",
			readDirErr: errors.New("read dir denied"),
			wantErr:    true,
		},
		{
			name: "directory entry skipped",
			entries: []os.DirEntry{
				mockDirEntry{name: "sfpg-file.log", isDir: false},
				mockDirEntry{name: "subdirectory", isDir: true},
			},
			wantNames: []string{"sfpg-file.log"},
		},
		{
			name: "info error skipped",
			entries: []os.DirEntry{
				mockDirEntry{name: "sfpg-good.log", infoErr: nil},
				mockDirEntry{name: "sfpg-bad.log", infoErr: errors.New("info denied")},
			},
			wantNames: []string{"sfpg-good.log"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			osReadDir = func(string) ([]os.DirEntry, error) {
				return tt.entries, tt.readDirErr
			}

			files, err := findLogFiles("/unused")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(files) != len(tt.wantNames) {
				t.Fatalf("expected %d files, got %d", len(tt.wantNames), len(files))
			}
			for i, want := range tt.wantNames {
				if files[i].Name() != want {
					t.Fatalf("expected file %q at index %d, got %q", want, i, files[i].Name())
				}
			}
		})
	}
}

// TestScheduleRetentionCleanup_RemoveTaskError verifies that scheduleRetentionCleanup
// continues and schedules a new task even when removing the old task fails.
func TestScheduleRetentionCleanup_RemoveTaskError(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer func() {
		if sErr := logger.Shutdown(); sErr != nil {
			t.Fatal(sErr)
		}
	}()

	logger.mu.Lock()
	logger.retentionTaskID = "non-existent-task-id"
	logger.mu.Unlock()

	originalRemoveTask := schedulerRemoveTask
	schedulerRemoveTask = func(*scheduler.Scheduler, string) error {
		return errors.New("remove denied")
	}
	t.Cleanup(func() { schedulerRemoveTask = originalRemoveTask })

	taskID, err := scheduleRetentionCleanup(logger, 7, sched)
	if err != nil {
		t.Fatalf("scheduleRetentionCleanup should not fail: %v", err)
	}
	if taskID == "" {
		t.Fatal("expected non-empty task ID")
	}
}

// TestScheduleRetentionCleanup_RemoveTaskSuccess verifies that scheduleRetentionCleanup
// removes an existing task and schedules a new one with a different ID.
func TestScheduleRetentionCleanup_RemoveTaskSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer func() {
		if sErr := logger.Shutdown(); sErr != nil {
			t.Fatal(sErr)
		}
	}()

	firstID, err := scheduleRetentionCleanup(logger, 7, sched)
	if err != nil {
		t.Fatalf("scheduleRetentionCleanup should not fail: %v", err)
	}
	if firstID == "" {
		t.Fatal("expected non-empty first task ID")
	}

	logger.mu.Lock()
	logger.retentionTaskID = firstID
	logger.mu.Unlock()

	secondID, err := scheduleRetentionCleanup(logger, 7, sched)
	if err != nil {
		t.Fatalf("scheduleRetentionCleanup should not fail: %v", err)
	}
	if secondID == "" {
		t.Fatal("expected non-empty second task ID")
	}
	if secondID == firstID {
		t.Fatal("expected new task ID to differ from the old task ID")
	}
}

// TestScheduleRetentionCleanup_AddTaskError verifies that scheduleRetentionCleanup
// returns an error when adding the retention task fails.
func TestScheduleRetentionCleanup_AddTaskError(t *testing.T) {
	tmpDir := t.TempDir()
	sched := testScheduler(t)

	logger, err := NewBootstrapLogger(tmpDir, sched, "x.y.z")
	if err != nil {
		t.Fatalf("NewBootstrapLogger should not fail: %v", err)
	}
	defer func() {
		if sErr := logger.Shutdown(); sErr != nil {
			t.Fatal(sErr)
		}
	}()

	originalAddTask := schedulerAddTask
	schedulerAddTask = func(*scheduler.Scheduler, scheduler.Task, scheduler.ExecutionMode, time.Time) (string, error) {
		return "", errors.New("add denied")
	}
	t.Cleanup(func() { schedulerAddTask = originalAddTask })

	_, err = scheduleRetentionCleanup(logger, 7, sched)
	if err == nil {
		t.Fatal("expected error when AddTask fails, got nil")
	}
	if !strings.Contains(err.Error(), "failed to add retention task to scheduler") {
		t.Fatalf("expected error to wrap %q, got %v", "failed to add retention task to scheduler", err)
	}
}
