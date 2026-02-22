package queue

// MockDequeuer is a mock implementation of Dequeuer for testing.
type MockDequeuer[T any] struct {
	Items  []T
	Err    error
	Closed bool
	index  int
}

// Dequeue removes and returns the next item from the mock queue.
func (m *MockDequeuer[T]) Dequeue() (T, error) {
	var zero T
	if m.Closed {
		return zero, ErrClosedQueue
	}
	if m.Err != nil {
		return zero, m.Err
	}
	if m.index >= len(m.Items) {
		return zero, ErrEmptyQueue
	}
	item := m.Items[m.index]
	m.index++
	return item, nil
}

// Len returns the number of remaining items.
func (m *MockDequeuer[T]) Len() int {
	return len(m.Items) - m.index
}

// IsEmpty returns true if all items have been dequeued.
func (m *MockDequeuer[T]) IsEmpty() bool {
	return m.Len() == 0
}

// MockEnqueuer is a mock implementation of Enqueuer for testing.
type MockEnqueuer[T any] struct {
	Items  []T
	Err    error
	Closed bool
}

// Enqueue adds an item to the mock queue.
func (m *MockEnqueuer[T]) Enqueue(item T) error {
	if m.Closed {
		return ErrClosedQueue
	}
	if m.Err != nil {
		return m.Err
	}
	m.Items = append(m.Items, item)
	return nil
}

// Len returns the number of items in the queue.
func (m *MockEnqueuer[T]) Len() int {
	return len(m.Items)
}

// MockQueuer is a mock implementation of Queuer for testing.
type MockQueuer[T any] struct {
	Items  []T
	Err    error
	Closed bool
	index  int
}

// Dequeue removes and returns the next item.
func (m *MockQueuer[T]) Dequeue() (T, error) {
	var zero T
	if m.Closed {
		return zero, ErrClosedQueue
	}
	if m.Err != nil {
		return zero, m.Err
	}
	if m.index >= len(m.Items) {
		return zero, ErrEmptyQueue
	}
	item := m.Items[m.index]
	m.index++
	return item, nil
}

// Enqueue adds an item to the queue.
func (m *MockQueuer[T]) Enqueue(item T) error {
	if m.Closed {
		return ErrClosedQueue
	}
	if m.Err != nil {
		return m.Err
	}
	m.Items = append(m.Items, item)
	return nil
}

// Len returns the number of items in the queue.
func (m *MockQueuer[T]) Len() int {
	return len(m.Items) - m.index
}

// IsEmpty returns true if the queue has no items.
func (m *MockQueuer[T]) IsEmpty() bool {
	return m.Len() == 0
}

// Close marks the queue as closed.
func (m *MockQueuer[T]) Close() {
	m.Closed = true
}

// Ensure mock implementations satisfy interfaces
var _ Dequeuer[string] = (*MockDequeuer[string])(nil)
var _ Enqueuer[string] = (*MockEnqueuer[string])(nil)
var _ Queuer[string] = (*MockQueuer[string])(nil)
