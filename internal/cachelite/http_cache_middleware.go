package cachelite

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

// HTTPCacheMiddleware is the SQLite-backed HTTP cache handler.
type HTTPCacheMiddleware struct {
	db          *dbconnpool.DbSQLConnPool
	config      CacheConfig
	sizeCounter *atomic.Int64         // Shared atomic counter for cache size
	submitFunc  func(*HTTPCacheEntry) // Optional custom submit function
	syncMode    bool                  // If true, submitFunc is called directly (for tests)
}

// cacheCapturingWriter buffers response data for caching.
type cacheCapturingWriter struct {
	http.ResponseWriter
	body        []byte
	statusCode  int
	wroteHeader bool
}

func (ccw *cacheCapturingWriter) WriteHeader(code int) {
	if !ccw.wroteHeader {
		ccw.statusCode = code
		ccw.wroteHeader = true
		ccw.ResponseWriter.WriteHeader(code)
	}
}

func (ccw *cacheCapturingWriter) Header() http.Header {
	return ccw.ResponseWriter.Header()
}

func (ccw *cacheCapturingWriter) Write(p []byte) (int, error) {
	if !ccw.wroteHeader {
		ccw.WriteHeader(http.StatusOK)
	}
	ccw.body = append(ccw.body, p...)
	return ccw.ResponseWriter.Write(p)
}

// NewHTTPCacheMiddleware constructs the cache middleware with a custom submit function instead of a queue.
func NewHTTPCacheMiddleware(db *dbconnpool.DbSQLConnPool, cfg CacheConfig, sizeCounter *atomic.Int64, submit func(*HTTPCacheEntry)) *HTTPCacheMiddleware {
	// In production, submitFunc is mandatory
	if submit == nil {
		panic("HTTPCacheMiddleware: submitFunc is required in production - use unified batcher")
	}
	return &HTTPCacheMiddleware{
		db:          db,
		config:      cfg,
		sizeCounter: sizeCounter,
		submitFunc:  submit,
	}
}

// NewHTTPCacheMiddlewareForTest creates middleware without production guard for testing.
// Tests can use this to bypass the panic on nil submitFunc.
func NewHTTPCacheMiddlewareForTest(
	db *dbconnpool.DbSQLConnPool,
	cfg CacheConfig,
	sizeCounter *atomic.Int64,
	submitFunc func(*HTTPCacheEntry), // test-only: accepts nil submitFunc
) *HTTPCacheMiddleware {
	return &HTTPCacheMiddleware{
		db:          db,
		config:      cfg,
		sizeCounter: sizeCounter,
		submitFunc:  submitFunc,
		syncMode:    true,
	}
}

// Config returns the cache configuration (e.g., CacheableRoutes).
func (hcm *HTTPCacheMiddleware) Config() CacheConfig {
	return hcm.config
}

// IsEnabled returns true if the cache is enabled.
func (hcm *HTTPCacheMiddleware) IsEnabled() bool {
	return hcm.config.Enabled
}

// UpdatePool updates the internal pool reference. Called when database pools are reconfigured.
func (hcm *HTTPCacheMiddleware) UpdatePool(newPool *dbconnpool.DbSQLConnPool) {
	if newPool != nil {
		hcm.db = newPool
	}
}

// GetSizeBytes returns the current cache size in bytes.
// Queries the database directly for accurate size (the atomic counter is only
// used for runtime eviction calculations, not for reporting metrics).
func (hcm *HTTPCacheMiddleware) GetSizeBytes() int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	size, err := GetCacheSizeBytes(ctx, hcm.db)
	if err != nil {
		return 0
	}
	return size
}

// GetEntryCount returns the number of entries in the cache.
func (hcm *HTTPCacheMiddleware) GetEntryCount() int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	count, err := CountCacheEntries(ctx, hcm.db)
	if err != nil {
		return 0
	}
	return count
}

// parseGalleryFolderID extracts folder ID from a gallery path like /gallery/{id}.
// Returns (folderID, true) if path matches /gallery/{id} and ID is valid, else (0, false).
func parseGalleryFolderID(path string) (int64, bool) {
	if !strings.HasPrefix(path, "/gallery/") {
		return 0, false
	}
	rest := strings.TrimPrefix(path, "/gallery/")
	// Handle trailing slash or extra path segments
	if idx := strings.Index(rest, "/"); idx >= 0 {
		rest = rest[:idx]
	}
	if rest == "" {
		return 0, false
	}
	folderID, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return 0, false
	}
	return folderID, true
}

// getSessionIDForPreload extracts sessionID from request cookie (if SessionCookieName
// is set) or falls back to RemoteAddr. Matches the logic in handlers/gallery.go.
func (hcm *HTTPCacheMiddleware) getSessionIDForPreload(r *http.Request) string {
	if hcm.config.SessionCookieName != "" {
		if c, err := r.Cookie(hcm.config.SessionCookieName); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return r.RemoteAddr
}

// maybeTriggerGalleryPreload checks if this is a gallery cache HIT and triggers
// OnGalleryCacheHit callback if configured. Called from cache HIT branches.
func (hcm *HTTPCacheMiddleware) maybeTriggerGalleryPreload(ctx context.Context, r *http.Request) {
	if hcm.config.OnGalleryCacheHit == nil {
		return
	}

	// Check skip condition
	if hcm.config.SkipPreloadWhenHeader != "" && hcm.config.SkipPreloadWhenValue != "" {
		if r.Header.Get(hcm.config.SkipPreloadWhenHeader) == hcm.config.SkipPreloadWhenValue {
			return
		}
	}

	// Parse folder ID from path
	folderID, ok := parseGalleryFolderID(r.URL.Path)
	if !ok {
		return
	}

	// Extract sessionID and acceptEncoding
	sessionID := hcm.getSessionIDForPreload(r)
	acceptEncoding := r.Header.Get("Accept-Encoding")

	// Call callback in goroutine (fire-and-forget, like handler does)
	go hcm.config.OnGalleryCacheHit(ctx, folderID, sessionID, acceptEncoding)
}

// Middleware returns the http.Handler wrapper with SQLite-backed cache lookup, storage, and eviction.
func (hcm *HTTPCacheMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only cache GET and HEAD requests
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}

		// Only cache if path is in CacheableRoutes
		if !hcm.config.IsCacheablePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Compute cache key (method:path?query|encoding)
		// Respect client's Cache-Control bypass directives (e.g., from hard refresh)
		if hasCacheBypassDirective(r.Header["Cache-Control"]) {
			w.Header().Set("X-Cache", "BYPASS")
			next.ServeHTTP(w, r)
			return
		}

		// Get theme from cookie for cache key - theme affects rendered page
		theme := "dark" // default
		if cookie, err := r.Cookie("theme"); err == nil && cookie.Value != "" {
			theme = cookie.Value
		}
		// HX-Target distinguishes folder tile (gallery-content) from boosted link (empty/body)
		// Theme is included so different themes are cached separately
		params := NewCacheKeyForRequest(r, theme)
		cacheKey := NewCacheKey(params)

		// Check cache for existing entry
		entry, err := hcm.checkCache(r.Context(), cacheKey)
		if err == nil && entry != nil {
			// Cache hit: check ETag for 304 Not Modified
			if entry.ETag.Valid {
				ifNoneMatch := r.Header.Get("If-None-Match")
				if ifNoneMatch == entry.ETag.String {
					// ETag matches: return 304 Not Modified
					if entry.CacheControl.Valid {
						w.Header().Set("Cache-Control", entry.CacheControl.String)
					}
					if entry.ETag.Valid {
						w.Header().Set("ETag", entry.ETag.String)
					}
					if entry.LastModified.Valid {
						w.Header().Set("Last-Modified", entry.LastModified.String)
					}
					if entry.Vary.Valid {
						w.Header().Set("Vary", entry.Vary.String)
					}
					w.Header().Set("X-Cache", "HIT")
					w.WriteHeader(http.StatusNotModified)
					hcm.maybeTriggerGalleryPreload(r.Context(), r)
					return
				}
			}

			// Cache hit: serve cached response with full body
			if entry.ContentType.Valid {
				w.Header().Set("Content-Type", entry.ContentType.String)
			}
			if entry.ContentEncoding.Valid {
				w.Header().Set("Content-Encoding", entry.ContentEncoding.String)
			}
			if entry.CacheControl.Valid {
				w.Header().Set("Cache-Control", entry.CacheControl.String)
			}
			if entry.ETag.Valid {
				w.Header().Set("ETag", entry.ETag.String)
			}
			if entry.LastModified.Valid {
				w.Header().Set("Last-Modified", entry.LastModified.String)
			}
			if entry.Vary.Valid {
				w.Header().Set("Vary", entry.Vary.String)
			}
			w.Header().Set("X-Cache", "HIT")
			w.WriteHeader(int(entry.Status))
			_, _ = w.Write(entry.Body)
			hcm.maybeTriggerGalleryPreload(r.Context(), r)
			return
		}

		// Cache miss: buffer response and store if eligible
		w.Header().Set("X-Cache", "MISS")
		buf := &cacheCapturingWriter{
			ResponseWriter: w,
			body:           make([]byte, 0, 4096),
		}

		next.ServeHTTP(buf, r)

		// Check if response is eligible for server cache. We store 2xx with cacheable
		// Cache-Control (e.g. max-age) or 2xx with no-store when the path is in
		// CacheableRoutes (e.g. gallery partials). no-store is replayed to the client
		// so the browser does not cache; the server cache is separate.
		cacheControl := buf.Header().Get("Cache-Control")
		storeInServerCache := CanCacheResponse(buf.statusCode, cacheControl) ||
			(buf.statusCode == 200 && strings.Contains(cacheControl, "no-store") && hcm.config.IsCacheablePath(r.URL.Path))
		if !storeInServerCache {
			return
		}

		// Check size limits
		bodySize := int64(len(buf.body))
		if bodySize > hcm.config.MaxEntrySize {
			return // Entry too large
		}

		// Note: Eviction is now handled asynchronously by the cache write worker
		// to avoid blocking the response. The worker checks cache size before
		// writing and evicts LRU entries if needed.

		// Build cache entry
		now := time.Now().Unix()
		var expiresAt sql.NullInt64
		if hcm.config.DefaultTTL > 0 {
			expiresAt = sql.NullInt64{Int64: now + int64(hcm.config.DefaultTTL.Seconds()), Valid: true}
		}

		// Use the Content-Encoding header set by compression middleware
		contentEncoding := buf.Header().Get("Content-Encoding")

		// Get entry from pool (Body already has 8KB capacity)
		newEntry := GetHTTPCacheEntry()
		newEntry.Key = cacheKey
		newEntry.Method = r.Method
		newEntry.Path = r.URL.Path
		newEntry.QueryString = sql.NullString{String: params.Query, Valid: params.Query != ""}
		newEntry.Encoding = params.Encoding
		newEntry.Status = int64(buf.statusCode)
		newEntry.ContentType = sql.NullString{String: buf.Header().Get("Content-Type"), Valid: buf.Header().Get("Content-Type") != ""}
		newEntry.ContentEncoding = sql.NullString{String: contentEncoding, Valid: contentEncoding != ""}
		newEntry.CacheControl = sql.NullString{String: cacheControl, Valid: cacheControl != ""}
		newEntry.ETag = sql.NullString{String: buf.Header().Get("ETag"), Valid: buf.Header().Get("ETag") != ""}
		newEntry.LastModified = sql.NullString{String: buf.Header().Get("Last-Modified"), Valid: buf.Header().Get("Last-Modified") != ""}
		newEntry.Vary = sql.NullString{String: buf.Header().Get("Vary"), Valid: buf.Header().Get("Vary") != ""}
		// Copy body using append - reuses backing array if capacity suffices
		newEntry.Body = append(newEntry.Body[:0], buf.body...)
		newEntry.ContentLength = sql.NullInt64{Int64: bodySize, Valid: true}
		newEntry.CreatedAt = now
		newEntry.ExpiresAt = expiresAt

		// Queue cache write asynchronously via unified batcher
		// submitFunc is always set in production (enforced by NewHTTPCacheMiddleware)
		if hcm.syncMode {
			hcm.submitFunc(newEntry)
		} else {
			go hcm.submitFunc(newEntry)
		}
	})
}

// checkCache retrieves a cached entry from SQLite by key.
func (hcm *HTTPCacheMiddleware) checkCache(ctx context.Context, key string) (*HTTPCacheEntry, error) {
	return GetCacheEntry(ctx, hcm.db, key)
}

// evictIfNeeded checks total cache size and evicts LRU entries if budget exceeded.
// Returns the actual number of bytes freed.
func (hcm *HTTPCacheMiddleware) evictIfNeeded(ctx context.Context, newEntrySize int64) (int64, error) {
	if hcm.config.MaxTotalSize <= 0 {
		return 0, nil
	}

	var currentSize int64
	if hcm.sizeCounter != nil {
		currentSize = hcm.sizeCounter.Load()
	} else {
		var err error
		currentSize, err = GetCacheSizeBytes(ctx, hcm.db)
		if err != nil {
			return 0, err
		}
	}

	if currentSize+newEntrySize > hcm.config.MaxTotalSize {
		targetFree := (currentSize + newEntrySize) - hcm.config.MaxTotalSize
		freed, err := EvictLRU(ctx, hcm.db, targetFree)
		if err != nil {
			return freed, err
		}
		if hcm.sizeCounter != nil && freed > 0 {
			hcm.sizeCounter.Add(-freed)
		}
		return freed, nil
	}
	return 0, nil
}

// hasCacheBypassDirective checks for directives that should bypass the cache.
// Per RFC 7234, this includes no-cache, no-store, and max-age=0.
func hasCacheBypassDirective(cacheControls []string) bool {
	for _, cc := range cacheControls {
		for part := range strings.SplitSeq(cc, ",") {
			directive := strings.ToLower(strings.TrimSpace(part))
			if directive == "no-cache" || directive == "no-store" || strings.HasPrefix(directive, "no-cache=") {
				return true
			}
			if strings.HasPrefix(directive, "max-age") {
				kv := strings.SplitN(directive, "=", 2)
				if len(kv) == 2 && strings.TrimSpace(kv[0]) == "max-age" && strings.TrimSpace(kv[1]) == "0" {
					return true
				}
			}
		}
	}
	return false
}
