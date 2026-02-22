package cachelite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// TestEvictLRU_BytesBased verifies that EvictLRU frees the correct amount
// of bytes (not just entry count). This test catches the bug where
// freedBytes was incremented by 1 instead of entry.ContentLength.
func TestEvictLRU_BytesBased(t *testing.T) {
	pool := createTestDBPoolInternal(t)
	ctx := context.Background()
	queries := gallerydb.New(pool.DB())

	// Insert test entries with known sizes
	// Entry 1: 100 bytes, oldest (created 3 hours ago)
	// Entry 2: 200 bytes, middle (created 2 hours ago)
	// Entry 3: 400 bytes, newest (created 1 hour ago)
	entries := []struct {
		key     string
		size    int64
		created time.Time
	}{
		{"key1", 100, time.Now().Add(-3 * time.Hour)},
		{"key2", 200, time.Now().Add(-2 * time.Hour)},
		{"key3", 400, time.Now().Add(-1 * time.Hour)},
	}

	for _, e := range entries {
		body := make([]byte, e.size)
		err := queries.UpsertHttpCache(ctx, gallerydb.UpsertHttpCacheParams{
			Key:           e.key,
			Method:        "GET",
			Path:          "/test/" + e.key,
			ContentLength: sql.NullInt64{Int64: e.size, Valid: true},
			Body:          body,
			CreatedAt:     e.created.Unix(),
		})
		if err != nil {
			t.Fatalf("failed to insert entry %s: %v", e.key, err)
		}
	}

	// Verify total size
	totalSize, err := GetCacheSizeBytes(ctx, pool)
	if err != nil {
		t.Fatalf("failed to get cache size: %v", err)
	}
	if totalSize != 700 {
		t.Errorf("expected total size 700, got %d", totalSize)
	}

	// Evict 250 bytes - should remove key1 (100 bytes) and key2 (200 bytes)
	// because we need 250 bytes and key1 alone is not enough
	targetFreeBytes := int64(250)
	_, err = EvictLRU(ctx, pool, targetFreeBytes)
	if err != nil {
		t.Fatalf("EvictLRU failed: %v", err)
	}

	// Verify freed amount
	newSize, err := GetCacheSizeBytes(ctx, pool)
	if err != nil {
		t.Fatalf("failed to get new cache size: %v", err)
	}

	freedBytes := totalSize - newSize
	if freedBytes < targetFreeBytes {
		t.Errorf("EvictLRU freed %d bytes, expected at least %d bytes",
			freedBytes, targetFreeBytes)
	}

	// Verify correct entries were removed (key1 and key2 should be gone, key3 remains)
	_, err = queries.GetHttpCacheByKey(ctx, "key1")
	if err == nil {
		t.Error("key1 should have been evicted")
	}
	_, err = queries.GetHttpCacheByKey(ctx, "key2")
	if err == nil {
		t.Error("key2 should have been evicted")
	}
	_, err = queries.GetHttpCacheByKey(ctx, "key3")
	if err != nil {
		t.Error("key3 should still exist")
	}
}
