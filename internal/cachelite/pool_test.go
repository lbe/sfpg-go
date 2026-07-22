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
	entry.Status = 200
	entry.ContentType = sql.NullString{String: "text/html", Valid: true}
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

func TestHTTPCacheEntryPool_BodyCapacityReused(t *testing.T) {
	entry := GetHTTPCacheEntry()
	// entry.Body starts with cap == defaultBodyCapacity (8 KiB)
	// Append ~5 KiB without changing capacity
	entry.Body = append(entry.Body[:0], make([]byte, 5000)...)

	PutHTTPCacheEntry(entry)
	entry2 := GetHTTPCacheEntry()

	if cap(entry2.Body) != defaultBodyCapacity {
		t.Errorf("Body capacity after Put/Get = %d, want %d", cap(entry2.Body), defaultBodyCapacity)
	}

	PutHTTPCacheEntry(entry2)
}

func TestHTTPCacheEntryPool_UndersizedBodyGrown(t *testing.T) {
	entry := GetHTTPCacheEntry()

	// Simulate a post-compression body: small capacity + small payload
	entry.Body = make([]byte, 0, 512)
	entry.Body = append(entry.Body, []byte("small compressed payload")...)

	PutHTTPCacheEntry(entry)
	entry2 := GetHTTPCacheEntry()

	if cap(entry2.Body) != defaultBodyCapacity {
		t.Errorf("Undersized body not grown: cap = %d, want %d", cap(entry2.Body), defaultBodyCapacity)
	}

	PutHTTPCacheEntry(entry2)
}

func TestHTTPCacheEntryPool_NonDefaultCapacityReset(t *testing.T) {
	entry := GetHTTPCacheEntry()

	// Simulate a body with non-standard capacity (12 KiB, which is > defaultBodyCapacity,
	// covers both the old 8-16 KiB band and oversized cases)
	entry.Body = make([]byte, 0, 12*1024)
	entry.Body = append(entry.Body, make([]byte, 10000)...)

	PutHTTPCacheEntry(entry)
	entry2 := GetHTTPCacheEntry()

	if cap(entry2.Body) != defaultBodyCapacity {
		t.Errorf("Non-default capacity not reset: cap = %d, want %d", cap(entry2.Body), defaultBodyCapacity)
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
