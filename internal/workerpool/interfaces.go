package workerpool

import (
	"context"
)

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
type PoolFunc func(ctx context.Context,
	wc WorkerContext,
	queueLenFunc func() int,
	id int,
) error

// Ensure Pool implements WorkerContext
var _ WorkerContext = (*Pool)(nil)
