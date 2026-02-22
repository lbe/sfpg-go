package files

import (
	"context"
	"database/sql"
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
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/migrations"
)

type mockUnifiedBatcher struct {
	SubmitFileFunc        func(file *File) error
	SubmitInvalidFileFunc func(params gallerydb.UpsertInvalidFileParams) error
	PendingCountFunc      func() int64
	rwPool                *dbconnpool.DbSQLConnPool // For integration tests that need real writes
}

func (m *mockUnifiedBatcher) SubmitFile(file *File) error {
	if m.SubmitFileFunc != nil {
		return m.SubmitFileFunc(file)
	}
	// If rwPool is set, write synchronously (for integration tests)
	if m.rwPool != nil {
		cpc, err := m.rwPool.Get()
		if err != nil {
			return err
		}
		defer m.rwPool.Put(cpc)

		tx, err := cpc.Conn.BeginTx(context.Background(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()

		if err := WriteFileInTx(context.Background(), tx, file); err != nil {
			return err
		}
		return tx.Commit()
	}
	return nil
}

func (m *mockUnifiedBatcher) SubmitInvalidFile(params gallerydb.UpsertInvalidFileParams) error {
	if m.SubmitInvalidFileFunc != nil {
		return m.SubmitInvalidFileFunc(params)
	}
	// If rwPool is set, write synchronously (for integration tests)
	if m.rwPool != nil {
		cpc, err := m.rwPool.Get()
		if err != nil {
			return err
		}
		defer m.rwPool.Put(cpc)
		return cpc.Queries.UpsertInvalidFile(context.Background(), params)
	}
	return nil
}

func (m *mockUnifiedBatcher) PendingCount() int64 {
	if m.PendingCountFunc != nil {
		return m.PendingCountFunc()
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
	if upErr := m.Up(); upErr != nil && upErr != migrate.ErrNoChange {
		db.Close()
		t.Fatalf("migrate up: %v", upErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close migration db: %v", closeErr)
	}

	// Create RW pool first to set up WAL mode, then RO pool
	rwDSN := "file:" + filepath.ToSlash(tempDB) + "?_txlock=immediate&mode=rwc"
	rwPool, err = dbconnpool.NewDbSQLConnPool(ctx, rwDSN, dbconnpool.Config{
		DriverName:         "sqlite3",
		MaxConnections:     10,
		MinIdleConnections: 1,
		ReadOnly:           false,
		QueriesFunc:        gallerydb.NewCustomQueries,
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
