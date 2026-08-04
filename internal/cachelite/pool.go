package cachelite

import (
	"database/sql"
	"net/http"

	"github.com/lbe/sfpg-go/internal/gensyncpool"
)

const (
	// defaultBodyCapacity is the pre-allocated capacity for Body slices.
	// Based on production data: 83% of entries are ≤6KB, so 8KB covers ~90%+ of cases.
	defaultBodyCapacity = 8 * 1024 // 8KB

	// maxRetainedCaptureCap is the maximum body capacity retained when returning
	// a cacheCapturingWriter to its pool. Buffers above this threshold are
	// replaced with fresh 64KiB slices to avoid retaining large allocations
	// from single oversized responses.
	maxRetainedCaptureCap = 256 << 10 // 256KB

	// defaultCapturerBodyCapacity is the initial body capacity for pooled
	// cacheCapturingWriter instances. Larger than the old 4KiB to reduce
	// reallocation during typical gallery page responses.
	defaultCapturerBodyCapacity = 64 << 10 // 64KB
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

		// Body: retain backing array if cap is between defaultBodyCapacity and
		// maxRetainedCaptureCap (covering most gallery/bodies that grew during
		// capture); otherwise allocate a fresh buffer at the standard size.
		c := cap(e.Body)
		if c >= defaultBodyCapacity && c <= maxRetainedCaptureCap {
			e.Body = e.Body[:0] // Reuse backing array
		} else {
			e.Body = make([]byte, 0, defaultBodyCapacity) // Reset to standard size
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

// cacheCapturerPool reuses cacheCapturingWriter instances to reduce allocation
// on the cache MISS path. Each instance starts with a 64 KiB body buffer.
var cacheCapturerPool = gensyncpool.New(
	func() *cacheCapturingWriter {
		return &cacheCapturingWriter{
			body: make([]byte, 0, defaultCapturerBodyCapacity),
		}
	},
	func(ccw *cacheCapturingWriter) {
		// Reset body: retain if cap <= maxRetainedCaptureCap, otherwise
		// allocate a fresh 64 KiB buffer to avoid pinning large allocations.
		c := cap(ccw.body)
		if c <= maxRetainedCaptureCap {
			ccw.body = ccw.body[:0]
		} else {
			ccw.body = make([]byte, 0, defaultCapturerBodyCapacity)
		}
		ccw.statusCode = 0
		ccw.wroteHeader = false
		ccw.ResponseWriter = nil
	},
)

// getCacheCapturingWriter retrieves a cacheCapturingWriter from the pool
// and wires it to the given http.ResponseWriter.
func getCacheCapturingWriter(w http.ResponseWriter) *cacheCapturingWriter {
	ccw := cacheCapturerPool.Get()
	ccw.ResponseWriter = w
	return ccw
}

// putCacheCapturingWriter returns a cacheCapturingWriter to the pool.
func putCacheCapturingWriter(ccw *cacheCapturingWriter) {
	if ccw != nil {
		cacheCapturerPool.Put(ccw)
	}
}
