package files

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/queue"
	"go.local/sfpg/internal/workerpool"
)

type statsFakeProcessor struct {
	existsMap map[string]bool
	mu        sync.Mutex
}

func (f *statsFakeProcessor) ProcessFile(ctx context.Context, path string) (*File, error) {
	f.mu.Lock()
	exists := f.existsMap[path]
	f.mu.Unlock()
	return &File{Path: path, Exists: exists}, nil
}

func (f *statsFakeProcessor) ProcessFileWithConn(ctx context.Context, path string, cpcRo *dbconnpool.CpConn) (*File, error) {
	return f.ProcessFile(ctx, path)
}

func (f *statsFakeProcessor) CheckIfModified(ctx context.Context, path string) (bool, error) {
	return false, nil
}

func (f *statsFakeProcessor) GenerateThumbnail(ctx context.Context, file *File) error {
	return nil
}

func (f *statsFakeProcessor) SubmitFileForWrite(file *File) error {
	return nil
}

func (f *statsFakeProcessor) PendingWriteCount() int64 {
	return 0
}

func (f *statsFakeProcessor) RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string) error {
	return nil
}

func (f *statsFakeProcessor) Close() error {
	return nil
}

// submitRecordingProcessor records every SubmitFileForWrite call (path and Exists)
// so tests can assert that already-existing files are not submitted for write.
type submitRecordingProcessor struct {
	existsMap   map[string]bool
	submitCalls []struct {
		Path   string
		Exists bool
	}
	mu sync.Mutex
}

func (f *submitRecordingProcessor) ProcessFile(ctx context.Context, path string) (*File, error) {
	f.mu.Lock()
	exists := f.existsMap[path]
	f.mu.Unlock()
	return &File{Path: path, Exists: exists}, nil
}

func (f *submitRecordingProcessor) ProcessFileWithConn(ctx context.Context, path string, cpcRo *dbconnpool.CpConn) (*File, error) {
	return f.ProcessFile(ctx, path)
}

func (f *submitRecordingProcessor) CheckIfModified(ctx context.Context, path string) (bool, error) {
	return false, nil
}

func (f *submitRecordingProcessor) GenerateThumbnail(ctx context.Context, file *File) error {
	return nil
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

func (f *submitRecordingProcessor) RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string) error {
	return nil
}

func (f *submitRecordingProcessor) Close() error {
	return nil
}

// TestRunPoolWorkerWithProcessor_DoesNotSubmitExistingFiles verifies that when
// ProcessFile returns a file with Exists=true (already in DB, unchanged), the
// worker does NOT call SubmitFileForWrite. Only new or modified files are submitted.
func TestRunPoolWorkerWithProcessor_DoesNotSubmitExistingFiles(t *testing.T) {
	q := queue.NewQueue[string](2)
	q.Enqueue("/tmp/Images/existing.jpg")
	q.Enqueue("/tmp/Images/new.jpg")

	fp := &submitRecordingProcessor{
		existsMap: map[string]bool{
			"existing.jpg": true,
			"new.jpg":      false,
		},
	}

	stats := &ProcessingStats{}
	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poolFunc := NewPoolFuncWithProcessor(fp, q, "/tmp/Images", testRemovePrefix, stats)
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

	// Should submit only the "new" file. Already-existing file must not be submitted.
	if len(calls) != 1 {
		t.Errorf("SubmitFileForWrite call count: got %d, want 1 (existing file must not be submitted)", len(calls))
		for i, c := range calls {
			t.Logf("  call %d: path=%q Exists=%v", i, c.Path, c.Exists)
		}
	}
	for i, c := range calls {
		if c.Exists {
			t.Errorf("SubmitFileForWrite call %d: path=%q has Exists=true (already in DB must not be submitted)", i, c.Path)
		}
	}
}

func TestNewPoolFuncWithProcessor_Stats(t *testing.T) {
	// Setup
	q := queue.NewQueue[string](2)
	// Add 2 files
	q.Enqueue("/tmp/Images/existing.jpg")
	q.Enqueue("/tmp/Images/new.jpg")

	fp := &statsFakeProcessor{
		existsMap: map[string]bool{
			"existing.jpg": true,
			"new.jpg":      false,
		},
	}

	stats := &ProcessingStats{}

	// Create pool
	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create pool func with stats
	// NOTE: This will fail compilation until I update the signature of NewPoolFuncWithProcessor
	poolFunc := NewPoolFuncWithProcessor(fp, q, "/tmp/Images", testRemovePrefix, stats)

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

	// Verify Stats
	if val := stats.TotalFound.Load(); val != 2 {
		t.Errorf("TotalFound: got %d, want 2", val)
	}
	if val := stats.AlreadyExisting.Load(); val != 1 {
		t.Errorf("AlreadyExisting: got %d, want 1", val)
	}
	if val := stats.NewlyInserted.Load(); val != 1 {
		t.Errorf("NewlyInserted: got %d, want 1", val)
	}
	if val := stats.InFlight.Load(); val != 0 {
		t.Errorf("InFlight: got %d, want 0", val)
	}
}
