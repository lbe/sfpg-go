package cachelite

import (
	"database/sql"

	"github.com/lbe/sfpg-go/internal/gensyncpool"
)

const (
	// defaultBodyCapacity is the pre-allocated capacity for Body slices.
	// Based on production data: 83% of entries are ≤6KB, so 8KB covers ~90%+ of cases.
	defaultBodyCapacity = 8 * 1024 // 8KB
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
		e.Status = 0
		e.ContentType = sql.NullString{}
		e.CacheControl = sql.NullString{}
		e.ETag = sql.NullString{}
		e.LastModified = sql.NullString{}
		e.Vary = sql.NullString{}
		e.ContentLength = sql.NullInt64{}
		e.CreatedAt = 0
		e.ExpiresAt = sql.NullInt64{}

		// Body: reuse the backing array only if cap is exactly defaultBodyCapacity;
		// otherwise allocate a fresh buffer at the standard size. This handles
		// undersized (e.g. post-compression) and oversized bodies uniformly.
		c := cap(e.Body)
		if c == defaultBodyCapacity {
			e.Body = e.Body[:0] // Reuse backing array at standard size
		} else {
			e.Body = make([]byte, 0, defaultBodyCapacity) // Grow undersized or reset non-standard cap
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
