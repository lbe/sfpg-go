package cachelite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
)

func defaultUnitConfig() cachelite.CacheConfig {
	return cachelite.CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/unit"},
	}
}

// TestHTTPCacheMiddleware_SetOnGalleryCacheHit verifies the callback is stored
// and invoked through the exported Config.
func TestHTTPCacheMiddleware_SetOnGalleryCacheHit(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	hcm := cachelite.NewHTTPCacheMiddlewareForTest(db, defaultUnitConfig(), nil, nil)

	var called bool
	var gotFolderID int64
	hcm.SetOnGalleryCacheHit(func(ctx context.Context, folderID int64, sessionID string) {
		called = true
		gotFolderID = folderID
	})

	cfg := hcm.Config()
	if cfg.OnGalleryCacheHit == nil {
		t.Fatal("OnGalleryCacheHit callback was not stored")
	}

	cfg.OnGalleryCacheHit(context.Background(), 42, "session-id")

	if !called {
		t.Error("OnGalleryCacheHit callback was not called")
	}
	if gotFolderID != 42 {
		t.Errorf("callback folderID = %d, want %d", gotFolderID, 42)
	}
}

// TestHTTPCacheMiddleware_UpdatePool verifies that UpdatePool replaces the
// internal pool when given a non-nil pool and is a no-op for nil.
func TestHTTPCacheMiddleware_UpdatePool(t *testing.T) {
	db1 := createTestDBPool(t)
	defer db1.Close()
	db2 := createTestDBPool(t)
	defer db2.Close()

	hcm := cachelite.NewHTTPCacheMiddlewareForTest(db1, defaultUnitConfig(), nil, nil)

	// Seed db2 with a single entry so we can distinguish the pools.
	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key: cachelite.NewCacheKey(cachelite.CacheKeyParams{
			Method: "GET",
			Path:   "/unit",
		}),
		Method:        "GET",
		Path:          "/unit",
		Status:        200,
		Body:          []byte("pool-two"),
		ContentLength: sql.NullInt64{Int64: 8, Valid: true},
		CreatedAt:     now,
	}
	if err := cachelite.StoreCacheEntry(context.Background(), db2, entry); err != nil {
		t.Fatalf("failed to seed db2: %v", err)
	}

	// Initial pool (db1) should have no entries.
	if count := hcm.GetEntryCount(); count != 0 {
		t.Errorf("initial entry count = %d, want 0", count)
	}

	// nil update must be a no-op.
	hcm.UpdatePool(nil)
	if count := hcm.GetEntryCount(); count != 0 {
		t.Errorf("entry count after nil UpdatePool = %d, want 0", count)
	}

	// Non-nil update should switch to db2.
	hcm.UpdatePool(db2)
	if count := hcm.GetEntryCount(); count != 1 {
		t.Errorf("entry count after UpdatePool(db2) = %d, want 1", count)
	}
	if size := hcm.GetSizeBytes(); size <= 0 {
		t.Errorf("size bytes after UpdatePool(db2) = %d, want > 0", size)
	}
}

// TestHTTPCacheMiddleware_GetSizeBytesAndEntryCount verifies metrics helpers
// return zero for an empty cache and positive values after storing an entry.
func TestHTTPCacheMiddleware_GetSizeBytesAndEntryCount(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	hcm := cachelite.NewHTTPCacheMiddlewareForTest(db, defaultUnitConfig(), nil, nil)

	if count := hcm.GetEntryCount(); count != 0 {
		t.Errorf("empty entry count = %d, want 0", count)
	}
	if size := hcm.GetSizeBytes(); size != 0 {
		t.Errorf("empty size bytes = %d, want 0", size)
	}

	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key: cachelite.NewCacheKey(cachelite.CacheKeyParams{
			Method: "GET",
			Path:   "/unit",
		}),
		Method:        "GET",
		Path:          "/unit",
		Status:        200,
		Body:          []byte("hello cache"),
		ContentLength: sql.NullInt64{Int64: 11, Valid: true},
		CreatedAt:     now,
	}
	if err := cachelite.StoreCacheEntry(context.Background(), db, entry); err != nil {
		t.Fatalf("failed to store entry: %v", err)
	}

	if count := hcm.GetEntryCount(); count != 1 {
		t.Errorf("entry count after store = %d, want 1", count)
	}
	if size := hcm.GetSizeBytes(); size <= 0 {
		t.Errorf("size bytes after store = %d, want > 0", size)
	}
}

// TestCountCacheEntries verifies the package-level entry counter.
func TestCountCacheEntries(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	ctx := context.Background()
	count, err := cachelite.CountCacheEntries(ctx, db)
	if err != nil {
		t.Fatalf("CountCacheEntries failed: %v", err)
	}
	if count != 0 {
		t.Errorf("empty count = %d, want 0", count)
	}

	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key: cachelite.NewCacheKey(cachelite.CacheKeyParams{
			Method: "GET",
			Path:   "/unit",
		}),
		Method:    "GET",
		Path:      "/unit",
		Status:    200,
		Body:      []byte("x"),
		CreatedAt: now,
	}
	if storeErr := cachelite.StoreCacheEntry(ctx, db, entry); storeErr != nil {
		t.Fatalf("failed to store entry: %v", storeErr)
	}

	count, err = cachelite.CountCacheEntries(ctx, db)
	if err != nil {
		t.Fatalf("CountCacheEntries failed after store: %v", err)
	}
	if count != 1 {
		t.Errorf("count after store = %d, want 1", count)
	}
}

// TestDropStaleCacheTableIfExists verifies the helper reports false when there
// is no stale table and true after creating one.
func TestDropStaleCacheTableIfExists(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	ctx := context.Background()

	dropped, err := cachelite.DropStaleCacheTableIfExists(ctx, db)
	if err != nil {
		t.Fatalf("DropStaleCacheTableIfExists failed: %v", err)
	}
	if dropped {
		t.Error("expected no stale table to drop initially")
	}

	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("failed to get connection: %v", err)
	}
	defer db.Put(cpc)

	// Create the stale table directly rather than renaming the live http_cache
	// table. Renaming would remove http_cache, which breaks lazy preparation of
	// the custom queries (e.g. ClearHttpCache) that reference it. Using CREATE
	// TABLE keeps the live cache table intact while still exercising the
	// existence check and DROP path in DropStaleCacheTableIfExists.
	createStale := `CREATE TABLE http_cache_to_be_dropped (id INTEGER PRIMARY KEY)`
	if _, execErr := cpc.Conn.ExecContext(ctx, createStale); execErr != nil {
		t.Fatalf("failed to create stale table: %v", execErr)
	}

	dropped, err = cachelite.DropStaleCacheTableIfExists(ctx, db)
	if err != nil {
		t.Fatalf("DropStaleCacheTableIfExists failed after creating stale table: %v", err)
	}
	if !dropped {
		t.Error("expected stale table to be dropped")
	}

	// Second call should find nothing.
	dropped, err = cachelite.DropStaleCacheTableIfExists(ctx, db)
	if err != nil {
		t.Fatalf("DropStaleCacheTableIfExists failed second call: %v", err)
	}
	if dropped {
		t.Error("expected no stale table after drop")
	}
}
