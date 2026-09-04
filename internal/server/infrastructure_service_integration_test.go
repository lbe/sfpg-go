//go:build integration

package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/tableswap"
	"github.com/lbe/sfpg-go/internal/writebatcher"

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
	infra.StartWriteBatcher(ctx, true, config.DefaultDQueMaxDiskBytes)
	return infra
}

// waitForBatcherDrain blocks until the write batcher has flushed all pending
// items. PendingCount reaches zero only after the flush (and its success/error
// side effects) completes, so this is a safe wait before asserting on counters.
func waitForBatcherDrain(t *testing.T, wb *writebatcher.WriteBatcher[BatchedWrite]) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for wb.PendingCount() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for write batcher to drain")
		}
		time.Sleep(5 * time.Millisecond)
	}
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
		// Close drains remaining items synchronously: the worker attempts
		// BeginTx, runs OnError, and re-enqueues before Close returns.
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

	waitForBatcherDrain(t, infra.writeBatcher)

	// Counter must use len(Body) = 40, not ContentLength = 100
	if infra.cacheSizeBytes.Load() != 40 {
		t.Fatalf("cacheSizeBytes = %d, want 40 (len(Body), not ContentLength 100)",
			infra.cacheSizeBytes.Load())
	}
	if infra.cacheEntryCount.Load() != 1 {
		t.Fatalf("cacheEntryCount = %d, want 1", infra.cacheEntryCount.Load())
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
		// Close drains remaining items synchronously: the flush fails, OnError
		// runs, and the batch is re-enqueued before Close returns.
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

	waitForBatcherDrain(t, infra.writeBatcher)
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

	waitForBatcherDrain(t, infra.writeBatcher)

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
		infra.walCheckpointAfterCommit(ctx, time.Now().Add(-6*time.Minute), time.Now(), 0, false)
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

	infra.lastFlushWroteDML.Store(true)
	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.walCheckpointAfterCommit(ctx, time.Time{}, time.Time{}, 0, true)
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
	if infra.cacheEntryCount.Load() != 0 {
		t.Fatalf("cacheEntryCount = %d, want 0", infra.cacheEntryCount.Load())
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
	infra.cacheEntryCount.Store(1)

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
	if infra.cacheEntryCount.Load() != 0 {
		t.Fatalf("cacheEntryCount = %d, want 0", infra.cacheEntryCount.Load())
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

// submitBatch submits each write individually (writeBatcher.Submit takes one item).
func submitBatch(infra *InfrastructureService, batch []BatchedWrite) error {
	for i := range batch {
		if err := infra.writeBatcher.Submit(batch[i]); err != nil {
			return err
		}
	}
	return nil
}

// cloneEmptyFileFolderIndex creates an empty file_folder_index_new by cloning
// the schema of the existing file_folder_index table. It uses the infrastructure
// service's RW pool so the freshly-built database (with migrations applied)
// provides the source schema.
func cloneEmptyFileFolderIndex(t *testing.T, infra *InfrastructureService, ctx context.Context) {
	cpc, err := infra.dbRwPool.Get()
	if err != nil {
		t.Fatalf("get RW conn: %v", err)
	}
	defer infra.dbRwPool.Put(cpc)
	if err := tableswap.CloneEmpty(ctx, cpc.Conn, "file_folder_index"); err != nil {
		t.Fatalf("clone empty file_folder_index: %v", err)
	}
}

// seedFolderIndexRefs inserts the folder and file rows referenced by the
// file_folder_index foreign keys so INSERTs into file_folder_index_new do not
// trip the file_id -> files(id) constraint. Files are referenced by id only.
func seedFolderIndexRefs(t *testing.T, infra *InfrastructureService, ctx context.Context, fileIDs []int64, folderID int64) {
	t.Helper()
	cpc, err := infra.dbRwPool.Get()
	if err != nil {
		t.Fatalf("get RW conn: %v", err)
	}
	defer infra.dbRwPool.Put(cpc)
	if _, err := cpc.Conn.ExecContext(ctx,
		"INSERT OR IGNORE INTO folder_paths (id, path) VALUES (?, ?)", folderID, fmt.Sprintf("/folder-%d", folderID)); err != nil {
		t.Fatalf("insert folder_path: %v", err)
	}
	if _, err := cpc.Conn.ExecContext(ctx,
		"INSERT OR IGNORE INTO folders (id, parent_id, path_id, name) VALUES (?, NULL, ?, ?)",
		folderID, folderID, fmt.Sprintf("f%d", folderID)); err != nil {
		t.Fatalf("insert folder: %v", err)
	}
	for _, id := range fileIDs {
		if _, err := cpc.Conn.ExecContext(ctx,
			"INSERT OR IGNORE INTO file_paths (id, path) VALUES (?, ?)", id, fmt.Sprintf("/file-%d.jpg", id)); err != nil {
			t.Fatalf("insert file_path: %v", err)
		}
		if _, err := cpc.Conn.ExecContext(ctx,
			"INSERT OR IGNORE INTO files (id, folder_id, path_id, filename) VALUES (?, ?, ?, ?)",
			id, folderID, id, fmt.Sprintf("file-%d.jpg", id)); err != nil {
			t.Fatalf("insert file: %v", err)
		}
	}
}

// countFolderIndexRows returns the number of rows in file_folder_index_new.
func countFolderIndexRows(t *testing.T, infra *InfrastructureService, ctx context.Context) int {
	t.Helper()
	cpc, err := infra.dbRwPool.Get()
	if err != nil {
		t.Fatalf("get RW conn: %v", err)
	}
	defer infra.dbRwPool.Put(cpc)
	var n int
	if err := cpc.Conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM file_folder_index_new").Scan(&n); err != nil {
		t.Fatalf("count file_folder_index_new: %v", err)
	}
	return n
}

func TestFlushBatchedWrites_FolderIndexInsertsRow(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	cloneEmptyFileFolderIndex(t, infra, ctx)
	seedFolderIndexRefs(t, infra, ctx, []int64{10, 11, 12}, 2)

	infra.folderIndexRebuildActive.Store(true)
	const gen = int64(77)
	infra.folderIndexGeneration.Store(gen)

	row := &files.FolderIndexRow{
		FileID:     10,
		FolderID:   2,
		ImageIndex: 1,
		ImageCount: 3,
		PrevID:     sql.NullInt64{Valid: false},
		NextID:     sql.NullInt64{Int64: 11, Valid: true},
		FirstID:    10,
		LastID:     12,
		Generation: gen,
	}
	if err := infra.writeBatcher.Submit(BatchedWrite{FolderIndex: row}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitForBatcherDrain(t, infra.writeBatcher)

	if got := countFolderIndexRows(t, infra, ctx); got != 1 {
		t.Fatalf("file_folder_index_new rows = %d, want 1", got)
	}

	cpc, err := infra.dbRwPool.Get()
	if err != nil {
		t.Fatalf("get RW conn: %v", err)
	}
	defer infra.dbRwPool.Put(cpc)
	var (
		fileID, folderID, imageIndex, imageCount, firstID, lastID int64
		prevID, nextID                                            sql.NullInt64
	)
	if err := cpc.Conn.QueryRowContext(ctx,
		"SELECT file_id, folder_id, image_index, image_count, prev_id, next_id, first_id, last_id FROM file_folder_index_new").
		Scan(&fileID, &folderID, &imageIndex, &imageCount, &prevID, &nextID, &firstID, &lastID); err != nil {
		t.Fatalf("scan row: %v", err)
	}
	if fileID != 10 || folderID != 2 || imageIndex != 1 || imageCount != 3 || firstID != 10 || lastID != 12 {
		t.Errorf("base columns mismatch: %+v", map[string]any{
			"file_id": fileID, "folder_id": folderID, "image_index": imageIndex,
			"image_count": imageCount, "first_id": firstID, "last_id": lastID,
		})
	}
	if prevID.Valid {
		t.Errorf("prev_id should be NULL, got %d", prevID.Int64)
	}
	if !nextID.Valid || nextID.Int64 != 11 {
		t.Errorf("next_id = %+v, want Valid=11", nextID)
	}
}

func TestFlushBatchedWrites_FolderIndexSkippedWhenDestMissing(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	// No file_folder_index_new exists. Flag state is irrelevant for this case.
	infra.folderIndexRebuildActive.Store(true)

	entry := &cachelite.HTTPCacheEntry{
		Key:           "key",
		Path:          "/gallery/1",
		Method:        "GET",
		Status:        200,
		ContentLength: sql.NullInt64{Int64: 4, Valid: true},
		Body:          []byte("body"),
	}
	batch := []BatchedWrite{
		{CacheEntry: entry},
		{FolderIndex: &files.FolderIndexRow{FileID: 1, FolderID: 1, Generation: 1}},
	}
	if err := submitBatch(infra, batch); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitForBatcherDrain(t, infra.writeBatcher)

	// Cache entry must still be flushed.
	size, err := infra.cacheStore.SizeBytes(ctx)
	if err != nil {
		t.Fatalf("SizeBytes: %v", err)
	}
	if size != 4 {
		t.Errorf("cache size = %d, want 4 (cache entry still flushed)", size)
	}

	// file_folder_index_new must not exist.
	cpc, err := infra.dbRwPool.Get()
	if err != nil {
		t.Fatalf("get RW conn: %v", err)
	}
	defer infra.dbRwPool.Put(cpc)
	var exists int64
	if err := cpc.Conn.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='file_folder_index_new')").
		Scan(&exists); err != nil {
		t.Fatalf("check dest exists: %v", err)
	}
	if exists != 0 {
		t.Error("file_folder_index_new should not exist after a skip")
	}
}

func TestFlushBatchedWrites_FolderIndexSkippedWhenRebuildInactive(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	cloneEmptyFileFolderIndex(t, infra, ctx)
	seedFolderIndexRefs(t, infra, ctx, []int64{1}, 1)
	infra.folderIndexRebuildActive.Store(false)

	entry := &cachelite.HTTPCacheEntry{
		Key:           "key",
		Path:          "/gallery/1",
		Method:        "GET",
		Status:        200,
		ContentLength: sql.NullInt64{Int64: 4, Valid: true},
		Body:          []byte("body"),
	}
	batch := []BatchedWrite{
		{CacheEntry: entry},
		{FolderIndex: &files.FolderIndexRow{FileID: 1, FolderID: 1, Generation: 1}},
	}
	if err := submitBatch(infra, batch); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitForBatcherDrain(t, infra.writeBatcher)

	size, err := infra.cacheStore.SizeBytes(ctx)
	if err != nil {
		t.Fatalf("SizeBytes: %v", err)
	}
	if size != 4 {
		t.Errorf("cache size = %d, want 4 (cache entry still flushed)", size)
	}
	if got := countFolderIndexRows(t, infra, ctx); got != 0 {
		t.Errorf("file_folder_index_new rows = %d, want 0 (rebuild inactive)", got)
	}
}

func TestFlushBatchedWrites_FolderIndexSkippedWhenGenerationMismatch(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	cloneEmptyFileFolderIndex(t, infra, ctx)
	seedFolderIndexRefs(t, infra, ctx, []int64{1, 2}, 1)
	infra.folderIndexRebuildActive.Store(true)
	infra.folderIndexGeneration.Store(99) // current generation

	entry := &cachelite.HTTPCacheEntry{
		Key:           "key",
		Path:          "/gallery/1",
		Method:        "GET",
		Status:        200,
		ContentLength: sql.NullInt64{Int64: 4, Valid: true},
		Body:          []byte("body"),
	}
	// Stale leftover row with an unequal (not 0/1) generation must be skipped.
	stale := &files.FolderIndexRow{FileID: 1, FolderID: 1, Generation: 5}
	matching := &files.FolderIndexRow{FileID: 2, FolderID: 1, Generation: 99}
	batch := []BatchedWrite{
		{CacheEntry: entry},
		{FolderIndex: stale},
		{FolderIndex: matching},
	}
	if err := submitBatch(infra, batch); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitForBatcherDrain(t, infra.writeBatcher)

	size, err := infra.cacheStore.SizeBytes(ctx)
	if err != nil {
		t.Fatalf("SizeBytes: %v", err)
	}
	if size != 4 {
		t.Errorf("cache size = %d, want 4 (cache entry still flushed)", size)
	}
	if got := countFolderIndexRows(t, infra, ctx); got != 1 {
		t.Fatalf("file_folder_index_new rows = %d, want 1 (only matching generation)", got)
	}

	cpc, err := infra.dbRwPool.Get()
	if err != nil {
		t.Fatalf("get RW conn: %v", err)
	}
	defer infra.dbRwPool.Put(cpc)
	var fileID int64
	if err := cpc.Conn.QueryRowContext(ctx, "SELECT file_id FROM file_folder_index_new").Scan(&fileID); err != nil {
		t.Fatalf("scan file_id: %v", err)
	}
	if fileID != 2 {
		t.Errorf("inserted file_id = %d, want 2 (matching generation); stale 1 must be skipped", fileID)
	}
}

// TestFlushBatchedWrites_IndexOnlySkippedWithoutBeginTx verifies that an
// index-only batch with rebuild inactive is dropped via DropWithoutFlush
// before BeginTx. After GREEN, the OnBeginTx hook counter stays 0.
func TestFlushBatchedWrites_IndexOnlySkippedWithoutBeginTx(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	var beginTxCount atomic.Int64
	infra.testSeams.OnBeginTx = func() { beginTxCount.Add(1) }

	// Rebuild inactive (default). Index-only batch.
	if err := infra.writeBatcher.Submit(BatchedWrite{
		FolderIndex: &files.FolderIndexRow{FileID: 1, FolderID: 1, Generation: 1},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitForBatcherDrain(t, infra.writeBatcher)

	if got := beginTxCount.Load(); got != 0 {
		t.Errorf("OnBeginTx called %d times, want 0 (DropWithoutFlush should skip BeginTx)", got)
	}
}

// TestFlushBatchedWrites_DropWithoutFlushFalseIncrementsBeginTx is a positive
// control: a DropWithoutFlush-false batch must reach BeginTx and Put. Uses
// a Cache entry so the classifier does not drop the batch.
func TestFlushBatchedWrites_DropWithoutFlushFalseIncrementsBeginTx(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	var beginTxCount, putCount atomic.Int64
	infra.testSeams.OnBeginTx = func() { beginTxCount.Add(1) }
	infra.testSeams.OnPut = func() { putCount.Add(1) }

	// Cache entry — not FolderIndex only, so DropWithoutFlush returns false.
	entry := &cachelite.HTTPCacheEntry{
		Key:           "key",
		Path:          "/gallery/1",
		Method:        "GET",
		Status:        200,
		ContentLength: sql.NullInt64{Int64: 4, Valid: true},
		Body:          []byte("body"),
	}
	if err := infra.writeBatcher.Submit(BatchedWrite{CacheEntry: entry}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitForBatcherDrain(t, infra.writeBatcher)

	if got := beginTxCount.Load(); got == 0 {
		t.Error("OnBeginTx was not called (DropWithoutFlush-false batch must reach BeginTx)")
	}
	if got := putCount.Load(); got == 0 {
		t.Error("OnPut was not called (DropWithoutFlush-false batch must Put the connection)")
	}
}

// TestFlushBatchedWrites_LastFlushWroteDMLFalseOnSkipOnlyBeginTx verifies
// that a skip-only BeginTx (dest-missing with rebuild active) leaves
// lastFlushWroteDML false even when it was true before.
func TestFlushBatchedWrites_LastFlushWroteDMLFalseOnSkipOnlyBeginTx(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	// Rebuild active but no dest table — index-only skip inside flushBatchedWrites.
	infra.folderIndexRebuildActive.Store(true)
	infra.lastFlushWroteDML.Store(true)

	if err := infra.writeBatcher.Submit(BatchedWrite{
		FolderIndex: &files.FolderIndexRow{FileID: 1, FolderID: 1, Generation: 1},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitForBatcherDrain(t, infra.writeBatcher)

	if infra.lastFlushWroteDML.Load() {
		t.Error("lastFlushWroteDML should be false after skip-only BeginTx")
	}
}

// TestFlushBatchedWrites_LastFlushWroteDMLTrueOnDML verifies that a flush
// with a Cache entry sets lastFlushWroteDML true.
func TestFlushBatchedWrites_LastFlushWroteDMLTrueOnDML(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedService(t, ctx, config.DefaultConfig())
	defer infra.Shutdown()

	entry := &cachelite.HTTPCacheEntry{
		Key:           "key",
		Path:          "/gallery/1",
		Method:        "GET",
		Status:        200,
		ContentLength: sql.NullInt64{Int64: 4, Valid: true},
		Body:          []byte("body"),
	}
	if err := infra.writeBatcher.Submit(BatchedWrite{CacheEntry: entry}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitForBatcherDrain(t, infra.writeBatcher)

	if !infra.lastFlushWroteDML.Load() {
		t.Error("lastFlushWroteDML should be true after Cache write")
	}
}
