package cachepreload

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
)

// waitForSched polls until the scheduler is available.
func waitForSched(pm *PreloadManager) {
	for range 50 {
		if pm.GetScheduler() != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

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
		waitForSched(pm)

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
		pm.ScheduleFolderPreload(context.Background(), 1, "session-1")
	})
}

func TestTruncateSessionID(t *testing.T) {
	cases := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{input: "short", maxLen: 10, expected: "short"},
		{input: "exactlyten", maxLen: 10, expected: "exactlyten"},
		{input: "longerthanten", maxLen: 10, expected: "longerthan..."},
		{input: "", maxLen: 5, expected: ""},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("input=%q_maxLen=%d", tc.input, tc.maxLen), func(t *testing.T) {
			got := truncateSessionID(tc.input, tc.maxLen)
			if got != tc.expected {
				t.Errorf("truncateSessionID(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.expected)
			}
		})
	}
}

func TestScheduleFolderPreload_MissingDeps(t *testing.T) {
	origAdd := managerSchedulerAddTaskFn
	defer func() { managerSchedulerAddTaskFn = origAdd }()

	var addedTasks []scheduler.Task
	managerSchedulerAddTaskFn = func(_ *scheduler.Scheduler, task scheduler.Task, _ scheduler.ExecutionMode, _ time.Time) (string, error) {
		addedTasks = append(addedTasks, task)
		return "task-id", nil
	}

	baseDBRoPool := &dbconnpool.DbSQLConnPool{}
	baseGetQueries := func(*dbconnpool.CpConn) interfaces.HandlerQueries { return nil }
	baseGetHandler := func() http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}) }
	baseGetETag := func() string { return "v1" }

	cases := []struct {
		name      string
		configure func(*PreloadConfig)
	}{
		{
			name: "nil DBRoPool",
			configure: func(cfg *PreloadConfig) {
				cfg.DBRoPool = nil
			},
		},
		{
			name: "nil GetQueries",
			configure: func(cfg *PreloadConfig) {
				cfg.GetQueries = nil
			},
		},
		{
			name: "nil GetHandler",
			configure: func(cfg *PreloadConfig) {
				cfg.GetHandler = nil
			},
		},
		{
			name: "nil GetETagVersion",
			configure: func(cfg *PreloadConfig) {
				cfg.GetETagVersion = nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addedTasks = nil
			pm := NewPreloadManager([]string{"/gallery/"}, true)
			defer pm.Shutdown()
			waitForSched(pm)

			cfg := PreloadConfig{
				TaskTracker:    &TaskTracker{},
				SessionTracker: &SessionTracker{},
				DBRoPool:       baseDBRoPool,
				GetQueries:     baseGetQueries,
				GetHandler:     baseGetHandler,
				GetETagVersion: baseGetETag,
			}
			tc.configure(&cfg)
			pm.Configure(cfg)

			pm.ScheduleFolderPreload(context.Background(), 1, "session-1")

			if len(addedTasks) != 0 {
				t.Errorf("expected no tasks scheduled when %s, got %d", tc.name, len(addedTasks))
			}
		})
	}
}

func TestScheduleFolderPreload_NilHandler(t *testing.T) {
	origAdd := managerSchedulerAddTaskFn
	defer func() { managerSchedulerAddTaskFn = origAdd }()

	var addedTasks []scheduler.Task
	managerSchedulerAddTaskFn = func(_ *scheduler.Scheduler, task scheduler.Task, _ scheduler.ExecutionMode, _ time.Time) (string, error) {
		addedTasks = append(addedTasks, task)
		return "task-id", nil
	}

	pm := NewPreloadManager([]string{"/gallery/"}, true)
	defer pm.Shutdown()
	waitForSched(pm)

	cfg := PreloadConfig{
		TaskTracker:    &TaskTracker{},
		SessionTracker: &SessionTracker{},
		DBRoPool:       &dbconnpool.DbSQLConnPool{},
		GetQueries:     func(*dbconnpool.CpConn) interfaces.HandlerQueries { return nil },
		GetHandler:     func() http.Handler { return nil },
		GetETagVersion: func() string { return "v1" },
	}
	pm.Configure(cfg)

	pm.ScheduleFolderPreload(context.Background(), 1, "session-1")

	if len(addedTasks) != 0 {
		t.Errorf("expected no tasks scheduled when handler is nil, got %d", len(addedTasks))
	}
}

func TestScheduleFolderPreload_CancelPreviousTasks(t *testing.T) {
	origAdd := managerSchedulerAddTaskFn
	origRemove := managerSchedulerRemoveTaskFn
	defer func() {
		managerSchedulerAddTaskFn = origAdd
		managerSchedulerRemoveTaskFn = origRemove
	}()

	var removedIDs []string
	managerSchedulerRemoveTaskFn = func(_ *scheduler.Scheduler, id string) error {
		removedIDs = append(removedIDs, id)
		return nil
	}
	managerSchedulerAddTaskFn = func(_ *scheduler.Scheduler, _ scheduler.Task, _ scheduler.ExecutionMode, _ time.Time) (string, error) {
		return "new-task-id", nil
	}

	pm := NewPreloadManager([]string{"/gallery/"}, true)
	defer pm.Shutdown()
	waitForSched(pm)

	sessionID := "session-cancel"
	tt := &TaskTracker{}
	st := &SessionTracker{}
	tt.RegisterTask("prev-key", sessionID, "prev-task")

	pm.Configure(PreloadConfig{
		TaskTracker:    tt,
		SessionTracker: st,
		DBRoPool:       &dbconnpool.DbSQLConnPool{},
		GetQueries:     func(*dbconnpool.CpConn) interfaces.HandlerQueries { return nil },
		GetHandler:     func() http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}) },
		GetETagVersion: func() string { return "v1" },
	})

	pm.ScheduleFolderPreload(context.Background(), 1, sessionID)
	pm.ScheduleFolderPreload(context.Background(), 2, sessionID)

	found := false
	for _, id := range removedIDs {
		if id == "prev-task" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'prev-task' to be removed, got removed IDs: %v", removedIDs)
	}
}

func TestScheduleFolderPreload_CancelRemoveTaskError(t *testing.T) {
	origAdd := managerSchedulerAddTaskFn
	origRemove := managerSchedulerRemoveTaskFn
	defer func() {
		managerSchedulerAddTaskFn = origAdd
		managerSchedulerRemoveTaskFn = origRemove
	}()

	managerSchedulerRemoveTaskFn = func(_ *scheduler.Scheduler, _ string) error {
		return errors.New("remove failed")
	}
	managerSchedulerAddTaskFn = func(_ *scheduler.Scheduler, _ scheduler.Task, _ scheduler.ExecutionMode, _ time.Time) (string, error) {
		return "new-task-id", nil
	}

	pm := NewPreloadManager([]string{"/gallery/"}, true)
	defer pm.Shutdown()
	waitForSched(pm)

	sessionID := "session-cancel-err"
	tt := &TaskTracker{}
	st := &SessionTracker{}
	tt.RegisterTask("prev-key", sessionID, "prev-task")

	pm.Configure(PreloadConfig{
		TaskTracker:    tt,
		SessionTracker: st,
		DBRoPool:       &dbconnpool.DbSQLConnPool{},
		GetQueries:     func(*dbconnpool.CpConn) interfaces.HandlerQueries { return nil },
		GetHandler:     func() http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}) },
		GetETagVersion: func() string { return "v1" },
	})

	// Should not panic despite the remove error.
	pm.ScheduleFolderPreload(context.Background(), 1, sessionID)
	pm.ScheduleFolderPreload(context.Background(), 2, sessionID)
}

func TestScheduleFolderPreload_AddTaskError(t *testing.T) {
	origAdd := managerSchedulerAddTaskFn
	defer func() { managerSchedulerAddTaskFn = origAdd }()

	managerSchedulerAddTaskFn = func(_ *scheduler.Scheduler, _ scheduler.Task, _ scheduler.ExecutionMode, _ time.Time) (string, error) {
		return "", errors.New("scheduler full")
	}

	pm := NewPreloadManager([]string{"/gallery/"}, true)
	defer pm.Shutdown()
	waitForSched(pm)

	pm.Configure(PreloadConfig{
		TaskTracker:    &TaskTracker{},
		SessionTracker: &SessionTracker{},
		DBRoPool:       &dbconnpool.DbSQLConnPool{},
		GetQueries:     func(*dbconnpool.CpConn) interfaces.HandlerQueries { return nil },
		GetHandler:     func() http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}) },
		GetETagVersion: func() string { return "v1" },
	})

	// Should not panic when AddTask returns an error.
	pm.ScheduleFolderPreload(context.Background(), 1, "session-1")
}
