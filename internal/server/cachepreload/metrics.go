package cachepreload

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// pathMetrics holds per-path statistics.
type pathMetrics struct {
	count    int64
	failures int64
	totalMs  int64
}

// PreloadMetrics collects observability data for cache preloading operations.
type PreloadMetrics struct {
	TasksScheduled atomic.Int64
	TasksCompleted atomic.Int64
	TasksFailed    atomic.Int64
	TasksCancelled atomic.Int64
	TasksSkipped   atomic.Int64 // Due to deduplication or existing cache
	TotalDuration  atomic.Int64 // Nanoseconds

	mu        sync.RWMutex
	pathStats map[string]*pathMetrics
}

// RecordSuccess records a successful preload.
func (m *PreloadMetrics) RecordSuccess(path string, duration time.Duration) {
	m.TasksCompleted.Add(1)
	m.TotalDuration.Add(duration.Nanoseconds())
	m.mu.Lock()
	if m.pathStats == nil {
		m.pathStats = make(map[string]*pathMetrics)
	}
	if m.pathStats[path] == nil {
		m.pathStats[path] = &pathMetrics{}
	}
	m.pathStats[path].count++
	m.pathStats[path].totalMs += duration.Milliseconds()
	m.mu.Unlock()
}

// RecordFailure records a failed preload.
func (m *PreloadMetrics) RecordFailure(path string, err error, duration time.Duration) {
	m.TasksFailed.Add(1)
	m.TotalDuration.Add(duration.Nanoseconds())
	m.mu.Lock()
	if m.pathStats == nil {
		m.pathStats = make(map[string]*pathMetrics)
	}
	if m.pathStats[path] == nil {
		m.pathStats[path] = &pathMetrics{}
	}
	m.pathStats[path].failures++
	m.pathStats[path].totalMs += duration.Milliseconds()
	m.mu.Unlock()
	slog.Warn("preload task failed", "path", path, "error", err, "duration", duration)
}

// RecordSkipped records a skipped task (deduplicated or cached).
func (m *PreloadMetrics) RecordSkipped(reason string) {
	m.TasksSkipped.Add(1)
}

// RecordCancelled records a cancelled task.
func (m *PreloadMetrics) RecordCancelled() {
	m.TasksCancelled.Add(1)
}

// Summary returns a string summary for logging.
func (m *PreloadMetrics) Summary() string {
	sched := m.TasksScheduled.Load()
	done := m.TasksCompleted.Load()
	failed := m.TasksFailed.Load()
	skipped := m.TasksSkipped.Load()
	cancelled := m.TasksCancelled.Load()
	durNs := m.TotalDuration.Load()
	return slog.Any("summary", map[string]any{
		"scheduled":   sched,
		"completed":   done,
		"failed":      failed,
		"skipped":     skipped,
		"cancelled":   cancelled,
		"duration_ms": durNs / 1e6,
	}).String()
}
