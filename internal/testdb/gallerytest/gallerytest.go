package gallerytest

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
	sharedDB     *sql.DB
	sharedDBOnce sync.Once
	sharedDBErr  error
	sharedDBMu   sync.Mutex // serializes tests on the shared memory DB (parallel Test* otherwise races resets)

	sharedMemDBs     *testdb.MemoryDatabases
	sharedMemDBsOnce sync.Once
	sharedMemDBsErr  error
)

// resetStatements empties every table in the shared database.
var resetStatements = []string{
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

// SharedDB returns the process-shared memory-backed gallery database with its
// memory-backed thumbs peer attached, emptied of all rows. It holds a mutex
// until t's cleanup, so tests using it run one at a time. The caller must not
// close the returned database.
func SharedDB(t testing.TB) *sql.DB {
	t.Helper()
	sharedDBMu.Lock()
	t.Cleanup(sharedDBMu.Unlock)
	ensureSharedDB()
	if sharedDBErr != nil {
		t.Fatalf("shared test db: %v", sharedDBErr)
	}
	ctx := context.Background()
	for _, q := range resetStatements {
		if _, err := sharedDB.ExecContext(ctx, q); err != nil {
			t.Fatalf("reset gallery db (%s): %v", q, err)
		}
	}
	return sharedDB
}

// IsolatedDB returns a per-test memory-backed gallery database with its thumbs
// peer attached. It is closed when t finishes, so the caller may also close it.
func IsolatedDB(t testing.TB) *sql.DB {
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

	db, err := openWithThumbs(mdbs.MainURI(false), thumbsURI)
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

// ensureSharedMemDBs constructs the process-shared memory-backed test
// databases. The lease is retained for the process lifetime.
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

// ensureSharedDB opens the shared database and attaches its thumbs peer.
func ensureSharedDB() {
	sharedDBOnce.Do(func() {
		ensureSharedMemDBs()
		if sharedMemDBsErr != nil {
			sharedDBErr = sharedMemDBsErr
			return
		}
		thumbsURI, err := sharedMemDBs.ThumbsURI(context.Background(), false)
		if err != nil {
			sharedDBErr = err
			return
		}
		db, err := openWithThumbs(sharedMemDBs.MainURI(false), thumbsURI)
		if err != nil {
			sharedDBErr = err
			return
		}
		sharedDB = db
	})
}

func openWithThumbs(mainURI, thumbsURI string) (*sql.DB, error) {
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
