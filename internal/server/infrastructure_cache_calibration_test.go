package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"log/slog"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
)

func TestInfrastructureService_CalibrateCacheSizeNow_UsesROPool(t *testing.T) {
	infra := NewInfrastructureService()
	rw := newFakePool(10, 2)
	ro := newFakePool(10, 2)
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    rw,
		setupRo:    ro,
	}
	infra.SetupDB(context.Background(), config.DefaultConfig())

	var gotPool *dbconnpool.DbSQLConnPool
	infra.testSeams.GetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		gotPool = pool
		return 42, nil
	}
	infra.testSeams.GetCacheEntryCount = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 7, nil
	}

	infra.CalibrateCacheSizeNow(context.Background())

	if gotPool != ro {
		t.Fatal("GetCacheSizeBytes should use RO pool")
	}
	if infra.cacheSizeBytes.Load() != 42 {
		t.Fatalf("cacheSizeBytes = %d, want 42", infra.cacheSizeBytes.Load())
	}
	if infra.cacheEntryCount.Load() != 7 {
		t.Fatalf("cacheEntryCount = %d, want 7", infra.cacheEntryCount.Load())
	}
}

func TestInfrastructureService_CalibrateCacheSizeNow_EntryCountError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    newFakePool(10, 2),
		setupRo:    newFakePool(10, 2),
	}
	infra.SetupDB(context.Background(), config.DefaultConfig())

	infra.testSeams.GetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 55, nil
	}
	infra.testSeams.GetCacheEntryCount = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 0, errors.New("count error")
	}

	infra.CalibrateCacheSizeNow(context.Background())

	if infra.cacheSizeBytes.Load() != 55 {
		t.Fatalf("cacheSizeBytes = %d, want 55", infra.cacheSizeBytes.Load())
	}
	if infra.cacheEntryCount.Load() != 0 {
		t.Fatalf("cacheEntryCount = %d, want 0 after count error", infra.cacheEntryCount.Load())
	}
}

func TestInfrastructureService_InvalidateHTTPCache_ZerosCounters(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = newFakePool(10, 2)
	infra.cacheRotator = &fakeCacheRotator{}
	infra.cacheSizeBytes.Store(123)
	infra.cacheEntryCount.Store(9)

	infra.InvalidateHTTPCache()

	if infra.cacheSizeBytes.Load() != 0 {
		t.Fatalf("cacheSizeBytes = %d, want 0", infra.cacheSizeBytes.Load())
	}
	if infra.cacheEntryCount.Load() != 0 {
		t.Fatalf("cacheEntryCount = %d, want 0", infra.cacheEntryCount.Load())
	}
}

func TestInfrastructureService_resyncCacheSizeFromDB_UsesROPool(t *testing.T) {
	infra := NewInfrastructureService()
	rw := newFakePool(10, 2)
	ro := newFakePool(10, 2)
	infra.dbRwPool = rw
	infra.dbRoPool = ro

	var gotPool *dbconnpool.DbSQLConnPool
	infra.testSeams.GetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		gotPool = pool
		return 55, nil
	}
	infra.testSeams.GetCacheEntryCount = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 4, nil
	}

	infra.resyncCacheSizeFromDB(context.Background())

	if gotPool != ro {
		t.Fatal("resync should use RO pool")
	}
	if infra.cacheSizeBytes.Load() != 55 {
		t.Fatalf("cacheSizeBytes = %d, want 55", infra.cacheSizeBytes.Load())
	}
}

func TestInfrastructureService_resyncCacheSizeFromDB_Error(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRoPool = newFakePool(10, 2)
	infra.testSeams.GetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 0, errors.New("boom")
	}

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.resyncCacheSizeFromDB(context.Background())
	})
	if !strings.Contains(logs, "failed to resync cache size counter") {
		t.Errorf("expected resync warning, got: %s", logs)
	}
}
