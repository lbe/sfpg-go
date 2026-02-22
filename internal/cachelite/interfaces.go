package cachelite

import (
	"context"

	"go.local/sfpg/internal/dbconnpool"
)

// CacheStore abstracts HTTP cache storage operations.
type CacheStore interface {
	// Get retrieves a cache entry by key. Returns nil if not found.
	Get(ctx context.Context, key string) (*HTTPCacheEntry, error)

	// Store saves a cache entry.
	Store(ctx context.Context, entry *HTTPCacheEntry) error

	// Delete removes a cache entry by key.
	Delete(ctx context.Context, key string) error

	// EvictLRU removes least-recently-used entries to free targetBytes.
	// Returns the actual number of bytes freed.
	EvictLRU(ctx context.Context, targetBytes int64) (int64, error)

	// SizeBytes returns the total size of cached data.
	SizeBytes(ctx context.Context) (int64, error)

	// Clear removes all cache entries.
	Clear(ctx context.Context) error
}

// SQLiteCacheStore implements CacheStore using SQLite via connection pool.
type SQLiteCacheStore struct {
	pool dbconnpool.ConnectionPool
}

// NewSQLiteCacheStore creates a new SQLite-backed cache store.
func NewSQLiteCacheStore(pool dbconnpool.ConnectionPool) CacheStore {
	return &SQLiteCacheStore{pool: pool}
}

// Get retrieves a cache entry by key.
func (s *SQLiteCacheStore) Get(ctx context.Context, key string) (*HTTPCacheEntry, error) {
	return GetCacheEntry(ctx, s.pool.(*dbconnpool.DbSQLConnPool), key)
}

// Store saves a cache entry.
func (s *SQLiteCacheStore) Store(ctx context.Context, entry *HTTPCacheEntry) error {
	return StoreCacheEntry(ctx, s.pool.(*dbconnpool.DbSQLConnPool), entry)
}

// Delete removes a cache entry by key.
func (s *SQLiteCacheStore) Delete(ctx context.Context, key string) error {
	return DeleteCacheEntry(ctx, s.pool.(*dbconnpool.DbSQLConnPool), key)
}

// EvictLRU removes least-recently-used entries to free targetBytes.
func (s *SQLiteCacheStore) EvictLRU(ctx context.Context, targetBytes int64) (int64, error) {
	return EvictLRU(ctx, s.pool.(*dbconnpool.DbSQLConnPool), targetBytes)
}

// SizeBytes returns the total size of cached data.
func (s *SQLiteCacheStore) SizeBytes(ctx context.Context) (int64, error) {
	return GetCacheSizeBytes(ctx, s.pool.(*dbconnpool.DbSQLConnPool))
}

// Clear removes all cache entries.
func (s *SQLiteCacheStore) Clear(ctx context.Context) error {
	return ClearCache(ctx, s.pool.(*dbconnpool.DbSQLConnPool))
}

// Ensure SQLiteCacheStore implements CacheStore
var _ CacheStore = (*SQLiteCacheStore)(nil)
