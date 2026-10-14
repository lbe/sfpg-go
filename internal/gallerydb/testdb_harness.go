package gallerydb

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"github.com/lbe/sfpg-go/internal/testdb"
	"github.com/lbe/sfpg-go/migrations"
	_ "github.com/ncruces/go-sqlite3/driver"
)

var (
	sharedTestDB     *sql.DB
	sharedTestDBOnce sync.Once
	sharedTestDBErr  error
	sharedTestDBMu   sync.Mutex // serializes tests on the shared memory DB (parallel Test* otherwise races resets)

	sharedMemDBs     *testdb.MemoryDatabases
	sharedMemDBsOnce sync.Once
	sharedMemDBsErr  error
)

// ensureSharedMemDBs resolves migrations.LatestSchemaKey and constructs the
// process-shared memory-backed test databases. The lease is retained for
// the process lifetime.
func ensureSharedMemDBs() {
	sharedMemDBsOnce.Do(func() {
		ctx := context.Background()
		schemaKey, err := migrations.LatestSchemaKey()
		if err != nil {
			sharedMemDBsErr = err
			return
		}
		mdbs, err := testdb.NewMemoryDatabases(ctx, schemaKey)
		if err != nil {
			sharedMemDBsErr = err
			return
		}
		sharedMemDBs = mdbs
	})
}

// ensureSharedTestDB opens the shared memory-backed test database and
// attaches its memory-backed thumbs peer.
func ensureSharedTestDB() {
	sharedTestDBOnce.Do(func() {
		ensureSharedMemDBs()
		if sharedMemDBsErr != nil {
			sharedTestDBErr = sharedMemDBsErr
			return
		}
		ctx := context.Background()
		thumbsURI, err := sharedMemDBs.ThumbsURI(ctx, false)
		if err != nil {
			sharedTestDBErr = err
			return
		}
		db, err := openGallerySQLDB(sharedMemDBs.MainURI(false), thumbsURI)
		if err != nil {
			sharedTestDBErr = err
			return
		}
		sharedTestDB = db
	})
}

func openGallerySQLDB(mainURI, thumbsURI string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", mainURI)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(context.Background(),
		fmt.Sprintf("ATTACH DATABASE '%s' AS thumbs", thumbsURI)); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func resetSharedGalleryDB(t *testing.T) {
	t.Helper()
	ensureSharedTestDB()
	if sharedTestDBErr != nil {
		t.Fatalf("shared test db: %v", sharedTestDBErr)
	}
	ctx := context.Background()
	stmts := []string{
		"DELETE FROM http_cache",
		"DROP TABLE IF EXISTS file_folder_index_new",
		"DELETE FROM file_folder_index",
		"DELETE FROM files",
		"DELETE FROM folders",
		"DELETE FROM file_paths",
		"DELETE FROM folder_paths",
		"DELETE FROM thumbnails",
		"DELETE FROM thumbnail_blobs",
		"DELETE FROM exif_metadata",
		"DELETE FROM iptc_metadata",
		"DELETE FROM iptc_keywords",
		"DELETE FROM xmp_properties",
		"DELETE FROM xmp_raw",
		"DELETE FROM config",
		"DELETE FROM login_attempts",
		"DELETE FROM invalid_files",
		"DELETE FROM module_state",
		"DELETE FROM sqlite_sequence",
	}
	for _, q := range stmts {
		if _, execErr := sharedTestDB.ExecContext(ctx, q); execErr != nil {
			t.Fatalf("reset gallery db (%s): %v", q, execErr)
		}
	}
}

func setupTestDB(t *testing.T) (*sql.DB, *CustomQueries, context.Context) {
	t.Helper()
	sharedTestDBMu.Lock()
	t.Cleanup(func() { sharedTestDBMu.Unlock() })
	resetSharedGalleryDB(t)
	ctx := context.Background()
	q, err := PrepareCustomQueries(ctx, sharedTestDB)
	if err != nil {
		t.Fatalf("PrepareCustomQueries: %v", err)
	}
	return sharedTestDB, q, ctx
}

// SetupTestDBForTests exposes the shared memory-backed test database to other packages' tests (e.g. gallerylib).
func SetupTestDBForTests(t *testing.T) (*sql.DB, *CustomQueries, context.Context) {
	return setupTestDB(t)
}

// NewIsolatedTestSQLDBForTests opens a per-test memory-backed DB (safe to Close).
func NewIsolatedTestSQLDBForTests(t *testing.T) *sql.DB {
	return newIsolatedGallerySQLDB(t)
}

func newIsolatedGallerySQLDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	schemaKey, err := migrations.LatestSchemaKey()
	if err != nil {
		t.Fatalf("resolve schema key: %v", err)
	}
	mdbs, err := testdb.NewMemoryDatabases(ctx, schemaKey)
	if err != nil {
		t.Fatalf("new memory test databases: %v", err)
	}
	thumbsURI, err := mdbs.ThumbsURI(ctx, false)
	if err != nil {
		_ = mdbs.Close()
		t.Fatalf("materialize thumbs test database: %v", err)
	}

	db, err := openGallerySQLDB(mdbs.MainURI(false), thumbsURI)
	if err != nil {
		_ = mdbs.Close()
		t.Fatalf("open isolated db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close isolated db: %v", err)
		}
		if err := mdbs.Close(); err != nil {
			t.Fatalf("close memory test databases: %v", err)
		}
	})
	return db
}
