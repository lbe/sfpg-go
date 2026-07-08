package queue

import (
	"sync"
	"testing"
)

func TestQueueBasicInt(t *testing.T) {
	q := NewQueue[int](16)
	if !q.IsEmpty() {
		t.Error("Queue should be empty initially")
	}
	if q.Len() != 0 {
		t.Error("Queue length should be 0 initially")
	}
	if q.Cap() < 16 {
		t.Error("Queue capacity should be at least 16")
	}

	// Enqueue and Dequeue
	for i := range 32 {
		if err := q.Enqueue(i); err != nil {
			t.Errorf("Enqueue failed: %v", err)
		}
	}
	if q.Len() != 32 {
		t.Errorf("Queue length should be 32, got %d", q.Len())
	}
	for i := range 32 {
		v, err := q.Dequeue()
		if err != nil {
			t.Errorf("Dequeue failed: %v", err)
		}
		if v != i {
			t.Errorf("Expected %d, got %d", i, v)
		}
	}
	if !q.IsEmpty() {
		t.Error("Queue should be empty after dequeuing all items")
	}
}

func TestQueueStackOps(t *testing.T) {
	q := NewQueue[string](16)
	words := []string{"a", "b", "c", "d"}
	for _, w := range words {
		if err := q.Push(w); err != nil {
			t.Errorf("Push failed: %v", err)
		}
	}
	for i := len(words) - 1; i >= 0; i-- {
		v, err := q.Pop()
		if err != nil {
			t.Errorf("Pop failed: %v", err)
		}
		if v != words[i] {
			t.Errorf("Expected %s, got %s", words[i], v)
		}
	}
	if !q.IsEmpty() {
		t.Error("Queue should be empty after popping all items")
	}
}

func TestQueueDoubleEnded(t *testing.T) {
	q := NewQueue[int](16)
	for i := range 10 {
		if err := q.AddFront(i); err != nil {
			t.Errorf("AddFront failed: %v", err)
		}
	}
	for i := 9; i >= 0; i-- {
		v, err := q.RemoveFront()
		if err != nil {
			t.Errorf("RemoveFront failed: %v", err)
		}
		if v != i {
			t.Errorf("Expected %d, got %d", i, v)
		}
	}
	if !q.IsEmpty() {
		t.Error("Queue should be empty after RemoveFront all items")
	}
}

func TestQueuePeekAndSlice(t *testing.T) {
	q := NewQueue[int](16)
	for i := 1; i <= 5; i++ {
		if err := q.Enqueue(i); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	front, err := q.PeekFront()
	if err != nil || front != 1 {
		t.Errorf("PeekFront expected 1, got %d, err %v", front, err)
	}
	back, err := q.PeekBack()
	if err != nil || back != 5 {
		t.Errorf("PeekBack expected 5, got %d, err %v", back, err)
	}
	slice := q.Slice()
	if len(slice) != 5 {
		t.Errorf("Slice length expected 5, got %d", len(slice))
	}
	for i, v := range slice {
		if v != i+1 {
			t.Errorf("Slice[%d] expected %d, got %d", i, i+1, v)
		}
	}
}

func TestQueueErrors(t *testing.T) {
	q := NewQueue[int](16)
	_, err := q.Dequeue()
	if err == nil {
		t.Error("Dequeue should error on empty queue")
	}
	_, err = q.Pop()
	if err == nil {
		t.Error("Pop should error on empty queue")
	}
	_, err = q.PeekFront()
	if err == nil {
		t.Error("PeekFront should error on empty queue")
	}
	_, err = q.PeekBack()
	if err == nil {
		t.Error("PeekBack should error on empty queue")
	}
	q.Close()
	if qErr := q.Enqueue(1); qErr == nil {
		t.Error("Enqueue should error on closed queue")
	}
	if qErr := q.AddFront(2); qErr == nil {
		t.Error("AddFront should error on closed queue")
	}
	_, err = q.Dequeue()
	if err == nil {
		t.Error("Dequeue should error on closed queue")
	}
}

func TestQueueResizeShrink(t *testing.T) {
	q := NewQueue[int](16)
	for i := range 128 {
		if err := q.Enqueue(i); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	if q.Cap() < 128 {
		t.Errorf("Queue should have grown, got cap %d", q.Cap())
	}
	for range 120 {
		if _, err := q.Dequeue(); err != nil {
			t.Fatalf("Dequeue: %v", err)
		}
	}
	if q.Cap() > 32 {
		t.Errorf("Queue should have shrunk, got cap %d", q.Cap())
	}
	for range 8 {
		if _, err := q.Dequeue(); err != nil {
			t.Fatalf("Dequeue: %v", err)
		}
	}
	if q.Cap() != 16 {
		t.Errorf("Queue should not shrink below minCapacity, got cap %d", q.Cap())
	}
}

func TestQueueClear(t *testing.T) {
	q := NewQueue[int](16)
	for i := range 10 {
		if err := q.Enqueue(i); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	q.Clear()
	if !q.IsEmpty() {
		t.Error("Queue should be empty after Clear")
	}
	if q.Cap() != 16 {
		t.Errorf("Queue capacity should be reset to minCapacity after Clear, got %d", q.Cap())
	}
}

func TestNewQueue_CapsBelowMinimum(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"zero", 0, 16},
		{"one", 1, 16},
		{"fifteen", 15, 16},
		{"sixteen", 16, 16},
		{"seventeen", 17, 32},
		{"thirty-three", 33, 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQueue[int](tt.input)
			if q.Len() != 0 {
				t.Errorf("Len() = %d, want 0", q.Len())
			}
			if q.Cap() != tt.expected {
				t.Errorf("Cap() = %d, want %d", q.Cap(), tt.expected)
			}
		})
	}
}

func TestQueueStats(t *testing.T) {
	q := NewQueue[int](16)

	assertStats := func(t *testing.T, got, want QueueStats) {
		t.Helper()
		if got != want {
			t.Errorf("Stats() = %+v, want %+v", got, want)
		}
	}

	// Initial state.
	assertStats(t, q.Stats(), QueueStats{
		CtAddBack:     0,
		CtAddFront:    0,
		CtRemoveBack:  0,
		CtRemoveFront: 0,
		Size:          0,
		Capacity:      16,
		HeadIndex:     0,
		TailIndex:     0,
		IsClosed:      false,
	})

	// Add three items to the back.
	for _, v := range []int{1, 2, 3} {
		if err := q.AddBack(v); err != nil {
			t.Fatalf("AddBack(%d): %v", v, err)
		}
	}
	assertStats(t, q.Stats(), QueueStats{
		CtAddBack:     3,
		CtAddFront:    0,
		CtRemoveBack:  0,
		CtRemoveFront: 0,
		Size:          3,
		Capacity:      16,
		HeadIndex:     0,
		TailIndex:     3,
		IsClosed:      false,
	})

	// Add two items to the front, wrapping around the circular buffer.
	for _, v := range []int{4, 5} {
		if err := q.AddFront(v); err != nil {
			t.Fatalf("AddFront(%d): %v", v, err)
		}
	}
	assertStats(t, q.Stats(), QueueStats{
		CtAddBack:     3,
		CtAddFront:    2,
		CtRemoveBack:  0,
		CtRemoveFront: 0,
		Size:          5,
		Capacity:      16,
		HeadIndex:     14,
		TailIndex:     3,
		IsClosed:      false,
	})

	// Remove one item from the front.
	if _, err := q.RemoveFront(); err != nil {
		t.Fatalf("RemoveFront(): %v", err)
	}
	assertStats(t, q.Stats(), QueueStats{
		CtAddBack:     3,
		CtAddFront:    2,
		CtRemoveBack:  0,
		CtRemoveFront: 1,
		Size:          4,
		Capacity:      16,
		HeadIndex:     15,
		TailIndex:     3,
		IsClosed:      false,
	})

	// Remove one item from the back.
	if _, err := q.RemoveBack(); err != nil {
		t.Fatalf("RemoveBack(): %v", err)
	}
	assertStats(t, q.Stats(), QueueStats{
		CtAddBack:     3,
		CtAddFront:    2,
		CtRemoveBack:  1,
		CtRemoveFront: 1,
		Size:          3,
		Capacity:      16,
		HeadIndex:     15,
		TailIndex:     2,
		IsClosed:      false,
	})

	// Close the queue; counters and indices should remain unchanged.
	q.Close()
	assertStats(t, q.Stats(), QueueStats{
		CtAddBack:     3,
		CtAddFront:    2,
		CtRemoveBack:  1,
		CtRemoveFront: 1,
		Size:          3,
		Capacity:      16,
		HeadIndex:     15,
		TailIndex:     2,
		IsClosed:      true,
	})
}

func TestQueueConcurrent(t *testing.T) {
	q := NewQueue[int](16)
	var (
		wg       sync.WaitGroup
		results  = make([]int, 10000)
		resultMu sync.Mutex
	)
	// Start concurrent Enqueue and Dequeue
	for i := range 10000 {
		wg.Add(2)
		go func(val int) {
			defer wg.Done()
			if enqErr := q.Enqueue(val); enqErr != nil {
				t.Errorf("Enqueue: %v", enqErr)
			}
		}(i)
		go func(idx int) {
			defer wg.Done()
			for {
				v, err := q.Dequeue()
				if err == nil {
					resultMu.Lock()
					results[idx] = v
					resultMu.Unlock()
					break
				}
				// If queue is empty, try again
			}
		}(i)
	}
	wg.Wait()
	if !q.IsEmpty() {
		t.Error("Queue should be empty after concurrent Enqueue/Dequeue")
	}
	// Check that all values are present (order not guaranteed)
	found := make(map[int]bool)
	for _, v := range results {
		found[v] = true
	}
	for i := range 10000 {
		if !found[i] {
			t.Errorf("Value %d missing from concurrent Enqueue/Dequeue results", i)
		}
	}
}
