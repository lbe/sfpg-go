package log

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/phsym/console-slog"

	"github.com/lbe/sfpg-go/internal/multihandler"
	"github.com/lbe/sfpg-go/internal/scheduler"
)

// calculateNextRolloverTime calculates the initial startTime for scheduler.AddTask()
// when setting up or re-scheduling the rollover task.
// The scheduler handles all subsequent rollovers automatically based on the startTime.
func calculateNextRolloverTime(rollover string, now time.Time) time.Time {
	loc := now.Location()

	switch rollover {
	case "daily":
		// Next 00:00:00 (midnight of next day)
		return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)

	case "weekly":
		// Next Monday at 00:00:00
		// time.Weekday: 0=Sunday, 1=Monday, ..., 6=Saturday
		daysUntilMonday := (8 - int(now.Weekday())) % 7
		if daysUntilMonday == 0 {
			// If today is Monday, next Monday is 7 days away
			daysUntilMonday = 7
		}
		return time.Date(now.Year(), now.Month(), now.Day()+daysUntilMonday, 0, 0, 0, 0, loc)

	case "monthly":
		// First day of next month at 00:00:00
		return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, loc)

	default:
		// Default to daily if invalid
		return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)
	}
}

// RolloverTask implements scheduler.Task for executing log file rollover.
type RolloverTask struct {
	logger            *Logger
	logRollover       string
	logRetentionCount int
}

// Run executes the rollover: closes current log file, creates new file, updates logger state, and triggers retention cleanup.
func (t *RolloverTask) Run(ctx context.Context) error {
	t.logger.mu.Lock()
	defer t.logger.mu.Unlock()

	// Get current file info
	currentFile := t.logger.file
	currentFilePath := t.logger.filePath
	currentDir := t.logger.dir

	// Close current log file
	if currentFile != nil {
		if err := currentFile.Close(); err != nil {
			slog.Error("failed to close current log file during rollover", "err", err)
			// Continue anyway - try to create new file
		}
	}

	// Create new log file with current timestamp (include milliseconds for uniqueness)
	newLogFileName := fmt.Sprintf("sfpg-%s.log", time.Now().Format("2006-01-02_15-04-05.000"))
	newLogFilePath := filepath.Join(currentDir, newLogFileName)
	newLogFile, err := os.OpenFile(newLogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		return fmt.Errorf("failed to open new log file during rollover: %w", err)
	}

	// Get current log level
	currentLevel := t.logger.level

	// Create new slog handlers with same log level
	fileHandler := slog.NewJSONHandler(newLogFile, &slog.HandlerOptions{
		AddSource: true,
		Level:     currentLevel,
		ReplaceAttr: func( /*groups*/ _ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(time.Now().Format("2006-01-02 15:04:05"))
			}
			return a
		},
	})
	consoleHandler := console.NewHandler(os.Stderr, &console.HandlerOptions{
		Level:      currentLevel,
		AddSource:  true,
		TimeFormat: "2006-01-02 15:04:05.000000",
	})

	// Create new logger
	newLogger := slog.New(multihandler.NewMultiHandler(fileHandler, consoleHandler))

	// Update logger state atomically
	t.logger.file = newLogFile
	t.logger.filePath = newLogFilePath
	t.logger.fileName = newLogFileName
	t.logger.logger = newLogger

	slog.SetDefault(newLogger)

	// Trigger retention cleanup
	if err := CleanupOldLogs(currentDir, newLogFilePath, t.logRetentionCount); err != nil {
		slog.Error("retention cleanup failed during rollover", "err", err)
		// Don't fail rollover if retention cleanup fails
	}

	slog.Info("log file rolled over", "old_file", filepath.Base(currentFilePath), "new_file", newLogFileName)

	return nil
}

// scheduleRollover schedules the rollover task with the scheduler.
// If logger.rolloverTaskID is set, it removes the old task first.
// Returns the new task ID and any error.
func scheduleRollover(logger *Logger, logRollover string, logRetentionCount int, sched *scheduler.Scheduler) (string, error) {
	// Remove old task if it exists
	if logger.rolloverTaskID != "" {
		if err := sched.RemoveTask(logger.rolloverTaskID); err != nil {
			// Log warning but continue - add new task anyway
			slog.Warn("failed to remove old rollover task", "task_id", logger.rolloverTaskID, "err", err)
		}
	}

	// Calculate next rollover time
	now := time.Now()
	startTime := calculateNextRolloverTime(logRollover, now)

	// Determine scheduler mode based on rollover period
	var mode scheduler.ExecutionMode
	switch logRollover {
	case "daily":
		mode = scheduler.Daily
	case "weekly":
		mode = scheduler.Weekly
	case "monthly":
		mode = scheduler.Monthly
	default:
		// Default to daily
		mode = scheduler.Daily
	}

	// Create and add rollover task
	task := &RolloverTask{
		logger:            logger,
		logRollover:       logRollover,
		logRetentionCount: logRetentionCount,
	}

	taskID, err := sched.AddTask(task, mode, startTime)
	if err != nil {
		return "", fmt.Errorf("failed to add rollover task to scheduler: %w", err)
	}

	return taskID, nil
}
