package workerpool

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/queue"
)

// logCaptureWriter captures slog output for tests.
type logCaptureWriter struct {
	cb func([]byte)
}

func (w *logCaptureWriter) Write(p []byte) (int, error) {
	if w.cb != nil {
		w.cb(p)
	}
	return len(p), nil
}

// TestNewPool tests the creation of a new worker pool.
func TestNewPool(t *testing.T) {
	ctx := context.Background()

	t.Run("Valid config", func(t *testing.T) {
		maxWorkers := 10
		minWorkers := 2
		maxIdleTime := 5 * time.Second

		pool := NewPool(ctx, maxWorkers, minWorkers, maxIdleTime)

		if pool == nil {
			t.Fatal("NewPool returned nil")
		}
		if pool.MaxWorkers != maxWorkers {
			t.Errorf("Expected MaxWorkers to be %d, got %d", maxWorkers, pool.MaxWorkers)
		}
		if pool.MinWorkers != minWorkers {
			t.Errorf("Expected MinWorkers to be %d, got %d", minWorkers, pool.MinWorkers)
		}
		if pool.MaxIdleTime != maxIdleTime {
			t.Errorf("Expected MaxIdleTime to be %v, got %v", maxIdleTime, pool.MaxIdleTime)
		}
		if pool.ctx != ctx {
			t.Error("Expected context to be set")
		}
		if pool.G == nil {
			t.Error("Expected errgroup.Group to be initialized")
		}
		if pool.Stats == nil {
			t.Error("Expected PoolStats to be initialized")
		}
	})

	t.Run("Zero workers defaults based on NumCPU", func(t *testing.T) {
		pool := NewPool(ctx, 0, 0, 1*time.Second)

		// Calculate the expected value based on the production logic in getMinMaxPoolWorkers
		numCPU := runtime.NumCPU()
		var expectedMax int
		switch {
		case numCPU > 4:
			expectedMax = numCPU - 2
		case numCPU > 2 && numCPU <= 4:
			expectedMax = 2
		default:
			expectedMax = 1
		}

		if pool.MaxWorkers != expectedMax {
			t.Errorf("Expected MaxWorkers to default to %d, got %d", expectedMax, pool.MaxWorkers)
		}
	})
}

// TestPoolStats tests the statistics tracking of the pool.
func TestPoolStats(t *testing.T) {
	pool := NewPool(context.Background(), 4, 1, 1*time.Second)

	pool.AddSubmitted() // Initial is 1, so this makes it 2
	if pool.Stats.SubmittedTasks.Load() != 2 {
		t.Errorf("Expected SubmittedTasks to be 2, got %d", pool.Stats.SubmittedTasks.Load())
	}

	pool.AddCompleted()               // Initial is 1, so this makes it 2
	time.Sleep(10 * time.Millisecond) // Sleep AFTER completion

	if pool.Stats.CompletedTasks.Load() != 2 {
		t.Errorf("Expected CompletedTasks to be 2, got %d", pool.Stats.CompletedTasks.Load())
	}

	if pool.TimeSinceLastCompletion() < 10*time.Millisecond {
		t.Errorf("Expected TimeSinceLastCompletion to be >= 10ms, got %v", pool.TimeSinceLastCompletion())
	}
}

// TestShouldIStop tests the logic for a worker deciding to stop.
func TestShouldIStop(t *testing.T) {
	ctx := context.Background()
	pool := NewPool(ctx, 10, 2, 50*time.Millisecond)

	// Case 1: Running workers are at or below min workers, should not stop.
	pool.Stats.RunningWorkers.Store(2)
	if pool.ShouldIStop(0) {
		t.Error("ShouldIStop returned true when running workers <= min workers")
	}

	// Case 2: More tasks in queue than workers, should not stop.
	pool.Stats.RunningWorkers.Store(5)
	if pool.ShouldIStop(6) {
		t.Error("ShouldIStop returned true when queueLength > running workers")
	}

	// Case 3: Idle time is less than threshold, should not stop.
	pool.Stats.RunningWorkers.Store(5)
	pool.AddCompleted() // Reset idle timer
	if pool.ShouldIStop(0) {
		t.Error("ShouldIStop returned true when idle time < max idle time")
	}

	// Case 4: All conditions to stop are met.
	pool.Stats.RunningWorkers.Store(5)
	time.Sleep(60 * time.Millisecond) // Exceed idle time
	if !pool.ShouldIStop(0) {
		t.Error("ShouldIStop returned false when all stop conditions are met")
	}
}

// TestTimeSinceLastCompletion tests the TimeSinceLastCompletion method.
func TestTimeSinceLastCompletion(t *testing.T) {
	pool := NewPool(context.Background(), 1, 1, 1*time.Second)

	if pool.TimeSinceLastCompletion() > 1*time.Millisecond { // Allow for a tiny duration
		t.Errorf("Expected initial TimeSinceLastCompletion to be ~0, got %v", pool.TimeSinceLastCompletion())
	}

	pool.AddCompleted()
	time.Sleep(50 * time.Millisecond)
	if pool.TimeSinceLastCompletion() < 50*time.Millisecond {
		t.Errorf("Expected TimeSinceLastCompletion to be >= 50ms, got %v", pool.TimeSinceLastCompletion())
	}
}

// TestStartWorkerPool_BasicProcessing tests basic task processing.
func TestStartWorkerPool_BasicProcessing(t *testing.T) {
	ctx := t.Context()

	pool := NewPool(ctx, 4, 1, 1*time.Second)
	q := queue.NewQueue[string](100)
	var processedTasks int64

	mockPoolFunc := func(ctx context.Context, wc WorkerContext, dbRo, dbRw dbconnpool.ConnectionPool, qLen func() int, id int) error {
		for {
			select {
			case <-ctx.Done():
				return nil
			default:
				_, err := q.Dequeue()
				if err != nil {
					if errors.Is(err, queue.ErrEmptyQueue) {
						time.Sleep(10 * time.Millisecond)
						continue
					}
					return err
				}
				atomic.AddInt64(&processedTasks, 1)
				wc.AddCompleted()
			}
		}
	}

	go pool.StartWorkerPool(mockPoolFunc, nil, nil, q.Len)

	numTasks := 50
	for range numTasks {
		q.Enqueue("task")
	}

	// Wait for tasks to be processed
	time.Sleep(500 * time.Millisecond)

	if atomic.LoadInt64(&processedTasks) != int64(numTasks) {
		t.Errorf("Expected %d tasks to be processed, got %d", numTasks, atomic.LoadInt64(&processedTasks))
	}
}

// TestStartWorkerPool_Scaling tests if the pool scales workers up and down.
func TestStartWorkerPool_Scaling(t *testing.T) {
	ctx := t.Context()

	pool := NewPool(ctx, 5, 1, 100*time.Millisecond)
	q := queue.NewQueue[string](100)
	var wg sync.WaitGroup

	mockPoolFunc := func(ctx context.Context, wc WorkerContext, dbRo, dbRw dbconnpool.ConnectionPool, qLen func() int, id int) error {
		wg.Done() // Signal that a worker has started
		for {
			if wc.ShouldIStop(qLen()) {
				return nil
			}
			_, err := q.Dequeue()
			if err != nil {
				if errors.Is(err, queue.ErrEmptyQueue) {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				return err
			}
			wc.AddCompleted()
			time.Sleep(150 * time.Millisecond) // Simulate work
		}
	}

	wg.Add(1) // Expect one initial worker
	go pool.StartWorkerPool(mockPoolFunc, nil, nil, q.Len)
	wg.Wait() // Wait for the initial worker to start

	// Initial state
	if pool.Stats.RunningWorkers.Load() != 1 {
		t.Fatalf("Expected 1 running worker initially, got %d", pool.Stats.RunningWorkers.Load())
	}

	// Add tasks to trigger scale-up
	wg.Add(4) // Expect 4 more workers to start
	for range 20 {
		q.Enqueue("task")
	}

	// Wait for scale-up, with a timeout
	waitChan := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitChan)
	}()

	select {
	case <-waitChan:
		// All workers started
	case <-time.After(2 * time.Second):
		t.Fatalf("Timed out waiting for workers to scale up. Running: %d", pool.Stats.RunningWorkers.Load())
	}

	if pool.Stats.RunningWorkers.Load() != 5 {
		t.Errorf("Expected workers to scale up to 5, but got %d", pool.Stats.RunningWorkers.Load())
	}

	// Wait for tasks to finish and trigger scale-down
	time.Sleep(500 * time.Millisecond)

	if pool.Stats.RunningWorkers.Load() != 1 {
		t.Errorf("Expected workers to scale down to min (1), but at %d", pool.Stats.RunningWorkers.Load())
	}
}

// TestStartWorkerPool_ContextCancellation tests graceful shutdown.
func TestStartWorkerPool_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pool := NewPool(ctx, 4, 1, 1*time.Second)
	q := queue.NewQueue[string](10)

	var workerStarted sync.WaitGroup
	workerStarted.Add(1)

	mockPoolFunc := func(ctx context.Context, wc WorkerContext, dbRo, dbRw dbconnpool.ConnectionPool, qLen func() int, id int) error {
		workerStarted.Done() // Signal that at least one worker has started
		<-ctx.Done()         // Wait for cancellation
		return nil
	}

	var poolExited sync.WaitGroup

	poolExited.Go(func() {
		pool.StartWorkerPool(mockPoolFunc, nil, nil, q.Len)
	})

	// Wait for the initial worker to start before we cancel
	workerStarted.Wait()

	cancel() // Trigger shutdown

	// Wait for the StartWorkerPool goroutine to exit cleanly
	poolExited.Wait()
}

// TestStartWorkerPool_ErrorHandling tests how the pool handles errors from poolFunc.
func TestStartWorkerPool_ErrorHandling(t *testing.T) {
	ctx := t.Context()

	pool := NewPool(ctx, 2, 1, 1*time.Second)
	q := queue.NewQueue[string](10)
	testErr := errors.New("test worker error")
	var wg sync.WaitGroup

	// This worker will run once and exit with an error.
	mockPoolFunc := func(ctx context.Context, wc WorkerContext, dbRo, dbRw dbconnpool.ConnectionPool, qLen func() int, id int) error {
		wg.Done()
		return testErr
	}

	// Capture log output to detect the error without racing on errgroup
	var observedErr atomic.Bool
	oldLogger := slog.Default()
	handler := slog.NewTextHandler(&logCaptureWriter{cb: func(p []byte) {
		if bytes.Contains(p, []byte("workerpool Go returned error")) && bytes.Contains(p, []byte(testErr.Error())) {
			observedErr.Store(true)
		}
	}}, nil)
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(oldLogger)

	wg.Add(1)
	go func() {
		pool.StartWorkerPool(mockPoolFunc, nil, nil, q.Len)
	}()
	wg.Wait() // worker started and immediately returns testErr

	// Wait briefly for log emission
	for i := 0; i < 50 && !observedErr.Load(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !observedErr.Load() {
		t.Errorf("Expected logged worker error '%v' not observed", testErr)
	}
}
