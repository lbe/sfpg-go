package cachepreload

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/server/interfaces"
)

// TestPreloadManager_Configure verifies Configure behavior.
func TestPreloadManager_Configure(t *testing.T) {
	t.Run("configures all dependencies", func(t *testing.T) {
		pm := NewPreloadManager([]string{"/gallery/"}, false)
		defer pm.Shutdown()

		taskTracker := &TaskTracker{}
		sessionTracker := &SessionTracker{}
		var dbRoPool *dbconnpool.DbSQLConnPool
		getQueries := func(*dbconnpool.CpConn) interfaces.HandlerQueries { return nil }
		getHandler := func() http.Handler { return nil }
		getETag := func() string { return "test-etag" }
		metrics := &PreloadMetrics{}

		cfg := PreloadConfig{
			TaskTracker:    taskTracker,
			SessionTracker: sessionTracker,
			DBRoPool:       dbRoPool,
			GetQueries:     getQueries,
			GetHandler:     getHandler,
			GetETagVersion: getETag,
			Metrics:        metrics,
		}

		pm.Configure(cfg)

		// Verify the scheduler still exists (was not replaced)
		if pm.GetScheduler() != nil {
			t.Error("expected scheduler to be nil when initially disabled")
		}
	})

	t.Run("creates default trackers when nil", func(t *testing.T) {
		pm := NewPreloadManager([]string{"/gallery/"}, false)
		defer pm.Shutdown()

		cfg := PreloadConfig{
			TaskTracker:    nil,
			SessionTracker: nil,
		}

		pm.Configure(cfg)

		// Should not panic and defaults should be created
		// We can't directly access the private fields, but we can verify no panic occurred
	})
}

// TestPreloadManager_GetMetrics verifies GetMetrics behavior.
func TestPreloadManager_GetMetrics(t *testing.T) {
	t.Run("returns zero snapshot when not configured", func(t *testing.T) {
		pm := NewPreloadManager([]string{"/gallery/"}, false)
		defer pm.Shutdown()

		snapshot := pm.GetMetrics()

		if snapshot.TasksScheduled != 0 {
			t.Errorf("expected TasksScheduled 0, got %d", snapshot.TasksScheduled)
		}
		if snapshot.TasksCompleted != 0 {
			t.Errorf("expected TasksCompleted 0, got %d", snapshot.TasksCompleted)
		}
		if snapshot.TasksFailed != 0 {
			t.Errorf("expected TasksFailed 0, got %d", snapshot.TasksFailed)
		}
		if snapshot.TasksCancelled != 0 {
			t.Errorf("expected TasksCancelled 0, got %d", snapshot.TasksCancelled)
		}
		if snapshot.TasksSkipped != 0 {
			t.Errorf("expected TasksSkipped 0, got %d", snapshot.TasksSkipped)
		}
		if snapshot.TotalDuration != 0 {
			t.Errorf("expected TotalDuration 0, got %v", snapshot.TotalDuration)
		}
	})

	t.Run("returns current metrics when configured", func(t *testing.T) {
		pm := NewPreloadManager([]string{"/gallery/"}, false)
		defer pm.Shutdown()

		metrics := &PreloadMetrics{}
		metrics.TasksScheduled.Store(5)
		metrics.TasksCompleted.Store(3)
		metrics.TasksFailed.Store(1)
		metrics.TasksCancelled.Store(1)
		metrics.TasksSkipped.Store(2)
		metrics.TotalDuration.Store(1000000) // 1ms in nanoseconds

		cfg := PreloadConfig{Metrics: metrics}
		pm.Configure(cfg)

		snapshot := pm.GetMetrics()

		if snapshot.TasksScheduled != 5 {
			t.Errorf("expected TasksScheduled 5, got %d", snapshot.TasksScheduled)
		}
		if snapshot.TasksCompleted != 3 {
			t.Errorf("expected TasksCompleted 3, got %d", snapshot.TasksCompleted)
		}
		if snapshot.TasksFailed != 1 {
			t.Errorf("expected TasksFailed 1, got %d", snapshot.TasksFailed)
		}
		if snapshot.TasksCancelled != 1 {
			t.Errorf("expected TasksCancelled 1, got %d", snapshot.TasksCancelled)
		}
		if snapshot.TasksSkipped != 2 {
			t.Errorf("expected TasksSkipped 2, got %d", snapshot.TasksSkipped)
		}
		if snapshot.TotalDuration != time.Millisecond {
			t.Errorf("expected TotalDuration 1ms, got %v", snapshot.TotalDuration)
		}
	})

	t.Run("concurrent GetMetrics is safe", func(t *testing.T) {
		pm := NewPreloadManager([]string{"/gallery/"}, false)
		defer pm.Shutdown()

		metrics := &PreloadMetrics{}
		cfg := PreloadConfig{Metrics: metrics}
		pm.Configure(cfg)

		done := make(chan struct{})
		for i := 0; i < 10; i++ {
			go func() {
				for j := 0; j < 100; j++ {
					_ = pm.GetMetrics()
				}
				done <- struct{}{}
			}()
		}

		for i := 0; i < 10; i++ {
			<-done
		}
	})
}

// TestPreloadManager_ConfigureAndSchedule verifies Configure enables ScheduleFolderPreload.
func TestPreloadManager_ConfigureAndSchedule(t *testing.T) {
	t.Run("ScheduleFolderPreload with full config", func(t *testing.T) {
		pm := NewPreloadManager([]string{"/gallery/"}, true)
		defer pm.Shutdown()
		pm.waitForSchedulerStart()

		// Configure with minimal dependencies
		taskTracker := &TaskTracker{}
		sessionTracker := &SessionTracker{}
		metrics := &PreloadMetrics{}

		cfg := PreloadConfig{
			TaskTracker:    taskTracker,
			SessionTracker: sessionTracker,
			Metrics:        metrics,
			// Other deps nil - should log debug and return
		}
		pm.Configure(cfg)

		// Should not panic when called with incomplete config
		pm.ScheduleFolderPreload(context.Background(), 1, "session-1", "gzip")
	})
}
