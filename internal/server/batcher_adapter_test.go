package server

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// fakeBatchWriter is a deterministic batchWriter for tests. It succeeds for the
// first fullAfter calls and returns ErrFull for every subsequent call.
type fakeBatchWriter struct {
	calls     int
	fullAfter int
}

func (f *fakeBatchWriter) Submit(_ BatchedWrite) error {
	f.calls++
	if f.calls > f.fullAfter {
		return writebatcher.ErrFull
	}
	return nil
}

func (f *fakeBatchWriter) PendingCount() int64 { return int64(f.calls) }

// TestBatcherAdapter_SubmitFile verifies SubmitFile behavior.
func TestBatcherAdapter_SubmitFile(t *testing.T) {
	t.Run("successfully submits file", func(t *testing.T) {
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		defer wb.Close()
		ba := newBatcherAdapter(wb)

		file := &files.File{
			Path: "/test/path.jpg",
			File: gallerydb.File{
				Filename: "test.jpg",
				FolderID: sql.NullInt64{Int64: 1, Valid: true},
			},
		}

		err = ba.SubmitFile(file)
		if err != nil {
			t.Fatalf("SubmitFile failed: %v", err)
		}
		if ba.PendingCount() != 1 {
			t.Errorf("expected pending count 1, got %d", ba.PendingCount())
		}
	})

	t.Run("returns error when batcher is full", func(t *testing.T) {
		// Use a fake batchWriter to avoid timing dependency on the worker goroutine.
		// A live WriteBatcher drains items from the channel faster than the test can
		// fill it, making it impossible to reliably trigger ErrFull.
		fake := &fakeBatchWriter{fullAfter: 1}
		ba := &batcherAdapter{wb: fake}

		file := &files.File{
			Path: "/test/1.jpg",
			File: gallerydb.File{
				Filename: "1.jpg",
				FolderID: sql.NullInt64{Int64: 1, Valid: true},
			},
		}

		// First submit should succeed
		if err := ba.SubmitFile(file); err != nil {
			t.Fatalf("first SubmitFile failed: %v", err)
		}

		// Second submit should propagate ErrFull from the batcher
		if err := ba.SubmitFile(file); !errors.Is(err, writebatcher.ErrFull) {
			t.Errorf("expected ErrFull, got %v", err)
		}
	})
}

// TestBatcherAdapter_SubmitCache verifies SubmitCache behavior.
func TestBatcherAdapter_SubmitCache(t *testing.T) {
	t.Run("successfully submits cache entry", func(t *testing.T) {
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		defer wb.Close()
		// Cast to concrete type to access SubmitCache
		ba := &batcherAdapter{wb: wb}

		entry := &cachelite.HTTPCacheEntry{
			Path:          "/test/path",
			ETag:          sql.NullString{String: "etag123", Valid: true},
			ContentLength: sql.NullInt64{Int64: 100, Valid: true},
			Body:          []byte("test body"),
		}

		err = ba.SubmitCache(entry)
		if err != nil {
			t.Fatalf("SubmitCache failed: %v", err)
		}
		if ba.PendingCount() != 1 {
			t.Errorf("expected pending count 1, got %d", ba.PendingCount())
		}
	})
}

// TestBatcherAdapter_PendingCount verifies PendingCount behavior.
func TestBatcherAdapter_PendingCount(t *testing.T) {
	t.Run("returns zero when empty", func(t *testing.T) {
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		defer wb.Close()
		ba := newBatcherAdapter(wb)

		if count := ba.PendingCount(); count != 0 {
			t.Errorf("expected pending count 0, got %d", count)
		}
	})

	t.Run("returns count after submissions", func(t *testing.T) {
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		defer wb.Close()
		ba := newBatcherAdapter(wb)

		file := &files.File{
			Path: "/test/1.jpg",
			File: gallerydb.File{
				Filename: "1.jpg",
				FolderID: sql.NullInt64{Int64: 1, Valid: true},
			},
		}
		for i := 0; i < 5; i++ {
			_ = ba.SubmitFile(file)
		}

		if count := ba.PendingCount(); count != 5 {
			t.Errorf("expected pending count 5, got %d", count)
		}
	})
}
