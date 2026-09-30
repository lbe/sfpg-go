package writebatcher

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestFlush_OnMaxBatchSize(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var mu sync.Mutex
	var batches [][]int
	flushCh := make(chan struct{}, 4)

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			mu.Lock()
			defer mu.Unlock()
			b := make([]int, len(batch))
			copy(b, batch)
			batches = append(batches, b)
			flushCh <- struct{}{}
			return nil
		},
		MaxBatchSize:  3,
		FlushInterval: 10 * time.Second,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	// Submit 3 items (triggers flush)
	if err := wb.Submit(1); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(2); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(3); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitForFlushes(t, flushCh, 1, 20*time.Second)

	mu.Lock()
	if len(batches) != 1 {
		t.Errorf("expected 1 flush, got %d", len(batches))
	} else if len(batches[0]) != 3 {
		t.Errorf("expected batch size 3, got %d", len(batches[0]))
	}
	mu.Unlock()

	// Submit 3 more
	if err := wb.Submit(4); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(5); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(6); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitForFlushes(t, flushCh, 1, 20*time.Second)

	mu.Lock()
	if len(batches) != 2 {
		t.Errorf("expected 2 flushes total, got %d", len(batches))
	}
	mu.Unlock()
}

func TestFlush_OnMaxBatchBytes(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var mu sync.Mutex
	var batches [][]testItem
	flushCh := make(chan struct{}, 4)

	cfg := Config[testItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []testItem) error {
			mu.Lock()
			defer mu.Unlock()
			b := make([]testItem, len(batch))
			copy(b, batch)
			batches = append(batches, b)
			flushCh <- struct{}{}
			return nil
		},
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
		MaxBatchBytes: 30,
		SizeFunc: func(item testItem) int64 {
			return int64(item.Size)
		},
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	// Submit 3 items each with Size=10 (total 30 bytes -> triggers flush)
	if err := wb.Submit(testItem{Size: 10}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(testItem{Size: 10}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(testItem{Size: 10}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitForFlushes(t, flushCh, 1, 20*time.Second)

	mu.Lock()
	if len(batches) != 1 {
		t.Errorf("expected 1 flush, got %d", len(batches))
	} else if len(batches[0]) != 3 {
		t.Errorf("expected batch size 3, got %d", len(batches[0]))
	}
	mu.Unlock()

	// Submit 1 item with Size=30 (single item >= threshold -> triggers flush)
	if err := wb.Submit(testItem{Size: 30}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitForFlushes(t, flushCh, 1, 20*time.Second)

	mu.Lock()
	if len(batches) != 2 {
		t.Errorf("expected 2 flushes total, got %d", len(batches))
	}
	mu.Unlock()
}

func TestFlush_BytesBeforeCount(t *testing.T) {
	db := testDB(t)
	var mu sync.Mutex
	var batches [][]testItem
	flushCh := make(chan struct{}, 4)
	cfg := Config[testItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, b []testItem) error {
			mu.Lock()
			batches = append(batches, copyBatch(b))
			mu.Unlock()
			flushCh <- struct{}{}
			return nil
		},
		MaxBatchSize:  10,
		MaxBatchBytes: 20,
		SizeFunc:      func(i testItem) int64 { return int64(i.Size) },
	}
	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	if err := wb.Submit(testItem{Size: 5}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(testItem{Size: 5}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(testItem{Size: 5}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(testItem{Size: 5}); err != nil { // total 20 bytes -> should flush
		t.Fatalf("Submit: %v", err)
	}

	waitForFlushes(t, flushCh, 1, 20*time.Second)

	mu.Lock()
	if len(batches) != 1 {
		t.Errorf("expected 1 flush, got %d", len(batches))
	} else if len(batches[0]) != 4 {
		t.Errorf("expected 4 items (20 bytes), got %d", len(batches[0]))
	}
	mu.Unlock()
}

func TestFlush_CountBeforeBytes(t *testing.T) {
	db := testDB(t)
	var mu sync.Mutex
	var batches [][]testItem
	flushCh := make(chan struct{}, 4)
	cfg := Config[testItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, b []testItem) error {
			mu.Lock()
			batches = append(batches, copyBatch(b))
			mu.Unlock()
			flushCh <- struct{}{}
			return nil
		},
		MaxBatchSize:  3,
		MaxBatchBytes: 1000,
		SizeFunc:      func(i testItem) int64 { return int64(i.Size) },
	}
	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	if err := wb.Submit(testItem{Size: 1}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(testItem{Size: 1}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(testItem{Size: 1}); err != nil { // count is 3 -> should flush
		t.Fatalf("Submit: %v", err)
	}

	waitForFlushes(t, flushCh, 1, 20*time.Second)

	mu.Lock()
	if len(batches) != 1 {
		t.Errorf("expected 1 flush, got %d", len(batches))
	} else if len(batches[0]) != 3 {
		t.Errorf("expected 3 items, got %d", len(batches[0]))
	}
	mu.Unlock()
}

func TestFlush_TimeoutWithPartialBytes(t *testing.T) {
	db := testDB(t)
	var mu sync.Mutex
	var batches [][]testItem
	flushCh := make(chan struct{}, 4)
	cfg := Config[testItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, b []testItem) error {
			mu.Lock()
			batches = append(batches, copyBatch(b))
			mu.Unlock()
			flushCh <- struct{}{}
			return nil
		},
		MaxBatchSize:  100,
		MaxBatchBytes: 1000,
		FlushInterval: 50 * time.Millisecond,
		SizeFunc:      func(i testItem) int64 { return int64(i.Size) },
	}
	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	if err := wb.Submit(testItem{Size: 5}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(testItem{Size: 5}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitForFlushes(t, flushCh, 1, 20*time.Second)

	mu.Lock()
	if len(batches) != 1 {
		t.Errorf("expected 1 flush by timeout, got %d", len(batches))
	} else if len(batches[0]) != 2 {
		t.Errorf("expected 2 items, got %d", len(batches[0]))
	}
	mu.Unlock()
}

func TestClose_FlushesRemainingWithBytes(t *testing.T) {
	db := testDB(t)
	var mu sync.Mutex
	var batches [][]testItem
	cfg := Config[testItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, b []testItem) error {
			mu.Lock()
			batches = append(batches, copyBatch(b))
			mu.Unlock()
			return nil
		},
		MaxBatchSize:  100,
		MaxBatchBytes: 1000,
		FlushInterval: 10 * time.Second,
		SizeFunc:      func(i testItem) int64 { return int64(i.Size) },
	}
	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := wb.Submit(testItem{Size: 5}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(testItem{Size: 5}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	_ = wb.Close()

	mu.Lock()
	if len(batches) != 1 {
		t.Errorf("expected 1 flush on close, got %d", len(batches))
	} else if len(batches[0]) != 2 {
		t.Errorf("expected 2 items, got %d", len(batches[0]))
	}
	mu.Unlock()
}

// TestFlush_MaxBatchBytesZero_SizeFuncSet verifies that when MaxBatchBytes is 0
// and SizeFunc is set, size tracking runs but never triggers a flush (only count
// or interval or Close can trigger).
func TestFlush_MaxBatchBytesZero_SizeFuncSet(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	var mu sync.Mutex
	var batches [][]testItem
	cfg := Config[testItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, b []testItem) error {
			mu.Lock()
			batches = append(batches, copyBatch(b))
			mu.Unlock()
			return nil
		},
		MaxBatchSize:  100,
		MaxBatchBytes: 0, // size never triggers
		FlushInterval: 10 * time.Second,
		SizeFunc:      func(i testItem) int64 { return int64(i.Size) },
	}
	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	// Submit several items; total bytes would exceed any reasonable limit, but
	// with MaxBatchBytes=0 no size-based flush should occur.
	if err := wb.Submit(testItem{Size: 100}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(testItem{Size: 100}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(testItem{Size: 100}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// No size-based flush can have occurred: MaxBatchBytes=0 never triggers,
	// the batch is below MaxBatchSize, and FlushInterval is 10s. All three
	// items must still be pending.
	if pc := wb.PendingCount(); pc != 3 {
		t.Errorf("expected all 3 items still pending (no size-based flush), got %d", pc)
	}

	_ = wb.Close()
	mu.Lock()
	if len(batches) != 1 || len(batches[0]) != 3 {
		t.Errorf("expected 1 flush with 3 items on Close, got %d batches", len(batches))
	}
	mu.Unlock()
}

func TestFlush_DropWithoutFlushSkipsBeginTx(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var beginTxCount, flushCallCount, afterCommitCount, successCount atomic.Int64
	flushCh := make(chan struct{}, 1)

	cfg := Config[int]{
		BeginTx: func(ctx context.Context) (*sql.Tx, error) {
			beginTxCount.Add(1)
			return testBeginTx(db)(ctx)
		},
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			flushCallCount.Add(1)
			return nil
		},
		OnAfterCommit: func(ctx context.Context, lastWalCheckpoint time.Time, lastOptimize time.Time, totalCommitted int64, postFlush bool) {
			afterCommitCount.Add(1)
		},
		OnSuccess: func(batch []int) {
			successCount.Add(1)
			flushCh <- struct{}{}
		},
		DropWithoutFlush: func(batch []int) bool { return true },
		MaxBatchSize:     3,
		FlushInterval:    10 * time.Second,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	for i := range 3 {
		if err := wb.Submit(i); err != nil {
			t.Fatalf("Submit(%d): %v", i, err)
		}
	}

	waitForFlushes(t, flushCh, 1, 10*time.Second)

	if beginTxCount.Load() != 0 {
		t.Errorf("BeginTx called %d times, want 0 (DropWithoutFlush should skip BeginTx)", beginTxCount.Load())
	}
	if flushCallCount.Load() != 0 {
		t.Errorf("Flush called %d times, want 0 (DropWithoutFlush should skip Flush)", flushCallCount.Load())
	}
	if afterCommitCount.Load() != 0 {
		t.Errorf("OnAfterCommit called %d times, want 0 (DropWithoutFlush should skip OnAfterCommit)", afterCommitCount.Load())
	}
	if successCount.Load() == 0 {
		t.Error("OnSuccess was not called (DropWithoutFlush must still call OnSuccess)")
	}

	// PendingCount must drop even on the drop path (via the existing defer).
	waitForPendingZero(t, wb, 5*time.Second)
}

func TestFlush_DropWithoutFlushFalseStillBeginTx(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var beginTxCount atomic.Int64
	flushCh := make(chan struct{}, 1)

	cfg := Config[int]{
		BeginTx: func(ctx context.Context) (*sql.Tx, error) {
			beginTxCount.Add(1)
			return testBeginTx(db)(ctx)
		},
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			flushCh <- struct{}{}
			return nil
		},
		DropWithoutFlush: func(batch []int) bool { return false },
		MaxBatchSize:     3,
		FlushInterval:    10 * time.Second,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	for i := range 3 {
		if err := wb.Submit(i); err != nil {
			t.Fatalf("Submit(%d): %v", i, err)
		}
	}

	waitForFlushes(t, flushCh, 1, 10*time.Second)

	if beginTxCount.Load() == 0 {
		t.Error("BeginTx was not called (DropWithoutFlush false should proceed to BeginTx)")
	}
}

func TestFlush_OnInterval(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var mu sync.Mutex
	var flushedItems []int
	flushCh := make(chan struct{}, 4)

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			mu.Lock()
			flushedItems = append(flushedItems, batch...)
			mu.Unlock()
			flushCh <- struct{}{}
			return nil
		},
		MaxBatchSize:  10,
		FlushInterval: 100 * time.Millisecond,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	if err := wb.Submit(1); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitForFlushes(t, flushCh, 1, 20*time.Second)

	mu.Lock()
	if len(flushedItems) != 1 {
		t.Errorf("expected 1 item flushed on interval, got %d", len(flushedItems))
	} else if flushedItems[0] != 1 {
		t.Errorf("expected item 1, got %d", flushedItems[0])
	}
	mu.Unlock()
}

// TestFlush_OnInterval_WithDQueDirPath verifies that an idle drain poll
// (drained==0) does not trigger drain_end — the channel batch waits for
// the worker-main interval flush. Fail if GREEN fires drain_end on
// empty-dque+leftover.
func TestFlush_OnInterval_WithDQueDirPath(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	dir := t.TempDir()
	flushCh := make(chan struct{}, 1)

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			flushCh <- struct{}{}
			return nil
		},
		MaxBatchSize:  10,
		FlushInterval: 100 * time.Millisecond,
		DQueDirPath:   dir,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	if err := wb.Submit(1); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Idle drain (drained==0) must not steal the channel interval.
	select {
	case <-flushCh:
		t.Fatal("flush happened before FlushInterval (drain_end must not fire on idle poll)")
	case <-time.After(cfg.FlushInterval / 2):
	}
	waitForFlushes(t, flushCh, 1, 5*time.Second)
}

func TestClose_DrainsRemaining(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var mu sync.Mutex
	var flushedItems []int

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			mu.Lock()
			flushedItems = append(flushedItems, batch...)
			mu.Unlock()
			return nil
		},
		MaxBatchSize:  10,
		FlushInterval: 10 * time.Second,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := wb.Submit(1); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(2); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Close immediately. Should flush the 2 items.
	if err := wb.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	mu.Lock()
	if len(flushedItems) != 2 {
		t.Errorf("expected 2 items flushed on close, got %d", len(flushedItems))
	}
	mu.Unlock()
}

func TestOnError(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var mu sync.Mutex
	var errReported error
	var batchReported []int
	expectedErr := errors.New("flush failed")
	flushCh := make(chan struct{}, 8)

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			return expectedErr
		},
		OnError: func(err error, batch []int) {
			mu.Lock()
			errReported = err
			batchReported = batch
			mu.Unlock()
			select {
			case flushCh <- struct{}{}:
			default:
			}
		},
		MaxBatchSize: 1,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	if err := wb.Submit(123); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitForFlushes(t, flushCh, 1, 20*time.Second)

	mu.Lock()
	if !errors.Is(errReported, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, errReported)
	}
	if len(batchReported) != 1 || batchReported[0] != 123 {
		t.Errorf("expected batch [123], got %v", batchReported)
	}
	mu.Unlock()
}

func TestContextCancellation(t *testing.T) {
	db := testDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			return nil
		},
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Worker should exit immediately or shortly after
	select {
	case <-wb.done:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Error("worker did not exit on cancelled context")
	}
}
