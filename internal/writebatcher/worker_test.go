package writebatcher

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestWorker_MaintenanceTimer(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	var mu sync.Mutex
	var calls []struct {
		ctxCtx            context.Context
		lastWalCheckpoint time.Time
		lastOptimize      time.Time
		totalCommitted    int64
		postFlush         bool
	}
	maintenanceCh := make(chan struct{}, 8)

	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			return nil
		},
		OnAfterCommit: func(ctx context.Context, lastWalCheckpoint time.Time, lastOptimize time.Time, totalCommitted int64, postFlush bool) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, struct {
				ctxCtx            context.Context
				lastWalCheckpoint time.Time
				lastOptimize      time.Time
				totalCommitted    int64
				postFlush         bool
			}{ctxCtx: ctx, lastWalCheckpoint: lastWalCheckpoint, lastOptimize: lastOptimize, totalCommitted: totalCommitted, postFlush: postFlush})
			select {
			case maintenanceCh <- struct{}{}:
			default:
			}
		},
		MaxBatchSize:        1,
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

	// Wait for 3 OnAfterCommit callbacks:
	//   1 = flush: postFlush=true, zeros
	//   2 = first timer tick: postFlush=false, still zeros
	//   3 = second timer tick: postFlush=false, non-zero times
	waitForFlushes(t, maintenanceCh, 3, 20*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 3 {
		t.Fatalf("expected at least 3 OnAfterCommit calls, got %d", len(calls))
	}
	// Callback 1 (flush) must have postFlush=true.
	if !calls[0].postFlush {
		t.Error("callback 1 (flush): expected postFlush=true, got false")
	}
	// Callback 2 and 3 (maintenance timer) must have postFlush=false.
	if calls[1].postFlush {
		t.Error("callback 2 (timer): expected postFlush=false, got true")
	}
	if calls[2].postFlush {
		t.Error("callback 3 (timer): expected postFlush=false, got true")
	}

	if calls[0].ctxCtx == nil {
		t.Error("expected non-nil context in first OnAfterCommit call")
	}

	var sawNonZeroWal bool
	var sawNonZeroOpt bool
	for _, c := range calls[2:] {
		if !c.lastWalCheckpoint.IsZero() {
			sawNonZeroWal = true
		}
		if !c.lastOptimize.IsZero() {
			sawNonZeroOpt = true
		}
	}
	if !sawNonZeroWal {
		t.Error("expected at least one OnAfterCommit call (after first timer tick) with non-zero lastWalCheckpointTime")
	}
	if !sawNonZeroOpt {
		t.Error("expected at least one OnAfterCommit call (after first timer tick) with non-zero lastOptimizeTime")
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
