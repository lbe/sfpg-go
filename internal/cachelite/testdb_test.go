package cachelite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
)

const (
	sharedCachePoolMaxConns     = 10
	sqliteBusyTimeoutDefaultMS  = 60000 // ncruces driver default (see internal/server/database/app.go)
	sqliteBusyTimeoutFastTestMS = 1000
)

var (
	sharedCacheRWP *dbconnpool.DbSQLConnPool
	testDBInitOnce sync.Once
	testDBInitErr  error
)

func ensureSharedCachePools() {
	testDBInitOnce.Do(func() {
		ctx := context.Background()
		cfg := dbconnpool.Config{
			DriverName:         "sqlite3",
			MaxConnections:     10,
			MinIdleConnections: 1,
			ReadOnly:           false,
			QueriesFunc:        gallerydb.NewCustomQueries,
			ThumbsDBPath:       "thumbs.db",
			TestUseMemDB:       true,
		}
		sharedCacheRWP, testDBInitErr = dbconnpool.NewDbSQLConnPool(ctx, "", cfg)
	})
}

func resetCacheTables(t *testing.T) {
	t.Helper()
	if sharedCacheRWP == nil {
		t.Fatal("resetCacheTables: shared pool not initialized")
	}
	ctx := context.Background()
	cpc, err := sharedCacheRWP.Get()
	if err != nil {
		t.Fatalf("reset cache db: get conn: %v", err)
	}
	defer sharedCacheRWP.Put(cpc)
	stmts := []string{
		"DELETE FROM http_cache",
		"DROP TABLE IF EXISTS http_cache_to_be_dropped",
		"DROP TABLE IF EXISTS http_cache_new",
	}
	for _, q := range stmts {
		if _, err := cpc.Conn.ExecContext(ctx, q); err != nil {
			t.Fatalf("reset cache db (%s): %v", q, err)
		}
	}
}

// createTestDBPoolTB returns the shared memory-mode RW pool after resetting cache tables.
func createTestDBPoolTB(tb testing.TB) *dbconnpool.DbSQLConnPool {
	tb.Helper()
	ensureSharedCachePools()
	if testDBInitErr != nil {
		tb.Fatalf("shared cache pool: %v", testDBInitErr)
	}
	if t, ok := tb.(*testing.T); ok {
		resetCacheTables(t)
	} else {
		// Benchmarks: reset without *testing.T helper chain.
		ctx := context.Background()
		cpc, err := sharedCacheRWP.Get()
		if err != nil {
			tb.Fatalf("reset cache db: get conn: %v", err)
		}
		defer sharedCacheRWP.Put(cpc)
		if _, err := cpc.Conn.ExecContext(ctx, "DELETE FROM http_cache"); err != nil {
			tb.Fatalf("reset cache db: %v", err)
		}
	}
	return sharedCacheRWP
}

func createTestDBPoolInternal(t *testing.T) *dbconnpool.DbSQLConnPool {
	t.Helper()
	return createTestDBPoolTB(t)
}

func setConnBusyTimeoutMS(ctx context.Context, conn *sql.Conn, ms int) error {
	_, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", ms))
	return err
}

// setSharedPoolIdleConnBusyTimeoutMS sets busy_timeout on up to sharedCachePoolMaxConns
// pool connections. PRAGMA persists on each connection until changed again.
// Connections are warmed concurrently so each slot gets its own *sql.Conn before PRAGMA.
func setSharedPoolIdleConnBusyTimeoutMS(t *testing.T, ms int) {
	t.Helper()
	ensureSharedCachePools()
	if sharedCacheRWP == nil {
		t.Fatal("setSharedPoolIdleConnBusyTimeoutMS: shared pool not initialized")
	}
	ctx := context.Background()
	held := make([]*dbconnpool.CpConn, 0, sharedCachePoolMaxConns)
	for i := 0; i < sharedCachePoolMaxConns; i++ {
		cpc, err := sharedCacheRWP.Get()
		if err != nil {
			for _, h := range held {
				sharedCacheRWP.Put(h)
			}
			t.Fatalf("setSharedPoolIdleConnBusyTimeoutMS Get: %v", err)
		}
		held = append(held, cpc)
	}
	for _, cpc := range held {
		if err := setConnBusyTimeoutMS(ctx, cpc.Conn, ms); err != nil {
			for _, h := range held {
				sharedCacheRWP.Put(h)
			}
			t.Fatalf("setSharedPoolIdleConnBusyTimeoutMS PRAGMA: %v", err)
		}
	}
	for _, cpc := range held {
		sharedCacheRWP.Put(cpc)
	}
}

func putSharedPoolConnRestoringBusyTimeout(t *testing.T, cpc *dbconnpool.CpConn) {
	t.Helper()
	ctx := context.Background()
	if err := setConnBusyTimeoutMS(ctx, cpc.Conn, sqliteBusyTimeoutDefaultMS); err != nil {
		t.Fatalf("restore busy_timeout before Put: %v", err)
	}
	sharedCacheRWP.Put(cpc)
}

// newIsolatedTestDBPool opens a private test-database copy file DB (for pool Close tests).
func newIsolatedTestDBPool(t *testing.T) *dbconnpool.DbSQLConnPool {
	t.Helper()

	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.db")
	thumbsPath := filepath.Join(dir, "thumbs.db")
	if copyErr := dbconnpool.CopyMainTestDatabaseTo(mainPath); copyErr != nil {
		t.Fatalf("copy main test database: %v", copyErr)
	}
	if copyErr := dbconnpool.CopyThumbsTestDatabaseTo(thumbsPath); copyErr != nil {
		t.Fatalf("copy thumbs test database: %v", copyErr)
	}

	ctx := context.Background()
	dsn := "file:" + filepath.ToSlash(mainPath) + "?_txlock=immediate&mode=rwc"
	pool, err := dbconnpool.NewDbSQLConnPool(ctx, dsn, dbconnpool.Config{
		DriverName:         "sqlite3",
		MaxConnections:     4,
		MinIdleConnections: 1,
		ReadOnly:           false,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsPath,
	})
	if err != nil {
		t.Fatalf("isolated pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// createTestDBPoolInDir creates RW (or RO) pools against one copied test database in dir.
// The first call copies the main test database into dir; later calls in the same dir reuse the file.
func createTestDBPoolInDir(t *testing.T, dir string, readOnly bool) *dbconnpool.DbSQLConnPool {
	t.Helper()

	mainPath := filepath.Join(dir, "test.db")
	thumbsPath := filepath.Join(dir, "thumbs.db")
	if _, statErr := os.Stat(mainPath); statErr != nil {
		if copyErr := dbconnpool.CopyMainTestDatabaseTo(mainPath); copyErr != nil {
			t.Fatalf("copy main test database: %v", copyErr)
		}
	}
	if _, statErr := os.Stat(thumbsPath); statErr != nil {
		if copyErr := dbconnpool.CopyThumbsTestDatabaseTo(thumbsPath); copyErr != nil {
			t.Fatalf("copy thumbs test database: %v", copyErr)
		}
	}

	dsn := "file:" + filepath.ToSlash(mainPath) + "?_txlock=immediate&mode=rwc"
	if readOnly {
		dsn = "file:" + filepath.ToSlash(mainPath) + "?mode=ro"
	}

	ctx := context.Background()
	pool, err := dbconnpool.NewDbSQLConnPool(ctx, dsn, dbconnpool.Config{
		DriverName:         "sqlite3",
		MaxConnections:     10,
		MinIdleConnections: 1,
		ReadOnly:           readOnly,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsPath,
	})
	if err != nil {
		t.Fatalf("pool in dir: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}
