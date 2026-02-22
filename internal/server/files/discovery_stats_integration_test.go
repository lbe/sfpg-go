package files

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/queue"
	"go.local/sfpg/internal/workerpool"
)

// TestDiscoveryStats_ExistingFilesNotCountedAsNew verifies that when running
// discovery on files that already exist in the database, they are counted as
// AlreadyExisting, not NewlyInserted.
func TestDiscoveryStats_ExistingFilesNotCountedAsNew(t *testing.T) {
	// Create a temporary directory with test files
	tmpDir := t.TempDir()
	testFiles := []string{"file1.jpg", "file2.jpg", "file3.jpg"}
	for _, name := range testFiles {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte("test content "+name), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	// Create queue
	q := queue.NewQueue[string](10)

	// Create a mock file processor that simulates files existing in DB
	fp := &existingFileProcessor{
		t:      t,
		tmpDir: tmpDir,
		existsMap: map[string]bool{
			"file1.jpg": true, // Exists in DB
			"file2.jpg": true, // Exists in DB
			"file3.jpg": true, // Exists in DB
		},
	}

	stats := &ProcessingStats{}

	// Enqueue all files
	for _, name := range testFiles {
		q.Enqueue(filepath.Join(tmpDir, name))
	}

	// Create worker pool
	pool := workerpool.NewPool(context.Background(), 2, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create pool func with stats
	poolFunc := NewPoolFuncWithProcessor(fp, q, tmpDir, func(_, path string) (string, error) {
		return filepath.Base(path), nil
	}, stats)

	// Run workers
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			poolFunc(ctx, pool, nil, nil, q.Len, id)
		}(i)
	}

	// Wait for all files to be processed
	time.Sleep(100 * time.Millisecond)
	cancel()
	wg.Wait()

	// Verify stats
	t.Logf("TotalFound: %d", stats.TotalFound.Load())
	t.Logf("AlreadyExisting: %d", stats.AlreadyExisting.Load())
	t.Logf("NewlyInserted: %d", stats.NewlyInserted.Load())

	if val := stats.TotalFound.Load(); val != 3 {
		t.Errorf("TotalFound: got %d, want 3", val)
	}
	if val := stats.AlreadyExisting.Load(); val != 3 {
		t.Errorf("AlreadyExisting: got %d, want 3", val)
	}
	if val := stats.NewlyInserted.Load(); val != 0 {
		t.Errorf("NewlyInserted: got %d, want 0", val)
	}
}

// existingFileProcessor is a mock processor that returns files as existing
type existingFileProcessor struct {
	t         *testing.T
	tmpDir    string
	existsMap map[string]bool
}

func (p *existingFileProcessor) ProcessFile(ctx context.Context, path string) (*File, error) {
	basename := filepath.Base(path)

	// Get actual file info
	fullPath := filepath.Join(p.tmpDir, basename)
	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}

	file := &File{
		Path:      basename,
		ImagesDir: p.tmpDir,
		Exists:    p.existsMap[basename], // This is the key field
		Ok:        true,
		File: gallerydb.File{
			Mtime:     sql.NullInt64{Valid: true, Int64: info.ModTime().Unix()},
			SizeBytes: sql.NullInt64{Valid: true, Int64: info.Size()},
		},
	}

	p.t.Logf("ProcessFile: %s, Exists=%v", basename, file.Exists)
	return file, nil
}

func (p *existingFileProcessor) ProcessFileWithConn(ctx context.Context, path string, cpc *dbconnpool.CpConn) (*File, error) {
	return p.ProcessFile(ctx, path)
}

func (p *existingFileProcessor) CheckIfModified(ctx context.Context, path string) (bool, error) {
	return false, nil
}

func (p *existingFileProcessor) GenerateThumbnail(ctx context.Context, file *File) error {
	return nil
}

func (p *existingFileProcessor) RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string) error {
	return nil
}

func (p *existingFileProcessor) SubmitFileForWrite(file *File) error {
	return nil
}

func (p *existingFileProcessor) PendingWriteCount() int64 {
	return 0
}

func (p *existingFileProcessor) Close() error {
	return nil
}

// AtomicBool for thread-safe boolean operations
type AtomicBool struct {
	value atomic.Int32
}

func (a *AtomicBool) Store(val bool) {
	if val {
		a.value.Store(1)
	} else {
		a.value.Store(0)
	}
}

func (a *AtomicBool) Load() bool {
	return a.value.Load() != 0
}
