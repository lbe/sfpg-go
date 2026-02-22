// Package queue provides a generic, thread-safe, dynamically resizing double-ended queue (deque).
// It offers efficient O(1) time complexity for adding and removing items from both
// ends, making it suitable for both queue (FIFO) and stack (LIFO) operations.
//
// The queue automatically resizes its internal buffer by powers of two to
// optimize for CPU and garbage collection performance, growing when capacity
// is needed and shrinking when the buffer is less than a quarter full.
// It uses bitwise arithmetic for efficient index calculations and supports
// concurrent use through internal locking.
package queue

import (
	"errors"
	"sync"
)

// minCapacity is the minimum capacity of the queue's internal buffer.
const minCapacity = 16

var (
	// ErrEmptyQueue is returned when attempting to remove or peek from an empty queue.
	ErrEmptyQueue = errors.New("queue is empty")
	// ErrClosedQueue is returned when operating on a closed queue.
	ErrClosedQueue = errors.New("queue is closed")
)

// Queue is a generic, thread-safe, dynamically resizing double-ended queue (deque).
// Use NewQueue to create a queue. All methods are safe for concurrent use.
type Queue[T any] struct {
	mu            sync.Mutex // Mutex to protect access to the queue's internal state.
	buf           []T        // The underlying circular buffer.
	ctAddBack     int        // Counter for AddBack operations.
	ctAddFront    int        // Counter for AddFront operations.
	ctRemoveBack  int        // Counter for RemoveBack operations.
	ctRemoveFront int        // Counter for RemoveFront operations.
	head          int        // Index of the front element in the buffer.
	tail          int        // Index where the next element will be added to the back.
	size          int        // Current number of elements in the queue.
	closed        bool       // Flag indicating if the queue has been closed.
}

// QueueStats holds various statistics about the queue's current state.
type QueueStats struct {
	CtAddBack     int  // Total number of AddBack operations.
	CtAddFront    int  // Total number of AddFront operations.
	CtRemoveBack  int  // Total number of RemoveBack operations.
	CtRemoveFront int  // Total number of RemoveFront operations.
	Size          int  // Current number of items in the queue.
	Capacity      int  // Current capacity of the queue's internal buffer.
	HeadIndex     int  // Current head index of the internal buffer.
	TailIndex     int  // Current tail index of the internal buffer.
	IsClosed      bool // Whether the queue is closed.
}

// NewQueue creates a new queue with at least initialCap capacity.
// The actual capacity will be the next power of two greater than or equal to initialCap.
func NewQueue[T any](initialCap int) *Queue[T] {
	if initialCap < minCapacity {
		initialCap = minCapacity
	}
	// Ensure power of two
	cap := minCapacity
	for cap < initialCap {
		cap <<= 1
	}
	return &Queue[T]{
		buf:    make([]T, cap),
		head:   0,
		tail:   0,
		size:   0,
		closed: false,
	}
}

// Stats returns statistics about the queue.
func (q *Queue[T]) Stats() QueueStats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return QueueStats{
		CtAddBack:     q.ctAddBack,
		CtAddFront:    q.ctAddFront,
		CtRemoveBack:  q.ctRemoveBack,
		CtRemoveFront: q.ctRemoveFront,
		Size:          q.size,
		Capacity:      len(q.buf),
		HeadIndex:     q.head,
		TailIndex:     q.tail,
		IsClosed:      q.closed,
	}
}

// Len returns the number of items in the queue.
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.size
}

// Cap returns the current capacity of the queue.
func (q *Queue[T]) Cap() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.buf)
}

// IsEmpty returns true if the queue is empty.
func (q *Queue[T]) IsEmpty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.size == 0
}

// Clear removes all items from the queue and resets its capacity.
func (q *Queue[T]) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.head = 0
	q.tail = 0
	q.size = 0
	q.buf = make([]T, minCapacity)
}

// Close marks the queue as closed. Further operations will return ErrClosedQueue.
func (q *Queue[T]) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
}

// Enqueue adds an item to the back of the queue.
// Returns ErrClosedQueue if the queue is closed.
func (q *Queue[T]) Enqueue(item T) error {
	return q.AddBack(item)
}

// Push adds an item to the back of the queue.
// Returns ErrClosedQueue if the queue is closed.
func (q *Queue[T]) Push(item T) error {
	return q.AddBack(item)
}

// AddBack adds an item to the back of the queue.
// Returns ErrClosedQueue if the queue is closed.
func (q *Queue[T]) AddBack(item T) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosedQueue
	}
	if q.size == len(q.buf) {
		q.resize(len(q.buf) << 1)
	}
	q.buf[q.tail] = item
	q.tail = (q.tail + 1) & (len(q.buf) - 1)
	q.size++
	return nil
}

// AddFront adds an item to the front of the queue.
// Returns ErrClosedQueue if the queue is closed.
func (q *Queue[T]) AddFront(item T) error {
	q.mu.Lock()
	defer func() {
		q.mu.Unlock()
	}()
	if q.closed {
		return ErrClosedQueue
	}
	if q.size == len(q.buf) {
		q.resize(len(q.buf) << 1)
	}
	q.head = (q.head - 1 + len(q.buf)) & (len(q.buf) - 1)
	q.buf[q.head] = item
	q.size++
	return nil
}

// Dequeue removes and returns the item at the front of the queue.
// Returns ErrEmptyQueue if the queue is empty, or ErrClosedQueue if closed.
func (q *Queue[T]) Dequeue() (T, error) {
	return q.RemoveFront()
}

// RemoveFront removes and returns the item at the front of the queue.
// Returns ErrEmptyQueue if the queue is empty, or ErrClosedQueue if closed.
func (q *Queue[T]) RemoveFront() (T, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var zero T
	if q.size == 0 {
		return zero, ErrEmptyQueue
	}
	if q.closed {
		return zero, ErrClosedQueue
	}
	item := q.buf[q.head]
	q.buf[q.head] = zero // GC
	q.head = (q.head + 1) & (len(q.buf) - 1)
	q.size--
	q.shrinkIfNeeded()
	return item, nil
}

// Pop removes and returns the item at the back of the queue.
// Returns ErrEmptyQueue if the queue is empty, or ErrClosedQueue if closed.
func (q *Queue[T]) Pop() (T, error) {
	return q.RemoveBack()
}

// RemoveBack removes and returns the item at the back of the queue.
// Returns ErrEmptyQueue if the queue is empty, or ErrClosedQueue if closed.
func (q *Queue[T]) RemoveBack() (T, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var zero T
	if q.size == 0 {
		return zero, ErrEmptyQueue
	}
	if q.closed {
		return zero, ErrClosedQueue
	}
	q.tail = (q.tail - 1 + len(q.buf)) & (len(q.buf) - 1)
	item := q.buf[q.tail]
	q.buf[q.tail] = zero // GC
	q.size--
	q.shrinkIfNeeded()
	return item, nil
}

// PeekFront returns the item at the front of the queue without removing it.
// Returns ErrEmptyQueue if the queue is empty, or ErrClosedQueue if closed.
func (q *Queue[T]) PeekFront() (T, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var zero T
	if q.size == 0 {
		return zero, ErrEmptyQueue
	}
	if q.closed {
		return zero, ErrClosedQueue
	}
	return q.buf[q.head], nil
}

// PeekBack returns the item at the back of the queue without removing it.
// Returns ErrEmptyQueue if the queue is empty, or ErrClosedQueue if closed.
func (q *Queue[T]) PeekBack() (T, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var zero T
	if q.size == 0 {
		return zero, ErrEmptyQueue
	}
	if q.closed {
		return zero, ErrClosedQueue
	}
	idx := (q.tail - 1 + len(q.buf)) & (len(q.buf) - 1)
	return q.buf[idx], nil
}

// Slice returns a copy of the queue's contents as a slice.
func (q *Queue[T]) Slice() []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]T, q.size)
	for i := 0; i < q.size; i++ {
		idx := (q.head + i) & (len(q.buf) - 1)
		result[i] = q.buf[idx]
	}
	return result
}

// resize changes the capacity of the queue's internal buffer to newCap.
// It re-arranges the elements to the beginning of the new buffer.
func (q *Queue[T]) resize(newCap int) {
	newBuf := make([]T, newCap)
	for i := 0; i < q.size; i++ {
		idx := (q.head + i) & (len(q.buf) - 1)
		newBuf[i] = q.buf[idx]
	}
	q.buf = newBuf
	q.head = 0
	q.tail = q.size
}

// shrinkIfNeeded reduces the internal buffer size if the queue is less than 1/4 full.
// The capacity will not shrink below minCapacity.
func (q *Queue[T]) shrinkIfNeeded() {
	cap := len(q.buf)
	if cap <= minCapacity {
		return
	}
	if q.size <= cap/4 {
		newCap := max(cap/2, minCapacity)
		q.resize(newCap)
	}
}
