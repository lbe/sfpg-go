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
		_ = q.Enqueue(i)
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
		_ = q.Enqueue(i)
	}
	if q.Cap() < 128 {
		t.Errorf("Queue should have grown, got cap %d", q.Cap())
	}
	for range 120 {
		_, _ = q.Dequeue()
	}
	if q.Cap() > 32 {
		t.Errorf("Queue should have shrunk, got cap %d", q.Cap())
	}
	for range 8 {
		_, _ = q.Dequeue()
	}
	if q.Cap() != 16 {
		t.Errorf("Queue should not shrink below minCapacity, got cap %d", q.Cap())
	}
}

func TestQueueClear(t *testing.T) {
	q := NewQueue[int](16)
	for i := range 10 {
		_ = q.Enqueue(i)
	}
	q.Clear()
	if !q.IsEmpty() {
		t.Error("Queue should be empty after Clear")
	}
	if q.Cap() != 16 {
		t.Errorf("Queue capacity should be reset to minCapacity after Clear, got %d", q.Cap())
	}
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
			_ = q.Enqueue(val)
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
