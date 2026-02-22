// Package log provides structured logging functionality with file and console output.
// It supports bootstrap logging during initialization, configuration-based reloading,
// log file rollover (daily, weekly, monthly), and log retention management.
// All logging operations are thread-safe.
package log

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/phsym/console-slog"

	"go.local/sfpg/internal/multihandler"
	"go.local/sfpg/internal/scheduler"
)

const (
	// Bootstrap logging constants - used during early initialization
	// before configuration is loaded from database/YAML
	bootstrapLogDir   = "logs"
	bootstrapLogLevel = slog.LevelDebug
)

// Logger encapsulates all logging state and functionality.
// It manages log file creation, handler setup, and scheduler task management.
// All operations are thread-safe.
type Logger struct {
	// Logging state
	file     *os.File
	filePath string
	fileName string
	dir      string
	level    slog.Level
	logger   *slog.Logger
	rootDir  string // Root directory for resolving relative paths

	// Mutex protects all fields below
	mu              sync.RWMutex
	currentLogLevel slog.Level // Cache of current log level for change detection
	currentLogDir   string     // Cache of current log directory for change detection
	rolloverTaskID  string     // Task ID for rollover scheduler task
	retentionTaskID string     // Task ID for retention scheduler task
}

// NewBootstrapLogger creates a new logger for the bootstrap phase.
// It creates the logs directory and log file, but does NOT schedule rollover tasks.
// Rollover is only scheduled after config is loaded in ReloadFromConfig().
func NewBootstrapLogger(rootDir string, sched *scheduler.Scheduler, version string) (*Logger, error) {
	// Create the logs directory if it doesn't exist
	logsDir := filepath.Clean(filepath.Join(rootDir, bootstrapLogDir))
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		if err := os.Mkdir(logsDir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create logs directory: %w", err)
		}
	}

	// Create the log file with timestamp
	logFileName := fmt.Sprintf("sfpg-%s.log", time.Now().Format("2006-01-02_15-04-05"))
	logFilePath := filepath.Join(logsDir, logFileName)
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	// Create the slog handler with bootstrap log level
	level := bootstrapLogLevel
	fileHandler := slog.NewJSONHandler(logFile, &slog.HandlerOptions{
		AddSource: true,
		Level:     level,
		ReplaceAttr: func( /*groups*/ _ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(time.Now().Format("2006-01-02 15:04:05"))
			}
			return a
		},
	})
	consoleHandler := console.NewHandler(os.Stderr, &console.HandlerOptions{
		Level:      level,
		AddSource:  true,
		TimeFormat: "2006-01-02 15:04:05.000000",
	})

	logger := slog.New(multihandler.NewMultiHandler(fileHandler, consoleHandler))
	slog.SetDefault(logger)

	slog.Info("Application starting", "version", version)

	return &Logger{
		file:            logFile,
		filePath:        logFilePath,
		fileName:        logFileName,
		dir:             logsDir,
		level:           level,
		logger:          logger,
		rootDir:         rootDir,
		currentLogLevel: level,
		currentLogDir:   logsDir,
		// rolloverTaskID and retentionTaskID remain empty - scheduled in ReloadFromConfig()
	}, nil
}

// ReloadFromConfig reinitializes logging based on the provided config values.
// If the configured LogDirectory matches the bootstrap directory, it only updates the log level if needed.
// If the configured LogDirectory differs, it closes the old log file and opens a new one in the
// configured directory with the same filename (for alignment purposes).
// It also schedules rollover and retention tasks if not already scheduled.
// Returns error if the new directory cannot be created, the file cannot be opened, or the log level is invalid.
// sched may be nil if scheduler is not yet initialized - in that case, tasks are not scheduled.
func (l *Logger) ReloadFromConfig(logDirectory, logLevel, logRollover string, logRetentionCount int, sched *scheduler.Scheduler) error {
	configLogDir := logDirectory
	configLogLevel := logLevel

	// Resolve absolute path for configured directory
	l.mu.RLock()
	rootDir := l.rootDir
	l.mu.RUnlock()

	var absConfigLogDir string
	if filepath.IsAbs(configLogDir) {
		absConfigLogDir = configLogDir
	} else {
		absConfigLogDir = filepath.Join(rootDir, configLogDir)
	}
	absConfigLogDir = filepath.Clean(absConfigLogDir)

	// Resolve absolute path for bootstrap directory (protected read)
	l.mu.RLock()
	absBootstrapLogDir := filepath.Clean(l.dir)
	currentLogLevel := l.level
	currentLogFile := l.file
	l.mu.RUnlock()

	// Parse log level from config early
	var newLogLevel slog.Level
	switch configLogLevel {
	case "debug":
		newLogLevel = slog.LevelDebug
	case "info":
		newLogLevel = slog.LevelInfo
	case "warn":
		newLogLevel = slog.LevelWarn
	case "error":
		newLogLevel = slog.LevelError
	default:
		return fmt.Errorf("invalid log level: %s (must be debug, info, warn, or error)", configLogLevel)
	}

	// Acquire lock for task scheduling and state updates
	l.mu.Lock()
	defer l.mu.Unlock()

	// Phase 2.1: If same directory, check if we need to update log level
	if absConfigLogDir == absBootstrapLogDir {
		// If log level changed, update handlers
		if newLogLevel != currentLogLevel {
			// Create new slog handlers with new log level
			fileHandler := slog.NewJSONHandler(currentLogFile, &slog.HandlerOptions{
				AddSource: true,
				Level:     newLogLevel,
				ReplaceAttr: func( /*groups*/ _ []string, a slog.Attr) slog.Attr {
					if a.Key == slog.TimeKey {
						a.Value = slog.StringValue(time.Now().Format("2006-01-02 15:04:05"))
					}
					return a
				},
			})
			consoleHandler := console.NewHandler(os.Stderr, &console.HandlerOptions{
				Level:      newLogLevel,
				AddSource:  true,
				TimeFormat: "2006-01-02 15:04:05.000000",
			})

			// Create new logger
			newLogger := slog.New(multihandler.NewMultiHandler(fileHandler, consoleHandler))

			// Update state
			l.level = newLogLevel
			l.logger = newLogger
			l.currentLogLevel = newLogLevel

			slog.SetDefault(newLogger)
		}

		// Schedule rollover and retention tasks if not already scheduled and scheduler is available
		if sched != nil {
			if l.rolloverTaskID == "" {
				taskID, err := scheduleRollover(l, logRollover, logRetentionCount, sched)
				if err != nil {
					return fmt.Errorf("failed to schedule rollover: %w", err)
				}
				l.rolloverTaskID = taskID
			}

			if l.retentionTaskID == "" {
				taskID, err := scheduleRetentionCleanup(l, logRetentionCount, sched)
				if err != nil {
					return fmt.Errorf("failed to schedule retention cleanup: %w", err)
				}
				l.retentionTaskID = taskID
			}
		}

		slog.Debug("logging directory matches bootstrap, keeping current setup", "directory", absConfigLogDir)
		return nil
	}

	// Phase 2.2: Different directory - reinitialize with aligned filename

	// Create the new logs directory if it doesn't exist
	if _, err := os.Stat(absConfigLogDir); os.IsNotExist(err) {
		if err := os.MkdirAll(absConfigLogDir, 0o755); err != nil {
			slog.Error("failed to create configured logs directory", "path", absConfigLogDir, "err", err)
			return fmt.Errorf("failed to create logs directory: %w", err)
		}
	}

	// Create new log file with same name as bootstrap (aligned filename)
	bootstrapFileName := filepath.Base(l.filePath)
	newLogFilePath := filepath.Join(absConfigLogDir, bootstrapFileName)

	// Open new log file
	newLogFile, err := os.OpenFile(newLogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		slog.Error("failed to open new log file", "path", newLogFilePath, "err", err)
		return fmt.Errorf("failed to open new log file: %w", err)
	}

	// Create new slog handlers with new log level
	fileHandler := slog.NewJSONHandler(newLogFile, &slog.HandlerOptions{
		AddSource: true,
		Level:     newLogLevel,
		ReplaceAttr: func( /*groups*/ _ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(time.Now().Format("2006-01-02 15:04:05"))
			}
			return a
		},
	})
	consoleHandler := console.NewHandler(os.Stderr, &console.HandlerOptions{
		Level:      newLogLevel,
		AddSource:  true,
		TimeFormat: "2006-01-02 15:04:05.000000",
	})

	// Create new logger
	newLogger := slog.New(multihandler.NewMultiHandler(fileHandler, consoleHandler))

	// Update state atomically
	_ = l.file.Close()
	l.file = newLogFile
	l.filePath = newLogFilePath
	l.fileName = bootstrapFileName
	l.dir = absConfigLogDir
	l.level = newLogLevel
	l.logger = newLogger
	l.currentLogLevel = newLogLevel
	l.currentLogDir = absConfigLogDir

	slog.SetDefault(newLogger)

	// Schedule rollover and retention tasks if not already scheduled and scheduler is available
	if sched != nil {
		if l.rolloverTaskID == "" {
			taskID, err := scheduleRollover(l, logRollover, logRetentionCount, sched)
			if err != nil {
				return fmt.Errorf("failed to schedule rollover: %w", err)
			}
			l.rolloverTaskID = taskID
		}

		if l.retentionTaskID == "" {
			taskID, err := scheduleRetentionCleanup(l, logRetentionCount, sched)
			if err != nil {
				return fmt.Errorf("failed to schedule retention cleanup: %w", err)
			}
			l.retentionTaskID = taskID
		}
	}

	// Log transition
	slog.Info("logging configuration reloaded", "directory", absConfigLogDir, "level", configLogLevel)

	return nil
}

// Shutdown gracefully shuts down the logger by closing the log file.
// Note: Scheduler shutdown is handled by App.Shutdown(), not here.
func (l *Logger) Shutdown() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		if err := l.file.Close(); err != nil {
			return fmt.Errorf("failed to close log file: %w", err)
		}
	}

	return nil
}

// File returns the current log file handle.
// The returned file should not be closed by the caller as it is managed by Logger.
func (l *Logger) File() *os.File {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.file
}

// FilePath returns the absolute path to the current log file.
func (l *Logger) FilePath() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.filePath
}

// LogsDir returns the absolute path to the current logs directory.
func (l *Logger) LogsDir() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.dir
}

// LogLevel returns the current log level.
func (l *Logger) LogLevel() slog.Level {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.level
}

// SlogLogger returns the underlying slog.Logger instance.
// This can be used to access the structured logger directly if needed.
func (l *Logger) SlogLogger() *slog.Logger {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.logger
}

// scheduleRollover and scheduleRetentionCleanup are implemented in rollover.go and retention.go respectively.
