// Package scheduler provides an in-memory asynchronous task scheduler with
// configurable concurrency limits. It supports one-time and recurring tasks
// (hourly, daily, weekly, monthly) with drift-free interval calculations.
//
// The scheduler uses context-based cancellation for graceful shutdown,
// ensuring all active tasks complete before the scheduler stops. Task errors
// are logged but do not prevent future executions or stop the scheduler.
//
// Example usage:
//
//	scheduler := scheduler.NewScheduler(5) // Max 5 concurrent tasks (0 = NumCPU)
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	// Add a one-time task (ID is auto-generated)
//	task := &MyTask{}
//	taskID, err := scheduler.AddTask(task, scheduler.OneTime, time.Now())
//
//	// Start scheduler (blocks until context cancelled)
//	go scheduler.Start(ctx)
//
//	// Add more tasks while running
//	dailyID, _ := scheduler.AddTask(dailyTask, scheduler.Daily, startTime)
//
//	// Remove a task (prevents future executions, but current execution continues)
//	scheduler.RemoveTask(dailyID)
//
//	// Shutdown gracefully (either via context cancellation or Shutdown method)
//	cancel() // Scheduler waits for active tasks to complete
//	// OR: scheduler.Shutdown() // Blocks until tasks complete
package scheduler

import (
	"context"
	"log/slog"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Task defines the interface that all scheduled tasks must implement.
// The Run method is called when the task is scheduled to execute.
// If Run returns an error, it is logged but does not prevent future executions.
type Task interface {
	Run(ctx context.Context) error
}

// ExecutionMode specifies when a task should execute.
type ExecutionMode int

const (
	// OneTime executes the task once at the specified start time.
	OneTime ExecutionMode = iota

	// Hourly executes the task every hour, starting at the specified start time.
	// The next execution time is calculated as startTime + (runCount * 1 hour),
	// ensuring drift-free scheduling relative to the original start time.
	Hourly

	// Daily executes the task every day, starting at the specified start time.
	// The next execution time is calculated as startTime + (runCount * 1 day),
	// ensuring drift-free scheduling relative to the original start time.
	Daily

	// Weekly executes the task every week (7 days), starting at the specified start time.
	// The next execution time is calculated as startTime + (runCount * 7 days),
	// ensuring drift-free scheduling relative to the original start time.
	Weekly

	// Monthly executes the task every month, starting at the specified start time.
	// The next execution time is calculated as startTime + (runCount * 1 month),
	// ensuring drift-free scheduling relative to the original start time.
	Monthly
)

// scheduledTask represents a task that has been scheduled for execution.
type scheduledTask struct {
	id                string
	task              Task
	mode              ExecutionMode
	startTime         time.Time
	nextRun           time.Time
	runCount          atomic.Int64 // Number of times this task has executed
	markedForDeletion atomic.Bool  // Flag indicating task should not be scheduled again
	mu                sync.Mutex   // Protects nextRun updates
}

// Scheduler manages the execution of tasks with configurable concurrency limits.
// It is safe for concurrent use: tasks can be added while the scheduler is running.
//
// The scheduler enforces a maximum number of concurrent task executions using
// a semaphore pattern with a buffered channel. Tasks that exceed the limit
// wait for a slot to become available.
type Scheduler struct {
	// MaxConcurrentTasks is the maximum number of tasks that can execute
	// concurrently. This value is set at creation time and cannot be changed.
	MaxConcurrentTasks int

	tasks          map[string]*scheduledTask // Registry of all scheduled tasks, keyed by task ID
	semaphore      chan struct{}             // Buffered channel for concurrency control (capacity = MaxConcurrentTasks)
	mu             sync.RWMutex              // Protects tasks map and running state
	wg             sync.WaitGroup            // Tracks active task executions for graceful shutdown
	running        atomic.Bool               // Indicates if the scheduler is currently running
	shutdown       atomic.Bool               // Indicates if the scheduler has been shut down
	shutdownCtx    context.Context           // Internal context cancelled by Shutdown()
	shutdownCancel context.CancelFunc        // Function to cancel shutdownCtx
	nextID         atomic.Int64              // Counter for auto-generating task IDs
	wakeCh         chan struct{}             // Wakes scheduler on new work
}

const (
	schedulerMinDelay  = 10 * time.Millisecond
	schedulerIdleDelay = 250 * time.Millisecond
	schedulerMaxDelay  = 1 * time.Second
)

// NewScheduler creates a new scheduler with the specified maximum concurrent tasks.
// If maxConcurrentTasks is 0, it is set to runtime.NumCPU().
// If maxConcurrentTasks is less than 1 (but not 0), it is set to 1.
//
// The scheduler is created in a stopped state. Call Start to begin execution.
func NewScheduler(maxConcurrentTasks int) *Scheduler {
	if maxConcurrentTasks == 0 {
		maxConcurrentTasks = runtime.NumCPU()
	}
	if maxConcurrentTasks < 1 {
		maxConcurrentTasks = 1
	}
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	return &Scheduler{
		MaxConcurrentTasks: maxConcurrentTasks,
		tasks:              make(map[string]*scheduledTask),
		semaphore:          make(chan struct{}, maxConcurrentTasks),
		shutdownCtx:        shutdownCtx,
		shutdownCancel:     shutdownCancel,
		wakeCh:             make(chan struct{}, 1),
	}
}

// AddTask adds a task to the scheduler with the specified execution mode and start time.
// Tasks can be added before or while the scheduler is running (thread-safe).
// The task ID is auto-generated and returned.
//
// Parameters:
//   - task: The task to execute. Must implement the Task interface. Must not be nil.
//   - mode: The execution mode (OneTime, Hourly, Daily, Weekly, or Monthly).
//   - startTime: The initial execution time. For recurring tasks, this is the base time
//     used for all interval calculations (ensuring drift-free scheduling). If startTime
//     is in the past, the task will execute immediately (for one-time) or catch up to
//     the current time (for recurring tasks).
//
// Returns the auto-generated task ID and an error if:
//   - task is nil (ErrNilTask)
//   - scheduler has been shut down (ErrSchedulerShutdown)
func (s *Scheduler) AddTask(task Task, mode ExecutionMode, startTime time.Time) (string, error) {
	if task == nil {
		return "", ErrNilTask
	}

	// Check if scheduler has been shut down
	if s.shutdown.Load() {
		return "", ErrSchedulerShutdown
	}

	// Generate unique ID
	id := strconv.FormatInt(s.nextID.Add(1), 10)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check shutdown state after acquiring lock
	if s.shutdown.Load() {
		return "", ErrSchedulerShutdown
	}

	st := &scheduledTask{
		id:        id,
		task:      task,
		mode:      mode,
		startTime: startTime,
		nextRun:   startTime,
	}

	s.tasks[id] = st
	s.signalWake()
	return id, nil
}

func (s *Scheduler) signalWake() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// Start starts the scheduler and runs until the context is cancelled or Shutdown is called.
// It performs a graceful shutdown, waiting for all active tasks to complete before returning.
//
// The scheduler checks for ready tasks every 10 milliseconds. When the context is cancelled
// or Shutdown is called, the scheduler stops scheduling new tasks but waits for all
// currently executing tasks to finish before returning.
//
// Start can only be called once per Scheduler instance. If called while already running,
// it returns ErrSchedulerAlreadyRunning. If Shutdown has been called, Start returns
// ErrSchedulerShutdown immediately.
//
// The method blocks until the context is cancelled, Shutdown is called, and all active
// tasks complete. It returns context.Canceled if the context was cancelled, ErrSchedulerShutdown
// if Shutdown was called, or another error if the context was cancelled with a different error.
func (s *Scheduler) Start(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return ErrSchedulerAlreadyRunning
	}
	defer s.running.Store(false)

	// Check if already shut down
	if s.shutdown.Load() {
		return ErrSchedulerShutdown
	}

	timer := time.NewTimer(schedulerMinDelay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			// Graceful shutdown: wait for all active tasks to complete
			s.wg.Wait()
			return ctx.Err()
		case <-s.shutdownCtx.Done():
			// Shutdown was called: wait for all active tasks to complete
			s.wg.Wait()
			return ErrSchedulerShutdown
		case <-s.wakeCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			nextDelay := s.executeReadyTasks(ctx)
			timer.Reset(nextDelay)
		case <-timer.C:
			nextDelay := s.executeReadyTasks(ctx)
			timer.Reset(nextDelay)
		}
	}
}

// executeReadyTasks checks all tasks and executes those that are ready to run.
func (s *Scheduler) executeReadyTasks(ctx context.Context) time.Duration {
	// Don't execute new tasks if shutdown
	if s.shutdown.Load() {
		return schedulerIdleDelay
	}

	now := time.Now()
	var readyTasks []*scheduledTask
	var earliest time.Time
	hadReady := false

	s.mu.Lock()
	for id, st := range s.tasks {
		if st.markedForDeletion.Load() {
			delete(s.tasks, id)
			continue
		}
		st.mu.Lock()
		nextRun := st.nextRun
		if !nextRun.After(now) {
			readyTasks = append(readyTasks, st)
			hadReady = true
		} else if earliest.IsZero() || nextRun.Before(earliest) {
			earliest = nextRun
		}
		st.mu.Unlock()
	}
	s.mu.Unlock()

	for _, st := range readyTasks {
		// Check shutdown and deletion again before executing
		if s.shutdown.Load() {
			return schedulerIdleDelay
		}
		if st.markedForDeletion.Load() {
			continue
		}
		// Try to acquire semaphore (non-blocking check first)
		select {
		case s.semaphore <- struct{}{}:
			// For OneTime tasks, mark nextRun immediately to prevent double-scheduling
			// This must happen BEFORE spawning the goroutine to prevent another tick
			// from seeing this task as ready while it's being executed
			if st.mode == OneTime {
				st.mu.Lock()
				st.nextRun = time.Now().Add(365 * 24 * time.Hour)
				st.mu.Unlock()
			}
			// Acquired, can execute
			s.wg.Add(1)
			go s.executeTask(ctx, st)
		default:
			// Semaphore full, skip for now (will retry on next tick)
			continue
		}
	}

	if hadReady {
		return schedulerMinDelay
	}
	if earliest.IsZero() {
		return schedulerIdleDelay
	}

	if delay := time.Until(earliest); delay > 0 {
		if delay < schedulerMinDelay {
			return schedulerMinDelay
		}
		if delay > schedulerMaxDelay {
			return schedulerMaxDelay
		}
		return delay
	}

	return schedulerMinDelay
}

// executeTask executes a single task and updates its schedule if recurring.
func (s *Scheduler) executeTask(ctx context.Context, st *scheduledTask) {
	defer s.wg.Done()
	defer func() { <-s.semaphore }() // Release semaphore

	// Execute the task
	err := st.task.Run(ctx)
	if err != nil {
		slog.Error("task execution failed", "task_id", st.id, "error", err)
	}

	// Update run count and calculate next run time for recurring tasks
	if st.mode == OneTime {
		s.mu.Lock()
		delete(s.tasks, st.id)
		s.mu.Unlock()
		return
	}

	st.mu.Lock()
	runCount := st.runCount.Add(1)
	st.nextRun = s.calculateNextRun(st.startTime, st.mode, runCount)
	st.mu.Unlock()

	if st.markedForDeletion.Load() {
		s.mu.Lock()
		delete(s.tasks, st.id)
		s.mu.Unlock()
	}
}

// calculateNextRun calculates the next run time for a recurring task based on the original startTime.
// This ensures drift-free scheduling by always calculating relative to the startTime.
func (s *Scheduler) calculateNextRun(startTime time.Time, mode ExecutionMode, runCount int64) time.Time {
	switch mode {
	case Hourly:
		return startTime.Add(time.Duration(runCount) * time.Hour)
	case Daily:
		return startTime.AddDate(0, 0, int(runCount))
	case Weekly:
		return startTime.AddDate(0, 0, 7*int(runCount))
	case Monthly:
		return startTime.AddDate(0, int(runCount), 0)
	default:
		return time.Now().Add(365 * 24 * time.Hour)
	}
}

// RemoveTask marks a task for deletion, preventing future executions.
// If the task is currently executing, it will continue but won't be scheduled again.
// Returns ErrTaskNotFound if the task ID doesn't exist.
func (s *Scheduler) RemoveTask(taskID string) error {
	if taskID == "" {
		return ErrTaskNotFound
	}

	s.mu.RLock()
	st, exists := s.tasks[taskID]
	s.mu.RUnlock()

	if !exists {
		return ErrTaskNotFound
	}

	// Mark task for deletion
	st.markedForDeletion.Store(true)
	s.signalWake()

	return nil
}

// Shutdown gracefully shuts down the scheduler. It prevents new tasks from being added
// and blocks until all active tasks complete. If Start is currently running, Shutdown
// returns ErrSchedulerRunning immediately without blocking.
//
// After Shutdown is called, AddTask will return ErrSchedulerShutdown, and Start will
// return ErrSchedulerShutdown if called.
func (s *Scheduler) Shutdown() error {
	// Check if Start is running
	if s.running.Load() {
		return ErrSchedulerRunning
	}

	// Set shutdown flag first to prevent new tasks
	if !s.shutdown.CompareAndSwap(false, true) {
		// Already shut down
		return nil
	}

	// Cancel internal context to signal shutdown
	s.shutdownCancel()

	// Wait for all active tasks to complete
	s.wg.Wait()

	return nil
}

// Errors that can be returned by scheduler methods.
var (
	// ErrNilTask is returned when AddTask is called with a nil task.
	ErrNilTask = &SchedulerError{msg: "task cannot be nil"}

	// ErrSchedulerAlreadyRunning is returned when Start is called while the
	// scheduler is already running.
	ErrSchedulerAlreadyRunning = &SchedulerError{msg: "scheduler is already running"}

	// ErrSchedulerShutdown is returned when AddTask or Start is called after
	// Shutdown has been called.
	ErrSchedulerShutdown = &SchedulerError{msg: "scheduler has been shut down"}

	// ErrSchedulerRunning is returned when Shutdown is called while Start is running.
	ErrSchedulerRunning = &SchedulerError{msg: "cannot shutdown scheduler while it is running"}

	// ErrTaskNotFound is returned when RemoveTask is called with a task ID that doesn't exist.
	ErrTaskNotFound = &SchedulerError{msg: "task not found"}
)

// SchedulerError represents an error returned by the scheduler.
type SchedulerError struct {
	msg string
}

// Error returns the error message.
func (e *SchedulerError) Error() string {
	return e.msg
}
