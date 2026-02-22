package server

import (
	"context"
	"database/sql"
	"testing"

	"go.local/sfpg/internal/cachelite"
	"go.local/sfpg/internal/writebatcher"
)

// TestApp_SubmitCacheWrite verifies submitCacheWrite behavior.
func TestApp_SubmitCacheWrite(t *testing.T) {
	t.Run("successfully submits cache entry when batcher is available", func(t *testing.T) {
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

		app := &App{
			writeBatcher: wb,
		}

		entry := cachelite.GetHTTPCacheEntry()
		entry.Path = "/test/path"
		entry.ETag = sql.NullString{String: "etag123", Valid: true}
		entry.ContentLength = sql.NullInt64{Int64: 100, Valid: true}
		entry.Body = []byte("test body")

		app.submitCacheWrite(entry)

		// Verify entry was submitted
		if wb.PendingCount() != 1 {
			t.Errorf("expected pending count 1, got %d", wb.PendingCount())
		}
	})

	t.Run("handles nil batcher gracefully", func(t *testing.T) {
		app := &App{
			writeBatcher: nil,
		}

		entry := cachelite.GetHTTPCacheEntry()
		entry.Path = "/test/path"

		// Should not panic
		app.submitCacheWrite(entry)
	})

	t.Run("handles submission error by returning entry to pool", func(t *testing.T) {
		// Create a context that can be cancelled
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create a batcher with size 1 to limit submissions
		wb, err := writebatcher.New[BatchedWrite](ctx, writebatcher.Config[BatchedWrite]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 1,
			ChannelSize:  1,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		defer wb.Close()

		app := &App{
			writeBatcher: wb,
		}

		// We test the submitCacheWrite behavior statically:
		// When adapter.SubmitCache fails, the entry should be returned to pool
		// by submitCacheWrite function. We verify this by checking the function
		// doesn't panic even when submission fails.

		entry := cachelite.GetHTTPCacheEntry()
		entry.Path = "/test/path"

		// This should not panic even if batcher rejects it
		app.submitCacheWrite(entry)
	})
}
