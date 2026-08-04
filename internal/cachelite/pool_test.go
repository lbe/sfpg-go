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
	// Record the starting capacity (varies based on prior pool reuse)
	originalCap := cap(entry.Body)
	if originalCap < defaultBodyCapacity {
		t.Fatalf("Body cap = %d, want >= %d", originalCap, defaultBodyCapacity)
	}

	// Append ~5 KiB — this should not grow the capacity if there's room
	entry.Body = append(entry.Body[:0], make([]byte, 5000)...)

	PutHTTPCacheEntry(entry)
	entry2 := GetHTTPCacheEntry()

	// Capacity should be at least the original (reuse, not shrink or reset)
	if cap(entry2.Body) < originalCap {
		t.Errorf("Body capacity after Put/Get = %d, want >= %d", cap(entry2.Body), originalCap)
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

func TestHTTPCacheEntryPool_NonDefaultCapacityRetained(t *testing.T) {
	entry := GetHTTPCacheEntry()

	// Simulate a body with non-standard capacity (12 KiB, which falls between
	// defaultBodyCapacity and maxRetainedCaptureCap — should be retained).
	entry.Body = make([]byte, 0, 12*1024)
	entry.Body = append(entry.Body, make([]byte, 10000)...)

	// sync.Pool is nondeterministic: under -race, Put randomly drops ~25% of
	// items (see sync/pool.go), and Get may return an unrelated pooled entry.
	// Re-Put the 12 KiB entry each iteration until Get hands it back, holding
	// non-matching entries aside so a recycled entry cannot loop forever.
	var matched *HTTPCacheEntry
	var leftovers []*HTTPCacheEntry
	lastCaps := make([]int, 0, 8)
	for range 64 {
		PutHTTPCacheEntry(entry)
		got := GetHTTPCacheEntry()
		if cap(got.Body) == 12*1024 {
			matched = got
			break
		}
		lastCaps = append(lastCaps, cap(got.Body))
		leftovers = append(leftovers, got)
	}
	// Return the matched 12 KiB entry first so it lands in the private slot
	// (consumed by the next test's first Get) rather than leaking a
	// non-default body into the shared lists where other pool tests could
	// observe it after a race-dropped Put.
	if matched != nil {
		PutHTTPCacheEntry(matched)
	}
	for _, e := range leftovers {
		PutHTTPCacheEntry(e)
	}
	if matched == nil {
		t.Fatalf("Non-default capacity not retained: observed caps %v, want %d", lastCaps, 12*1024)
	}
}

func TestHTTPCacheEntryPool_OversizedBodyReset(t *testing.T) {
	entry := GetHTTPCacheEntry()

	// Simulate a body with capacity > maxRetainedCaptureCap — should be reset.
	entry.Body = make([]byte, 0, 300*1024) // 300 KiB, > 256 KiB max
	entry.Body = append(entry.Body, make([]byte, 250000)...)

	PutHTTPCacheEntry(entry)
	entry2 := GetHTTPCacheEntry()

	if cap(entry2.Body) != defaultBodyCapacity {
		t.Errorf("Oversized body not reset: cap = %d, want %d", cap(entry2.Body), defaultBodyCapacity)
	}

	PutHTTPCacheEntry(entry2)
}

func TestCacheCapturerPool_GetPut(t *testing.T) {
	ccw := getCacheCapturingWriter(nil)
	if ccw == nil {
		t.Fatal("getCacheCapturingWriter returned nil")
	}
	if cap(ccw.body) == 0 {
		t.Error("capturer body has zero capacity")
	}

	// Write some data
	ccw.body = append(ccw.body, []byte("test data")...)
	if len(ccw.body) == 0 {
		t.Error("capturer body should have data after write")
	}

	putCacheCapturingWriter(ccw)

	// Get again — should be recycled (clean body, nil ResponseWriter)
	ccw2 := getCacheCapturingWriter(nil)
	if len(ccw2.body) != 0 {
		t.Error("recycled capturer body should be empty")
	}
	if ccw2.ResponseWriter != nil {
		t.Error("recycled capturer ResponseWriter should be nil")
	}
	if ccw2.wroteHeader {
		t.Error("recycled capturer wroteHeader should be false")
	}
	if ccw2.statusCode != 0 {
		t.Error("recycled capturer statusCode should be 0")
	}

	putCacheCapturingWriter(ccw2)
}

func TestCacheCapturerPool_OversizeReset(t *testing.T) {
	ccw := getCacheCapturingWriter(nil)

	// Simulate growing past maxRetainedCaptureCap
	ccw.body = make([]byte, 0, 300*1024) // 300 KiB, > 256 KiB max
	ccw.body = append(ccw.body, make([]byte, 250000)...)

	putCacheCapturingWriter(ccw)

	// Get again — body should be reset to clean 64 KiB
	ccw2 := getCacheCapturingWriter(nil)
	if len(ccw2.body) != 0 {
		t.Error("oversized recycled capturer body should be empty")
	}
	if cap(ccw2.body) < 64*1024 {
		t.Errorf("oversized recycled capturer cap = %d, want >= %d", cap(ccw2.body), 64*1024)
	}

	putCacheCapturingWriter(ccw2)
}

func TestCacheCapturerPool_PutNilSafe(t *testing.T) {
	// Should not panic
	putCacheCapturingWriter(nil)
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
