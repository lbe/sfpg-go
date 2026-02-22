package cachelite

import (
	"database/sql"

	"github.com/lbe/sfpg-go/internal/gensyncpool"
)

const (
	// defaultBodyCapacity is the pre-allocated capacity for Body slices.
	// Based on production data: 83% of entries are ≤6KB, so 8KB covers ~90%+ of cases.
	defaultBodyCapacity = 8 * 1024 // 8KB

	// maxRetainedCapacity prevents memory bloat from occasional large responses.
	// Bodies exceeding this capacity are reallocated to defaultBodyCapacity on Put.
	maxRetainedCapacity = 16 * 1024 // 16KB
)

// httpCacheEntryPool reuses HTTPCacheEntry instances on the cache write path to reduce allocations.
var httpCacheEntryPool = gensyncpool.New(
	func() *HTTPCacheEntry {
		return &HTTPCacheEntry{
			Body: make([]byte, 0, defaultBodyCapacity),
		}
	},
	func(e *HTTPCacheEntry) {
		// Reset all fields to zero values
		e.ID = 0
		e.Key = ""
		e.Method = ""
		e.Path = ""
		e.QueryString = sql.NullString{}
		e.Encoding = ""
		e.Status = 0
		e.ContentType = sql.NullString{}
		e.ContentEncoding = sql.NullString{}
		e.CacheControl = sql.NullString{}
		e.ETag = sql.NullString{}
		e.LastModified = sql.NullString{}
		e.Vary = sql.NullString{}
		e.ContentLength = sql.NullInt64{}
		e.CreatedAt = 0
		e.ExpiresAt = sql.NullInt64{}

		// Body: preserve capacity if reasonable, else shrink to standard size
		if cap(e.Body) <= maxRetainedCapacity {
			e.Body = e.Body[:0] // Reuse backing array
		} else {
			e.Body = make([]byte, 0, defaultBodyCapacity) // Shrink oversized
		}
	},
)

// GetHTTPCacheEntry retrieves an HTTPCacheEntry from the pool.
func GetHTTPCacheEntry() *HTTPCacheEntry {
	return httpCacheEntryPool.Get()
}

// PutHTTPCacheEntry returns an HTTPCacheEntry to the pool.
func PutHTTPCacheEntry(entry *HTTPCacheEntry) {
	if entry != nil {
		httpCacheEntryPool.Put(entry)
	}
}
