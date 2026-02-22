package cachepreload

import (
	"sync"
	"testing"
	"time"
)

func TestPreloadMetrics_RecordSuccess(t *testing.T) {
	m := &PreloadMetrics{}
	m.RecordSuccess("/gallery/1", 10*time.Millisecond)
	if m.TasksCompleted.Load() != 1 {
		t.Error("expected TasksCompleted 1")
	}
}

func TestPreloadMetrics_RecordFailure(t *testing.T) {
	m := &PreloadMetrics{}
	m.RecordFailure("/gallery/1", nil, 5*time.Millisecond)
	if m.TasksFailed.Load() != 1 {
		t.Error("expected TasksFailed 1")
	}
}

func TestPreloadMetrics_RecordSkipped(t *testing.T) {
	m := &PreloadMetrics{}
	m.RecordSkipped("deduplicated")
	if m.TasksSkipped.Load() != 1 {
		t.Error("expected TasksSkipped 1")
	}
}

func TestPreloadMetrics_RecordCancelled(t *testing.T) {
	m := &PreloadMetrics{}
	m.RecordCancelled()
	if m.TasksCancelled.Load() != 1 {
		t.Error("expected TasksCancelled 1")
	}
}

func TestPreloadMetrics_ConcurrentAccess(t *testing.T) {
	m := &PreloadMetrics{}
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			m.RecordSuccess("/gallery/1", 1*time.Millisecond)
			m.RecordFailure("/gallery/2", nil, 1*time.Millisecond)
		})
	}
	wg.Wait()
	if m.TasksCompleted.Load() != 100 {
		t.Errorf("expected TasksCompleted 100, got %d", m.TasksCompleted.Load())
	}
}
