package writebatcher

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dque"
)

func TestDeferDQueDrain_HoldsUntilStartDQueDrain(t *testing.T) {
	ctx := context.Background()
	dqueDir := filepath.Join(t.TempDir(), "dque")
	if err := os.MkdirAll(dqueDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	dq, err := dque.NewOrOpen[int]("writebatcher", dqueDir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen: %v", err)
	}
	item := 42
	if err = dq.Enqueue(&item); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err = dq.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var flushed atomic.Int32
	wb, err := New(ctx, Config[int]{
		BeginTx:        func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
		Flush:          func(ctx context.Context, tx *sql.Tx, batch []int) error { flushed.Add(int32(len(batch))); return nil },
		DQueDirPath:    dqueDir,
		DeferDQueDrain: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	if flushed.Load() != 0 {
		t.Fatalf("dque drained before StartDQueDrain, flushed = %d", flushed.Load())
	}
	if pc := wb.PendingCount(); pc != 1 {
		t.Fatalf("PendingCount() = %d, want 1 pending dque item", pc)
	}

	wb.StartDQueDrain()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if flushed.Load() > 0 && wb.PendingCount() == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("dque item not flushed after StartDQueDrain: flushed=%d pending=%d",
		flushed.Load(), wb.PendingCount())
}
