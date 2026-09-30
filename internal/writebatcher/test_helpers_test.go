package writebatcher

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

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
