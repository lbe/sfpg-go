package workerpool

import (
	"context"

	"go.local/sfpg/internal/dbconnpool"
)

// PoolStatsSnapshot provides read-only access to pool statistics.
type PoolStatsSnapshot struct {
	RunningWorkers  int64
	SubmittedTasks  uint64
	SuccessfulTasks uint64
	FailedTasks     uint64
	CompletedTasks  uint64
}

// WorkerContext provides the interface workers use to interact with the pool.
// This abstracts the Pool's methods that workers call during execution.
type WorkerContext interface {
	// ShouldIStop returns true if this worker should terminate based on
	// current pool state and queue length.
	ShouldIStop(queueLength int) bool

	// AddSubmitted increments the submitted task counter.
	AddSubmitted()

	// AddCompleted increments the completed task counter.
	AddCompleted()

	// AddFailed increments the failed task counter.
	AddFailed()

	// AddSuccessful increments the successful task counter.
	AddSuccessful()
}

// PoolFunc is the function signature for worker functions.
// Using ConnectionPool interface instead of concrete type.
type PoolFunc func(ctx context.Context,
	wc WorkerContext,
	dbRoPool, dbRwPool dbconnpool.ConnectionPool,
	queueLenFunc func() int,
	id int,
) error

// Ensure Pool implements WorkerContext
var _ WorkerContext = (*Pool)(nil)
