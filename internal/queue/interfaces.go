package queue

// Dequeuer abstracts read operations on a queue.
// Used by consumers that only need to pull items.
type Dequeuer[T any] interface {
	// Dequeue removes and returns the item at the front.
	// Returns ErrEmptyQueue if empty, ErrClosedQueue if closed.
	Dequeue() (T, error)

	// Len returns the current number of items.
	Len() int

	// IsEmpty returns true if the queue has no items.
	IsEmpty() bool
}

// Enqueuer abstracts write operations on a queue.
// Used by producers that only need to push items.
type Enqueuer[T any] interface {
	// Enqueue adds an item to the back of the queue.
	// Returns ErrClosedQueue if closed.
	Enqueue(item T) error

	// Len returns the current number of items.
	Len() int
}

// Queuer combines read and write operations.
// Used when both operations are needed.
type Queuer[T any] interface {
	Dequeuer[T]
	Enqueuer[T]
	Close()
}

// Ensure Queue implements Queuer
var _ Queuer[any] = (*Queue[any])(nil)
