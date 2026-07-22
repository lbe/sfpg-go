//go:build integration

package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/files"

	_ "github.com/ncruces/go-sqlite3/driver"
)

// newIntegratedService creates an InfrastructureService with a real on-disk
// database in a temporary directory. The caller must call Shutdown.
func newIntegratedService(t *testing.T, ctx context.Context, cfg *config.Config) *InfrastructureService {
	t.Helper()
	rootDir := t.TempDir()
	infra := NewInfrastructureService()
	infra.rootDir = rootDir
	infra.SetupDB(ctx, cfg)
	infra.CalibrateCacheSizeNow(ctx)
	infra.StartWriteBatcher(ctx, true)
	return infra
}

func TestInfrastructureService_SetupDB_RealPools(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 2
	cfg.DBMinIdleConnections = 1

	infra := newIntegratedService(t, ctx, cfg)
	defer infra.Shutdown()

	if infra.dbRwPool == nil || infra.dbRoPool == nil {
		t.Fatal("pools not created")
	}
	if infra.writeBatcher == nil {
		t.Fatal("writeBatcher not created")
	}
	if infra.cacheStore == nil {
		t.Fatal("cacheStore not created")
	}
	size, err := infra.cacheStore.SizeBytes(ctx)
	if err != nil {
		t.Fatalf("SizeBytes error: %v", err)
	}
	if size != 0 {
		t.Fatalf("initial cache size = %d, want 0", size)
	}
}

func TestInfrastructureService_ReconfigurePools_RealPools(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 2
	cfg.DBMinIdleConnections = 1

	infra := newIntegratedService(t, ctx, cfg)
	defer infra.Shutdown()

	newCfg := config.DefaultConfig()
	newCfg.DBMaxPoolSize = 3
	newCfg.DBMinIdleConnections = 1

	if err := infra.ReconfigurePools(ctx, newCfg); err != nil {
		t.Fatalf("ReconfigurePools error: %v", err)
	}
	if infra.dbRwPool.Config.MaxConnections != 3 {
		t.Fatalf("RW max connections = %d, want 3", infra.dbRwPool.Config.MaxConnections)
	}
	if infra.dbRwPool.Config.MinIdleConnections != 1 {
		t.Fatalf("RW min idle = %d, want 1", infra.dbRwPool.Config.MinIdleConnections)
	}
	if infra.dbRoPool.Config.MaxConnections != 3 {
		t.Fatalf("RO max connections = %d, want 3", infra.dbRoPool.Config.MaxConnections)
	}
	if infra.writeBatcher == nil {
		t.Fatal("writeBatcher not recreated")
	}
}

func TestInfrastructureService_buildWriteBatcher_BeginTxError(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 2
	cfg.DBMinIdleConnections = 1

	infra := newIntegratedService(t, ctx, cfg)
	defer infra.Shutdown()

	// Force BeginTx failures by closing the pool.
	if err := infra.dbRwPool.Close(); err != nil {
		t.Fatalf("close pool: %v", err)
	}

	infra.testSeams.GetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 1234, nil
	}

	entry := &cachelite.HTTPCacheEntry{
		Path:          "/gallery/1",
		ContentLength: sql.NullInt64{Int64: 100, Valid: true},
		Body:          []byte("body"),
	}

	logs := withLogCapture(t, slog.LevelError, func() {
		if err := infra.writeBatcher.Submit(BatchedWrite{CacheEntry: entry}); err != nil {
			t.Fatalf("submit: %v", err)
		}
		// Give the worker a moment to attempt BeginTx and run OnError.
		time.Sleep(100 * time.Millisecond)
		if err := infra.writeBatcher.Close(); err != nil {
			t.Fatalf("close batcher: %v", err)
		}
	})
	if !strings.Contains(logs, "failed to flush unified batch") {
		t.Errorf("expected flush error log, got: %s", logs)
	}
	if !strings.Contains(logs, "cache_entries=1") {
		t.Errorf("expected cache_entries=1 log, got: %s", logs)
	}
	if infra.cacheSizeBytes.Load() != 1234 {
		t.Fatalf("cacheSizeBytes = %d, want 1234", infra.cacheSizeBytes.Load())
	}
}

func TestInfrastructureService_flushBatchedWrites_FileError(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	cpc, err := infra.dbRwPool.Get()
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	defer infra.dbRwPool.Put(cpc)
	infra.batcherQueries = cpc.Queries

	tx, err := cpc.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if rbErr := tx.Rollback(); rbErr != nil {
		t.Fatalf("rollback: %v", rbErr)
	}

	batch := []BatchedWrite{{File: &files.File{Path: "test.jpg"}}}
	err = infra.flushBatchedWrites(ctx, tx, batch)
	if err == nil || !strings.Contains(err.Error(), "write file test.jpg") {
		t.Fatalf("error = %v, want write file test.jpg", err)
	}
}

func TestInfrastructureService_flushBatchedWrites_GalleryCacheError(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	cpc, err := infra.dbRwPool.Get()
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	defer infra.dbRwPool.Put(cpc)
	infra.batcherQueries = cpc.Queries

	tx, err := cpc.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if rbErr := tx.Rollback(); rbErr != nil {
		t.Fatalf("rollback: %v", rbErr)
	}

	batch := []BatchedWrite{{
		CacheEntry: &cachelite.HTTPCacheEntry{
			Path:          "/gallery/1",
			ETag:          sql.NullString{String: "e", Valid: true},
			ContentLength: sql.NullInt64{Int64: 4, Valid: true},
			Body:          []byte("body"),
		},
	}}
	err = infra.flushBatchedWrites(ctx, tx, batch)
	if err == nil || !strings.Contains(err.Error(), "store gallery cache /gallery/1") {
		t.Fatalf("error = %v, want store gallery cache", err)
	}
}

func TestInfrastructureService_flushBatchedWrites_OtherCacheError(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	cpc, err := infra.dbRwPool.Get()
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	defer infra.dbRwPool.Put(cpc)
	infra.batcherQueries = cpc.Queries

	tx, err := cpc.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if rbErr := tx.Rollback(); rbErr != nil {
		t.Fatalf("rollback: %v", rbErr)
	}

	batch := []BatchedWrite{{
		CacheEntry: &cachelite.HTTPCacheEntry{
			Path:          "/image/1",
			ETag:          sql.NullString{String: "e", Valid: true},
			ContentLength: sql.NullInt64{Int64: 4, Valid: true},
			Body:          []byte("body"),
		},
	}}
	err = infra.flushBatchedWrites(ctx, tx, batch)
	if err == nil || !strings.Contains(err.Error(), "store cache /image/1") {
		t.Fatalf("error = %v, want store cache", err)
	}
}

func TestInfrastructureService_buildWriteBatcher_OnSuccessCacheEntry(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 2
	cfg.DBMinIdleConnections = 1

	infra := newIntegratedService(t, ctx, cfg)
	defer infra.Shutdown()

	// Use Body length 40 with ContentLength 100 to prove the counter uses
	// len(Body) (stored bytes) rather than ContentLength (uncompressed).
	entry := &cachelite.HTTPCacheEntry{
		Key:           "key",
		Path:          "/gallery/1",
		Method:        "GET",
		Status:        200,
		ContentLength: sql.NullInt64{Int64: 100, Valid: true},
		Body:          bytes.Repeat([]byte("b"), 40),
	}
	if err := infra.writeBatcher.Submit(BatchedWrite{CacheEntry: entry}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for infra.writeBatcher.PendingCount() > 0 {
			time.Sleep(5 * time.Millisecond)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for batcher")
	}

	// Counter must use len(Body) = 40, not ContentLength = 100
	if infra.cacheSizeBytes.Load() != 40 {
		t.Fatalf("cacheSizeBytes = %d, want 40 (len(Body), not ContentLength 100)",
			infra.cacheSizeBytes.Load())
	}
}

func TestInfrastructureService_buildWriteBatcher_OnErrorFileFlush(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 2
	cfg.DBMinIdleConnections = 1

	infra := newIntegratedService(t, ctx, cfg)
	defer infra.Shutdown()

	infra.testSeams.FlushBatchedWrites = func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
		return errors.New("forced flush failure")
	}

	file := &files.File{
		Path:      "test.jpg",
		Thumbnail: bytes.NewBuffer([]byte("x")),
	}

	logs := withLogCapture(t, slog.LevelError, func() {
		if err := infra.writeBatcher.Submit(BatchedWrite{File: file}); err != nil {
			t.Fatalf("submit: %v", err)
		}
		// Give the worker time to attempt the flush and invoke OnError. The
		// batcher re-enqueues on error, so we close it rather than wait for
		// PendingCount to reach zero.
		time.Sleep(100 * time.Millisecond)
		if err := infra.writeBatcher.Close(); err != nil {
			t.Fatalf("close batcher: %v", err)
		}
	})
	if !strings.Contains(logs, "failed to flush unified batch") {
		t.Errorf("expected flush error log, got: %s", logs)
	}
	if !strings.Contains(logs, "files=1") {
		t.Errorf("expected files=1 log, got: %s", logs)
	}
	if strings.Contains(logs, "cache_entries=1") {
		t.Error("did not expect cache_entries=1 for a file-only batch")
	}
}

func TestInfrastructureService_buildWriteBatcher_OnSuccessFileStats(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 2
	cfg.DBMinIdleConnections = 1

	infra := newIntegratedService(t, ctx, cfg)
	defer infra.Shutdown()

	filesBatch := []*files.File{
		{Path: "test.jpg", Thumbnail: bytes.NewBuffer([]byte("thumb"))},
		{Path: "dir1/dir2/test.jpg", Thumbnail: bytes.NewBuffer([]byte("thumb"))},
	}
	for _, file := range filesBatch {
		if err := infra.writeBatcher.Submit(BatchedWrite{File: file}); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}

	done := make(chan struct{})
	go func() {
		for infra.writeBatcher.PendingCount() > 0 {
			time.Sleep(5 * time.Millisecond)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for batcher")
	}
}

func TestInfrastructureService_buildWriteBatcher_OnSuccessEvicts(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 2
	cfg.DBMinIdleConnections = 1

	infra := newIntegratedService(t, ctx, cfg)
	defer infra.Shutdown()

	cfgHTTPCache := config.DefaultConfig()
	cfgHTTPCache.EnableHTTPCache = true
	cfgHTTPCache.CacheMaxSize = 200
	cfgHTTPCache.CacheMaxEntrySize = 200
	infra.InitializeHTTPCache(cfgHTTPCache)

	// Seed cache store with entries that exceed the max total size.
	for i := 0; i < 3; i++ {
		entry := &cachelite.HTTPCacheEntry{
			Key:           "seed-" + string(rune('a'+i)),
			Path:          "/gallery/1",
			Method:        "GET",
			Status:        200,
			ContentLength: sql.NullInt64{Int64: 100, Valid: true},
			Body:          bytes.Repeat([]byte("a"), 100),
		}
		if err := infra.cacheStore.Store(ctx, entry); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
	}
	seededSize, sizeErr := infra.cacheStore.SizeBytes(ctx)
	if sizeErr != nil {
		t.Fatalf("seeded SizeBytes: %v", sizeErr)
	}
	infra.cacheSizeBytes.Store(seededSize)

	// Submit another entry through the batcher.
	entry := &cachelite.HTTPCacheEntry{
		Key:           "new",
		Path:          "/gallery/1",
		Method:        "GET",
		Status:        200,
		ContentLength: sql.NullInt64{Int64: 50, Valid: true},
		Body:          bytes.Repeat([]byte("b"), 50),
	}
	if err := infra.writeBatcher.Submit(BatchedWrite{CacheEntry: entry}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for infra.writeBatcher.PendingCount() > 0 {
			time.Sleep(5 * time.Millisecond)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for batcher")
	}

	// Actual DB size should be within the configured limit.
	size, err := infra.cacheStore.SizeBytes(ctx)
	if err != nil {
		t.Fatalf("SizeBytes: %v", err)
	}
	if size > cfgHTTPCache.CacheMaxSize {
		t.Fatalf("cache size = %d, want <= %d", size, cfgHTTPCache.CacheMaxSize)
	}
	if infra.cacheSizeBytes.Load() != size {
		t.Fatalf("cacheSizeBytes = %d, want %d", infra.cacheSizeBytes.Load(), size)
	}
}

func TestInfrastructureService_walCheckpointAfterCommit_FiveMinuteRealCheckpoint(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.walCheckpointAfterCommit(ctx, time.Now().Add(-6*time.Minute), time.Now(), 0)
	})
	if !strings.Contains(logs, "WAL checkpoint: 5 minutes elapsed") {
		t.Errorf("expected 5-minute log, got: %s", logs)
	}
}

func TestInfrastructureService_MaybeRunPeriodicOptimize_RealOptimize(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()
	infra.lastPragmaOptimizeRun.Store(time.Now().Add(-65 * time.Minute))
	infra.dbOptimizeInterval.Store(int64(time.Hour))

	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.maybeRunPeriodicOptimize(ctx)
	})
	if !strings.Contains(logs, "PRAGMA optimize: interval elapsed") {
		t.Errorf("expected optimize log, got: %s", logs)
	}
}

func TestInfrastructureService_walCheckpointAfterCommit_RealWALThreshold(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	// Create an oversized WAL file to trigger the threshold path with a real pool.
	walPath := infra.dbPaths.Main + "-wal"
	f, err := os.Create(walPath)
	if err != nil {
		t.Fatalf("create WAL: %v", err)
	}
	if _, err := f.Write(make([]byte, 256*1024*1024+1)); err != nil {
		t.Fatalf("write WAL: %v", err)
	}
	f.Close()

	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.walCheckpointAfterCommit(ctx, time.Time{}, time.Time{}, 0)
	})
	if !strings.Contains(logs, "WAL file exceeds threshold, forcing checkpoint") {
		t.Errorf("expected WAL threshold log, got: %s", logs)
	}
}

func TestInfrastructureService_InvalidateHTTPCache_RealPool(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.EnableHTTPCache = true
	infra := newIntegratedService(t, ctx, cfg)
	defer infra.Shutdown()
	infra.InitializeHTTPCache(cfg)

	// Seed a cache entry.
	entry := &cachelite.HTTPCacheEntry{
		Key:           "key",
		Path:          "/gallery/1",
		Method:        "GET",
		Status:        200,
		ContentLength: sql.NullInt64{Int64: 10, Valid: true},
		Body:          []byte("0123456789"),
	}
	if err := infra.cacheStore.Store(ctx, entry); err != nil {
		t.Fatalf("store cache: %v", err)
	}
	infra.cacheSizeBytes.Store(10)

	infra.InvalidateHTTPCache()
	if infra.cacheSizeBytes.Load() != 0 {
		t.Fatalf("cacheSizeBytes = %d, want 0", infra.cacheSizeBytes.Load())
	}
	size, err := infra.cacheStore.SizeBytes(ctx)
	if err != nil {
		t.Fatalf("SizeBytes: %v", err)
	}
	if size != 0 {
		t.Fatalf("cache store size = %d, want 0", size)
	}
}

func TestInfrastructureService_IncrementETag_RealPool(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	cfgService := config.NewService(infra.dbRwPool, infra.dbRoPool)
	if err := cfgService.EnsureDefaults(ctx, infra.rootDir); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}

	oldCfg, err := cfgService.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Seed a cache entry so we can verify rotation clears the counter.
	entry := &cachelite.HTTPCacheEntry{
		Key:           "key",
		Path:          "/gallery/1",
		Method:        "GET",
		Status:        200,
		ContentLength: sql.NullInt64{Int64: 10, Valid: true},
		Body:          []byte("0123456789"),
	}
	if storeErr := infra.cacheStore.Store(ctx, entry); storeErr != nil {
		t.Fatalf("store cache: %v", storeErr)
	}
	infra.cacheSizeBytes.Store(10)

	newETag, err := infra.IncrementETag(ctx, cfgService)
	if err != nil {
		t.Fatalf("IncrementETag: %v", err)
	}
	if newETag == oldCfg.ETagVersion {
		t.Fatalf("ETag did not change: %s", newETag)
	}

	loaded, err := cfgService.Load(ctx)
	if err != nil {
		t.Fatalf("Load after increment: %v", err)
	}
	if loaded.ETagVersion != newETag {
		t.Fatalf("DB ETag = %q, want %q", loaded.ETagVersion, newETag)
	}
	if infra.cacheSizeBytes.Load() != 0 {
		t.Fatalf("cacheSizeBytes = %d, want 0", infra.cacheSizeBytes.Load())
	}
	size, err := infra.cacheStore.SizeBytes(ctx)
	if err != nil {
		t.Fatalf("SizeBytes: %v", err)
	}
	if size != 0 {
		t.Fatalf("cache store size = %d, want 0", size)
	}
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
	infra.testSeams.WALCheckpointQuery = func(ctx context.Context, conn *sql.Conn) (*sql.Rows, error) {
		return conn.QueryContext(ctx, "SELECT 1")
	}

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.performWALCheckpoint(context.Background(), fakePool)
	})
	if !strings.Contains(logs, "failed to parse wal_checkpoint result") {
		t.Errorf("expected scan error log, got: %s", logs)
	}
}
