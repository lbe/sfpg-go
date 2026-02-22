//go:build integration

package server

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"go.local/sfpg/internal/scheduler"
)

// testTask is a simple task implementation for testing
type testTask struct {
	id             string
	executionCount atomic.Int64
	executed       chan struct{}
}

func (t *testTask) Run(ctx context.Context) error {
	t.executionCount.Add(1)
	if t.executed != nil {
		select {
		case t.executed <- struct{}{}:
		default:
		}
	}
	return nil
}

func (t *testTask) ExecutionCount() int64 {
	return t.executionCount.Load()
}

// TestAppSchedulerIntegration verifies that the scheduler can be initialized and used with the App
func TestAppSchedulerIntegration(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Initialize scheduler manually (mimicking what Run() does)
	app.scheduler = scheduler.NewScheduler(0)

	// Verify scheduler is initialized with correct MaxConcurrentTasks
	expected := runtime.NumCPU()
	if app.scheduler.MaxConcurrentTasks != expected {
		t.Errorf("Expected MaxConcurrentTasks to be %d (NumCPU), got %d", expected, app.scheduler.MaxConcurrentTasks)
	}

	// Start scheduler in goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := app.scheduler.Start(app.ctx); err != nil {
			t.Logf("Scheduler error: %v", err)
		}
	}()

	// Wait a bit for scheduler to start
	time.Sleep(50 * time.Millisecond)

	// Create and add a task
	task := &testTask{
		id:       "test-task",
		executed: make(chan struct{}, 1),
	}

	id, err := app.scheduler.AddTask(task, scheduler.OneTime, time.Now())
	if err != nil {
		t.Fatalf("Failed to add task: %v", err)
	}
	if id == "" {
		t.Error("AddTask should return a non-empty ID")
	}

	// Wait for task to execute
	select {
	case <-task.executed:
		// Task executed successfully
	case <-time.After(2 * time.Second):
		t.Fatal("Task should have executed within 2 seconds")
	}

	// Verify task executed once
	if task.ExecutionCount() != 1 {
		t.Errorf("Expected task to execute once, got %d executions", task.ExecutionCount())
	}

	// Note: OneTime task is automatically deleted after execution, so we can't test RemoveTask on it.
	// Verify we get ErrTaskNotFound for a non-existent task ID
	err = app.scheduler.RemoveTask("nonexistent-id")
	if err != scheduler.ErrTaskNotFound {
		t.Errorf("Expected ErrTaskNotFound when removing non-existent task, got: %v", err)
	}

	// Test RemoveTask with a recurring task
	recurringTask := &testTask{
		id:       "recurring-task",
		executed: make(chan struct{}, 10),
	}

	recurringID, err := app.scheduler.AddTask(recurringTask, scheduler.Hourly, time.Now())
	if err != nil {
		t.Fatalf("Failed to add recurring task: %v", err)
	}

	// Remove the recurring task (should succeed)
	err = app.scheduler.RemoveTask(recurringID)
	if err != nil {
		t.Fatalf("Failed to remove recurring task: %v", err)
	}

	// RemoveTask is idempotent - calling it again should succeed (task is still marked for deletion)
	err = app.scheduler.RemoveTask(recurringID)
	if err != nil {
		t.Errorf("Calling RemoveTask again on marked task should succeed, got error: %v", err)
	}

	// Cancel context to stop scheduler
	app.cancel()
	<-done
}

// TestAppSchedulerShutdownIntegration verifies that scheduler shutdown works correctly
func TestAppSchedulerShutdownIntegration(t *testing.T) {
	app := CreateApp(t, false)

	// Initialize scheduler manually
	app.scheduler = scheduler.NewScheduler(0)

	// Start scheduler
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = app.scheduler.Start(app.ctx)
	}()

	// Wait a bit for scheduler to start
	time.Sleep(50 * time.Millisecond)

	// Add a task
	task := &testTask{
		id:       "slow-task",
		executed: make(chan struct{}, 1),
	}

	_, err := app.scheduler.AddTask(task, scheduler.OneTime, time.Now())
	if err != nil {
		t.Fatalf("Failed to add task: %v", err)
	}

	// Cancel context to stop scheduler Start() first
	app.cancel()

	// Wait for scheduler Start() to exit (it exits when context is cancelled)
	select {
	case <-done:
		// Scheduler Start() has exited
	case <-time.After(1 * time.Second):
		t.Fatal("Scheduler Start() should exit after context cancellation")
	}

	// Now shutdown should complete (scheduler Start() has already exited)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		app.Shutdown()
	}()

	select {
	case <-shutdownDone:
		// Shutdown completed successfully
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown should complete within 5 seconds")
	}

	// Verify we can't add tasks after shutdown
	_, err = app.scheduler.AddTask(&testTask{id: "new-task"}, scheduler.OneTime, time.Now())
	if err != scheduler.ErrSchedulerShutdown {
		t.Errorf("Expected ErrSchedulerShutdown after shutdown, got: %v", err)
	}
}
