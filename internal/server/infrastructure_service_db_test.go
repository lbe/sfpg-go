//nolint:govet // infra DB tests reuse err and share fixture setup scopes
package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"

	_ "github.com/ncruces/go-sqlite3/driver"
)

// newIntegratedService creates an InfrastructureService with memory-mode pools.
// The caller must call Shutdown.
func newIntegratedService(t *testing.T, ctx context.Context, cfg *config.Config) *InfrastructureService {
	t.Helper()
	rootDir := t.TempDir()
	infra := NewInfrastructureService()
	infra.rootDir = rootDir

	max, minIdle, interval := database.EffectivePoolLimits(cfg)
	poolCfg := func(ro bool) dbconnpool.Config {
		return dbconnpool.Config{
			DriverName:         "sqlite3",
			MaxConnections:     max,
			MinIdleConnections: minIdle,
			MonitorInterval:    interval,
			ReadOnly:           ro,
			QueriesFunc:        gallerydb.NewCustomQueries,
			ThumbsDBPath:       "thumbs.db",
			TestUseMemDB:       true,
		}
	}
	rw, err := dbconnpool.NewDbSQLConnPool(ctx, "", poolCfg(false))
	if err != nil {
		t.Fatalf("mem RW pool: %v", err)
	}
	ro, err := dbconnpool.NewDbSQLConnPool(ctx, "", poolCfg(true))
	if err != nil {
		_ = rw.Close()
		t.Fatalf("mem RO pool: %v", err)
	}

	dbPath := filepath.Join(rootDir, "DB", "sfpg.db")
	thumbsPath := filepath.Join(rootDir, "DB", "thumbs", "thumbs.db")
	infra.dbPaths = database.DatabasePaths{Main: dbPath, Thumbs: thumbsPath}
	infra.dbRwPool = rw
	infra.dbRoPool = ro
	infra.discoveryDQueDirPath = filepath.Join(filepath.Dir(dbPath), "discovery-dque")

	cpc, err := rw.Get()
	if err != nil {
		t.Fatalf("get RW for root folder: %v", err)
	}
	_, qerr := cpc.Queries.GetFolderIDByPath(ctx, "")
	if errors.Is(qerr, sql.ErrNoRows) {
		imp := &gallerylib.Importer{Q: cpc.Queries}
		if _, ierr := imp.CreateRootFolderEntry(ctx, time.Now().Unix()); ierr != nil {
			rw.Put(cpc)
			t.Fatalf("create root folder: %v", ierr)
		}
	} else if qerr != nil {
		rw.Put(cpc)
		t.Fatalf("root folder: %v", qerr)
	}
	rw.Put(cpc)

	infra.cacheStore = cachelite.NewSQLiteCacheStore(rw)
	infra.CalibrateCacheSizeNow(ctx)
	infra.StartWriteBatcher(ctx, true, config.DefaultDQueMaxDiskBytes)
	t.Cleanup(func() {
		infra.Shutdown()
	})
	return infra
}

// newIntegratedServiceOnDisk uses a real migrated database under a temp directory.
func newIntegratedServiceOnDisk(t *testing.T, ctx context.Context, cfg *config.Config) *InfrastructureService {
	t.Helper()
	rootDir := t.TempDir()
	infra := NewInfrastructureService()
	infra.rootDir = rootDir
	infra.SetupDB(ctx, cfg)
	infra.CalibrateCacheSizeNow(ctx)
	infra.StartWriteBatcher(ctx, true, config.DefaultDQueMaxDiskBytes)
	t.Cleanup(func() {
		infra.Shutdown()
	})
	return infra
}

func TestInfrastructureService_SetupDB_RealPools(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 2
	cfg.DBMinIdleConnections = 1

	infra := newIntegratedService(t, ctx, cfg)
	resetInfraMemDB(t, infra.dbRwPool)
	defer infra.Shutdown()

	if infra.dbRwPool == nil || infra.dbRoPool == nil {
		t.Fatal("pools not created")
	}
	if infra.writer == nil {
		t.Fatal("writer not created")
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

// TestInfrastructureService_ReconfigurePools_RealPools verifies that pools
// reconfigured before the writer starts are the ones the writer flushes through.
func TestInfrastructureService_ReconfigurePools_RealPools(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 2
	cfg.DBMinIdleConnections = 1

	infra := NewInfrastructureService()
	infra.rootDir = t.TempDir()
	infra.SetupDB(ctx, cfg)
	t.Cleanup(func() {
		infra.Shutdown()
	})

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

	infra.StartWriteBatcher(ctx, true, config.DefaultDQueMaxDiskBytes)

	// The writer must flush through the new pools.
	entry := &cachelite.HTTPCacheEntry{
		Key:           "reconfigured",
		Path:          "/gallery/1",
		Method:        "GET",
		Status:        200,
		ContentLength: sql.NullInt64{Int64: 10, Valid: true},
		Body:          []byte("0123456789"),
	}
	infra.writer.SubmitCacheEntry(entry)
	if err := infra.writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	got, err := infra.cacheStore.Get(ctx, "reconfigured")
	if err != nil {
		t.Fatalf("cache Get: %v", err)
	}
	if got == nil {
		t.Fatal("cache entry submitted after ReconfigurePools was not written")
	}
}

// TestInfrastructureService_CacheStored_CountsStoredBytes checks that the
// counters take the byte and entry counts reported by the writer.
func TestInfrastructureService_CacheStored_CountsStoredBytes(t *testing.T) {
	infra := NewInfrastructureService()

	infra.cacheStored(40, 1)

	if got := infra.cacheSizeBytes.Load(); got != 40 {
		t.Fatalf("cacheSizeBytes = %d, want 40", got)
	}
	if got := infra.cacheEntryCount.Load(); got != 1 {
		t.Fatalf("cacheEntryCount = %d, want 1", got)
	}
}

func TestInfrastructureService_CacheStored_EvictsOverLimit(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 2
	cfg.DBMinIdleConnections = 1

	infra := newIntegratedService(t, ctx, cfg)
	resetInfraMemDB(t, infra.dbRwPool)
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

	// Store the new entry, then report it the way the writer does.
	entry := &cachelite.HTTPCacheEntry{
		Key:           "new",
		Path:          "/gallery/1",
		Method:        "GET",
		Status:        200,
		ContentLength: sql.NullInt64{Int64: 50, Valid: true},
		Body:          bytes.Repeat([]byte("b"), 50),
	}
	if err := infra.cacheStore.Store(ctx, entry); err != nil {
		t.Fatalf("store cache: %v", err)
	}
	infra.cacheStored(int64(len(entry.Body)), 1)

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

func TestInfrastructureService_InvalidateHTTPCache_RealPool(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.EnableHTTPCache = true
	infra := newIntegratedService(t, ctx, cfg)
	resetInfraMemDB(t, infra.dbRwPool)
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
	resetInfraMemDB(t, infra.dbRwPool)
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

// cacheEntryOf returns a cache entry of n body bytes at path, from the pool.
func cacheEntryOf(path string, n int) *cachelite.HTTPCacheEntry {
	e := cachelite.GetHTTPCacheEntry()
	e.Key, e.Path, e.Method, e.Status, e.CreatedAt = path, path, "GET", 200, 1
	e.Body = append(e.Body, bytes.Repeat([]byte("a"), n)...)
	e.ContentLength = sql.NullInt64{Int64: int64(n), Valid: true}
	return e
}

// TestInfrastructureService_WriterEvictsCache checks the wiring from the
// Writer to the cache counters: entries written through the Writer are
// counted, and the cache is evicted below its size limit.
func TestInfrastructureService_WriterEvictsCache(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedServiceOnDisk(t, ctx, config.DefaultConfig())
	cfg := config.DefaultConfig()
	cfg.EnableHTTPCache = true
	cfg.CacheMaxSize = 250
	cfg.CacheMaxEntrySize = 200
	infra.InitializeHTTPCache(cfg)

	for _, path := range []string{"/gallery/1", "/gallery/2", "/gallery/3"} {
		infra.writer.SubmitCacheEntry(cacheEntryOf(path, 100))
	}
	if err := infra.writer.Close(); err != nil { // writes the batch
		t.Fatal(err)
	}

	size, err := infra.cacheStore.SizeBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if size > cfg.CacheMaxSize {
		t.Errorf("cache is %d bytes after the write, want at most %d", size, cfg.CacheMaxSize)
	}
	if got := infra.cacheSizeBytes.Load(); got != size {
		t.Errorf("cacheSizeBytes = %d, want %d, the stored size", got, size)
	}
}

// TestInfrastructureService_WriterResyncsCacheAfterFailure checks that a
// failed cache write makes the counters match the database again.
func TestInfrastructureService_WriterResyncsCacheAfterFailure(t *testing.T) {
	ctx := context.Background()
	infra := newIntegratedServiceOnDisk(t, ctx, config.DefaultConfig())
	cpc, err := infra.dbRwPool.Get()
	if err != nil {
		t.Fatal(err)
	}
	_, err = cpc.Conn.ExecContext(ctx, `CREATE TRIGGER fail_cache BEFORE INSERT ON http_cache
		BEGIN SELECT RAISE(ABORT, 'test abort'); END`)
	infra.dbRwPool.Put(cpc)
	if err != nil {
		t.Fatal(err)
	}
	infra.cacheSizeBytes.Store(12345)
	infra.cacheEntryCount.Store(7)

	infra.writer.SubmitCacheEntry(cacheEntryOf("/gallery/1", 10))
	if err := infra.writer.Close(); err != nil { // the write fails
		t.Fatal(err)
	}
	if got := infra.cacheSizeBytes.Load(); got != 0 {
		t.Errorf("cacheSizeBytes = %d after the failed write, want 0 from the database", got)
	}
	if got := infra.cacheEntryCount.Load(); got != 0 {
		t.Errorf("cacheEntryCount = %d after the failed write, want 0 from the database", got)
	}
}
