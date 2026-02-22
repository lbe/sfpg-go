package queue

import (
	"errors"
	"testing"
)

// TestMockDequeuer demonstrates fast dequeuer testing.
func TestMockDequeuer(t *testing.T) {
	mock := &MockDequeuer[string]{
		Items: []string{"item1", "item2", "item3"},
	}

	// Test sequential dequeue
	for i := 1; i <= 3; i++ {
		item, err := mock.Dequeue()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if item != "item"+string(rune('0'+i)) {
			t.Errorf("expected item%d, got %s", i, item)
		}
	}

	// Test empty queue
	_, err := mock.Dequeue()
	if !errors.Is(err, ErrEmptyQueue) {
		t.Errorf("expected ErrEmptyQueue, got %v", err)
	}

	// Test Len
	if mock.Len() != 0 {
		t.Errorf("expected Len 0, got %d", mock.Len())
	}
}

// TestMockDequeuer_Closed tests closed queue behavior.
func TestMockDequeuer_Closed(t *testing.T) {
	mock := &MockDequeuer[string]{
		Items:  []string{"item1"},
		Closed: true,
	}

	_, err := mock.Dequeue()
	if !errors.Is(err, ErrClosedQueue) {
		t.Errorf("expected ErrClosedQueue, got %v", err)
	}
}

// TestMockDequeuer_Error tests error injection.
func TestMockDequeuer_Error(t *testing.T) {
	mockErr := errors.New("mock error")
	mock := &MockDequeuer[string]{
		Items: []string{"item1"},
		Err:   mockErr,
	}

	_, err := mock.Dequeue()
	if err != mockErr {
		t.Errorf("expected %v, got %v", mockErr, err)
	}
}

// TestMockEnqueuer tests the mock enqueuer.
func TestMockEnqueuer(t *testing.T) {
	mock := &MockEnqueuer[string]{}

	// Test enqueue
	if err := mock.Enqueue("item1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.Enqueue("item2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.Len() != 2 {
		t.Errorf("expected Len 2, got %d", mock.Len())
	}

	// Test closed
	mock.Closed = true
	err := mock.Enqueue("item3")
	if !errors.Is(err, ErrClosedQueue) {
		t.Errorf("expected ErrClosedQueue, got %v", err)
	}
}

// TestMockQueuer tests the combined mock.
func TestMockQueuer(t *testing.T) {
	mock := &MockQueuer[string]{}

	// Enqueue items
	for i := range 5 {
		if err := mock.Enqueue(string(rune('a' + i))); err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}

	if mock.Len() != 5 {
		t.Errorf("expected Len 5, got %d", mock.Len())
	}

	// Dequeue items
	for i := range 5 {
		item, err := mock.Dequeue()
		if err != nil {
			t.Fatalf("dequeue failed: %v", err)
		}
		if item != string(rune('a'+i)) {
			t.Errorf("expected %c, got %s", rune('a'+i), item)
		}
	}

	// Close and verify
	mock.Close()
	if !mock.Closed {
		t.Error("expected Closed to be true")
	}

	_, err := mock.Dequeue()
	if !errors.Is(err, ErrClosedQueue) {
		t.Errorf("expected ErrClosedQueue after close, got %v", err)
	}
}

// BenchmarkMockDequeuer shows the speed advantage.
func BenchmarkMockDequeuer(b *testing.B) {
	items := make([]string, 1000)
	for i := range items {
		items[i] = string(rune(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mock := &MockDequeuer[string]{Items: items}
		for !mock.IsEmpty() {
			mock.Dequeue()
		}
	}
}

// BenchmarkQueue_Real shows real queue overhead for comparison.
func BenchmarkQueue_Real(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := NewQueue[string](1000)
		for j := range 1000 {
			q.Enqueue(string(rune(j)))
		}
		for !q.IsEmpty() {
			q.Dequeue()
		}
	}
}
