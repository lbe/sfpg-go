package files

import (
	"context"
	"database/sql"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
)

type mockUnifiedBatcher struct {
	SubmitFileFunc                    func(file *File) error
	SubmitFolderIndexFunc             func(row FolderIndexRow) error
	PendingCountFunc                  func() int64
	FolderIndexInflightFunc           func() int64
	SetFolderIndexRebuildActiveFunc   func(active bool)
	SetFolderIndexRebuildScanHeldFunc func(held bool)
	BumpFolderIndexGenerationFunc     func() int64
	rwPool                            *dbconnpool.DbSQLConnPool // For integration tests that need real writes
}

func (m *mockUnifiedBatcher) SubmitFile(file *File) error {
	if m.SubmitFileFunc != nil {
		return m.SubmitFileFunc(file)
	}
	// If rwPool is set, write synchronously (for integration tests)
	if m.rwPool != nil {
		cpcRw, err := m.rwPool.Get()
		if err != nil {
			return err
		}
		defer m.rwPool.Put(cpcRw)

		tx, err := cpcRw.Conn.BeginTx(context.Background(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()

		qtx := cpcRw.Queries.WithTx(tx)
		imp := &gallerylib.Importer{Q: qtx}
		if err := WriteFileInTx(context.Background(), imp, file); err != nil {
			return err
		}
		return tx.Commit()
	}
	return nil
}

func (m *mockUnifiedBatcher) SubmitFolderIndex(row FolderIndexRow) error {
	if m.SubmitFolderIndexFunc != nil {
		return m.SubmitFolderIndexFunc(row)
	}
	return nil
}

func (m *mockUnifiedBatcher) PendingCount() int64 {
	if m.PendingCountFunc != nil {
		return m.PendingCountFunc()
	}
	return 0
}

func (m *mockUnifiedBatcher) FolderIndexInflight() int64 {
	if m.FolderIndexInflightFunc != nil {
		return m.FolderIndexInflightFunc()
	}
	return 0
}

func (m *mockUnifiedBatcher) SetFolderIndexRebuildActive(active bool) {
	if m.SetFolderIndexRebuildActiveFunc != nil {
		m.SetFolderIndexRebuildActiveFunc(active)
	}
}

func (m *mockUnifiedBatcher) SetFolderIndexRebuildScanHeld(held bool) {
	if m.SetFolderIndexRebuildScanHeldFunc != nil {
		m.SetFolderIndexRebuildScanHeldFunc(held)
	}
}

func (m *mockUnifiedBatcher) BumpFolderIndexGeneration() int64 {
	if m.BumpFolderIndexGenerationFunc != nil {
		return m.BumpFolderIndexGenerationFunc()
	}
	return 0
}

// createTestPoolsAndDir returns shared golden-restore memory-mode pools (reset per test), a temp Images dir, and ctx.
func createTestPoolsAndDir(t *testing.T) (roPool *dbconnpool.DbSQLConnPool, rwPool *dbconnpool.DbSQLConnPool, imagesDir string, ctx context.Context) {
	t.Helper()
	ensureSharedFilesPools()
	if sharedFilesInitErr != nil {
		t.Fatalf("shared files pools: %v", sharedFilesInitErr)
	}
	sharedFilesMu.Lock()
	t.Cleanup(func() { sharedFilesMu.Unlock() })
	resetSharedFilesDB(t)

	ctx = context.Background()
	imagesDir = filepath.Join(t.TempDir(), "Images")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		t.Fatalf("mkdir Images: %v", err)
	}
	return sharedFilesROP, sharedFilesRWP, imagesDir, ctx
}

// createTestProcessor creates a FileProcessor with a temporary DB, migrations applied,
// root folder ensured, and a temporary Images directory. Also returns roPool, rwPool,
// and imagesDir for tests that need to pre-process files (e.g. for GenerateThumbnail).
// The processor is closed via t.Cleanup.
func createTestProcessor(t *testing.T, ub UnifiedBatcher) (processor FileProcessor, roPool *dbconnpool.DbSQLConnPool, rwPool *dbconnpool.DbSQLConnPool, imagesDir string) {
	t.Helper()
	roPool, rwPool, imagesDir, _ = createTestPoolsAndDir(t)
	importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}
	if ub == nil {
		// For integration tests, create a mock that writes synchronously
		ub = &mockUnifiedBatcher{rwPool: rwPool}
	}
	processor = NewFileProcessor(roPool, rwPool, importerFactory, imagesDir, ub)
	t.Cleanup(func() { _ = processor.Close() })
	return processor, roPool, rwPool, imagesDir
}

func fileWithWalkMetadata(t *testing.T, imagesDir, path string) *File {
	t.Helper()
	info, err := os.Stat(filepath.Join(imagesDir, path))
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return &File{
		Path: path,
		File: gallerydb.File{
			Mtime:     sql.NullInt64{Int64: info.ModTime().Unix(), Valid: true},
			SizeBytes: sql.NullInt64{Int64: info.Size(), Valid: true},
		},
	}
}

func TestFileProcessor_ProcessDiscoveryFile(t *testing.T) {
	t.Run("cancelled context", func(t *testing.T) {
		processor, _, _, _ := createTestProcessor(t, nil)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := processor.ProcessDiscoveryFile(ctx, &File{Path: "test.jpg"})
		if err == nil {
			t.Fatal("expected error for cancelled context")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("empty file path", func(t *testing.T) {
		processor, _, _, _ := createTestProcessor(t, nil)

		_, err := processor.ProcessDiscoveryFile(context.Background(), &File{Path: ""})
		if err == nil {
			t.Fatal("expected error for empty path")
		}
	})

	t.Run("success with real file", func(t *testing.T) {
		processor, _, _, imagesDir := createTestProcessor(t, nil)

		path := createTestImage(t, imagesDir, "process-discovery-success.jpg")
		file, err := processor.ProcessDiscoveryFile(context.Background(), fileWithWalkMetadata(t, imagesDir, path))
		if err != nil {
			t.Fatalf("ProcessDiscoveryFile: %v", err)
		}
		if file == nil {
			t.Fatal("expected non-nil file")
		}
		if file.Path != path {
			t.Errorf("Path = %q, want %q", file.Path, path)
		}
	})

	t.Run("missing walk metadata", func(t *testing.T) {
		processor, _, _, _ := createTestProcessor(t, nil)

		_, err := processor.ProcessDiscoveryFile(context.Background(), &File{Path: "test.jpg"})
		if err == nil {
			t.Fatal("expected error for missing walk metadata")
		}
	})
}

// createTestImage writes a minimal 1x1 JPEG at dir/name and returns the relative path.
func createTestImage(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create test image: %v", err)
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		f.Close()
		t.Fatalf("encode test image: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close test image: %v", err)
	}
	return name
}
