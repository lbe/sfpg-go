package cachepreload

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// newTestPools returns read-write and read-only pools on a copy of the
// migrated test database. Cleanup closes them.
func newTestPools(t *testing.T) (rw, ro *dbconnpool.DbSQLConnPool) {
	t.Helper()
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "test.db")
	thumbsPath := filepath.Join(dir, "thumbs.db")
	if err := dbconnpool.CopyMainTestDatabaseTo(mainPath); err != nil {
		t.Fatalf("copy main test database: %v", err)
	}
	if err := dbconnpool.CopyThumbsTestDatabaseTo(thumbsPath); err != nil {
		t.Fatalf("copy thumbs test database: %v", err)
	}
	open := func(dsn string, readOnly bool) *dbconnpool.DbSQLConnPool {
		pool, err := dbconnpool.NewDbSQLConnPool(context.Background(), dsn, dbconnpool.Config{
			DriverName:         "sqlite3",
			MaxConnections:     4,
			MinIdleConnections: 1,
			ReadOnly:           readOnly,
			QueriesFunc:        gallerydb.NewCustomQueries,
			ThumbsDBPath:       thumbsPath,
		})
		if err != nil {
			t.Fatalf("open pool: %v", err)
		}
		t.Cleanup(func() { _ = pool.Close() })
		return pool
	}
	main := "file:" + filepath.ToSlash(mainPath)
	return open(main+"?_txlock=immediate&mode=rwc", false), open(main+"?mode=ro", true)
}
