package cachepreload

import (
	"strings"
	"testing"
	"time"
)

// TestPreloadMetrics_Summary verifies Summary output format.
func TestPreloadMetrics_Summary(t *testing.T) {
	t.Run("returns summary with all zeros when empty", func(t *testing.T) {
		m := &PreloadMetrics{}
		summary := m.Summary()

		if summary == "" {
			t.Error("expected non-empty summary string")
		}

		// Verify it contains expected keys
		expectedKeys := []string{"scheduled", "completed", "failed", "skipped", "cancelled", "duration_ms"}
		for _, key := range expectedKeys {
			if !strings.Contains(summary, key) {
				t.Errorf("expected summary to contain key %q", key)
			}
		}
	})

	t.Run("returns summary with recorded values", func(t *testing.T) {
		m := &PreloadMetrics{}
		m.TasksScheduled.Store(10)
		m.TasksCompleted.Store(8)
		m.TasksFailed.Store(1)
		m.TasksCancelled.Store(1)
		m.TasksSkipped.Store(2)
		m.TotalDuration.Store(5_000_000) // 5ms

		summary := m.Summary()

		if summary == "" {
			t.Error("expected non-empty summary string")
		}

		// Verify values are present (as strings)
		if !strings.Contains(summary, "10") {
			t.Error("expected summary to contain scheduled count")
		}
		if !strings.Contains(summary, "8") {
			t.Error("expected summary to contain completed count")
		}
		if !strings.Contains(summary, "duration_ms") {
			t.Error("expected summary to contain duration_ms key")
		}
	})

	t.Run("summary format is valid slog string", func(t *testing.T) {
		m := &PreloadMetrics{}
		m.TasksScheduled.Store(5)
		summary := m.Summary()

		// The summary should be a valid slog.Any string output
		// which looks like: "summary=map[scheduled:5 ...]"
		if !strings.Contains(summary, "summary=") || !strings.Contains(summary, "map[") {
			t.Errorf("summary format looks incorrect: %s", summary)
		}
	})
}

// TestPreloadMetrics_Summary_ConcurrentAccess verifies thread safety.
func TestPreloadMetrics_Summary_ConcurrentAccess(t *testing.T) {
	m := &PreloadMetrics{}

	done := make(chan struct{})
	// Concurrent writes
	for i := 0; i < 5; i++ {
		go func(n int) {
			for j := 0; j < 50; j++ {
				m.RecordSuccess("/test/path", time.Duration(n)*time.Millisecond)
			}
			done <- struct{}{}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				_ = m.Summary()
			}
			done <- struct{}{}
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify final state is consistent
	summary := m.Summary()
	if summary == "" {
		t.Error("expected non-empty summary after concurrent access")
	}
}
