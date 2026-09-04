// Package cachelite provides an SQLite-backed HTTP response cache with middleware,
// optional asynchronous write batching, and pooled HTTPCacheEntry to reduce allocations.
package cachelite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/tableswap"
)

var _ tableswap.DB = (*sql.Conn)(nil) // REQUIRED gate — if this fails to compile, STOP

var (
	// getHttpCacheSizeBytes is a testable hook for the SUM query used by GetCacheSizeBytes.
	getHttpCacheSizeBytes = func(ctx context.Context, cpc *dbconnpool.CpConn) (int64, error) {
		return cpc.Queries.GetHttpCacheSizeBytes(ctx)
	}
)

// HTTPCacheEntry represents a cached HTTP response.
type HTTPCacheEntry struct {
	ID            int64
	Key           string
	Method        string
	Path          string
	QueryString   sql.NullString
	Status        int64
	ContentType   sql.NullString
	CacheControl  sql.NullString
	ETag          sql.NullString
	LastModified  sql.NullString
	Vary          sql.NullString
	Body          []byte
	ContentLength sql.NullInt64
	CreatedAt     int64
	ExpiresAt     sql.NullInt64
}

// CacheConfig holds configuration for the HTTP cache.
type CacheConfig struct {
	Enabled         bool
	MaxEntrySize    int64
	MaxTotalSize    int64
	DefaultTTL      time.Duration
	CacheableRoutes []string // Only these routes are cacheable; empty = all

	// OnGalleryCacheHit is an optional callback invoked when serving a cache HIT
	// for a gallery path (/gallery/{id}). Called with folderID parsed from path and
	// sessionID from cookie (if SessionCookieName is set) or RemoteAddr.
	// Called in a goroutine (fire-and-forget).
	// If SkipPreloadWhenHeader is set and matches the request header, this callback
	// is not invoked (e.g., to avoid cascading preloads from internal requests).
	OnGalleryCacheHit func(ctx context.Context, folderID int64, sessionID string)

	// SessionCookieName is the cookie name used to extract sessionID for OnGalleryCacheHit.
	// If set and cookie is present, its value is used as sessionID; otherwise RemoteAddr is used.
	SessionCookieName string

	// SkipPreloadWhenHeader and SkipPreloadWhenValue: if both are set and
	// r.Header.Get(SkipPreloadWhenHeader) == SkipPreloadWhenValue, OnGalleryCacheHit
	// is not called (e.g., to skip preload for internal preload requests).
	SkipPreloadWhenHeader string
	SkipPreloadWhenValue  string
}

// IsCacheablePath returns true if the given path matches any CacheableRoutes entry.
func (cfg *CacheConfig) IsCacheablePath(path string) bool {
	if len(cfg.CacheableRoutes) == 0 {
		return true // default: all routes cacheable
	}
	for _, route := range cfg.CacheableRoutes {
		if strings.HasPrefix(path, route) {
			return true
		}
	}
	return false
}

// GetCacheEntry retrieves a cache entry by key from the database.
// Returns nil if not found or expired (query already filters expired).
// The returned Body is always uncompressed; decoding is performed
// automatically based on the stored format's magic prefix.
func GetCacheEntry(ctx context.Context, db *dbconnpool.DbSQLConnPool, key string) (*HTTPCacheEntry, error) {
	cpc, err := db.Get()
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)

	result, err := cpc.Queries.GetHttpCacheByKey(ctx, key)
	if err != nil {
		return nil, err // sql.ErrNoRows passes through; middleware treats as MISS
	}

	uncompressedLen := 0
	if result.ContentLength.Valid {
		uncompressedLen = int(result.ContentLength.Int64)
	}
	plain, err := decodeCacheBodyForServe(result.Body, uncompressedLen)
	if err != nil {
		return nil, err // includes ErrUnrecognizedCacheBody → MISS
	}

	return &HTTPCacheEntry{
		ID:            result.ID,
		Key:           result.Key,
		Method:        result.Method,
		Path:          result.Path,
		QueryString:   result.QueryString,
		Status:        result.Status,
		ContentType:   result.ContentType,
		CacheControl:  result.CacheControl,
		ETag:          result.Etag,
		LastModified:  result.LastModified,
		Vary:          result.Vary,
		Body:          plain,
		ContentLength: result.ContentLength,
		CreatedAt:     result.CreatedAt,
		ExpiresAt:     result.ExpiresAt,
	}, nil
}

// StoreCacheEntry inserts or updates a cache entry in the database.
func StoreCacheEntry(ctx context.Context, db *dbconnpool.DbSQLConnPool, entry *HTTPCacheEntry) error {
	if err := FinalizeForStorage(entry); err != nil {
		return err
	}

	cpc, err := db.Get()
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)

	return cpc.Queries.UpsertHttpCache(ctx, gallerydb.UpsertHttpCacheParams{
		Key:           entry.Key,
		Method:        entry.Method,
		Path:          entry.Path,
		QueryString:   entry.QueryString,
		Status:        entry.Status,
		ContentType:   entry.ContentType,
		CacheControl:  entry.CacheControl,
		Etag:          entry.ETag,
		LastModified:  entry.LastModified,
		Vary:          entry.Vary,
		Body:          entry.Body,
		ContentLength: entry.ContentLength,
		CreatedAt:     entry.CreatedAt,
		ExpiresAt:     entry.ExpiresAt,
	})
}

// StoreCacheEntryInTx stores a cache entry within an existing transaction.
// Used by unified WriteBatcher to batch cache writes with other operations.
func StoreCacheEntryInTx(ctx context.Context, tx *sql.Tx, entry *HTTPCacheEntry) error {
	if entry == nil {
		return nil
	}

	if err := FinalizeForStorage(entry); err != nil {
		return err
	}

	q := gallerydb.New(tx)

	return q.UpsertHttpCache(ctx, gallerydb.UpsertHttpCacheParams{
		Key:           entry.Key,
		Method:        entry.Method,
		Path:          entry.Path,
		QueryString:   entry.QueryString,
		Status:        entry.Status,
		ContentType:   entry.ContentType,
		CacheControl:  entry.CacheControl,
		Etag:          entry.ETag,
		LastModified:  entry.LastModified,
		Vary:          entry.Vary,
		Body:          entry.Body,
		ContentLength: entry.ContentLength,
		CreatedAt:     entry.CreatedAt,
		ExpiresAt:     entry.ExpiresAt,
	})
}

// DeleteCacheEntry removes a single cache entry by key.
func DeleteCacheEntry(ctx context.Context, db *dbconnpool.DbSQLConnPool, key string) error {
	cpc, err := db.Get()
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)
	return cpc.Queries.DeleteHttpCacheByKey(ctx, key)
}

// ClearCache deletes all cache entries from the database.
func ClearCache(ctx context.Context, db *dbconnpool.DbSQLConnPool) error {
	cpc, err := db.Get()
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)
	return cpc.Queries.ClearHttpCache(ctx)
}

// RotateCacheTable replaces http_cache with an empty table via tableswap.
// It leases one RW connection, runs CloneEmpty and CreateIndexes on it, then
// calls Swap, which DROP TABLEs http_cache_to_be_dropped and Puts the
// connection before returning. If CloneEmpty or CreateIndexes fails,
// RotateCacheTable Puts the connection itself.
func RotateCacheTable(ctx context.Context, db *dbconnpool.DbSQLConnPool) error {
	cpc, err := db.Get()
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	if err := tableswap.CloneEmpty(ctx, cpc.Conn, "http_cache"); err != nil {
		db.Put(cpc)
		return fmt.Errorf("clone empty http_cache: %w", err)
	}
	if err := tableswap.CreateIndexes(ctx, cpc.Conn, "http_cache"); err != nil {
		db.Put(cpc)
		return fmt.Errorf("create http_cache indexes on dest: %w", err)
	}
	if err := tableswap.Swap(ctx, cpc, db.Put, "http_cache"); err != nil {
		return fmt.Errorf("swap http_cache: %w", err)
	}
	return nil
}

// EvictLRU removes oldest cache entries until at least targetFreeBytes are available.
// Uses LRU (Least Recently Used) strategy based on created_at timestamp.
// Returns the actual number of bytes freed and the number of entries deleted.
func EvictLRU(ctx context.Context, db *dbconnpool.DbSQLConnPool, targetFreeBytes int64) (freedBytes int64, entriesDeleted int64, err error) {
	// Check for already-canceled context before starting database operations.
	// This prevents panics in database/sql when rows.Next() is called with a canceled context.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, 0, fmt.Errorf("context canceled before eviction: %w", ctxErr)
	}

	cpc, err := db.Get()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)

	oldest, err := cpc.Queries.GetHttpCacheOldestCreated(ctx, 1000)
	if err != nil {
		return 0, 0, fmt.Errorf("GetHttpCacheOldestCreated failed: %w", err)
	}

	freedBytes = int64(0)
	for _, row := range oldest {
		if freedBytes >= targetFreeBytes {
			break
		}

		if err := cpc.Queries.DeleteHttpCacheByID(ctx, row.ID); err != nil {
			return freedBytes, entriesDeleted, fmt.Errorf("DeleteHttpCacheByID failed: %w", err)
		}

		entriesDeleted++

		// Add actual stored length from LENGTH(body) (handle NULL as 0)
		if row.StoredLength.Valid {
			freedBytes += row.StoredLength.Int64
		}
	}

	return freedBytes, entriesDeleted, nil
}

// GetCacheSizeBytes returns the total size of all cache entries in bytes.
func GetCacheSizeBytes(ctx context.Context, db *dbconnpool.DbSQLConnPool) (int64, error) {
	// Check for already-canceled context before starting database operations.
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("context canceled: %w", err)
	}

	cpc, err := db.Get()
	if err != nil {
		return 0, fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)

	return getHttpCacheSizeBytes(ctx, cpc)
}

// CountCacheEntries returns the number of entries in the cache.
func CountCacheEntries(ctx context.Context, db *dbconnpool.DbSQLConnPool) (int64, error) {
	cpc, err := db.Get()
	if err != nil {
		return 0, fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)
	return cpc.Queries.CountHttpCacheEntries(ctx)
}

// CleanupExpired removes all expired cache entries from the database.
func CleanupExpired(ctx context.Context, db *dbconnpool.DbSQLConnPool) (int64, error) {
	cpc, err := db.Get()
	if err != nil {
		return 0, fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)

	err = cpc.Queries.DeleteHttpCacheExpired(ctx)
	// sqlc does not return affected rows; return 1 to signal attempt.
	return 1, err
}

// CanCacheResponse determines if an HTTP response is eligible for caching.
// Returns false if status != 200, Cache-Control contains "no-store",
// or a Set-Cookie header is present (responses that set cookies must not be cached).
func CanCacheResponse(status int, cacheControl string, setCookie string) bool {
	if status != 200 {
		return false
	}
	if strings.Contains(cacheControl, "no-store") {
		return false
	}
	if setCookie != "" {
		return false
	}
	return true
}
