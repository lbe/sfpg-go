package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/dque"
)

const queuedWrites = 500

// queueWrites sets up a new database whose overflow queue holds n cache
// writes from an earlier run.
func queueWrites(t *testing.T, n int) (DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool) {
	t.Helper()
	paths, rw, ro, err := Setup(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := paths.Main + "-dque"
	if err = os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dq, err := dque.NewOrOpen[batchedWrite]("writebatcher", dir, 250)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		body := []byte("body")
		bw := batchedWrite{CacheEntry: &cachelite.HTTPCacheEntry{
			Key:           fmt.Sprintf("seed-%d", i),
			Path:          fmt.Sprintf("/gallery/%d", i),
			Method:        "GET",
			Status:        200,
			Body:          body,
			ContentLength: sql.NullInt64{Int64: int64(len(body)), Valid: true},
			CreatedAt:     1,
		}}
		if err = dq.Enqueue(&bw); err != nil {
			t.Fatal(err)
		}
	}
	if err = dq.Close(); err != nil {
		t.Fatal(err)
	}
	return paths, rw, ro
}

// queueSize opens the overflow queue in dir and returns its size.
func queueSize(t *testing.T, dir string) int {
	t.Helper()
	dq, err := dque.NewOrOpen[batchedWrite]("writebatcher", dir, 250)
	if err != nil {
		t.Fatal(err)
	}
	defer dq.Close()
	return dq.Size()
}

// TestWriterCloseKeepsQueuedWrites checks that Close leaves writes queued by
// an earlier run on disk until a drain starts, so it does not wait for them.
func TestWriterCloseKeepsQueuedWrites(t *testing.T) {
	paths, rw, ro := queueWrites(t, queuedWrites)
	t.Cleanup(func() {
		_ = rw.Close()
		_ = ro.Close()
	})
	w, err := NewWriter(context.Background(), WriterConfig{Paths: paths, RW: rw, RO: ro, DeferDQueDrain: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := w.PendingCount(); n != queuedWrites {
		t.Errorf("PendingCount = %d, want %d", n, queuedWrites)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if n := queueSize(t, paths.Main+"-dque"); n != queuedWrites {
		t.Errorf("%d writes queued after Close, want %d", n, queuedWrites)
	}
}

// TestWriterDrainsQueuedWrites checks that once the drain starts, the Writer
// writes the queue left by an earlier run.
func TestWriterDrainsQueuedWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		paths, rw, ro := queueWrites(t, queuedWrites)
		t.Cleanup(func() {
			_ = rw.Close()
			_ = ro.Close()
		})
		w, err := NewWriter(context.Background(), WriterConfig{Paths: paths, RW: rw, RO: ro, DeferDQueDrain: true})
		if err != nil {
			t.Fatal(err)
		}
		w.StartDQueDrain()
		time.Sleep(time.Second)
		synctest.Wait()
		if err = w.Close(); err != nil {
			t.Fatal(err)
		}
		cpc, err := ro.Get()
		if err != nil {
			t.Fatal(err)
		}
		defer ro.Put(cpc)
		var n int
		if err = cpc.Conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM http_cache").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != queuedWrites {
			t.Errorf("http_cache has %d rows after the drain, want %d", n, queuedWrites)
		}
	})
}
