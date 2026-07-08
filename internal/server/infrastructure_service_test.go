package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

func withLogCapture(t *testing.T, level slog.Level, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level}))
	old := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(old)
	fn()
	return buf.String()
}

func newFakePool(maxConns, minIdle int64) *dbconnpool.DbSQLConnPool {
	return &dbconnpool.DbSQLConnPool{Config: dbconnpool.Config{MaxConnections: maxConns, MinIdleConnections: minIdle}}
}

// newRawSQLiteConn opens a raw *sql.Conn to a temporary SQLite database.
// It avoids the custom query preparation performed by dbconnpool, making it
// suitable for unit tests that only exercise low-level connection operations.
func newRawSQLiteConn(t *testing.T, ctx context.Context) *sql.Conn {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+t.TempDir()+"/test.db?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	return conn
}

func TestNewInfrastructureService_ImporterFactory(t *testing.T) {
	infra := NewInfrastructureService()
	if infra.ImporterFactory == nil {
		t.Fatal("ImporterFactory is nil")
	}
	var conn *sql.Conn
	var q *gallerydb.CustomQueries
	imp := infra.ImporterFactory(conn, q)
	if _, ok := imp.(*gallerylib.Importer); !ok {
		t.Fatalf("ImporterFactory returned %T, want *gallerylib.Importer", imp)
	}
}

func TestInfrastructureService_GetConfigQueries(t *testing.T) {
	infra := NewInfrastructureService()
	queries := gallerydb.NewCustomQueries(nil)
	cpc := &dbconnpool.CpConn{Queries: queries}

	got := infra.GetConfigQueries(cpc)
	if got != queries {
		t.Error("GetConfigQueries did not return cpc.Queries")
	}
}

func TestInfrastructureService_SetupDB_NilConfig(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    newFakePool(100, 10),
		setupRo:    newFakePool(100, 10),
	}
	infra.testHookBuildWriteBatcher = func(ctx context.Context, maxBatchSize int, flushInterval time.Duration) (*writebatcher.WriteBatcher[BatchedWrite], error) {
		return nil, nil // avoid starting real batcher
	}
	infra.testHookGetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 0, nil
	}
	infra.testHookBuildWriteBatcher = func(ctx context.Context, maxBatchSize int, flushInterval time.Duration) (*writebatcher.WriteBatcher[BatchedWrite], error) {
		return nil, nil // avoid starting real batcher
	}

	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.SetupDB(context.Background(), nil)
	})

	want := "/tmp/fake/sfpg.db-dque"
	if infra.dqueDirPath != want {
		t.Fatalf("dqueDirPath = %q, want %q", infra.dqueDirPath, want)
	}
	if !strings.Contains(logs, "rw_configured_max=100") {
		t.Errorf("expected default configured max in logs, got: %s", logs)
	}
	if !strings.Contains(logs, "rw_configured_min_idle=10") {
		t.Errorf("expected default configured min idle in logs, got: %s", logs)
	}
}

func TestInfrastructureService_SetupDB_CacheSizeError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    newFakePool(100, 10),
		setupRo:    newFakePool(100, 10),
	}
	infra.testHookBuildWriteBatcher = func(ctx context.Context, maxBatchSize int, flushInterval time.Duration) (*writebatcher.WriteBatcher[BatchedWrite], error) {
		return nil, nil // avoid starting real batcher
	}
	infra.testHookGetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 0, errors.New("size error")
	}

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.SetupDB(context.Background(), config.DefaultConfig())
	})
	if !strings.Contains(logs, "Failed to initialize cache size counter") {
		t.Errorf("expected cache size warning, got: %s", logs)
	}
}

func TestInfrastructureService_SetupDB_PanicOnSetupError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{setupErr: errors.New("boom")}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if s, ok := r.(string); !ok || s != "main" {
			t.Fatalf("panic value = %v, want string 'main'", r)
		}
	}()

	logs := withLogCapture(t, slog.LevelError, func() {
		infra.SetupDB(context.Background(), config.DefaultConfig())
	})
	if !strings.Contains(logs, "failed to setup database") {
		t.Errorf("expected setup error log, got: %s", logs)
	}
}

func TestInfrastructureService_SetupDB_PanicOnWriteBatcherError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    newFakePool(100, 10),
		setupRo:    newFakePool(100, 10),
	}
	infra.testHookGetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 0, nil
	}
	infra.testHookBuildWriteBatcher = func(ctx context.Context, maxBatchSize int, flushInterval time.Duration) (*writebatcher.WriteBatcher[BatchedWrite], error) {
		return nil, errors.New("batcher boom")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if s, ok := r.(string); !ok || !strings.Contains(s, "failed to create unified WriteBatcher") {
			t.Fatalf("panic value = %v, want failure message", r)
		}
	}()

	withLogCapture(t, slog.LevelError, func() {
		infra.SetupDB(context.Background(), config.DefaultConfig())
	})
}

func TestInfrastructureService_ReconfigurePools_NilConfig(t *testing.T) {
	infra := NewInfrastructureService()
	initializer := &fakeDatabaseInitializer{}
	infra.dbInitializer = initializer
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)

	if err := infra.ReconfigurePools(context.Background(), nil); err != nil {
		t.Fatalf("ReconfigurePools(nil) error = %v", err)
	}
	if initializer.recreateCalled {
		t.Error("RecreatePoolsWithConfig should not be called for nil config")
	}
}

func TestInfrastructureService_ReconfigurePools_NoOpWhenUnchanged(t *testing.T) {
	infra := NewInfrastructureService()
	initializer := &fakeDatabaseInitializer{}
	infra.dbInitializer = initializer
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)

	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 10
	cfg.DBMinIdleConnections = 2

	if err := infra.ReconfigurePools(context.Background(), cfg); err != nil {
		t.Fatalf("ReconfigurePools error = %v", err)
	}
	if initializer.recreateCalled {
		t.Error("RecreatePoolsWithConfig should not be called when unchanged")
	}
}

func TestInfrastructureService_ReconfigurePools_PropagatesError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{recreateErr: errors.New("recreate failed")}
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)

	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 20
	cfg.DBMinIdleConnections = 4

	err := infra.ReconfigurePools(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "recreate failed") {
		t.Fatalf("ReconfigurePools error = %v, want recreate failed", err)
	}
}

func TestInfrastructureService_ReconfigurePools_UsesBuildBatcherHook(t *testing.T) {
	infra := NewInfrastructureService()
	hookCalled := false
	infra.testHookBuildWriteBatcher = func(ctx context.Context, maxBatchSize int, flushInterval time.Duration) (*writebatcher.WriteBatcher[BatchedWrite], error) {
		hookCalled = true
		return nil, nil
	}
	infra.dbInitializer = &fakeDatabaseInitializer{
		recreateRw: newFakePool(20, 4),
		recreateRo: newFakePool(20, 4),
	}
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)

	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 20
	cfg.DBMinIdleConnections = 4

	if err := infra.ReconfigurePools(context.Background(), cfg); err != nil {
		t.Fatalf("ReconfigurePools error = %v", err)
	}
	if !hookCalled {
		t.Error("testHookBuildWriteBatcher should be called")
	}
}

func TestInfrastructureService_ReconfigurePools_BatcherError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.testHookBuildWriteBatcher = func(ctx context.Context, maxBatchSize int, flushInterval time.Duration) (*writebatcher.WriteBatcher[BatchedWrite], error) {
		return nil, errors.New("batcher failed")
	}
	infra.dbInitializer = &fakeDatabaseInitializer{
		recreateRw: newFakePool(20, 4),
		recreateRo: newFakePool(20, 4),
	}
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)

	logs := withLogCapture(t, slog.LevelError, func() {
		cfg := config.DefaultConfig()
		cfg.DBMaxPoolSize = 20
		cfg.DBMinIdleConnections = 4
		if err := infra.ReconfigurePools(context.Background(), cfg); err != nil {
			t.Fatalf("ReconfigurePools error = %v", err)
		}
	})
	if !strings.Contains(logs, "failed to recreate write batcher") {
		t.Errorf("expected batcher error log, got: %s", logs)
	}
}

func TestInfrastructureService_ReconfigurePools_UpdatesCacheMW(t *testing.T) {
	infra := NewInfrastructureService()
	infra.testHookBuildWriteBatcher = func(ctx context.Context, maxBatchSize int, flushInterval time.Duration) (*writebatcher.WriteBatcher[BatchedWrite], error) {
		return nil, nil
	}
	infra.dbInitializer = &fakeDatabaseInitializer{
		recreateRw: newFakePool(20, 4),
		recreateRo: newFakePool(20, 4),
	}
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)
	infra.cacheMW = cachelite.NewHTTPCacheMiddlewareForTest(infra.dbRwPool, cachelite.CacheConfig{}, &infra.cacheSizeBytes, nil)

	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 20
	cfg.DBMinIdleConnections = 4

	if err := infra.ReconfigurePools(context.Background(), cfg); err != nil {
		t.Fatalf("ReconfigurePools error = %v", err)
	}
	if infra.cacheMW == nil {
		t.Fatal("cacheMW should not be nil")
	}
}

func TestInfrastructureService_Shutdown_NilBatcher(t *testing.T) {
	infra := NewInfrastructureService()
	infra.writeBatcher = nil
	// Should not panic.
	infra.Shutdown()
}

func TestInfrastructureService_Shutdown_LogsCloseError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.testHookShutdownWriteBatcher = func() error {
		return errors.New("close failed")
	}

	logs := withLogCapture(t, slog.LevelError, func() {
		infra.Shutdown()
	})
	if !strings.Contains(logs, "error closing write batcher") {
		t.Errorf("expected close error log, got: %s", logs)
	}
	if !strings.Contains(logs, "close failed") {
		t.Errorf("expected underlying error in log, got: %s", logs)
	}
}

func TestInfrastructureService_MaybeEvictCacheEntries_EarlyReturns(t *testing.T) {
	tests := []struct {
		name  string
		batch []BatchedWrite
		cmw   cacheMiddlewareForEvict
	}{
		{
			name:  "no cache entries",
			batch: []BatchedWrite{{File: &files.File{Path: "x.jpg"}}},
		},
		{
			name:  "nil cache middleware",
			batch: []BatchedWrite{{CacheEntry: &cachelite.HTTPCacheEntry{Path: "/gallery/1"}}},
		},
		{
			name:  "max total size zero",
			batch: []BatchedWrite{{CacheEntry: &cachelite.HTTPCacheEntry{Path: "/gallery/1"}}},
			cmw:   &fakeCacheMiddlewareForEvict{cfg: cachelite.CacheConfig{MaxTotalSize: 0}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			infra := NewInfrastructureService()
			infra.cacheMWForEvict = tt.cmw
			sizeCalled := false
			infra.testHookGetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
				sizeCalled = true
				return 0, nil
			}

			infra.maybeEvictCacheEntries(tt.batch)
			if sizeCalled {
				t.Error("GetCacheSizeBytes should not be called")
			}
		})
	}
}

func TestInfrastructureService_MaybeEvictCacheEntries_SizeError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.cacheMWForEvict = &fakeCacheMiddlewareForEvict{cfg: cachelite.CacheConfig{MaxTotalSize: 1000}}
	infra.testHookGetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 0, errors.New("size error")
	}

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.maybeEvictCacheEntries([]BatchedWrite{{CacheEntry: &cachelite.HTTPCacheEntry{Path: "/gallery/1"}}})
	})
	if !strings.Contains(logs, "failed to get cache size for eviction check") {
		t.Errorf("expected size error log, got: %s", logs)
	}
}

func TestInfrastructureService_MaybeEvictCacheEntries_UnderLimit(t *testing.T) {
	infra := NewInfrastructureService()
	infra.cacheMWForEvict = &fakeCacheMiddlewareForEvict{cfg: cachelite.CacheConfig{MaxTotalSize: 1000}}
	infra.testHookGetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 500, nil
	}
	evictCalled := false
	infra.testHookEvictLRU = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool, targetFree int64) (int64, error) {
		evictCalled = true
		return 0, nil
	}

	infra.maybeEvictCacheEntries([]BatchedWrite{{CacheEntry: &cachelite.HTTPCacheEntry{Path: "/gallery/1"}}})
	if evictCalled {
		t.Error("EvictLRU should not be called when under limit")
	}
}

func TestInfrastructureService_MaybeEvictCacheEntries_EvictionError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.cacheMWForEvict = &fakeCacheMiddlewareForEvict{cfg: cachelite.CacheConfig{MaxTotalSize: 1000}}
	infra.testHookGetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 1500, nil
	}
	infra.testHookEvictLRU = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool, targetFree int64) (int64, error) {
		return 0, errors.New("eviction failed")
	}

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.maybeEvictCacheEntries([]BatchedWrite{{CacheEntry: &cachelite.HTTPCacheEntry{Path: "/gallery/1"}}})
	})
	if !strings.Contains(logs, "cache eviction failed") {
		t.Errorf("expected eviction error log, got: %s", logs)
	}
}

func TestInfrastructureService_MaybeEvictCacheEntries_OverLimit(t *testing.T) {
	infra := NewInfrastructureService()
	infra.cacheMWForEvict = &fakeCacheMiddlewareForEvict{cfg: cachelite.CacheConfig{MaxTotalSize: 1000}}
	infra.testHookGetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 1500, nil
	}
	var evictTarget int64
	infra.testHookEvictLRU = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool, targetFree int64) (int64, error) {
		evictTarget = targetFree
		return 600, nil
	}

	infra.cacheSizeBytes.Store(1500)
	infra.maybeEvictCacheEntries([]BatchedWrite{{CacheEntry: &cachelite.HTTPCacheEntry{Path: "/gallery/1"}}})

	wantTarget := int64(1500 - 1000 + 1000/10) // 600
	if evictTarget != wantTarget {
		t.Fatalf("EvictLRU target = %d, want %d", evictTarget, wantTarget)
	}
	if got := infra.cacheSizeBytes.Load(); got != 900 {
		t.Fatalf("cacheSizeBytes = %d, want 900", got)
	}
}

func TestInfrastructureService_WalCheckpointAfterCommit_StatError(t *testing.T) {
	infra := NewInfrastructureService()
	// Use a path under a directory we cannot read so os.Stat returns a permission error.
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(dir, 0o755)
	infra.dbPaths.Main = filepath.Join(dir, "sfpg.db")

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.walCheckpointAfterCommit(context.Background(), time.Time{}, time.Time{}, 0)
	})
	if !strings.Contains(logs, "failed to stat WAL file") {
		t.Errorf("expected stat error log, got: %s", logs)
	}
}

func TestInfrastructureService_WalCheckpointAfterCommit_WALSizeThreshold(t *testing.T) {
	infra := NewInfrastructureService()
	dir := t.TempDir()
	infra.dbPaths.Main = filepath.Join(dir, "sfpg.db")

	walPath := infra.dbPaths.Main + "-wal"
	f, err := os.Create(walPath)
	if err != nil {
		t.Fatalf("create WAL: %v", err)
	}
	if _, err := f.Write(make([]byte, 256*1024*1024+1)); err != nil {
		t.Fatalf("write WAL: %v", err)
	}
	f.Close()

	checkpointCalled := false
	infra.testHookPerformWALCheckpoint = func(ctx context.Context) {
		checkpointCalled = true
	}

	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.walCheckpointAfterCommit(context.Background(), time.Time{}, time.Time{}, 0)
	})
	if !checkpointCalled {
		t.Error("performWALCheckpoint should be called")
	}
	if !strings.Contains(logs, "WAL file exceeds threshold") {
		t.Errorf("expected WAL threshold log, got: %s", logs)
	}
}

func TestInfrastructureService_WalCheckpointAfterCommit_FiveMinuteElapsed(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbPaths.Main = filepath.Join(t.TempDir(), "sfpg.db")

	checkpointCalled := false
	infra.testHookPerformWALCheckpoint = func(ctx context.Context) {
		checkpointCalled = true
	}

	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.walCheckpointAfterCommit(context.Background(), time.Now().Add(-6*time.Minute), time.Now(), 0)
	})
	if !checkpointCalled {
		t.Error("performWALCheckpoint should be called")
	}
	if !strings.Contains(logs, "WAL checkpoint: 5 minutes elapsed") {
		t.Errorf("expected 5-minute log, got: %s", logs)
	}
}

func TestInfrastructureService_WalCheckpointAfterCommit_OneHourOptimize(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbPaths.Main = filepath.Join(t.TempDir(), "sfpg.db")

	optimizeCalled := false
	infra.testHookPragmaOptimize = func(ctx context.Context, pool dbPoolForCheckpoint) {
		optimizeCalled = true
	}

	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.walCheckpointAfterCommit(context.Background(), time.Now(), time.Now().Add(-65*time.Minute), 0)
	})
	if !optimizeCalled {
		t.Error("PragmaOptimize should be called")
	}
	if !strings.Contains(logs, "PRAGMA optimize: 1 hour elapsed") {
		t.Errorf("expected optimize log, got: %s", logs)
	}
}

func TestInfrastructureService_WalCheckpointAfterCommit_OptimizeConnectionError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbPaths.Main = filepath.Join(t.TempDir(), "sfpg.db")

	// The optimize branch is replaced by a hook that invokes pragmaOptimize with
	// a fake pool whose Get returns an error.
	infra.testHookPragmaOptimize = func(ctx context.Context, pool dbPoolForCheckpoint) {
		fakePool := &fakeDBPoolForCheckpoint{getErr: errors.New("no conn")}
		infra.pragmaOptimize(ctx, fakePool)
	}

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.walCheckpointAfterCommit(context.Background(), time.Now(), time.Now().Add(-65*time.Minute), 0)
	})
	if !strings.Contains(logs, "failed to get connection for PRAGMA optimize") {
		t.Errorf("expected optimize connection error log, got: %s", logs)
	}
}

func TestInfrastructureService_PerformWALCheckpoint_Success(t *testing.T) {
	infra := NewInfrastructureService()
	cpc := &dbconnpool.CpConn{Conn: newRawSQLiteConn(t, context.Background())}
	fakePool := &fakeDBPoolForCheckpoint{getCpc: &fakeCpConn{CpConn: cpc}}

	logs := withLogCapture(t, slog.LevelDebug, func() {
		infra.performWALCheckpoint(context.Background(), fakePool)
	})
	if !strings.Contains(logs, "wal_frames") {
		t.Errorf("expected wal_frames log, got: %s", logs)
	}
	if !fakePool.putCalled {
		t.Error("Put should be called")
	}
}

func TestInfrastructureService_PerformWALCheckpoint_ConnectionError(t *testing.T) {
	infra := NewInfrastructureService()
	fakePool := &fakeDBPoolForCheckpoint{getErr: errors.New("no connection")}

	logs := withLogCapture(t, slog.LevelError, func() {
		infra.performWALCheckpoint(context.Background(), fakePool)
	})
	if !strings.Contains(logs, "failed to get connection for WAL checkpoint") {
		t.Errorf("expected connection error log, got: %s", logs)
	}
}

func TestInfrastructureService_PerformWALCheckpoint_QueryError(t *testing.T) {
	infra := NewInfrastructureService()
	cpc := &dbconnpool.CpConn{Conn: newRawSQLiteConn(t, context.Background())}
	if err := cpc.Conn.Close(); err != nil {
		t.Fatalf("close conn: %v", err)
	}
	fakePool := &fakeDBPoolForCheckpoint{getCpc: &fakeCpConn{CpConn: cpc}}

	logs := withLogCapture(t, slog.LevelError, func() {
		infra.performWALCheckpoint(context.Background(), fakePool)
	})
	if !strings.Contains(logs, "WAL checkpoint failed") {
		t.Errorf("expected query error log, got: %s", logs)
	}
}

func TestInfrastructureService_PerformWALCheckpoint_ScanError(t *testing.T) {
	infra := NewInfrastructureService()
	cpc := &dbconnpool.CpConn{Conn: newRawSQLiteConn(t, context.Background())}
	fakePool := &fakeDBPoolForCheckpoint{getCpc: &fakeCpConn{CpConn: cpc}}

	// Override the checkpoint query hook to return rows with a single column,
	// causing Scan to fail.
	infra.testHookWALCheckpointQuery = func(ctx context.Context, conn *sql.Conn) (*sql.Rows, error) {
		return conn.QueryContext(ctx, "SELECT 1")
	}

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.performWALCheckpoint(context.Background(), fakePool)
	})
	if !strings.Contains(logs, "failed to parse wal_checkpoint result") {
		t.Errorf("expected scan error log, got: %s", logs)
	}
}

func TestInfrastructureService_InitializeHTTPCache_EarlyReturns(t *testing.T) {
	tests := []struct {
		name   string
		config *config.Config
	}{
		{"nil config", nil},
		{"cache disabled", func() *config.Config { c := config.DefaultConfig(); c.EnableHTTPCache = false; return c }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			infra := NewInfrastructureService()
			infra.InitializeHTTPCache(tt.config)
			if infra.cacheMW != nil {
				t.Error("cacheMW should remain nil")
			}
		})
	}
}

func TestInfrastructureService_InvalidateHTTPCache_NilPool(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = nil
	rotator := &fakeCacheRotator{}
	infra.cacheRotator = rotator

	infra.InvalidateHTTPCache()
	if rotator.rotateCalled {
		t.Error("RotateCacheTable should not be called when dbRwPool is nil")
	}
}

func TestInfrastructureService_InvalidateHTTPCache_RotateError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = newFakePool(10, 2)
	infra.cacheRotator = &fakeCacheRotator{rotateErr: errors.New("rotate failed")}

	logs := withLogCapture(t, slog.LevelError, func() {
		infra.InvalidateHTTPCache()
	})
	if !strings.Contains(logs, "failed to invalidate HTTP cache") {
		t.Errorf("expected invalidate error log, got: %s", logs)
	}
}

func TestInfrastructureService_SubmitCacheWrite_NilBatcher(t *testing.T) {
	infra := NewInfrastructureService()
	infra.writeBatcher = nil
	entry := &cachelite.HTTPCacheEntry{Path: "/gallery/1"}

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.submitCacheWrite(entry)
	})
	if !strings.Contains(logs, "unified batcher not available") {
		t.Errorf("expected missing batcher log, got: %s", logs)
	}
}

func TestInfrastructureService_SubmitCacheWrite_AdapterError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    newFakePool(10, 2),
		setupRo:    newFakePool(10, 2),
	}
	infra.testHookBuildWriteBatcher = func(ctx context.Context, maxBatchSize int, flushInterval time.Duration) (*writebatcher.WriteBatcher[BatchedWrite], error) {
		return writebatcher.New(ctx, writebatcher.Config[BatchedWrite]{
			MaxBatchSize:  1,
			FlushInterval: time.Hour,
			BeginTx: func(ctx context.Context) (*sql.Tx, error) {
				return nil, nil
			},
			Flush: func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
				return nil
			},
		})
	}
	infra.testHookGetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 0, nil
	}
	infra.SetupDB(context.Background(), config.DefaultConfig())
	if err := infra.writeBatcher.Close(); err != nil {
		t.Fatalf("close batcher: %v", err)
	}

	entry := &cachelite.HTTPCacheEntry{Path: "/gallery/1"}
	logs := withLogCapture(t, slog.LevelDebug, func() {
		infra.submitCacheWrite(entry)
	})
	if !strings.Contains(logs, "failed to submit cache write") {
		t.Errorf("expected submit error log, got: %s", logs)
	}
}

func TestInfrastructureService_IncrementETag_LoadError(t *testing.T) {
	infra := NewInfrastructureService()
	mockSvc := &mockConfigServiceForInfraETag{loadErr: errors.New("load failed")}

	_, err := infra.IncrementETag(context.Background(), mockSvc)
	if err == nil || !strings.Contains(err.Error(), "failed to load config") {
		t.Fatalf("error = %v, want failed to load config", err)
	}
}

func TestInfrastructureService_IncrementETag_SaveError(t *testing.T) {
	infra := NewInfrastructureService()
	cfg := config.DefaultConfig()
	cfg.ETagVersion = "20260701-01"
	mockSvc := &mockConfigServiceForInfraETag{
		loadReturn: cfg,
		saveErr:    errors.New("save failed"),
	}

	_, err := infra.IncrementETag(context.Background(), mockSvc)
	if err == nil || !strings.Contains(err.Error(), "failed to save config") {
		t.Fatalf("error = %v, want failed to save config", err)
	}
}

func TestInfrastructureService_IncrementETag_RotateError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = newFakePool(10, 2)
	cfg := config.DefaultConfig()
	cfg.ETagVersion = "20260701-01"
	wantETag := config.IncrementETagVersion(cfg.ETagVersion)
	mockSvc := &mockConfigServiceForInfraETag{loadReturn: cfg}
	infra.cacheRotator = &fakeCacheRotator{rotateErr: errors.New("rotate failed")}

	logs := withLogCapture(t, slog.LevelWarn, func() {
		newETag, err := infra.IncrementETag(context.Background(), mockSvc)
		if err != nil {
			t.Fatalf("IncrementETag error = %v", err)
		}
		if newETag != wantETag {
			t.Fatalf("newETag = %q, want %q", newETag, wantETag)
		}
	})
	if !strings.Contains(logs, "failed to rotate HTTP cache after ETag increment") {
		t.Errorf("expected rotate error log, got: %s", logs)
	}
}

func TestInfrastructureService_IncrementETag_Success(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = newFakePool(10, 2)
	cfg := config.DefaultConfig()
	cfg.ETagVersion = "20260701-01"
	wantETag := config.IncrementETagVersion(cfg.ETagVersion)
	mockSvc := &mockConfigServiceForInfraETag{loadReturn: cfg}
	rotator := &fakeCacheRotator{}
	infra.cacheRotator = rotator
	infra.cacheSizeBytes.Store(999)

	newETag, err := infra.IncrementETag(context.Background(), mockSvc)
	if err != nil {
		t.Fatalf("IncrementETag error = %v", err)
	}
	if newETag != wantETag {
		t.Fatalf("newETag = %q, want %q", newETag, wantETag)
	}
	if !mockSvc.saveCalled {
		t.Error("Save should be called")
	}
	if mockSvc.savedConfig == nil || mockSvc.savedConfig.ETagVersion != wantETag {
		t.Errorf("saved ETag = %v, want %q", mockSvc.savedConfig, wantETag)
	}
	if !rotator.rotateCalled {
		t.Error("RotateCacheTable should be called")
	}
	if infra.cacheSizeBytes.Load() != 0 {
		t.Fatalf("cacheSizeBytes = %d, want 0", infra.cacheSizeBytes.Load())
	}
}

func TestInfrastructureService_LogDBPoolConfiguredVsEffective_NilPool(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = nil
	infra.dbRoPool = newFakePool(10, 2)

	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.logDBPoolConfiguredVsEffective("test", 10, 2)
	})
	if logs != "" {
		t.Errorf("expected no logs, got: %s", logs)
	}
}

func TestInfrastructureService_LogDBPoolConfiguredVsEffective_InvalidMinGreaterThanMax(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = newFakePool(5, 2)
	infra.dbRoPool = newFakePool(5, 2)

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.logDBPoolConfiguredVsEffective("test", 5, 20)
	})
	if !strings.Contains(logs, "invalid DB pool combination") {
		t.Errorf("expected invalid combination warning, got: %s", logs)
	}
}

func TestInfrastructureService_LogDBPoolConfiguredVsEffective_NormalPath(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = newFakePool(7, 3)
	infra.dbRoPool = newFakePool(7, 3)

	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.logDBPoolConfiguredVsEffective("test", 10, 2)
	})
	for _, want := range []string{
		"rw_configured_max=10",
		"rw_effective_max=7",
		"rw_configured_min_idle=2",
		"rw_effective_min_idle=3",
		"ro_configured_max=10",
		"ro_effective_max=7",
		"ro_configured_min_idle=2",
		"ro_effective_min_idle=3",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("expected %q in logs, got: %s", want, logs)
		}
	}
}
