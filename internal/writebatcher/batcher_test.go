package writebatcher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestNew(t *testing.T) {
	ctx := context.Background()

	t.Run("returns error when BeginTx is nil", func(t *testing.T) {
		cfg := Config[int]{
			Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		}
		_, err := New(ctx, cfg)
		if err == nil {
			t.Error("expected error when BeginTx is nil")
		}
	})

	t.Run("returns error when Flush is nil", func(t *testing.T) {
		cfg := Config[int]{
			BeginTx: func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
		}
		_, err := New(ctx, cfg)
		if err == nil {
			t.Error("expected error when Flush is nil")
		}
	})

	t.Run("returns error when MaxBatchBytes > 0 but SizeFunc is nil", func(t *testing.T) {
		cfg := Config[int]{
			BeginTx:       func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:         func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
			MaxBatchBytes: 1024,
			SizeFunc:      nil,
		}
		_, err := New(ctx, cfg)
		if err == nil {
			t.Error("expected error when MaxBatchBytes > 0 but SizeFunc is nil")
		}
	})

	t.Run("applies default values", func(t *testing.T) {
		cfg := Config[int]{
			BeginTx: func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:   func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		}
		wb, err := New(ctx, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer wb.Close()

		if wb.cfg.MaxBatchSize != 50 {
			t.Errorf("expected MaxBatchSize 50, got %d", wb.cfg.MaxBatchSize)
		}
		if wb.cfg.FlushInterval != 200*time.Millisecond {
			t.Errorf("expected FlushInterval 200ms, got %v", wb.cfg.FlushInterval)
		}
		if wb.cfg.ChannelSize != 1024 {
			t.Errorf("expected ChannelSize 1024, got %d", wb.cfg.ChannelSize)
		}
	})

	t.Run("can be closed immediately", func(t *testing.T) {
		cfg := Config[int]{
			BeginTx: func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:   func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		}
		wb, err := New(ctx, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := wb.Close(); err != nil {
			t.Errorf("Close() returned error: %v", err)
		}
	})
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testBeginTx(db *sql.DB) func(context.Context) (*sql.Tx, error) {
	return func(ctx context.Context) (*sql.Tx, error) {
		return db.BeginTx(ctx, nil)
	}
}

// testItem is used by size-based flush tests that need a Size field for SizeFunc.
type testItem struct{ Size int }

func waitForFlushes(t *testing.T, ch <-chan struct{}, count int, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	received := 0
	for received < count {
		select {
		case <-ch:
			received++
		case <-deadline.C:
			t.Fatalf("timed out waiting for %d flushes, got %d", count, received)
		}
	}
}

// waitForPendingZero polls until PendingCount reaches zero or the timeout
// elapses. It is used for drain-completion waits where flush signals are
// impractical (e.g. dque drain tests with unknown flush counts).
func waitForPendingZero[T any](t *testing.T, wb *WriteBatcher[T], timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for wb.PendingCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for PendingCount to reach 0 (got %d)", wb.PendingCount())
		}
		time.Sleep(time.Millisecond)
	}
}

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

func TestSubmit(t *testing.T) {
	ctx := context.Background()

	t.Run("sends item to channel", func(t *testing.T) {
		db := testDB(t)
		var mu sync.Mutex
		var flushed bool
		flushCh := make(chan struct{}, 4)
		cfg := Config[int]{
			BeginTx: testBeginTx(db),
			Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
				mu.Lock()
				flushed = true
				mu.Unlock()
				flushCh <- struct{}{}
				return nil
			},
			MaxBatchSize:  1,
			FlushInterval: 10 * time.Second,
		}
		wb, err := New(ctx, cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer wb.Close()

		err = wb.Submit(42)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		waitForFlushes(t, flushCh, 1, 20*time.Second)

		mu.Lock()
		if !flushed {
			t.Error("expected item to be flushed")
		}
		mu.Unlock()
	})

	t.Run("returns ErrFull when channel is full", func(t *testing.T) {
		// Block the worker inside Flush so it cannot drain the channel. Sync on
		// Flush entry before asserting ErrFull so a racing drain cannot return
		// nil success.
		var holdMu sync.Mutex
		holdMu.Lock() // hold so Flush blocks until the test unlocks

		enteredFlush := make(chan struct{})
		var flushOnce sync.Once

		db := testDB(t)
		cfg := Config[int]{
			BeginTx: testBeginTx(db),
			Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
				flushOnce.Do(func() { close(enteredFlush) })
				holdMu.Lock()  // blocks until test unlocks
				_ = len(batch) // use batch so critical section is non-empty (SA2001)
				holdMu.Unlock()
				return nil
			},
			MaxBatchSize: 1,
			ChannelSize:  1,
			// DQueDirPath is empty — no overflow absorb.
		}
		wb, err := New(ctx, cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer func() {
			holdMu.Unlock() // unblock the worker so Close() can complete
			wb.Close()
		}()

		_ = wb.Submit(1) // fills channel or is picked up by the worker

		// Wait until the worker has consumed item 1 and is blocked in Flush.
		select {
		case <-enteredFlush:
		case <-time.After(20 * time.Second):
			t.Fatal("timed out waiting for Flush to block on the held mutex")
		}

		_ = wb.Submit(2)   // fills the channel (worker is blocked in Flush)
		err = wb.Submit(3) // should return ErrFull
		if !errors.Is(err, ErrFull) {
			t.Errorf("expected ErrFull, got %v", err)
		}
	})

	t.Run("returns ErrClosed when batcher is closed", func(t *testing.T) {
		db := testDB(t)
		cfg := Config[int]{
			BeginTx: testBeginTx(db),
			Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
				return nil
			},
		}
		wb, err := New(ctx, cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_ = wb.Close()

		err = wb.Submit(1)
		if !errors.Is(err, ErrClosed) {
			t.Errorf("expected ErrClosed, got %v", err)
		}
	})
}

func TestOnError_NilCallback_DoesNotPanic(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	flushCh := make(chan struct{}, 8)
	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			select {
			case flushCh <- struct{}{}:
			default:
			}
			return errors.New("intentional failure")
		},
		OnError:      nil, // explicitly nil
		MaxBatchSize: 1,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer wb.Close()

	// Submit an item that will trigger a flush and fail.
	// Should not panic -- slog fallback handles it.
	if err := wb.Submit(42); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitForFlushes(t, flushCh, 1, 20*time.Second)
}

func TestClose_Idempotent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

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

	// First close should succeed
	if err := wb.Close(); err != nil {
		t.Errorf("first Close() returned error: %v", err)
	}

	// Second close should not panic and should return nil
	if err := wb.Close(); err != nil {
		t.Errorf("second Close() returned error: %v", err)
	}
}

func TestIntegration_WritesToSQLite(t *testing.T) {
	db := testDB(t)
	_, err := db.Exec("CREATE TABLE test_items (id INTEGER PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	ctx := context.Background()
	cfg := Config[string]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []string) error {
			stmt, prepErr := tx.PrepareContext(ctx, "INSERT INTO test_items (value) VALUES (?)")
			if prepErr != nil {
				return fmt.Errorf("prepare: %w", prepErr)
			}
			defer stmt.Close()
			for _, item := range batch {
				if _, execErr := stmt.ExecContext(ctx, item); execErr != nil {
					return fmt.Errorf("exec: %w", err)
				}
			}
			return nil
		},
		MaxBatchSize: 5,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := range 10 {
		if err := wb.Submit(fmt.Sprintf("item-%d", i)); err != nil {
			t.Fatalf("Submit(%d): %v", i, err)
		}
	}

	if err := wb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM test_items").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 10 {
		t.Errorf("expected 10 rows, got %d", count)
	}
}

func TestIntegration_RollbackOnError(t *testing.T) {
	db := testDB(t)
	_, err := db.Exec("CREATE TABLE test_rollback (id INTEGER PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	ctx := context.Background()

	var mu sync.Mutex
	var onErrorCalled bool
	flushCh := make(chan struct{}, 8)

	cfg := Config[string]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []string) error {
			// Insert one row, then return error.  The insert should be rolled back.
			_, _ = tx.ExecContext(ctx, "INSERT INTO test_rollback (value) VALUES (?)", batch[0])
			select {
			case flushCh <- struct{}{}:
			default:
			}
			return errors.New("intentional failure")
		},
		OnError: func(err error, batch []string) {
			mu.Lock()
			onErrorCalled = true
			mu.Unlock()
		},
		MaxBatchSize: 3,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := wb.Submit("a"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit("b"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit("c"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitForFlushes(t, flushCh, 1, 20*time.Second)
	_ = wb.Close()

	mu.Lock()
	if !onErrorCalled {
		t.Error("expected OnError to be called")
	}
	mu.Unlock()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM test_rollback").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows after rollback, got %d", count)
	}
}

func TestOnSuccess(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var mu sync.Mutex
	var successBatch []int
	flushCh := make(chan struct{}, 4)

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			return nil
		},
		OnSuccess: func(batch []int) {
			mu.Lock()
			successBatch = make([]int, len(batch))
			copy(successBatch, batch)
			mu.Unlock()
			select {
			case flushCh <- struct{}{}:
			default:
			}
		},
		MaxBatchSize: 2,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	if err := wb.Submit(10); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := wb.Submit(20); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitForFlushes(t, flushCh, 1, 20*time.Second)

	mu.Lock()
	if len(successBatch) != 2 || successBatch[0] != 10 || successBatch[1] != 20 {
		t.Errorf("expected successBatch [10, 20], got %v", successBatch)
	}
	mu.Unlock()
}

func TestConcurrent_SubmitFromMultipleGoroutines(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var totalFlushed atomic.Int64

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			totalFlushed.Add(int64(len(batch)))
			return nil
		},
		MaxBatchSize:  10,
		FlushInterval: 50 * time.Millisecond,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const goroutines = 20
	const itemsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := range goroutines {
		go func(base int) {
			defer wg.Done()
			for i := range itemsPerGoroutine {
				for {
					err := wb.Submit(base*itemsPerGoroutine + i)
					if errors.Is(err, ErrFull) {
						// WALL_OK: ErrFull backoff — brief sleep before retrying a full channel.
						time.Sleep(time.Millisecond)
						continue
					}
					if err != nil {
						t.Errorf("Submit: %v", err)
					}
					break
				}
			}
		}(g)
	}

	wg.Wait()
	if err := wb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	total := totalFlushed.Load()
	expected := int64(goroutines * itemsPerGoroutine)
	if total != expected {
		t.Errorf("expected %d items flushed, got %d", expected, total)
	}
}

// TestSubmit_OverflowToDQue tests that when the channel is full and dque is

// TestSubmit_DQueDisabled_ReturnsErrFull tests that when DQueDirPath is empty,
// Submit returns ErrFull when the channel is full (unchanged behavior).
func TestSubmit_DQueDisabled_ReturnsErrFull(t *testing.T) {
	var blockMu sync.Mutex
	blockMu.Lock()
	flushEntered := make(chan struct{})
	var flushOnce sync.Once

	db := testDB(t)
	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			flushOnce.Do(func() { close(flushEntered) })
			blockMu.Lock()
			_ = len(batch)
			blockMu.Unlock()
			return nil
		},
		MaxBatchSize: 1,
		ChannelSize:  1,
		// DQueDirPath is empty — no dque
	}
	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		blockMu.Unlock()
		wb.Close()
	}()

	_ = wb.Submit(1)
	<-flushEntered // worker has consumed item 1 and is blocked in Flush
	_ = wb.Submit(2)
	err = wb.Submit(3)
	if !errors.Is(err, ErrFull) {
		t.Errorf("expected ErrFull, got %v", err)
	}
}

// TestSubmit_Overflow_IncrementsOverflowCount tests that each overflow

// TestSubmit_Overflow_IncrementsPendingCount tests that overflow items

// TestSubmit_Overflow_SendsDqNotify tests that dqNotify receives a signal

// TestSubmit_AfterClose_ReturnsErrClosed tests that after Close, Submit
// returns ErrClosed for both the channel path and the overflow path.
func TestSubmit_AfterClose_ReturnsErrClosed(t *testing.T) {
	t.Run("without dque", func(t *testing.T) {
		db := testDB(t)
		cfg := Config[int]{
			BeginTx: testBeginTx(db),
			Flush:   func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		}
		wb, err := New(context.Background(), cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_ = wb.Close()

		err = wb.Submit(1)
		if !errors.Is(err, ErrClosed) {
			t.Errorf("expected ErrClosed, got %v", err)
		}
	})

	t.Run("with dque", func(t *testing.T) {
		dir := t.TempDir()

		type overflowItem struct {
			Val int
		}

		db := testDB(t)
		cfg := Config[overflowItem]{
			BeginTx:     testBeginTx(db),
			Flush:       func(ctx context.Context, tx *sql.Tx, batch []overflowItem) error { return nil },
			DQueDirPath: dir,
		}
		wb, err := New(context.Background(), cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_ = wb.Close()

		err = wb.Submit(overflowItem{Val: 1})
		if !errors.Is(err, ErrClosed) {
			t.Errorf("expected ErrClosed for channel path with dque, got %v", err)
		}
	})
}

// TestSubmit_FastPath_NoOverflowMu tests that channel-path submissions
// complete without acquiring overflowMu.
func TestSubmit_FastPath_NoOverflowMu(t *testing.T) {
	db := testDB(t)
	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush:   func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
	}
	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	// Lock overflowMu. If fast-path Submit tried to acquire overflowMu,
	// this would cause a self-deadlock.
	wb.overflowMu.Lock()
	defer wb.overflowMu.Unlock()

	// Submit to the channel — must succeed without touching overflowMu.
	err = wb.Submit(42)
	if err != nil {
		t.Errorf("fast-path Submit returned error: %v", err)
	}
}

func TestBatchSliceReuse(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var mu sync.Mutex
	var caps []int
	flushCh := make(chan struct{}, 4)

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			mu.Lock()
			caps = append(caps, cap(batch))
			mu.Unlock()
			flushCh <- struct{}{}
			return nil
		},
		MaxBatchSize:  5,
		FlushInterval: 10 * time.Second,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	// First flush: 5 items
	for i := range 5 {
		if err := wb.Submit(i); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	waitForFlushes(t, flushCh, 1, 20*time.Second)

	// Second flush: 5 more items (batch slice should be reused)
	for i := 5; i < 10; i++ {
		if err := wb.Submit(i); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	waitForFlushes(t, flushCh, 1, 20*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(caps) != 2 {
		t.Fatalf("expected 2 flushes, got %d", len(caps))
	}
	for i, c := range caps {
		if c < 5 {
			t.Errorf("flush %d: expected cap(batch) >= 5, got %d", i+1, c)
		}
	}
}

// TestClose_DrainsDQue verifies that Close drains all items from both the
// channel and the dque overflow queue before returning. It forces overflow
// by blocking the worker, then calls Close and checks that every submitted

// TestClose_WithOverflowInFlight verifies that concurrent Submits during Close
// do not lose items. It runs a goroutine that submits while Close is in
// progress and checks that the total flushed count matches the total

// TestClose_OverflowMuBarrier verifies that Close acquires overflowMu after mu,

// TestClose_DoesNotPanicOnEmptyDque verifies that Close handles an empty dque

// TestPendingCount_NeverNegative verifies that pendingCount never drops below
// zero during normal operation. It submits 100 items (mixed channel and
// overflow), monitors pendingCount in a background goroutine, and asserts

// TestPendingCount_CrashRecovery simulates a process crash where items were
// persisted in the dque but never flushed. It creates a dque directly,
// enqueues items, closes the dque, then creates a new batcher pointing to
// the same directory. It verifies pendingCount is initialised to the dque

// TestGetStats_WithDQue verifies that when dque is configured, GetStats reports
// DQueEnabled=true, DQueSize reflects the current dque queue depth (0 initially,
// >0 after overflow, 0 after flush), and OverflowCount increments with each

// TestGetStats_WithoutDQue verifies that when dque is not configured, GetStats
// reports DQueEnabled=false, DQueSize=0, and OverflowCount=0.
func TestGetStats_WithoutDQue(t *testing.T) {
	db := testDB(t)
	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush:   func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	stats := wb.GetStats()
	if stats.DQueEnabled {
		t.Error("expected DQueEnabled to be false when DQueDirPath is empty")
	}
	if stats.DQueSize != 0 {
		t.Errorf("expected DQueSize = 0 without dque, got %d", stats.DQueSize)
	}
	if stats.OverflowCount != 0 {
		t.Errorf("expected OverflowCount = 0 without dque, got %d", stats.OverflowCount)
	}
}

// TestGetStats_DQueSize_AfterFlush verifies that DQueSize reports 0 after

// Test_E2E_OverflowAbsorbsBurst tests that a WriteBatcher with a small channel

// Test_E2E_CrashRecovery tests that items persisted in a dque survive a

// TestFlush_FailureReEnqueuesBatch verifies that when a flush fails,
// the batch items are re-submitted and eventually committed.
func TestFlush_FailureReEnqueuesBatch(t *testing.T) {
	var mu sync.Mutex
	var callCount int
	var finalFlushed []int
	flushCh := make(chan struct{}, 8)

	db := testDB(t)
	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			mu.Lock()
			callCount++
			isFirstCall := callCount == 1
			mu.Unlock()
			select {
			case flushCh <- struct{}{}:
			default:
			}
			if isFirstCall {
				return errors.New("simulated flush failure")
			}
			mu.Lock()
			finalFlushed = append(finalFlushed, batch...)
			mu.Unlock()
			return nil
		},
		MaxBatchSize:  10,
		FlushInterval: time.Second,
		ChannelSize:   100,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := range 15 {
		if err := wb.Submit(i); err != nil {
			t.Fatalf("Submit(%d): %v", i, err)
		}
	}

	// Wait for the failing flush (call 1) and the successful re-enqueued flush
	// (call 2); any remaining items are flushed by Close below.
	waitForFlushes(t, flushCh, 2, 20*time.Second)

	if err := wb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	flushedCount := len(finalFlushed)
	firstCall := callCount == 1 // verify only one call was needed (no retries needed after Close)
	mu.Unlock()

	if flushedCount != 15 {
		t.Errorf("expected 15 items flushed, got %d", flushedCount)
	}

	if cnt := wb.PendingCount(); cnt != 0 {
		t.Errorf("expected PendingCount() = 0, got %d", cnt)
	}

	if firstCall {
		t.Error("FlushFunc was never called successfully — re-enqueue may not have worked")
	}
}

func TestFlush_BeginTxFails(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var mu sync.Mutex
	var errReported error
	var batchReported []int
	beginAttempts := 0
	onErrorCh := make(chan struct{}, 8)

	cfg := Config[int]{
		BeginTx: func(ctx context.Context) (*sql.Tx, error) {
			mu.Lock()
			beginAttempts++
			if beginAttempts == 1 {
				mu.Unlock()
				return nil, errors.New("begin denied")
			}
			mu.Unlock()
			return testBeginTx(db)(ctx)
		},
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			return nil
		},
		OnError: func(err error, batch []int) {
			mu.Lock()
			defer mu.Unlock()
			errReported = err
			batchReported = append([]int(nil), batch...)
			select {
			case onErrorCh <- struct{}{}:
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

	if err := wb.Submit(42); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case <-onErrorCh:
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for OnError after BeginTx failure")
	}

	mu.Lock()
	defer mu.Unlock()
	if errReported == nil || !strings.Contains(errReported.Error(), "begin denied") {
		t.Errorf("expected 'begin denied' error, got %v", errReported)
	}
	if len(batchReported) != 1 || batchReported[0] != 42 {
		t.Errorf("expected batch [42], got %v", batchReported)
	}
	if wb.totalErrors.Load() < 1 {
		t.Errorf("expected totalErrors >= 1, got %d", wb.totalErrors.Load())
	}
}

func TestFlush_CommitFails(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	origCommit := commitTx
	commitTx = func(tx *sql.Tx) error { return errors.New("commit denied") }
	t.Cleanup(func() { commitTx = origCommit })

	rollbackCalled := make(chan struct{}, 1)
	origRollback := rollbackTx
	rollbackTx = func(tx *sql.Tx) error {
		select {
		case rollbackCalled <- struct{}{}:
		default:
		}
		return origRollback(tx)
	}
	t.Cleanup(func() { rollbackTx = origRollback })

	onErrorCalled := make(chan struct{}, 1)
	var mu sync.Mutex
	var errReported error

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			return nil
		},
		OnError: func(err error, batch []int) {
			mu.Lock()
			defer mu.Unlock()
			errReported = err
			select {
			case onErrorCalled <- struct{}{}:
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

	if err := wb.Submit(42); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case <-rollbackCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("rollback was not called after commit failure")
	}

	select {
	case <-onErrorCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("OnError was not called after commit failure")
	}

	// Close the batcher before asserting to stop the worker's retry loop.
	if err := wb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if errReported == nil || !strings.Contains(errReported.Error(), "commit denied") {
		t.Errorf("expected 'commit denied' error, got %v", errReported)
	}
}

func TestFlush_RollbackAfterFlushErrorFails(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	origRollback := rollbackTx
	rollbackTx = func(tx *sql.Tx) error { return errors.New("rollback denied") }
	t.Cleanup(func() { rollbackTx = origRollback })

	var mu sync.Mutex
	var errReported error
	onErrorCh := make(chan struct{}, 8)

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			return errors.New("flush denied")
		},
		OnError: func(err error, batch []int) {
			mu.Lock()
			defer mu.Unlock()
			errReported = err
			select {
			case onErrorCh <- struct{}{}:
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

	if err := wb.Submit(42); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case <-onErrorCh:
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for OnError after flush failure")
	}

	mu.Lock()
	defer mu.Unlock()
	if errReported == nil || !strings.Contains(errReported.Error(), "flush denied") {
		t.Errorf("expected 'flush denied' error, got %v", errReported)
	}
}

func TestFlush_RollbackAfterCommitErrorFails(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	commitTx = func(tx *sql.Tx) error { return errors.New("commit denied") }
	rollbackTx = func(tx *sql.Tx) error { return errors.New("rollback denied") }
	t.Cleanup(func() {
		commitTx = func(tx *sql.Tx) error { return tx.Commit() }
		rollbackTx = func(tx *sql.Tx) error { return tx.Rollback() }
	})

	var mu sync.Mutex
	var errReported error
	onErrorCh := make(chan struct{}, 8)

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			return nil
		},
		OnError: func(err error, batch []int) {
			mu.Lock()
			defer mu.Unlock()
			errReported = err
			select {
			case onErrorCh <- struct{}{}:
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

	if err := wb.Submit(42); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case <-onErrorCh:
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for OnError after commit failure")
	}

	mu.Lock()
	defer mu.Unlock()
	if errReported == nil || !strings.Contains(errReported.Error(), "commit denied") {
		t.Errorf("expected 'commit denied' error, got %v", errReported)
	}
}

func TestWorker_MaintenanceTimer(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var mu sync.Mutex
	var calls []struct {
		ctxCtx            context.Context
		lastWalCheckpoint time.Time
		lastOptimize      time.Time
		totalCommitted    int64
	}
	maintenanceCh := make(chan struct{}, 8)

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			return nil
		},
		OnAfterCommit: func(ctx context.Context, lastWalCheckpoint time.Time, lastOptimize time.Time, totalCommitted int64) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, struct {
				ctxCtx            context.Context
				lastWalCheckpoint time.Time
				lastOptimize      time.Time
				totalCommitted    int64
			}{ctxCtx: ctx, lastWalCheckpoint: lastWalCheckpoint, lastOptimize: lastOptimize, totalCommitted: totalCommitted})
			select {
			case maintenanceCh <- struct{}{}:
			default:
			}
		},
		MaxBatchSize:        10,
		FlushInterval:       10 * time.Second,
		MaintenanceInterval: 50 * time.Millisecond,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	if err := wb.Submit(1); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for two OnAfterCommit maintenance calls: the first stores the
	// timestamps, the second reports them as non-zero.
	waitForFlushes(t, maintenanceCh, 2, 20*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("expected OnAfterCommit to be called at least once")
	}
	if calls[0].ctxCtx == nil {
		t.Error("expected non-nil context in first OnAfterCommit call")
	}
	if calls[0].totalCommitted < 0 {
		t.Errorf("expected totalCommitted >= 0, got %d", calls[0].totalCommitted)
	}

	var sawNonZeroWal bool
	var sawNonZeroOpt bool
	for _, c := range calls {
		if !c.lastWalCheckpoint.IsZero() {
			sawNonZeroWal = true
		}
		if !c.lastOptimize.IsZero() {
			sawNonZeroOpt = true
		}
	}
	if !sawNonZeroWal {
		t.Error("expected at least one OnAfterCommit call with non-zero lastWalCheckpointTime")
	}
	if !sawNonZeroOpt {
		t.Error("expected at least one OnAfterCommit call with non-zero lastOptimizeTime")
	}
}

func TestWorker_ContextCancel_DrainsPendingBatch(t *testing.T) {
	db := testDB(t)
	ctx, cancel := context.WithCancel(context.Background())

	var mu sync.Mutex
	var flushed []int

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			mu.Lock()
			defer mu.Unlock()
			flushed = append(flushed, batch...)
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

	cancel()

	select {
	case <-wb.done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not exit after context cancellation")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 2 {
		t.Errorf("expected 2 items flushed on context cancel, got %d: %v", len(flushed), flushed)
	}
	if wb.PendingCount() != 0 {
		t.Errorf("expected PendingCount() = 0, got %d", wb.PendingCount())
	}
}
