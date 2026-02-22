package server

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"go.local/sfpg/internal/cachelite"
	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/server/files"
	"go.local/sfpg/internal/writebatcher"
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
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 1,
			ChannelSize:  1,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
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
		err = ba.SubmitFile(file2)
		if !errors.Is(err, writebatcher.ErrFull) {
			t.Errorf("expected ErrFull, got %v", err)
		}
	})
}

// TestBatcherAdapter_SubmitInvalidFile verifies SubmitInvalidFile behavior.
func TestBatcherAdapter_SubmitInvalidFile(t *testing.T) {
	t.Run("successfully submits invalid file", func(t *testing.T) {
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

		params := gallerydb.UpsertInvalidFileParams{
			Path:   "/test/invalid.jpg",
			Reason: sql.NullString{String: "test", Valid: true},
			Mtime:  12345,
			Size:   0,
		}

		err = ba.SubmitInvalidFile(params)
		if err != nil {
			t.Fatalf("SubmitInvalidFile failed: %v", err)
		}
		if ba.PendingCount() != 1 {
			t.Errorf("expected pending count 1, got %d", ba.PendingCount())
		}
	})

	t.Run("returns error when batcher is full", func(t *testing.T) {
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 1,
			ChannelSize:  1,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		defer wb.Close()
		ba := newBatcherAdapter(wb)

		params1 := gallerydb.UpsertInvalidFileParams{
			Path: "/test/1.jpg", Reason: sql.NullString{String: "test1", Valid: true}, Mtime: 12345, Size: 0,
		}
		params2 := gallerydb.UpsertInvalidFileParams{
			Path: "/test/2.jpg", Reason: sql.NullString{String: "test2", Valid: true}, Mtime: 12346, Size: 0,
		}

		// First submit should succeed
		if err := ba.SubmitInvalidFile(params1); err != nil {
			t.Fatalf("first SubmitInvalidFile failed: %v", err)
		}

		// Second submit should fail with ErrFull
		err = ba.SubmitInvalidFile(params2)
		if !errors.Is(err, writebatcher.ErrFull) {
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
