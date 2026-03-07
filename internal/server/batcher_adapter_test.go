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
		// Create a batcher with size 1 and submit 2 items
		wb, writeBatcherErr := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 1,
			ChannelSize:  1,
		})
		if writeBatcherErr != nil {
			t.Fatalf("New writebatcher: %v", writeBatcherErr)
		}
		defer wb.Close()
		ba := newBatcherAdapter(wb)

		file1 := &files.File{
			Path: "/test/1.jpg",
			File: gallerydb.File{
				Filename: "1.jpg",
				FolderID: sql.NullInt64{Int64: 1, Valid: true},
			},
		}
		file2 := &files.File{
			Path: "/test/2.jpg",
			File: gallerydb.File{
				Filename: "2.jpg",
				FolderID: sql.NullInt64{Int64: 1, Valid: true},
			},
		}

		// First submit should succeed
		if err := ba.SubmitFile(file1); err != nil {
			t.Fatalf("first SubmitFile failed: %v", err)
		}

		// Second submit should fail with ErrFull
		submitErr := ba.SubmitFile(file2)
		if !errors.Is(submitErr, writebatcher.ErrFull) {
			t.Errorf("expected ErrFull, got %v", submitErr)
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
