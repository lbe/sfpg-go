package log

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/lbe/sfpg-go/internal/scheduler"
)

// CleanupOldLogs removes log files exceeding the retention count.
// It never deletes the active log file.
func CleanupOldLogs(logDir string, activeFilePath string, retentionCount int) error {
	// Find all log files
	logFiles, err := findLogFiles(logDir)
	if err != nil {
		return fmt.Errorf("failed to find log files: %w", err)
	}

	// Filter out active file and sort by modification time (newest first)
	var filesToConsider []os.FileInfo
	for _, file := range logFiles {
		filePath := filepath.Join(logDir, file.Name())
		if filePath != activeFilePath {
			filesToConsider = append(filesToConsider, file)
		}
	}

	// Sort by modification time (newest first)
	sort.Slice(filesToConsider, func(i, j int) bool {
		return filesToConsider[i].ModTime().After(filesToConsider[j].ModTime())
	})

	// Keep only retentionCount most recent files, delete the rest
	if len(filesToConsider) > retentionCount {
		filesToDelete := filesToConsider[retentionCount:]
		for _, file := range filesToDelete {
			filePath := filepath.Join(logDir, file.Name())
			if err := os.Remove(filePath); err != nil {
				slog.Warn("failed to delete old log file", "file", filePath, "err", err)
				// Continue deleting other files even if one fails
			} else {
				slog.Info("deleted old log file", "file", filePath)
			}
		}
	}

	return nil
}

// findLogFiles finds all log files matching the pattern sfpg-*.log in the given directory.
func findLogFiles(logDir string) ([]os.FileInfo, error) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return nil, err
	}

	var logFiles []os.FileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Check if file matches pattern sfpg-*.log
		name := entry.Name()
		if len(name) >= 5 && name[:5] == "sfpg-" && filepath.Ext(name) == ".log" {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			logFiles = append(logFiles, info)
		}
	}

	return logFiles, nil
}

// RetentionTask implements scheduler.Task for executing retention cleanup.
type RetentionTask struct {
	logger            *Logger
	logRetentionCount int
}

// Run executes the retention cleanup.
func (t *RetentionTask) Run(ctx context.Context) error {
	t.logger.mu.RLock()
	logDir := t.logger.dir
	activeFilePath := t.logger.filePath
	t.logger.mu.RUnlock()

	return CleanupOldLogs(logDir, activeFilePath, t.logRetentionCount)
}

// scheduleRetentionCleanup schedules the retention cleanup task with the scheduler.
// If logger.retentionTaskID is set, it removes the old task first.
// Returns the new task ID and any error.
func scheduleRetentionCleanup(logger *Logger, logRetentionCount int, sched *scheduler.Scheduler) (string, error) {
	// Remove old task if it exists
	if logger.retentionTaskID != "" {
		if err := sched.RemoveTask(logger.retentionTaskID); err != nil {
			// Log warning but continue - add new task anyway
			slog.Warn("failed to remove old retention task", "task_id", logger.retentionTaskID, "err", err)
		}
	}

	// Calculate next midnight (00:00:00) for daily execution
	now := time.Now()
	startTime := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())

	// Create and add retention task (runs daily)
	task := &RetentionTask{
		logger:            logger,
		logRetentionCount: logRetentionCount,
	}

	taskID, err := sched.AddTask(task, scheduler.Daily, startTime)
	if err != nil {
		return "", fmt.Errorf("failed to add retention task to scheduler: %w", err)
	}

	return taskID, nil
}
