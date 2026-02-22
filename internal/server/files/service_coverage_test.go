package files

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// TestFileProcessor_PendingWriteCount verifies PendingWriteCount behavior.
func TestFileProcessor_PendingWriteCount(t *testing.T) {
	t.Run("returns zero initially", func(t *testing.T) {
		roPool, rwPool, imagesDir, _ := createTestPoolsAndDir(t)

		wb, err := writebatcher.New[interface{}](context.Background(), writebatcher.Config[interface{}]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []interface{}) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		defer wb.Close()
		ba := &mockUnifiedBatcher{
			PendingCountFunc: func() int64 {
				return wb.PendingCount()
			},
		}

		processor := NewFileProcessor(roPool, rwPool, nil, imagesDir, ba)
		defer processor.Close()

		count := processor.PendingWriteCount()
		if count != 0 {
			t.Errorf("expected PendingWriteCount 0, got %d", count)
		}
	})

	t.Run("returns count from underlying batcher", func(t *testing.T) {
		roPool, rwPool, imagesDir, _ := createTestPoolsAndDir(t)

		var mockCount int64 = 5
		ba := &mockUnifiedBatcher{
			PendingCountFunc: func() int64 {
				return mockCount
			},
		}

		processor := NewFileProcessor(roPool, rwPool, nil, imagesDir, ba)
		defer processor.Close()

		count := processor.PendingWriteCount()
		if count != 5 {
			t.Errorf("expected PendingWriteCount 5, got %d", count)
		}
	})

	t.Run("count changes with submissions", func(t *testing.T) {
		roPool, rwPool, imagesDir, _ := createTestPoolsAndDir(t)

		wb, err := writebatcher.New[interface{}](context.Background(), writebatcher.Config[interface{}]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []interface{}) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		defer wb.Close()

		ba := &mockUnifiedBatcher{
			PendingCountFunc: func() int64 {
				return wb.PendingCount()
			},
			SubmitFileFunc: func(file *File) error {
				return wb.Submit(struct{}{})
			},
		}

		processor := NewFileProcessor(roPool, rwPool, nil, imagesDir, ba)
		defer processor.Close()

		// Submit some files
		for i := 0; i < 3; i++ {
			file := &File{
				Path: "/test/file.jpg",
				File: gallerydb.File{
					Filename: "file.jpg",
					FolderID: sql.NullInt64{Int64: 1, Valid: true},
				},
			}
			_ = processor.SubmitFileForWrite(file)
		}

		count := processor.PendingWriteCount()
		if count != 3 {
			t.Errorf("expected PendingWriteCount 3, got %d", count)
		}
	})
}

// TestFileProcessor_PendingWriteCount_Integration tests with real database.
func TestFileProcessor_PendingWriteCount_Integration(t *testing.T) {
	t.Run("tracks real database writes", func(t *testing.T) {
		roPool, rwPool, imagesDir, _ := createTestPoolsAndDir(t)

		wb, err := writebatcher.New[interface{}](context.Background(), writebatcher.Config[interface{}]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []interface{}) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		defer wb.Close()

		// Create a synchronous batcher that actually writes to the database
		ba := &mockUnifiedBatcher{
			rwPool: rwPool,
			PendingCountFunc: func() int64 {
				return wb.PendingCount()
			},
			SubmitFileFunc: func(file *File) error {
				return wb.Submit(struct{}{})
			},
		}

		processor := NewFileProcessor(roPool, rwPool, nil, imagesDir, ba)
		defer processor.Close()

		// Submit a file
		cpcRw, err := rwPool.Get()
		if err != nil {
			t.Fatalf("get rw conn: %v", err)
		}
		defer rwPool.Put(cpcRw)

		file := &File{
			Path: "/test/integration.jpg",
			File: gallerydb.File{
				Filename: "integration.jpg",
				FolderID: sql.NullInt64{Int64: 1, Valid: true},
				MimeType: sql.NullString{String: "image/jpeg", Valid: true},
				Width:    sql.NullInt64{Int64: 100, Valid: true},
				Height:   sql.NullInt64{Int64: 100, Valid: true},
				Mtime:    sql.NullInt64{Int64: 12345, Valid: true},
			},
		}

		_ = processor.SubmitFileForWrite(file)

		// Count should reflect the submission
		count := processor.PendingWriteCount()
		if count != 1 {
			t.Errorf("expected PendingWriteCount 1, got %d", count)
		}
	})
}

// TestFileProcessor_Close verifies Close behavior.
func TestFileProcessor_Close(t *testing.T) {
	t.Run("close returns nil", func(t *testing.T) {
		roPool, rwPool, imagesDir, _ := createTestPoolsAndDir(t)

		ba := &mockUnifiedBatcher{}
		processor := NewFileProcessor(roPool, rwPool, nil, imagesDir, ba)

		err := processor.Close()
		if err != nil {
			t.Errorf("Close returned error: %v", err)
		}
	})

	t.Run("multiple close is safe", func(t *testing.T) {
		roPool, rwPool, imagesDir, _ := createTestPoolsAndDir(t)

		ba := &mockUnifiedBatcher{}
		processor := NewFileProcessor(roPool, rwPool, nil, imagesDir, ba)

		err1 := processor.Close()
		err2 := processor.Close()

		if err1 != nil {
			t.Errorf("First Close returned error: %v", err1)
		}
		if err2 != nil {
			t.Errorf("Second Close returned error: %v", err2)
		}
	})
}

// TestFileProcessor_SubmitFileForWrite verifies SubmitFileForWrite integration.
func TestFileProcessor_SubmitFileForWrite(t *testing.T) {
	t.Run("delegates to underlying batcher", func(t *testing.T) {
		roPool, rwPool, imagesDir, _ := createTestPoolsAndDir(t)

		var submittedFile *File
		ba := &mockUnifiedBatcher{
			SubmitFileFunc: func(file *File) error {
				submittedFile = file
				return nil
			},
		}

		processor := NewFileProcessor(roPool, rwPool, nil, imagesDir, ba)
		defer processor.Close()

		file := &File{
			Path: "/test/test.jpg",
			File: gallerydb.File{
				Filename: "test.jpg",
				FolderID: sql.NullInt64{Int64: 1, Valid: true},
			},
		}

		err := processor.SubmitFileForWrite(file)
		if err != nil {
			t.Errorf("SubmitFileForWrite returned error: %v", err)
		}
		if submittedFile == nil {
			t.Error("expected file to be submitted to batcher")
		}
		if submittedFile != file {
			t.Error("expected same file to be submitted")
		}
	})

	t.Run("returns batcher errors", func(t *testing.T) {
		roPool, rwPool, imagesDir, _ := createTestPoolsAndDir(t)

		expectedErr := sql.ErrTxDone
		ba := &mockUnifiedBatcher{
			SubmitFileFunc: func(file *File) error {
				return expectedErr
			},
		}

		processor := NewFileProcessor(roPool, rwPool, nil, imagesDir, ba)
		defer processor.Close()

		file := &File{
			Path: "/test/test.jpg",
			File: gallerydb.File{
				Filename: "test.jpg",
				FolderID: sql.NullInt64{Int64: 1, Valid: true},
			},
		}

		err := processor.SubmitFileForWrite(file)
		if err != expectedErr {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})
}
