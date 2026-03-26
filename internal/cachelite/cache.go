// Package cachelite provides an SQLite-backed HTTP response cache with middleware,
// optional asynchronous write batching, and pooled HTTPCacheEntry to reduce allocations.
package cachelite

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
)

var httpCacheIndexDropStatements = []string{
	"DROP INDEX IF EXISTS idx_http_cache_key",
	"DROP INDEX IF EXISTS idx_http_cache_path",
	"DROP INDEX IF EXISTS idx_http_cache_encoding",
	"DROP INDEX IF EXISTS idx_http_cache_created",
	"DROP INDEX IF EXISTS idx_http_cache_expires",
	"DROP INDEX IF EXISTS idx_http_cache_content_length",
}

var httpCacheIndexCreateStatements = []string{
	"CREATE INDEX IF NOT EXISTS idx_http_cache_key ON http_cache(key)",
	"CREATE INDEX IF NOT EXISTS idx_http_cache_path ON http_cache(path)",
	"CREATE INDEX IF NOT EXISTS idx_http_cache_encoding ON http_cache(encoding)",
	"CREATE INDEX IF NOT EXISTS idx_http_cache_created ON http_cache(created_at)",
	"CREATE INDEX IF NOT EXISTS idx_http_cache_expires ON http_cache(expires_at)",
	"CREATE INDEX IF NOT EXISTS idx_http_cache_content_length ON http_cache(content_length)",
}

const rotateDropStaleTableSQL = "DROP TABLE IF EXISTS http_cache_to_be_dropped"
const rotateRenameActiveTableSQL = "ALTER TABLE http_cache RENAME TO http_cache_to_be_dropped"
const rotateCreateActiveTableSQL = `
CREATE TABLE IF NOT EXISTS http_cache (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  key                 TEXT NOT NULL UNIQUE,
  method              TEXT NOT NULL,
  path                TEXT NOT NULL,
  query_string        TEXT,
  encoding            TEXT NOT NULL,
  status              INTEGER NOT NULL,
  content_type        TEXT,
  cache_control       TEXT,
  etag                TEXT,
  last_modified       TEXT,
  vary                TEXT,
  body                BLOB NOT NULL,
  content_length      INTEGER,
  created_at          INTEGER NOT NULL,
  expires_at          INTEGER,
  content_encoding    TEXT
)`

// HTTPCacheEntry represents a cached HTTP response.
type HTTPCacheEntry struct {
	ID              int64
	Key             string
	Method          string
	Path            string
	QueryString     sql.NullString
	Encoding        string
	Status          int64
	ContentType     sql.NullString
	ContentEncoding sql.NullString
	CacheControl    sql.NullString
	ETag            sql.NullString
	LastModified    sql.NullString
	Vary            sql.NullString
	Body            []byte
	ContentLength   sql.NullInt64
	CreatedAt       int64
	ExpiresAt       sql.NullInt64
}

// CacheConfig holds configuration for the HTTP cache.
type CacheConfig struct {
	Enabled         bool
	MaxEntrySize    int64
	MaxTotalSize    int64
	DefaultTTL      time.Duration
	CacheableRoutes []string // Only these routes are cacheable; empty = all

	// OnGalleryCacheHit is an optional callback invoked when serving a cache HIT
	// for a gallery path (/gallery/{id}). Called with folderID parsed from path,
	// sessionID from cookie (if SessionCookieName is set) or RemoteAddr, and
	// acceptEncoding from request header. Called in a goroutine (fire-and-forget).
	// If SkipPreloadWhenHeader is set and matches the request header, this callback
	// is not invoked (e.g., to avoid cascading preloads from internal requests).
	OnGalleryCacheHit func(ctx context.Context, folderID int64, sessionID string, acceptEncoding string)

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

// NormalizeAcceptEncoding returns a canonical encoding for cache key use so that
// browser values like "gzip, deflate, br" match preload keys like "gzip". Uses the
// same preference as server compression: first of br, gzip in the header, else identity.
func NormalizeAcceptEncoding(acceptEncoding string) string {
	if acceptEncoding == "" {
		return "identity"
	}
	for part := range strings.SplitSeq(acceptEncoding, ",") {
		enc := strings.TrimSpace(part)
		if i := strings.Index(enc, ";"); i > 0 {
			enc = strings.TrimSpace(enc[:i])
		}
		switch enc {
		case "br":
			return "br"
		case "gzip":
			return "gzip"
		}
	}
	return "identity"
}

// GetCacheEntry retrieves a cache entry by key from the database.
// Returns nil if not found or expired (query already filters expired).
func GetCacheEntry(ctx context.Context, db *dbconnpool.DbSQLConnPool, key string) (*HTTPCacheEntry, error) {
	cpc, err := db.Get()
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)

	result, err := cpc.Queries.GetHttpCacheByKey(ctx, key)
	if err != nil {
		return nil, err
	}

	return &HTTPCacheEntry{
		ID:              result.ID,
		Key:             result.Key,
		Method:          result.Method,
		Path:            result.Path,
		QueryString:     result.QueryString,
		Encoding:        result.Encoding,
		Status:          result.Status,
		ContentType:     result.ContentType,
		ContentEncoding: result.ContentEncoding,
		CacheControl:    result.CacheControl,
		ETag:            result.Etag,
		LastModified:    result.LastModified,
		Vary:            result.Vary,
		Body:            result.Body,
		ContentLength:   result.ContentLength,
		CreatedAt:       result.CreatedAt,
		ExpiresAt:       result.ExpiresAt,
	}, nil
}

// StoreCacheEntry inserts or updates a cache entry in the database.
func StoreCacheEntry(ctx context.Context, db *dbconnpool.DbSQLConnPool, entry *HTTPCacheEntry) error {
	cpc, err := db.Get()
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)

	return cpc.Queries.UpsertHttpCache(ctx, gallerydb.UpsertHttpCacheParams{
		Key:             entry.Key,
		Method:          entry.Method,
		Path:            entry.Path,
		QueryString:     entry.QueryString,
		Encoding:        entry.Encoding,
		Status:          entry.Status,
		ContentType:     entry.ContentType,
		ContentEncoding: entry.ContentEncoding,
		CacheControl:    entry.CacheControl,
		Etag:            entry.ETag,
		LastModified:    entry.LastModified,
		Vary:            entry.Vary,
		Body:            entry.Body,
		ContentLength:   entry.ContentLength,
		CreatedAt:       entry.CreatedAt,
		ExpiresAt:       entry.ExpiresAt,
	})
}

// StoreCacheEntryInTx stores a cache entry within an existing transaction.
// Used by unified WriteBatcher to batch cache writes with other operations.
func StoreCacheEntryInTx(ctx context.Context, tx *sql.Tx, entry *HTTPCacheEntry) error {
	if entry == nil {
		return nil
	}

	q := gallerydb.New(tx)

	return q.UpsertHttpCache(ctx, gallerydb.UpsertHttpCacheParams{
		Key:             entry.Key,
		Method:          entry.Method,
		Path:            entry.Path,
		QueryString:     entry.QueryString,
		Encoding:        entry.Encoding,
		Status:          entry.Status,
		ContentType:     entry.ContentType,
		ContentEncoding: entry.ContentEncoding,
		CacheControl:    entry.CacheControl,
		Etag:            entry.ETag,
		LastModified:    entry.LastModified,
		Vary:            entry.Vary,
		Body:            entry.Body,
		ContentLength:   entry.ContentLength,
		CreatedAt:       entry.CreatedAt,
		ExpiresAt:       entry.ExpiresAt,
	})
}

// StoreCacheEntryBatch inserts multiple cache entries in a single transaction.
// Uses a loop of single inserts (SQLite best practice).
//
// Failure semantics: All-or-nothing. If any entry fails to insert, the entire
// batch is rolled back. Failed entries will be retried on subsequent preload runs.
func StoreCacheEntryBatch(ctx context.Context, db *dbconnpool.DbSQLConnPool, entries []*HTTPCacheEntry) error {
	if len(entries) == 0 {
		return nil
	}
	for i, e := range entries {
		if e == nil {
			return fmt.Errorf("nil entry at index %d in batch", i)
		}
	}

	cpc, err := db.Get()
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)

	tx, err := cpc.Conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	queries := gallerydb.New(tx)
	for _, entry := range entries {
		if err := queries.UpsertHttpCache(ctx, gallerydb.UpsertHttpCacheParams{
			Key:             entry.Key,
			Method:          entry.Method,
			Path:            entry.Path,
			QueryString:     entry.QueryString,
			Encoding:        entry.Encoding,
			Status:          entry.Status,
			ContentType:     entry.ContentType,
			ContentEncoding: entry.ContentEncoding,
			CacheControl:    entry.CacheControl,
			Etag:            entry.ETag,
			LastModified:    entry.LastModified,
			Vary:            entry.Vary,
			Body:            entry.Body,
			ContentLength:   entry.ContentLength,
			CreatedAt:       entry.CreatedAt,
			ExpiresAt:       entry.ExpiresAt,
		}); err != nil {
			slog.Warn("Batch insert failed for entry", "key", entry.Key, "path", entry.Path, "err", err)
			return fmt.Errorf("failed to insert entry %s: %w", entry.Key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
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

// RotateCacheTable atomically swaps out http_cache by renaming the current table,
// recreating a fresh http_cache table, and rebuilding its indexes.
func RotateCacheTable(ctx context.Context, db *dbconnpool.DbSQLConnPool) error {
	cpc, err := db.Get()
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)

	tx, err := cpc.Conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin rotation transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, rotateDropStaleTableSQL); err != nil {
		return fmt.Errorf("drop previous stale cache table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, rotateRenameActiveTableSQL); err != nil {
		return fmt.Errorf("rename http_cache to stale table: %w", err)
	}
	for _, stmt := range httpCacheIndexDropStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("drop stale cache index failed (%s): %w", stmt, err)
		}
	}
	if _, err := tx.ExecContext(ctx, rotateCreateActiveTableSQL); err != nil {
		return fmt.Errorf("create fresh http_cache table: %w", err)
	}
	for _, stmt := range httpCacheIndexCreateStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create cache index failed (%s): %w", stmt, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cache table rotation: %w", err)
	}
	return nil
}

// DropStaleCacheTableIfExists removes the deferred stale cache table.
// Returns true when a stale table was present and dropped.
func DropStaleCacheTableIfExists(ctx context.Context, db *dbconnpool.DbSQLConnPool) (bool, error) {
	cpc, err := db.Get()
	if err != nil {
		return false, fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)

	row := cpc.Conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'http_cache_to_be_dropped')`)
	var exists int64
	if err := row.Scan(&exists); err != nil {
		return false, fmt.Errorf("check stale cache table existence: %w", err)
	}
	if exists == 0 {
		return false, nil
	}

	if _, err := cpc.Conn.ExecContext(ctx, rotateDropStaleTableSQL); err != nil {
		return false, fmt.Errorf("drop stale cache table: %w", err)
	}
	return true, nil
}

// EvictLRU removes oldest cache entries until at least targetFreeBytes are available.
// Uses LRU (Least Recently Used) strategy based on created_at timestamp.
// Returns the actual number of bytes freed.
func EvictLRU(ctx context.Context, db *dbconnpool.DbSQLConnPool, targetFreeBytes int64) (int64, error) {
	// Check for already-canceled context before starting database operations.
	// This prevents panics in database/sql when rows.Next() is called with a canceled context.
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("context canceled before eviction: %w", err)
	}

	cpc, err := db.Get()
	if err != nil {
		return 0, fmt.Errorf("failed to get connection: %w", err)
	}
	defer db.Put(cpc)

	oldest, err := cpc.Queries.GetHttpCacheOldestCreated(ctx, 1000)
	if err != nil {
		return 0, fmt.Errorf("GetHttpCacheOldestCreated failed: %w", err)
	}

	freedBytes := int64(0)
	for _, row := range oldest {
		if freedBytes >= targetFreeBytes {
			break
		}

		if err := cpc.Queries.DeleteHttpCacheByID(ctx, row.ID); err != nil {
			return freedBytes, fmt.Errorf("DeleteHttpCacheByID failed: %w", err)
		}

		// Add actual content length (handle NULL as 0)
		if row.ContentLength.Valid {
			freedBytes += row.ContentLength.Int64
		}
	}

	return freedBytes, nil
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

	result, err := cpc.Queries.GetHttpCacheSizeBytes(ctx)
	if err != nil {
		return 0, err
	}

	switch v := result.(type) {
	case int64:
		return v, nil
	case nil:
		return 0, nil
	default:
		return 0, fmt.Errorf("unexpected type from GetHttpCacheSizeBytes: %T", v)
	}
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
// Returns false if status != 200 or Cache-Control contains "no-store".
func CanCacheResponse(status int, cacheControl string) bool {
	if status != 200 {
		return false
	}
	if strings.Contains(cacheControl, "no-store") {
		return false
	}
	return true
}
