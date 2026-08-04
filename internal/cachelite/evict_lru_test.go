package cachelite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// TestEvictLRU_StoredBytes verifies that eviction uses stored bytes (LENGTH(body))
// rather than content_length when they differ (e.g., compressed body).
func TestEvictLRU_StoredBytes(t *testing.T) {
	pool := createTestDBPoolInternal(t)
	ctx := context.Background()
	queries := gallerydb.New(pool.DB())

	// Insert entries where stored body length < content_length (simulating compression).
	// Entry 1: body 7 bytes, content_length=100, oldest
	// Entry 2: body 7 bytes, content_length=200
	// Entry 3: body 14 bytes, content_length=50, newest
	entries := []struct {
		key           string
		bodyLen       int64
		contentLength int64
		created       time.Time
	}{
		{"stored-key1", 7, 100, time.Now().Add(-3 * time.Hour)},
		{"stored-key2", 7, 200, time.Now().Add(-2 * time.Hour)},
		{"stored-key3", 14, 50, time.Now().Add(-1 * time.Hour)},
	}

	for _, e := range entries {
		body := make([]byte, e.bodyLen)
		err := queries.UpsertHttpCache(ctx, gallerydb.UpsertHttpCacheParams{
			Key:           e.key,
			Method:        "GET",
			Path:          "/test/" + e.key,
			ContentLength: sql.NullInt64{Int64: e.contentLength, Valid: true},
			Body:          body,
			CreatedAt:     e.created.Unix(),
		})
		if err != nil {
			t.Fatalf("failed to insert entry %s: %v", e.key, err)
		}
	}

	// Verify total size uses stored bytes (LENGTH(body)), not content_length
	totalSize, err := GetCacheSizeBytes(ctx, pool)
	if err != nil {
		t.Fatalf("failed to get cache size: %v", err)
	}
	// 7 + 7 + 14 = 28 stored bytes
	if totalSize != 28 {
		t.Fatalf("expected total size 28 (stored bytes), got %d; "+
			"sum of content_length would be 350", totalSize)
	}

	// Evict 12 bytes — should remove key1 (7 bytes) and key2 (7 bytes) = 14 freed
	// key1 alone (7) is not enough, so both oldest entries go.
	targetFreeBytes := int64(12)
	_, _, err = EvictLRU(ctx, pool, targetFreeBytes)
	if err != nil {
		t.Fatalf("EvictLRU failed: %v", err)
	}

	newSize, err := GetCacheSizeBytes(ctx, pool)
	if err != nil {
		t.Fatalf("failed to get new cache size: %v", err)
	}

	freedBytes := totalSize - newSize // should be 14
	if freedBytes < targetFreeBytes {
		t.Fatalf("EvictLRU freed %d bytes, want at least %d", freedBytes, targetFreeBytes)
	}
	// Verify freed is 14 (stored bytes of key1 + key2), not 300 (content_length sum)
	if freedBytes > 20 {
		t.Fatalf("EvictLRU freed %d bytes, expected ~14 (stored bytes); "+
			"if content_length was used it would free 300", freedBytes)
	}

	// Verify correct entries removed
	_, err = queries.GetHttpCacheByKey(ctx, "stored-key1")
	if err == nil {
		t.Error("stored-key1 should have been evicted")
	}
	_, err = queries.GetHttpCacheByKey(ctx, "stored-key2")
	if err == nil {
		t.Error("stored-key2 should have been evicted")
	}
	_, err = queries.GetHttpCacheByKey(ctx, "stored-key3")
	if err != nil {
		t.Error("stored-key3 should still exist")
	}
}

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
	_, _, err = EvictLRU(ctx, pool, targetFreeBytes)
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
