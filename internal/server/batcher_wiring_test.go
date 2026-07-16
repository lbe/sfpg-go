package server

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// testDB opens an in-memory SQLite database for testing.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// testBeginTx returns a BeginTx function using the given database.
func testBeginTx(db *sql.DB) func(context.Context) (*sql.Tx, error) {
	return func(ctx context.Context) (*sql.Tx, error) {
		return db.BeginTx(ctx, nil)
	}
}

// makeTestFile creates a minimal files.File for testing.
func makeTestFile(path, name string) *files.File {
	return &files.File{
		Path: path,
		File: gallerydb.File{
			Filename: name,
			FolderID: sql.NullInt64{Int64: 1, Valid: true},
		},
	}
}

func TestFileBatcher_SubmitFile(t *testing.T) {
	t.Run("successfully submits file", func(t *testing.T) {
		db := testDB(t)
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      testBeginTx(db),
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() { _ = wb.Close() })
		fb := newFileBatcher(wb)

		file := makeTestFile("/test/path.jpg", "test.jpg")

		err = fb.SubmitFile(file)
		if err != nil {
			t.Fatalf("SubmitFile failed: %v", err)
		}
		if fb.PendingCount() != 1 {
			t.Errorf("expected pending count 1, got %d", fb.PendingCount())
		}
	})

	t.Run("returns error when batcher is full", func(t *testing.T) {
		// Block the flush so the worker cannot drain the channel.
		var blockMu sync.Mutex
		blockMu.Lock()

		db := testDB(t)
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx: testBeginTx(db),
			Flush: func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
				blockMu.Lock() // blocks until test unblocks
				_ = len(batch)
				blockMu.Unlock()
				return nil
			},
			MaxBatchSize: 1,
			ChannelSize:  1,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() {
			blockMu.Unlock() // unblock worker so Close can complete
			wb.Close()
		})
		fb := newFileBatcher(wb)

		// First submit: worker picks it up, batch reaches MaxBatchSize=1,
		// flush begins but blocks in FlushFunc on blockMu.
		err = fb.SubmitFile(makeTestFile("/test/1.jpg", "1.jpg"))
		if err != nil {
			t.Fatalf("first SubmitFile failed: %v", err)
		}

		// Second submit should find the channel full and return ErrFull.
		err = fb.SubmitFile(makeTestFile("/test/2.jpg", "2.jpg"))
		if !errors.Is(err, writebatcher.ErrFull) {
			t.Errorf("expected ErrFull, got %v", err)
		}
	})
}

func TestFileBatcher_PendingCount(t *testing.T) {
	t.Run("returns zero when empty", func(t *testing.T) {
		db := testDB(t)
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      testBeginTx(db),
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() { _ = wb.Close() })
		fb := newFileBatcher(wb)

		if count := fb.PendingCount(); count != 0 {
			t.Errorf("expected pending count 0, got %d", count)
		}
	})

	t.Run("returns count after submissions", func(t *testing.T) {
		db := testDB(t)
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      testBeginTx(db),
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() { _ = wb.Close() })
		fb := newFileBatcher(wb)

		file := makeTestFile("/test/1.jpg", "1.jpg")
		for i := 0; i < 5; i++ {
			_ = fb.SubmitFile(file)
		}

		if count := fb.PendingCount(); count != 5 {
			t.Errorf("expected pending count 5, got %d", count)
		}
	})
}

func TestFileBatcher_NilGuard(t *testing.T) {
	fb := newFileBatcher(nil)

	// SubmitFile must return ErrClosed, not panic
	err := fb.SubmitFile(&files.File{Path: "/test/path.jpg"})
	if !errors.Is(err, writebatcher.ErrClosed) {
		t.Errorf("SubmitFile with nil batcher: expected ErrClosed, got %v", err)
	}

	// PendingCount must return 0, not panic
	if count := fb.PendingCount(); count != 0 {
		t.Errorf("PendingCount with nil batcher: expected 0, got %d", count)
	}
}
