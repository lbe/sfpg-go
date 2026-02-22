package cachelite

import (
	"database/sql"
	"sync"
	"testing"
)

func TestHTTPCacheEntryPool_GetReturnsEntry(t *testing.T) {
	entry := GetHTTPCacheEntry()
	if entry == nil {
		t.Fatal("GetHTTPCacheEntry returned nil")
	}
	if cap(entry.Body) < defaultBodyCapacity {
		t.Errorf("Body capacity = %d, want >= %d", cap(entry.Body), defaultBodyCapacity)
	}
	PutHTTPCacheEntry(entry)
}

func TestHTTPCacheEntryPool_ResetClearsAllFields(t *testing.T) {
	entry := GetHTTPCacheEntry()

	// Set all fields to non-zero values
	entry.ID = 123
	entry.Key = "test-key"
	entry.Method = "GET"
	entry.Path = "/test"
	entry.QueryString = sql.NullString{String: "q=1", Valid: true}
	entry.Encoding = "gzip"
	entry.Status = 200
	entry.ContentType = sql.NullString{String: "text/html", Valid: true}
	entry.ContentEncoding = sql.NullString{String: "gzip", Valid: true}
	entry.CacheControl = sql.NullString{String: "max-age=3600", Valid: true}
	entry.ETag = sql.NullString{String: "\"etag\"", Valid: true}
	entry.LastModified = sql.NullString{String: "Wed, 01 Jan 2020 00:00:00 GMT", Valid: true}
	entry.Vary = sql.NullString{String: "Accept-Encoding", Valid: true}
	entry.Body = append(entry.Body[:0], []byte("test body")...)
	entry.ContentLength = sql.NullInt64{Int64: 9, Valid: true}
	entry.CreatedAt = 1234567890
	entry.ExpiresAt = sql.NullInt64{Int64: 1234567890 + 3600, Valid: true}

	// Return to pool (triggers reset)
	PutHTTPCacheEntry(entry)

	// Get again (should be reset)
	entry2 := GetHTTPCacheEntry()

	if entry2.ID != 0 || entry2.Key != "" || entry2.Method != "" {
		t.Error("Fields not reset after Put")
	}
	if entry2.QueryString.Valid || entry2.ContentType.Valid {
		t.Error("NullString fields not reset")
	}
	if len(entry2.Body) != 0 {
		t.Errorf("Body length = %d, want 0", len(entry2.Body))
	}
	if entry2.Status != 0 {
		t.Errorf("Status = %d, want 0", entry2.Status)
	}
	if entry2.CreatedAt != 0 {
		t.Errorf("CreatedAt = %d, want 0", entry2.CreatedAt)
	}
	if entry2.ExpiresAt.Valid {
		t.Error("ExpiresAt not reset")
	}

	PutHTTPCacheEntry(entry2)
}

func TestHTTPCacheEntryPool_BodyCapacityPreserved(t *testing.T) {
	entry := GetHTTPCacheEntry()
	originalCap := cap(entry.Body)

	// Simulate typical body (< maxRetainedCapacity)
	entry.Body = append(entry.Body[:0], make([]byte, 5000)...)

	PutHTTPCacheEntry(entry)
	entry2 := GetHTTPCacheEntry()

	// Capacity should be preserved (not reallocated)
	if cap(entry2.Body) < originalCap {
		t.Errorf("Body capacity shrunk: was %d, now %d", originalCap, cap(entry2.Body))
	}

	PutHTTPCacheEntry(entry2)
}

func TestHTTPCacheEntryPool_OversizedBodyShrunk(t *testing.T) {
	entry := GetHTTPCacheEntry()

	// Simulate oversized body (> maxRetainedCapacity)
	entry.Body = make([]byte, 0, 32*1024) // 32KB capacity
	entry.Body = append(entry.Body, make([]byte, 20000)...)

	if cap(entry.Body) <= maxRetainedCapacity {
		t.Fatalf("Setup failed: capacity %d should exceed %d", cap(entry.Body), maxRetainedCapacity)
	}

	PutHTTPCacheEntry(entry)

	// After reset, capacity should be shrunk to default (not kept at oversized)
	entry2 := GetHTTPCacheEntry()
	if cap(entry2.Body) > maxRetainedCapacity {
		t.Errorf("Oversized body not shrunk: cap = %d, want <= %d", cap(entry2.Body), maxRetainedCapacity)
	}

	PutHTTPCacheEntry(entry2)
}

func TestHTTPCacheEntryPool_PutNilSafe(t *testing.T) {
	// Should not panic
	PutHTTPCacheEntry(nil)
}

func TestHTTPCacheEntryPool_ConcurrentAccess(t *testing.T) {
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			entry := GetHTTPCacheEntry()
			entry.Body = append(entry.Body[:0], []byte("concurrent test data")...)
			entry.Key = "test-key"
			entry.Path = "/test/path"
			PutHTTPCacheEntry(entry)
		})
	}
	wg.Wait()

	// Verify pool still works after concurrent access
	entry := GetHTTPCacheEntry()
	if cap(entry.Body) < defaultBodyCapacity {
		t.Errorf("Pool corrupted after concurrent access: got cap %d, want >= %d", cap(entry.Body), defaultBodyCapacity)
	}
	PutHTTPCacheEntry(entry)
}
