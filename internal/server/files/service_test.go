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
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/migrations"
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

// createTestPoolsAndDir creates a temporary DB with migrations, root folder, and Images dir.
// Returns roPool, rwPool, imagesDir, and ctx. Used by createTestProcessor and by unit tests
// that need raw pools (e.g. GenerateThumbnail_CallsImporterMethods, NeedsThumbnail).
func createTestPoolsAndDir(t *testing.T) (roPool *dbconnpool.DbSQLConnPool, rwPool *dbconnpool.DbSQLConnPool, imagesDir string, ctx context.Context) {
	t.Helper()
	ctx = context.Background()
	tempDir := t.TempDir()
	tempDB := filepath.Join(tempDir, "test.db")

	// Use simple DSN for migration - WAL will be set by first connection
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(tempDB))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		db.Close()
		t.Fatalf("sqlite driver: %v", err)
	}
	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		db.Close()
		t.Fatalf("iofs source: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
	if err != nil {
		db.Close()
		t.Fatalf("migrate instance: %v", err)
	}
	if upErr := m.Up(); upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		db.Close()
		t.Fatalf("migrate up: %v", upErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close migration db: %v", closeErr)
	}

	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")
	m2, err := migrations.NewThumbsMigrator(thumbsDBPath)
	if err != nil {
		t.Fatalf("NewThumbsMigrator: %v", err)
	}
	if upErr := m2.Up(); upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		m2.Close()
		t.Fatalf("thumbs migrate up: %v", upErr)
	}
	m2.Close()

	// Create RW pool first to set up WAL mode, then RO pool
	rwDSN := "file:" + filepath.ToSlash(tempDB) + "?_txlock=immediate&mode=rwc&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	rwPool, err = dbconnpool.NewDbSQLConnPool(ctx, rwDSN, dbconnpool.Config{
		DriverName:         "sqlite3",
		MaxConnections:     10,
		MinIdleConnections: 1,
		ReadOnly:           false,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsDBPath,
	})
	if err != nil {
		t.Fatalf("create RW pool: %v", err)
	}
	t.Cleanup(func() { _ = rwPool.Close() })

	// Now create RO pool (WAL is already set up by RW pool)
	roDSN := "file:" + filepath.ToSlash(tempDB) + "?mode=ro"
	roPool, err = dbconnpool.NewDbSQLConnPool(ctx, roDSN, dbconnpool.Config{
		DriverName:         "sqlite3",
		MaxConnections:     10,
		MinIdleConnections: 1,
		ReadOnly:           true,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsDBPath,
	})
	if err != nil {
		t.Fatalf("create RO pool: %v", err)
	}
	t.Cleanup(func() { _ = roPool.Close() })

	cpcRw, err := rwPool.Get()
	if err != nil {
		t.Fatalf("get RW conn: %v", err)
	}
	imp := &gallerylib.Importer{Q: cpcRw.Queries}
	if _, err := imp.CreateRootFolderEntry(ctx, time.Now().Unix()); err != nil {
		rwPool.Put(cpcRw)
		t.Fatalf("create root folder: %v", err)
	}
	rwPool.Put(cpcRw)

	imagesDir = filepath.Join(tempDir, "Images")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		t.Fatalf("mkdir Images: %v", err)
	}
	return roPool, rwPool, imagesDir, ctx
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
//
//nolint:unused // used in files_integration_test.go (same package)
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
