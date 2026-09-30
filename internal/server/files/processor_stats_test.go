package files

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

type statsFakeProcessor struct{}

func (f *statsFakeProcessor) ProcessDiscoveryFile(ctx context.Context, file *File) (*File, error) {
	return &File{Path: file.Path}, nil
}

func (f *statsFakeProcessor) SubmitFileForWrite(file *File) error {
	return nil
}

func (f *statsFakeProcessor) PendingWriteCount() int64 {
	return 0
}

func (f *statsFakeProcessor) RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string, folderID int64) error {
	return nil
}

func (f *statsFakeProcessor) Close() error {
	return nil
}

// submitRecordingProcessor records every SubmitFileForWrite call (path and Exists)
// so tests can assert that already-existing files are not submitted for write.
type submitRecordingProcessor struct {
	submitCalls []struct {
		Path   string
		Exists bool
	}
	mu sync.Mutex
}

func (f *submitRecordingProcessor) ProcessDiscoveryFile(ctx context.Context, file *File) (*File, error) {
	return &File{Path: file.Path}, nil
}

func (f *submitRecordingProcessor) SubmitFileForWrite(file *File) error {
	f.mu.Lock()
	f.submitCalls = append(f.submitCalls, struct {
		Path   string
		Exists bool
	}{file.Path, file.Exists})
	f.mu.Unlock()
	return nil
}

func (f *submitRecordingProcessor) PendingWriteCount() int64 {
	return 0
}

func (f *submitRecordingProcessor) RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string, folderID int64) error {
	return nil
}

func (f *submitRecordingProcessor) Close() error {
	return nil
}

// TestRunPoolWorkerWithProcessor_SubmitsAllDequeuedIncludingExisting verifies
// dequeued items always reach SubmitFileForWrite after successful processing,
// including paths that already exist in the DB (walk is the only skip gate).
func TestRunPoolWorkerWithProcessor_SubmitsAllDequeuedIncludingExisting(t *testing.T) {
	q := queue.NewQueue[DiscoveryPathWork](2)
	q.Enqueue(testDiscoveryPath("/tmp/Images/existing.jpg"))
	q.Enqueue(testDiscoveryPath("/tmp/Images/new.jpg"))

	fp := &submitRecordingProcessor{}

	stats := &ProcessingStats{}
	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poolFunc := NewPoolFuncWithProcessor(fp, q, "/tmp/Images", testRemovePrefix, stats, nil)
	done := make(chan error, 1)
	go func() {
		done <- poolFunc(ctx, pool, nil, nil, q.Len, 1)
	}()

	waitForCompleted(t, pool, 2)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("runPoolWorkerWithProcessor: %v", err)
	}

	fp.mu.Lock()
	calls := append([]struct {
		Path   string
		Exists bool
	}{}, fp.submitCalls...)
	fp.mu.Unlock()

	if len(calls) != 2 {
		t.Errorf("SubmitFileForWrite call count: got %d, want 2 (all dequeued paths submit)", len(calls))
		for i, c := range calls {
			t.Logf("  call %d: path=%q Exists=%v", i, c.Path, c.Exists)
		}
	}
}

func TestNewPoolFuncWithProcessor_Stats(t *testing.T) {
	// Setup
	q := queue.NewQueue[DiscoveryPathWork](2)
	// Add 2 files
	q.Enqueue(testDiscoveryPath("/tmp/Images/existing.jpg"))
	q.Enqueue(testDiscoveryPath("/tmp/Images/new.jpg"))

	fp := &statsFakeProcessor{}

	stats := &ProcessingStats{}

	// Create pool
	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create pool func with stats
	poolFunc := NewPoolFuncWithProcessor(fp, q, "/tmp/Images", testRemovePrefix, stats, nil)

	done := make(chan error, 1)

	// Run the worker
	go func() {
		done <- poolFunc(ctx, pool, nil, nil, q.Len, 1)
	}()

	// Wait for completion (2 tasks)
	waitForCompleted(t, pool, 2)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("runPoolWorkerWithProcessor returned error: %v", err)
	}

	// Verify Stats (TotalFound is walk-time only; worker path does not increment it)
	if val := stats.TotalFound.Load(); val != 0 {
		t.Errorf("TotalFound: got %d, want 0", val)
	}
	if val := stats.AlreadyExisting.Load(); val != 0 {
		t.Errorf("AlreadyExisting: got %d, want 0", val)
	}
	if val := stats.NewlyInserted.Load(); val != 2 {
		t.Errorf("NewlyInserted: got %d, want 2 (both dequeued paths reach submit)", val)
	}
	if val := stats.InFlight.Load(); val != 0 {
		t.Errorf("InFlight: got %d, want 0", val)
	}
}
