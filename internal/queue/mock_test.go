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
	if !errors.Is(err, mockErr) {
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

func TestMockDequeuer_IsEmpty(t *testing.T) {
	tests := []struct {
		name      string
		items     []string
		dequeue   int
		closed    bool
		wantEmpty bool
		wantLen   int
	}{
		{
			name:      "not empty",
			items:     []string{"a", "b"},
			dequeue:   0,
			wantEmpty: false,
			wantLen:   2,
		},
		{
			name:      "empty after dequeue",
			items:     []string{"a", "b"},
			dequeue:   2,
			wantEmpty: true,
			wantLen:   0,
		},
		{
			name:      "closed with remaining items",
			items:     []string{"a", "b"},
			dequeue:   0,
			closed:    true,
			wantEmpty: false,
			wantLen:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &MockDequeuer[string]{
				Items:  tt.items,
				Closed: tt.closed,
			}
			for range tt.dequeue {
				if _, err := mock.Dequeue(); err != nil {
					t.Fatalf("Dequeue: %v", err)
				}
			}
			if got := mock.IsEmpty(); got != tt.wantEmpty {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.wantEmpty)
			}
			if got := mock.Len(); got != tt.wantLen {
				t.Errorf("Len() = %d, want %d", got, tt.wantLen)
			}
		})
	}
}

func TestMockQueuer_Enqueue_Errors(t *testing.T) {
	customErr := errors.New("custom enqueue error")

	tests := []struct {
		name      string
		closed    bool
		err       error
		wantErr   error
		wantItems []string
	}{
		{
			name:      "closed",
			closed:    true,
			wantErr:   ErrClosedQueue,
			wantItems: []string{"existing"},
		},
		{
			name:      "custom error",
			err:       customErr,
			wantErr:   customErr,
			wantItems: []string{"existing"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &MockQueuer[string]{
				Items:  []string{"existing"},
				Closed: tt.closed,
				Err:    tt.err,
			}
			err := mock.Enqueue("new")
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Enqueue error = %v, want %v", err, tt.wantErr)
			}
			if len(mock.Items) != len(tt.wantItems) {
				t.Errorf("len(Items) = %d, want %d", len(mock.Items), len(tt.wantItems))
			}
			for i, want := range tt.wantItems {
				if mock.Items[i] != want {
					t.Errorf("Items[%d] = %q, want %q", i, mock.Items[i], want)
				}
			}
		})
	}
}

func TestMockQueuer_IsEmpty(t *testing.T) {
	tests := []struct {
		name      string
		items     []string
		dequeue   int
		closed    bool
		wantEmpty bool
		wantLen   int
	}{
		{
			name:      "not empty",
			items:     []string{"a", "b", "c"},
			dequeue:   0,
			wantEmpty: false,
			wantLen:   3,
		},
		{
			name:      "empty after dequeue",
			items:     []string{"a", "b", "c"},
			dequeue:   3,
			wantEmpty: true,
			wantLen:   0,
		},
		{
			name:      "closed with remaining items",
			items:     []string{"a", "b", "c"},
			dequeue:   0,
			closed:    true,
			wantEmpty: false,
			wantLen:   3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &MockQueuer[string]{
				Items:  tt.items,
				Closed: tt.closed,
			}
			for range tt.dequeue {
				if _, err := mock.Dequeue(); err != nil {
					t.Fatalf("Dequeue: %v", err)
				}
			}
			if got := mock.IsEmpty(); got != tt.wantEmpty {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.wantEmpty)
			}
			if got := mock.Len(); got != tt.wantLen {
				t.Errorf("Len() = %d, want %d", got, tt.wantLen)
			}
		})
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
			if _, deqErr := mock.Dequeue(); deqErr != nil {
				b.Fatalf("Dequeue: %v", deqErr)
			}
		}
	}
}

// BenchmarkQueue_Real shows real queue overhead for comparison.
func BenchmarkQueue_Real(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := NewQueue[string](1000)
		for j := range 1000 {
			if enqErr := q.Enqueue(string(rune(j))); enqErr != nil {
				b.Fatalf("Enqueue: %v", enqErr)
			}
		}
		for !q.IsEmpty() {
			if _, deqErr := q.Dequeue(); deqErr != nil {
				b.Fatalf("Dequeue: %v", deqErr)
			}
		}
	}
}
