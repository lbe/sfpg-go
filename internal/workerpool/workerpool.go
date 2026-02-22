// Package workerpool provides a dynamic pool of workers that can be used to
// process tasks concurrently. The pool automatically scales the number of
// workers based on load.
package workerpool

import (
	"context"
	"log/slog"
	"math/rand"
	"runtime"
	"sync/atomic"
	"time"

	"go.local/sfpg/internal/dbconnpool"
	"golang.org/x/sync/errgroup"
)

// PoolStats holds statistics about the worker pool.
// All fields are protected by atomic operations for lock-free access.
type PoolStats struct {
	RunningWorkers   atomic.Int64  // Number of currently running worker goroutines.
	SubmittedTasks   atomic.Uint64 // Total number of tasks submitted to the pool.
	WaitingTasks     atomic.Uint64 // Number of tasks waiting in the queue.
	SuccessfulTasks  atomic.Uint64 // Number of tasks that completed successfully.
	FailedTasks      atomic.Uint64 // Number of tasks that failed.
	CompletedTasks   atomic.Uint64 // Total number of tasks completed (success or failure).
	DroppedTasks     atomic.Uint64 // Number of tasks dropped due to a full queue in non-blocking mode.
	timeLastSubmit   atomic.Int64  // Timestamp of last task submission (UnixNano).
	timeLastComplete atomic.Int64  // Timestamp of last task completion (UnixNano).
}

// Pool represents a worker pool for processing tasks concurrently.
type Pool struct {
	ctx         context.Context
	G           *errgroup.Group
	MaxWorkers  int
	MinWorkers  int
	MaxIdleTime time.Duration
	Stats       *PoolStats
}

// NewPool creates and initializes a new worker pool with the specified
// minimum and maximum number of workers and maximum idle time.
func NewPool(ctx context.Context, maxWorkers int, minWorkers int, maxIdleTime time.Duration) *Pool {
	p := Pool{
		ctx:         ctx,
		G:           &errgroup.Group{},
		MaxWorkers:  maxWorkers,
		MinWorkers:  minWorkers,
		MaxIdleTime: maxIdleTime,
		Stats:       &PoolStats{},
	}
	if p.MaxWorkers <= 0 {
		p.MinWorkers, p.MaxWorkers = p.getMinMaxPoolWorkers(minWorkers, maxWorkers)
	}
	p.AddSubmitted()
	p.AddCompleted()
	return &p
}

// AddSubmitted increments the count of submitted tasks and updates the last submission time.
func (p *Pool) AddSubmitted() {
	p.Stats.SubmittedTasks.Add(1)
	p.Stats.timeLastSubmit.Store(time.Now().UnixNano())
}

// AddCompleted increments the count of completed tasks and updates the last completion time.
func (p *Pool) AddCompleted() {
	p.Stats.CompletedTasks.Add(1)
	p.Stats.timeLastComplete.Store(time.Now().UnixNano())
}

// AddFailed increments the count of failed tasks.
func (p *Pool) AddFailed() {
	p.Stats.FailedTasks.Add(1)
}

// AddSuccessful increments the count of successful tasks.
func (p *Pool) AddSuccessful() {
	p.Stats.SuccessfulTasks.Add(1)
}

// getMinMaxPoolWorkers determines the default minimum and maximum number of workers
// based on the number of available CPU cores.
func (p *Pool) getMinMaxPoolWorkers(minPoolWorkers, maxPoolWorkers int) (int, int) {
	numCPU := runtime.NumCPU()

	if maxPoolWorkers == 0 {
		switch {
		case numCPU > 4:
			maxPoolWorkers = numCPU - 2
		case numCPU > 2 && numCPU <= 4:
			maxPoolWorkers = 2
		default:
			maxPoolWorkers = 1
		}
	}

	if minPoolWorkers == 0 {
		switch {
		case (numCPU - 2) > 4:
			minPoolWorkers = 4
		case numCPU > 2 && numCPU <= 4:
			minPoolWorkers = 2
		default:
			minPoolWorkers = 1
		}
	}

	return minPoolWorkers, maxPoolWorkers
}

// MonitorPool dynamically adjusts the number of workers based on the queue length
// and pool state. It runs in a separate goroutine and periodically checks
// whether to scale up the number of workers.
func (p *Pool) MonitorPool(ctx context.Context, eg *errgroup.Group,
	poolFunc PoolFunc,
	dbRoPool, dbRwPool dbconnpool.ConnectionPool, queueLenFunc func() int,
) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			queueLength := queueLenFunc()
			runningWorkers := int(p.Stats.RunningWorkers.Load())
			if queueLength < runningWorkers {
				continue
			}
			if runningWorkers < p.MaxWorkers {
				eg.Go(func() error {
					defer p.Stats.RunningWorkers.Add(-1)
					return poolFunc(ctx, p, dbRoPool, dbRwPool, queueLenFunc, runningWorkers+1)
				})
				p.Stats.RunningWorkers.Add(1)
			}
		}
	}
}

// ShouldIStop determines if an individual worker should stop based on the pool's
// current state, such as the number of running workers, queue length, and idle time.
func (p *Pool) ShouldIStop(queueLength int) bool {
	if p.Stats.RunningWorkers.Load() <= int64(p.MinWorkers) {
		return false
	}
	if int64(queueLength) > p.Stats.RunningWorkers.Load() {
		return false
	}
	if p.TimeSinceLastCompletion() < p.MaxIdleTime {
		return false
	}
	return true
}

// StartWorkerPool initializes and starts the worker pool. It launches the initial
// set of workers and the pool monitor goroutine.
func (p *Pool) StartWorkerPool(
	poolFunc PoolFunc,
	dbRoPool, dbRwPool dbconnpool.ConnectionPool,
	queueLenFunc func() int,
) {
	eg, ctx := errgroup.WithContext(p.ctx)
	p.G = eg

	for i := range p.MinWorkers {
		p.G.Go(func() error {
			defer p.Stats.RunningWorkers.Add(-1)
			return poolFunc(ctx, p, dbRoPool, dbRwPool, queueLenFunc, i)
		})
		p.Stats.RunningWorkers.Add(1)
		time.Sleep(time.Duration(rand.Intn(100)+50) * time.Millisecond) // Stagger worker starts slightly
	}

	p.G.Go(func() error {
		return p.MonitorPool(ctx, eg, poolFunc, dbRoPool, dbRwPool, queueLenFunc)
	})

	err := p.G.Wait()
	if err != nil {
		slog.Error("workerpool Go returned error", "err", err)
	}
}

// TimeSinceLastCompletion returns the duration since the last task was completed.
func (p *Pool) TimeSinceLastCompletion() time.Duration {
	n := p.Stats.timeLastComplete.Load()
	if n == 0 {
		return time.Duration(0)
	}
	return time.Since(time.Unix(0, n))
}

// Stats holds a snapshot of pool statistics.
type Stats struct {
	RunningWorkers  int64
	SubmittedTasks  uint64
	WaitingTasks    uint64
	SuccessfulTasks uint64
	FailedTasks     uint64
	CompletedTasks  uint64
	DroppedTasks    uint64
	MaxWorkers      int
	MinWorkers      int
}

// GetStats returns a snapshot of the current pool statistics.
func (p *Pool) GetStats() Stats {
	return Stats{
		RunningWorkers:  p.Stats.RunningWorkers.Load(),
		SubmittedTasks:  p.Stats.SubmittedTasks.Load(),
		WaitingTasks:    p.Stats.WaitingTasks.Load(),
		SuccessfulTasks: p.Stats.SuccessfulTasks.Load(),
		FailedTasks:     p.Stats.FailedTasks.Load(),
		CompletedTasks:  p.Stats.CompletedTasks.Load(),
		DroppedTasks:    p.Stats.DroppedTasks.Load(),
		MaxWorkers:      p.MaxWorkers,
		MinWorkers:      p.MinWorkers,
	}
}
