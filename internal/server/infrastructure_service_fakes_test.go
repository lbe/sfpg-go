package server

import (
	"context"
	"database/sql"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
)

// fakeDatabaseInitializer is a test double for databaseInitializer.
type fakeDatabaseInitializer struct {
	setupPaths      database.DatabasePaths
	setupRw         *dbconnpool.DbSQLConnPool
	setupRo         *dbconnpool.DbSQLConnPool
	setupErr        error
	recreateRw      *dbconnpool.DbSQLConnPool
	recreateRo      *dbconnpool.DbSQLConnPool
	recreateErr     error
	setupCalled     bool
	recreateCalled  bool
	lastRecreateCfg *config.Config
}

func (f *fakeDatabaseInitializer) Setup(ctx context.Context, rootDir string, cfg *config.Config) (database.DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	f.setupCalled = true
	return f.setupPaths, f.setupRw, f.setupRo, f.setupErr
}

func (f *fakeDatabaseInitializer) RecreatePoolsWithConfig(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	f.recreateCalled = true
	f.lastRecreateCfg = cfg
	return f.recreateRw, f.recreateRo, f.recreateErr
}

// fakeDBPoolForCheckpoint is a test double for dbPoolForCheckpoint.
type fakeDBPoolForCheckpoint struct {
	getCpc    *fakeCpConn
	getErr    error
	putCalled bool
	putCpc    *dbconnpool.CpConn
}

func (f *fakeDBPoolForCheckpoint) Get() (*dbconnpool.CpConn, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getCpc.CpConn, nil
}

func (f *fakeDBPoolForCheckpoint) Put(cpc *dbconnpool.CpConn) {
	f.putCalled = true
	f.putCpc = cpc
}

// fakeCpConn wraps a *sql.Conn with configurable hooks.
type fakeCpConn struct {
	CpConn           *dbconnpool.CpConn
	QueryContextFn   func(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	PragmaOptimizeFn func(ctx context.Context)
}

// fakeCacheMiddlewareForEvict is a test double for cacheMiddlewareForEvict.
type fakeCacheMiddlewareForEvict struct {
	cfg cachelite.CacheConfig
}

func (f *fakeCacheMiddlewareForEvict) Config() cachelite.CacheConfig {
	return f.cfg
}

// fakeCacheRotator is a test double for cacheRotator.
type fakeCacheRotator struct {
	rotateErr    error
	rotateCalled bool
	rotatePool   *dbconnpool.DbSQLConnPool
}

func (f *fakeCacheRotator) RotateCacheTable(ctx context.Context, pool *dbconnpool.DbSQLConnPool) error {
	f.rotateCalled = true
	f.rotatePool = pool
	return f.rotateErr
}
