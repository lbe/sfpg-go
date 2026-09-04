package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/queue"
)

// TestWalkImageDir_EnqueuesOnlySupportedNonZeroImages verifies that WalkImageDir()
// enqueues only non-zero-sized files with extensions matching (jpg|jpeg|png|gif),
// and skips zero-length and non-image files.
func TestWalkImageDir_EnqueuesOnlySupportedNonZeroImages(t *testing.T) {
	imagesDir := t.TempDir()

	// Create a small set of files in the Images directory
	mustWrite := func(rel string, size int) string {
		p := filepath.Join(imagesDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir failed for %s: %v", p, err)
		}
		var data []byte
		if size > 0 {
			data = make([]byte, size)
			for i := range data {
				data[i] = byte(i%251 + 1)
			}
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatalf("write failed for %s: %v", p, err)
		}
		return p
	}

	// Supported and non-zero
	a := mustWrite("a.jpg", 10)
	b := mustWrite("b.jpeg", 1)
	c := mustWrite("c.png", 2)
	d := mustWrite("d.gif", 3)
	e := mustWrite("UPPER.JPG", 4)
	// Nested
	f := mustWrite("nested/x.jpg", 5)
	// Zero-length should be ignored
	_ = mustWrite("zero.jpg", 0)
	_ = mustWrite("nested/zero.png", 0)
	// Unsupported extensions should be ignored by WalkImageDir's regex
	_ = mustWrite("photo.webp", 8)
	_ = mustWrite("doc.txt", 12)
	_ = mustWrite("image.tiff", 14)

	// Create queue and deps
	q := queue.NewQueue[string](100)
	var wg sync.WaitGroup
	var qSendersActive atomic.Int64

	deps := &WalkDeps{
		Wg:             &wg,
		QSendersActive: &qSendersActive,
		Ctx:            context.Background(),
		ImagesDir:      imagesDir,
		Q:              q,
	}

	// Execute WalkImageDir synchronously
	WalkImageDir(deps)

	// Collect queued items; order is not guaranteed, so sort for comparison
	got := q.Slice()
	sort.Strings(got)

	want := []string{a, b, c, d, e, f}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("unexpected queue length: got %d, want %d; got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mismatch at %d: got %q, want %q\nall got=%v\nall want=%v", i, got[i], want[i], got, want)
		}
	}

	// Ensure sender accounting returned to zero
	if qSendersActive.Load() != 0 {
		t.Fatalf("qSendersActive not zero after walk: %d", qSendersActive.Load())
	}
}

// TestWalkImageDir_CompletesWithFiles verifies that WalkImageDir completes
// successfully when there are files to process, enqueues the expected files,
// and resets sender accounting after completion.
// This is the walker-level equivalent of the original "UpdatesModuleState" test.
func TestWalkImageDir_CompletesWithFiles(t *testing.T) {
	imagesDir := t.TempDir()

	// Create a few image files
	for _, name := range []string{"a.jpg", "b.png", "sub/c.gif"} {
		p := filepath.Join(imagesDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte{1, 2, 3, 4, 5}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	q := queue.NewQueue[string](100)
	var wg sync.WaitGroup
	var qSendersActive atomic.Int64

	deps := &WalkDeps{
		Wg:             &wg,
		QSendersActive: &qSendersActive,
		Ctx:            context.Background(),
		ImagesDir:      imagesDir,
		Q:              q,
	}

	// This call blocks until walk completes (synchronous WalkImageDir).
	WalkImageDir(deps)

	// Verify qSendersActive returned to zero after completion
	if qSendersActive.Load() != 0 {
		t.Fatalf("qSendersActive not zero after walk: %d", qSendersActive.Load())
	}

	// Verify at least the expected number of files were enqueued
	got := q.Slice()
	if len(got) < 3 {
		t.Errorf("expected at least 3 files in queue, got %d: %v", len(got), got)
	}
}

// TestWalkImageDir_CancelledContext verifies that WalkImageDir handles
// context cancellation gracefully.
func TestWalkImageDir_CancelledContext(t *testing.T) {
	imagesDir := t.TempDir()

	// Create some files
	p := filepath.Join(imagesDir, "test.jpg")
	if err := os.WriteFile(p, []byte{1, 2, 3}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Use a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	q := queue.NewQueue[string](100)
	var wg sync.WaitGroup
	var qSendersActive atomic.Int64

	deps := &WalkDeps{
		Wg:             &wg,
		QSendersActive: &qSendersActive,
		Ctx:            ctx,
		ImagesDir:      imagesDir,
		Q:              q,
	}

	WalkImageDir(deps)

	// With cancelled context, the walker should complete without error
	// and qSendersActive should be 0
	if qSendersActive.Load() != 0 {
		t.Fatalf("qSendersActive not zero after cancelled context: %d", qSendersActive.Load())
	}
}

// TestWalkImageDir_BoundedQueue verifies that WalkImageDir works correctly with
// a bounded queue that is large enough to hold all discovered files.
func TestWalkImageDir_BoundedQueue(t *testing.T) {
	imagesDir := t.TempDir()

	for i := range 10 {
		p := filepath.Join(imagesDir, fmt.Sprintf("img%d.jpg", i))
		if err := os.WriteFile(p, []byte{1, 2, 3, 4, 5}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Use a bounded queue large enough to hold all items.
	q := queue.NewBoundedQueue[string](16, 20)
	var wg sync.WaitGroup
	var qSendersActive atomic.Int64

	deps := &WalkDeps{
		Wg:             &wg,
		QSendersActive: &qSendersActive,
		Ctx:            context.Background(),
		ImagesDir:      imagesDir,
		Q:              q,
	}

	WalkImageDir(deps)

	got := q.Slice()
	if len(got) != 10 {
		t.Fatalf("expected 10 items in queue, got %d: %v", len(got), got)
	}
	if qSendersActive.Load() != 0 {
		t.Fatalf("qSendersActive not zero after walk: %d", qSendersActive.Load())
	}
}

// TestWalkImageDir_BackpressureOnFullQueue verifies that WalkImageDir handles
// ErrQueueFull gracefully when the queue is bounded and full, by processing
// items concurrently (simulating a real consumer).
func TestWalkImageDir_BackpressureOnFullQueue(t *testing.T) {
	imagesDir := t.TempDir()

	// Create files — more than the queue can hold.
	for i := range 10 {
		p := filepath.Join(imagesDir, fmt.Sprintf("img%d.jpg", i))
		if err := os.WriteFile(p, []byte{1, 2, 3, 4, 5}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Use a bounded queue with capacity 4.
	q := queue.NewBoundedQueue[string](8, 4)
	var wg sync.WaitGroup
	var qSendersActive atomic.Int64

	deps := &WalkDeps{
		Wg:             &wg,
		QSendersActive: &qSendersActive,
		Ctx:            context.Background(),
		ImagesDir:      imagesDir,
		Q:              q,
	}

	// Start a concurrent consumer that drains the queue as items arrive.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var consumerWg sync.WaitGroup
	consumerWg.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, err := q.Dequeue()
			if errors.Is(err, queue.ErrEmptyQueue) {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			if err != nil {
				return
			}
		}
	})

	// Run the walker — it should complete despite the bounded queue
	// because the consumer drains items, creating space for backpressure.
	WalkImageDir(deps)

	// Stop the consumer.
	cancel()
	consumerWg.Wait()

	// The key verification: WalkImageDir completed without error and
	// all sender accounting was properly reset.
	if qSendersActive.Load() != 0 {
		t.Fatalf("qSendersActive not zero after walk: %d", qSendersActive.Load())
	}

	// At least 4 items should have been enqueued (the queue capacity).
	// The consumer may have dequeued some, so remaining items may be fewer.
	remaining := q.Len()
	if remaining > 4 {
		t.Fatalf("unexpected remaining items in queue: %d (expected 0-4)", remaining)
	}
}
