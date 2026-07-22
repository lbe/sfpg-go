package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

	infra.CalibrateCacheSizeNow(context.Background())

	if gotPool != ro {
		t.Fatal("GetCacheSizeBytes should use RO pool")
	}
	if infra.cacheSizeBytes.Load() != 42 {
		t.Fatalf("cacheSizeBytes = %d, want 42", infra.cacheSizeBytes.Load())
	}
	if !infra.CacheSizeCalibrated() {
		t.Fatal("expected calibrated after success")
	}
}

func TestInfrastructureService_StartCacheSizeCalibration_QuietTrigger(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    newFakePool(10, 2),
		setupRo:    newFakePool(10, 2),
	}
	infra.SetupDB(context.Background(), config.DefaultConfig())
	infra.testSeams.CacheCalibrationPollInterval = 5 * time.Millisecond
	infra.testSeams.CacheCalibrationMaxWait = time.Hour
	infra.testSeams.GetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 99, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var quietCalls atomic.Int32
	quiet := func(ctx context.Context) bool {
		if !infra.cacheCalibListening.Load() {
			return false
		}
		return quietCalls.Add(1) >= 2
	}

	var wg sync.WaitGroup
	wg.Add(1)
	infra.StartCacheSizeCalibration(ctx, quiet, func(fn func()) {
		wg.Done()
		go fn()
	})
	wg.Wait()

	infra.SetCacheCalibrationListening(true)

	deadline := time.Now().Add(2 * time.Second)
	for !infra.CacheSizeCalibrated() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !infra.CacheSizeCalibrated() {
		t.Fatal("expected quiet calibration to complete")
	}
	if infra.cacheSizeBytes.Load() != 99 {
		t.Fatalf("cacheSizeBytes = %d, want 99", infra.cacheSizeBytes.Load())
	}
}

func TestInfrastructureService_StartCacheSizeCalibration_TimeoutFallback(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    newFakePool(10, 2),
		setupRo:    newFakePool(10, 2),
	}
	infra.SetupDB(context.Background(), config.DefaultConfig())
	infra.testSeams.CacheCalibrationPollInterval = time.Hour
	infra.testSeams.CacheCalibrationMaxWait = 15 * time.Millisecond
	infra.testSeams.GetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 7, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quiet := func(ctx context.Context) bool { return false }

	var wg sync.WaitGroup
	wg.Add(1)
	infra.StartCacheSizeCalibration(ctx, quiet, func(fn func()) {
		wg.Done()
		go fn()
	})
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for !infra.CacheSizeCalibrated() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !infra.CacheSizeCalibrated() {
		t.Fatal("expected timeout calibration to complete")
	}
	if infra.cacheSizeBytes.Load() != 7 {
		t.Fatalf("cacheSizeBytes = %d, want 7", infra.cacheSizeBytes.Load())
	}
}

func TestInfrastructureService_InvalidateHTTPCache_MarksCalibrated(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = newFakePool(10, 2)
	infra.cacheRotator = &fakeCacheRotator{}
	infra.cacheSizeBytes.Store(123)
	infra.cacheSizeCalibrated.Store(false)

	infra.InvalidateHTTPCache()

	if infra.cacheSizeBytes.Load() != 0 {
		t.Fatalf("cacheSizeBytes = %d, want 0", infra.cacheSizeBytes.Load())
	}
	if !infra.CacheSizeCalibrated() {
		t.Fatal("InvalidateHTTPCache should mark counter calibrated")
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
