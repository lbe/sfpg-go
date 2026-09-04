package cachelite_test

import (
	"context"
	"sync/atomic"
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
// Getters read from atomics, so we seed counter values directly.
func TestHTTPCacheMiddleware_UpdatePool(t *testing.T) {
	db1 := createTestDBPool(t)
	defer db1.Close()
	db2 := createTestDBPool(t)
	defer db2.Close()

	var entryCount, sizeBytes atomic.Int64
	var br atomic.Int32
	entryCount.Store(0)
	sizeBytes.Store(0)
	counter := &cachelite.HTTPCacheCounterState{
		SizeBytes:       &sizeBytes,
		EntryCount:      &entryCount,
		BaselineRunning: &br,
	}

	hcm := cachelite.NewHTTPCacheMiddlewareForTest(db1, defaultUnitConfig(), counter, nil)

	// Initial pool (db1) — counters are 0.
	if count := hcm.GetEntryCount(); count != 0 {
		t.Errorf("initial entry count = %d, want 0", count)
	}

	// nil update must be a no-op on counters.
	hcm.UpdatePool(nil)
	if count := hcm.GetEntryCount(); count != 0 {
		t.Errorf("entry count after nil UpdatePool = %d, want 0", count)
	}

	// Non-nil update: counters are independent of pool, so values persist.
	entryCount.Store(1)
	sizeBytes.Store(9)
	hcm.UpdatePool(db2)
	if count := hcm.GetEntryCount(); count != 1 {
		t.Errorf("entry count after UpdatePool(db2) = %d, want 1", count)
	}
	if size := hcm.GetSizeBytes(); size != 9 {
		t.Errorf("size bytes after UpdatePool(db2) = %d, want 9", size)
	}
}

// TestHTTPCacheMiddleware_GetSizeBytesAndEntryCount verifies metrics helpers
// return -1 when uncalibrated, seeded atomics without DB rows, closed pool
// still returns the value, and nil counters return -1.
func TestHTTPCacheMiddleware_GetSizeBytesAndEntryCount(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	t.Run("uncalibrated returns -1", func(t *testing.T) {
		var entryCount atomic.Int64
		counter := &cachelite.HTTPCacheCounterState{
			SizeBytes:  &atomic.Int64{},
			EntryCount: &entryCount,
			// BaselineRunning left nil
		}
		hcm := cachelite.NewHTTPCacheMiddlewareForTest(db, defaultUnitConfig(), counter, nil)
		if got := hcm.GetEntryCount(); got != -1 {
			t.Errorf("uncalibrated GetEntryCount = %d, want -1", got)
		}
		if got := hcm.GetSizeBytes(); got != -1 {
			t.Errorf("uncalibrated GetSizeBytes = %d, want -1", got)
		}
	})

	t.Run("calibrated seeded value without rows", func(t *testing.T) {
		var entryCount, sizeBytes atomic.Int64
		var br atomic.Int32
		entryCount.Store(5)
		sizeBytes.Store(100)
		counter := &cachelite.HTTPCacheCounterState{
			SizeBytes:       &sizeBytes,
			EntryCount:      &entryCount,
			BaselineRunning: &br,
		}
		hcm := cachelite.NewHTTPCacheMiddlewareForTest(db, defaultUnitConfig(), counter, nil)
		// No rows inserted — values come from atomics only.
		if got := hcm.GetEntryCount(); got != 5 {
			t.Errorf("calibrated GetEntryCount = %d, want 5", got)
		}
		if got := hcm.GetSizeBytes(); got != 100 {
			t.Errorf("calibrated GetSizeBytes = %d, want 100", got)
		}
	})

	t.Run("closed pool still returns seeded value", func(t *testing.T) {
		var entryCount, sizeBytes atomic.Int64
		var br atomic.Int32
		entryCount.Store(3)
		sizeBytes.Store(77)
		counter := &cachelite.HTTPCacheCounterState{
			SizeBytes:       &sizeBytes,
			EntryCount:      &entryCount,
			BaselineRunning: &br,
		}
		hcm := cachelite.NewHTTPCacheMiddlewareForTest(db, defaultUnitConfig(), counter, nil)
		db.Close()
		if got := hcm.GetEntryCount(); got != 3 {
			t.Errorf("closed pool GetEntryCount = %d, want 3 (getters must not touch DB)", got)
		}
		if got := hcm.GetSizeBytes(); got != 77 {
			t.Errorf("closed pool GetSizeBytes = %d, want 77 (getters must not touch DB)", got)
		}
	})

	t.Run("running baseline returns -1 when counters are 0", func(t *testing.T) {
		var entryCount, sizeBytes atomic.Int64
		var br atomic.Int32
		br.Store(1) // baseline running, both counters are 0
		counter := &cachelite.HTTPCacheCounterState{
			SizeBytes:       &sizeBytes,
			EntryCount:      &entryCount,
			BaselineRunning: &br,
		}
		hcm := cachelite.NewHTTPCacheMiddlewareForTest(db, defaultUnitConfig(), counter, nil)
		if got := hcm.GetEntryCount(); got != -1 {
			t.Errorf("running baseline GetEntryCount = %d, want -1 (N/A)", got)
		}
		if got := hcm.GetSizeBytes(); got != -1 {
			t.Errorf("running baseline GetSizeBytes = %d, want -1 (N/A)", got)
		}
	})

	t.Run("running baseline returns value when counters > 0", func(t *testing.T) {
		var entryCount, sizeBytes atomic.Int64
		var br atomic.Int32
		entryCount.Store(10)
		sizeBytes.Store(2000)
		br.Store(1) // baseline running, but counters > 0
		counter := &cachelite.HTTPCacheCounterState{
			SizeBytes:       &sizeBytes,
			EntryCount:      &entryCount,
			BaselineRunning: &br,
		}
		hcm := cachelite.NewHTTPCacheMiddlewareForTest(db, defaultUnitConfig(), counter, nil)
		if got := hcm.GetEntryCount(); got != 10 {
			t.Errorf("running baseline GetEntryCount = %d, want 10", got)
		}
		if got := hcm.GetSizeBytes(); got != 2000 {
			t.Errorf("running baseline GetSizeBytes = %d, want 2000", got)
		}
	})

	t.Run("not running returns 0 when counters are 0", func(t *testing.T) {
		var entryCount, sizeBytes atomic.Int64
		var br atomic.Int32
		// br is 0 (not running), both counters are 0
		counter := &cachelite.HTTPCacheCounterState{
			SizeBytes:       &sizeBytes,
			EntryCount:      &entryCount,
			BaselineRunning: &br,
		}
		hcm := cachelite.NewHTTPCacheMiddlewareForTest(db, defaultUnitConfig(), counter, nil)
		if got := hcm.GetEntryCount(); got != 0 {
			t.Errorf("not running GetEntryCount = %d, want 0", got)
		}
		if got := hcm.GetSizeBytes(); got != 0 {
			t.Errorf("not running GetSizeBytes = %d, want 0", got)
		}
	})

	t.Run("nil counters returns -1", func(t *testing.T) {
		hcm := cachelite.NewHTTPCacheMiddlewareForTest(db, defaultUnitConfig(), nil, nil)
		if got := hcm.GetEntryCount(); got != -1 {
			t.Errorf("nil counters GetEntryCount = %d, want -1", got)
		}
		if got := hcm.GetSizeBytes(); got != -1 {
			t.Errorf("nil counters GetSizeBytes = %d, want -1", got)
		}
	})
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
