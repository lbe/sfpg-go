package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// Package tests share memory-mode pools via TestUseMemDB + empty DSN (TestMain).
var (
	sharedConfigRWP *dbconnpool.DbSQLConnPool
	sharedConfigROP *dbconnpool.DbSQLConnPool
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	sharedConfigRWP, sharedConfigROP, err = openConfigServiceMemPools(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config TestMain: pools: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if sharedConfigRWP != nil {
		_ = sharedConfigRWP.Close()
	}
	if sharedConfigROP != nil {
		_ = sharedConfigROP.Close()
	}
	os.Exit(code)
}

func openConfigServiceMemPools(ctx context.Context) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	poolCfg := func(readOnly bool) dbconnpool.Config {
		return dbconnpool.Config{
			DriverName:         "sqlite3",
			MaxConnections:     4,
			MinIdleConnections: 1,
			ReadOnly:           readOnly,
			QueriesFunc:        gallerydb.NewCustomQueries,
			ThumbsDBPath:       "thumbs.db",
			TestUseMemDB:       true,
		}
	}
	rw, err := dbconnpool.NewDbSQLConnPool(ctx, "", poolCfg(false))
	if err != nil {
		return nil, nil, err
	}
	ro, err := dbconnpool.NewDbSQLConnPool(ctx, "", poolCfg(true))
	if err != nil {
		_ = rw.Close()
		return nil, nil, err
	}
	return rw, ro, nil
}

func resetSharedConfigDB(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	cpc, err := sharedConfigRWP.Get()
	if err != nil {
		t.Fatalf("reset config db: get conn: %v", err)
	}
	defer sharedConfigRWP.Put(cpc)
	if _, err := cpc.Conn.ExecContext(ctx, "DELETE FROM config"); err != nil {
		t.Fatalf("reset config db: %v", err)
	}
}

// setupTestDB returns a RW pool connection with migrated schema (shared memory mode).
// Caller must not close the connection; it is returned to the pool via t.Cleanup.
func setupTestDB(t *testing.T) (*gallerydb.Queries, context.Context, *dbconnpool.CpConn) {
	t.Helper()
	resetSharedConfigDB(t)
	ctx := context.Background()
	cpc, err := sharedConfigRWP.Get()
	if err != nil {
		t.Fatalf("setupTestDB: get conn: %v", err)
	}
	q, err := gallerydb.Prepare(ctx, cpc.Conn)
	if err != nil {
		sharedConfigRWP.Put(cpc)
		t.Fatalf("setupTestDB: prepare queries: %v", err)
	}
	t.Cleanup(func() { sharedConfigRWP.Put(cpc) })
	return q, ctx, cpc
}

func createBootstrapQueries(t *testing.T) (gallerydb.Querier, func()) {
	t.Helper()
	resetSharedConfigDB(t)
	ctx := context.Background()
	cpc, err := sharedConfigRWP.Get()
	if err != nil {
		t.Fatalf("createBootstrapQueries: get conn: %v", err)
	}
	_ = ctx
	return cpc.Queries, func() { sharedConfigRWP.Put(cpc) }
}

func sharedConfigService() ConfigService {
	return NewService(sharedConfigRWP, sharedConfigROP)
}

func openConfigServiceFilePools(ctx context.Context, mainDBPath, thumbsPath string) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	poolCfg := func(readOnly bool) dbconnpool.Config {
		return dbconnpool.Config{
			DriverName:         "sqlite3",
			MaxConnections:     4,
			MinIdleConnections: 1,
			ReadOnly:           readOnly,
			QueriesFunc:        gallerydb.NewCustomQueries,
			ThumbsDBPath:       thumbsPath,
		}
	}
	rwDSN := "file:" + filepath.ToSlash(mainDBPath) + "?_txlock=immediate&mode=rwc"
	roDSN := "file:" + filepath.ToSlash(mainDBPath) + "?mode=ro"
	rw, err := dbconnpool.NewDbSQLConnPool(ctx, rwDSN, poolCfg(false))
	if err != nil {
		return nil, nil, err
	}
	ro, err := dbconnpool.NewDbSQLConnPool(ctx, roDSN, poolCfg(true))
	if err != nil {
		_ = rw.Close()
		return nil, nil, err
	}
	return rw, ro, nil
}

// newIsolatedConfigService uses a private test database copy (file DSN) so Close tests do not share TestMain pools.
func newIsolatedConfigService(t *testing.T) *configService {
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
	rw, ro, err := openConfigServiceFilePools(ctx, mainPath, thumbsPath)
	if err != nil {
		t.Fatalf("isolated pools: %v", err)
	}
	t.Cleanup(func() {
		_ = rw.Close()
		_ = ro.Close()
	})
	return NewService(rw, ro).(*configService)
}
