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
	"testing/synctest"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/queue"
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
		if pool.MinWorkers != 0 {
			t.Errorf("Expected MinWorkers to stay 0 (no idle workers), got %d", pool.MinWorkers)
		}
	})
}

// TestShouldScaleUp exercises the monitor scale-up predicate.
func TestShouldScaleUp(t *testing.T) {
	tests := []struct {
		name           string
		queueLength    int
		runningWorkers int
		maxWorkers     int
		want           bool
	}{
		{"empty queue and zero workers", 0, 0, 22, false},
		{"queue backlog from zero workers", 1, 0, 22, true},
		{"queue at running count", 1, 1, 22, false},
		{"queue backlog under max", 5, 4, 22, true},
		{"running at max", 5, 22, 22, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldScaleUp(tt.queueLength, tt.runningWorkers, tt.maxWorkers); got != tt.want {
				t.Errorf("shouldScaleUp(%d, %d, %d) = %v, want %v",
					tt.queueLength, tt.runningWorkers, tt.maxWorkers, got, tt.want)
			}
		})
	}
}

// TestPoolStats tests the statistics tracking of the pool.
func TestPoolStats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pool := NewPool(context.Background(), 4, 1, 1*time.Second)

		pool.AddSubmitted() // Initial is 1, so this makes it 2
		if pool.Stats.SubmittedTasks.Load() != 2 {
			t.Errorf("Expected SubmittedTasks to be 2, got %d", pool.Stats.SubmittedTasks.Load())
		}

		pool.AddCompleted()               // Initial is 1, so this makes it 2
		time.Sleep(10 * time.Millisecond) // Bubble time — Sleep AFTER completion

		if pool.Stats.CompletedTasks.Load() != 2 {
			t.Errorf("Expected CompletedTasks to be 2, got %d", pool.Stats.CompletedTasks.Load())
		}

		if pool.TimeSinceLastCompletion() < 10*time.Millisecond {
			t.Errorf("Expected TimeSinceLastCompletion to be >= 10ms, got %v", pool.TimeSinceLastCompletion())
		}
	})
}

// TestShouldIStop tests the logic for a worker deciding to stop.
func TestShouldIStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		time.Sleep(60 * time.Millisecond) // Bubble time — exceed idle threshold
		if !pool.ShouldIStop(0) {
			t.Error("ShouldIStop returned false when all stop conditions are met")
		}

		// Case 5: Min workers 0, one running worker, empty queue, idle past the
		// max idle time: the last worker may exit.
		poolMinZero := NewPool(ctx, 10, 0, 50*time.Millisecond)
		poolMinZero.Stats.RunningWorkers.Store(1)
		time.Sleep(60 * time.Millisecond) // Bubble time — exceed idle threshold
		if !poolMinZero.ShouldIStop(0) {
			t.Error("ShouldIStop returned false for min 0, one running worker, idle past max idle time")
		}
	})
}

// TestTimeSinceLastCompletion tests the TimeSinceLastCompletion method.
func TestTimeSinceLastCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pool := NewPool(context.Background(), 1, 1, 1*time.Second)

		if pool.TimeSinceLastCompletion() > 1*time.Millisecond { // Allow for a tiny duration
			t.Errorf("Expected initial TimeSinceLastCompletion to be ~0, got %v", pool.TimeSinceLastCompletion())
		}

		pool.AddCompleted()
		time.Sleep(50 * time.Millisecond) // Bubble time
		if pool.TimeSinceLastCompletion() < 50*time.Millisecond {
			t.Errorf("Expected TimeSinceLastCompletion to be >= 50ms, got %v", pool.TimeSinceLastCompletion())
		}
	})
}

// TestStartWorkerPool_BasicProcessing tests basic task processing.
func TestStartWorkerPool_BasicProcessing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()

		pool := NewPool(ctx, 4, 1, 1*time.Second)
		q := queue.NewQueue[string](100)
		var processedTasks atomic.Int64

		mockPoolFunc := func(ctx context.Context, wc WorkerContext, dbRo, dbRw dbconnpool.ConnectionPool, qLen func() int, id int) error {
			for {
				select {
				case <-ctx.Done():
					return nil
				default:
				}
				_, err := q.Dequeue()
				if err != nil {
					if errors.Is(err, queue.ErrEmptyQueue) {
						select {
						case <-ctx.Done():
							// Bubble ended — the loop-top ctx check returns nil.
						case <-time.After(10 * time.Millisecond): // Bubble time
						}
						continue
					}
					return err
				}
				processedTasks.Add(1)
				wc.AddCompleted()
			}
		}

		go pool.StartWorkerPool(mockPoolFunc, nil, nil, q.Len)

		numTasks := 50
		for range numTasks {
			if enqErr := q.Enqueue("task"); enqErr != nil {
				t.Fatalf("Enqueue: %v", enqErr)
			}
		}

		// Advance bubble time so the worker drains the queue, then flush.
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()

		if processedTasks.Load() != int64(numTasks) {
			t.Errorf("Expected %d tasks to be processed, got %d", numTasks, processedTasks.Load())
		}
	})
}

// TestStartWorkerPool_Scaling tests if the pool scales workers up and down.
func TestStartWorkerPool_Scaling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()

		pool := NewPool(ctx, 5, 1, 100*time.Millisecond)
		q := queue.NewQueue[string](100)
		var wg sync.WaitGroup

		mockPoolFunc := func(ctx context.Context, wc WorkerContext, dbRo, dbRw dbconnpool.ConnectionPool, qLen func() int, id int) error {
			wg.Done() // Signal that a worker has started
			for {
				select {
				case <-ctx.Done():
					return nil // Bubble ended — let the pool wind down
				default:
				}
				if wc.ShouldIStop(qLen()) {
					return nil
				}
				_, err := q.Dequeue()
				if err != nil {
					if errors.Is(err, queue.ErrEmptyQueue) {
						select {
						case <-ctx.Done():
							// Bubble ended — the loop-top ctx check returns nil.
						case <-time.After(10 * time.Millisecond): // Bubble time
						}
						continue
					}
					return err
				}
				wc.AddCompleted()
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(150 * time.Millisecond): // Simulate work (bubble time)
				}
			}
		}

		wg.Add(1) // Expect one initial worker
		go pool.StartWorkerPool(mockPoolFunc, nil, nil, q.Len)
		wg.Wait()       // Wait for the initial worker to start
		synctest.Wait() // Flush RunningWorkers.Add(1) in the pool goroutine

		// Initial state
		if pool.Stats.RunningWorkers.Load() != 1 {
			t.Fatalf("Expected 1 running worker initially, got %d", pool.Stats.RunningWorkers.Load())
		}

		// Add tasks to trigger scale-up
		wg.Add(4) // Expect 4 more workers to start
		for range 20 {
			if enqErr := q.Enqueue("task"); enqErr != nil {
				t.Fatalf("Enqueue: %v", enqErr)
			}
		}

		// Wait for scale-up, with a bubble timeout
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
		synctest.Wait() // Flush RunningWorkers.Add(1) for each spawned worker

		if pool.Stats.RunningWorkers.Load() != 5 {
			t.Errorf("Expected workers to scale up to 5, but got %d", pool.Stats.RunningWorkers.Load())
		}

		// Advance bubble time past task processing and idle so the extra
		// workers stop themselves and the pool shrinks back to min.
		time.Sleep(2 * time.Second)
		synctest.Wait()

		if pool.Stats.RunningWorkers.Load() != 1 {
			t.Errorf("Expected workers to scale down to min (1), but at %d", pool.Stats.RunningWorkers.Load())
		}
	})
}

// TestStartWorkerPool_MinWorkersZero_EmptyStaysZeroThenScales verifies that a
// pool with MinWorkers 0 stays at zero running workers on an empty queue, then
// scales up from zero once the queue has a backlog.
func TestStartWorkerPool_MinWorkersZero_EmptyStaysZeroThenScales(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()

		pool := NewPool(ctx, 5, 0, 100*time.Millisecond)
		q := queue.NewQueue[string](100)
		var wg sync.WaitGroup

		mockPoolFunc := func(ctx context.Context, wc WorkerContext, dbRo, dbRw dbconnpool.ConnectionPool, qLen func() int, id int) error {
			wg.Done() // Signal that a worker has started
			for {
				select {
				case <-ctx.Done():
					return nil // Bubble ended — let the pool wind down
				default:
				}
				if wc.ShouldIStop(qLen()) {
					return nil
				}
				_, err := q.Dequeue()
				if err != nil {
					if errors.Is(err, queue.ErrEmptyQueue) {
						select {
						case <-ctx.Done():
							// Bubble ended — the loop-top ctx check returns nil.
						case <-time.After(10 * time.Millisecond): // Bubble time
						}
						continue
					}
					return err
				}
				wc.AddCompleted()
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(150 * time.Millisecond): // Simulate work (bubble time)
				}
			}
		}

		go pool.StartWorkerPool(mockPoolFunc, nil, nil, q.Len)
		synctest.Wait() // Let the monitor start

		// Empty queue: no worker may be spawned across several monitor ticks.
		time.Sleep(500 * time.Millisecond) // ~5 monitor ticks (bubble time)
		synctest.Wait()
		if got := pool.Stats.RunningWorkers.Load(); got != 0 {
			t.Fatalf("Expected 0 running workers on empty queue, got %d", got)
		}

		// Add tasks to trigger scale-up from zero.
		wg.Add(5) // Expect all 5 workers to start
		for range 20 {
			if enqErr := q.Enqueue("task"); enqErr != nil {
				t.Fatalf("Enqueue: %v", enqErr)
			}
		}

		// Wait for scale-up, with a bubble timeout.
		waitChan := make(chan struct{})
		go func() {
			wg.Wait()
			close(waitChan)
		}()

		select {
		case <-waitChan:
			// All workers started
		case <-time.After(2 * time.Second):
			t.Fatalf("Timed out waiting for workers to scale up from zero. Running: %d", pool.Stats.RunningWorkers.Load())
		}
		synctest.Wait() // Flush RunningWorkers.Add(1) for each spawned worker

		if pool.Stats.RunningWorkers.Load() != 5 {
			t.Errorf("Expected workers to scale up to 5, got %d", pool.Stats.RunningWorkers.Load())
		}
	})
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
	testErr := errors.New("test worker error")

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

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()

		pool := NewPool(ctx, 2, 1, 1*time.Second)
		q := queue.NewQueue[string](10)
		var wg sync.WaitGroup

		// This worker will run once and exit with an error.
		mockPoolFunc := func(ctx context.Context, wc WorkerContext, dbRo, dbRw dbconnpool.ConnectionPool, qLen func() int, id int) error {
			wg.Done()
			return testErr
		}

		var poolDone sync.WaitGroup
		poolDone.Add(1)
		wg.Add(1)
		go func() {
			defer poolDone.Done()
			pool.StartWorkerPool(mockPoolFunc, nil, nil, q.Len)
		}()
		wg.Wait() // worker started and immediately returns testErr

		// The pool goroutine emits the error log before returning.
		poolDone.Wait()

		if !observedErr.Load() {
			t.Errorf("Expected logged worker error '%v' not observed", testErr)
		}
	})
}

// TestPoolResultCounters tests AddSuccessful and AddFailed on a real Pool.
func TestPoolResultCounters(t *testing.T) {
	tests := []struct {
		name    string
		call    func(*Pool)
		counter func(*Pool) uint64
	}{
		{
			name: "AddSuccessful",
			call: func(p *Pool) { p.AddSuccessful() },
			counter: func(p *Pool) uint64 {
				return p.Stats.SuccessfulTasks.Load()
			},
		},
		{
			name: "AddFailed",
			call: func(p *Pool) { p.AddFailed() },
			counter: func(p *Pool) uint64 {
				return p.Stats.FailedTasks.Load()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := NewPool(context.Background(), 1, 1, 1*time.Second)
			tt.call(pool)
			if got := tt.counter(pool); got != 1 {
				t.Errorf("Expected counter to be 1 after %s, got %d", tt.name, got)
			}
		})
	}
}

// TestGetStats verifies that GetStats returns a consistent snapshot of pool statistics.
func TestGetStats(t *testing.T) {
	pool := NewPool(context.Background(), 5, 2, 1*time.Second)

	pool.Stats.RunningWorkers.Store(3)
	pool.Stats.SubmittedTasks.Store(10)
	pool.Stats.WaitingTasks.Store(4)
	pool.Stats.SuccessfulTasks.Store(7)
	pool.Stats.FailedTasks.Store(2)
	pool.Stats.CompletedTasks.Store(9)
	pool.Stats.DroppedTasks.Store(1)

	stats := pool.GetStats()

	if stats.RunningWorkers != 3 {
		t.Errorf("Expected RunningWorkers 3, got %d", stats.RunningWorkers)
	}
	if stats.SubmittedTasks != 10 {
		t.Errorf("Expected SubmittedTasks 10, got %d", stats.SubmittedTasks)
	}
	if stats.WaitingTasks != 4 {
		t.Errorf("Expected WaitingTasks 4, got %d", stats.WaitingTasks)
	}
	if stats.SuccessfulTasks != 7 {
		t.Errorf("Expected SuccessfulTasks 7, got %d", stats.SuccessfulTasks)
	}
	if stats.FailedTasks != 2 {
		t.Errorf("Expected FailedTasks 2, got %d", stats.FailedTasks)
	}
	if stats.CompletedTasks != 9 {
		t.Errorf("Expected CompletedTasks 9, got %d", stats.CompletedTasks)
	}
	if stats.DroppedTasks != 1 {
		t.Errorf("Expected DroppedTasks 1, got %d", stats.DroppedTasks)
	}
	if stats.MaxWorkers != 5 {
		t.Errorf("Expected MaxWorkers 5, got %d", stats.MaxWorkers)
	}
	if stats.MinWorkers != 2 {
		t.Errorf("Expected MinWorkers 2, got %d", stats.MinWorkers)
	}
}

// TestTimeSinceLastCompletion_Zero verifies behavior when no completion has occurred.
func TestTimeSinceLastCompletion_Zero(t *testing.T) {
	pool := NewPool(context.Background(), 1, 1, 1*time.Second)
	pool.Stats.timeLastComplete.Store(0)

	if got := pool.TimeSinceLastCompletion(); got != time.Duration(0) {
		t.Errorf("Expected TimeSinceLastCompletion to be 0 when timestamp is zero, got %v", got)
	}
}

// TestGetMinMaxPoolWorkers exercises every branch of getMinMaxPoolWorkers deterministically.
func TestGetMinMaxPoolWorkers(t *testing.T) {
	tests := []struct {
		name           string
		numCPU         int
		minPoolWorkers int
		maxPoolWorkers int
		wantMin        int
		wantMax        int
	}{
		{"single core defaults", 1, 0, 0, 0, 1},
		{"dual core defaults", 2, 0, 0, 0, 1},
		{"quad core defaults", 4, 0, 0, 0, 2},
		{"eight core defaults", 8, 0, 0, 0, 6},
		{"sixteen core defaults", 16, 0, 0, 0, 14},
		{"explicit overrides", 8, 3, 5, 3, 5},
		{"explicit min with auto max", 8, 3, 0, 3, 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origRuntimeNumCPU := runtimeNumCPU
			runtimeNumCPU = func() int { return tt.numCPU }
			t.Cleanup(func() { runtimeNumCPU = origRuntimeNumCPU })

			pool := &Pool{}
			gotMin, gotMax := pool.getMinMaxPoolWorkers(tt.minPoolWorkers, tt.maxPoolWorkers)
			if gotMin != tt.wantMin {
				t.Errorf("Expected min %d, got %d", tt.wantMin, gotMin)
			}
			if gotMax != tt.wantMax {
				t.Errorf("Expected max %d, got %d", tt.wantMax, gotMax)
			}
		})
	}
}
