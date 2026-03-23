package cachelite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/migrations"
)

// createTestDBPoolTB provisions a temporary SQLite database with migrations applied.
// Used by both tests and benchmarks (testing.TB is implemented by *testing.T and *testing.B).
func createTestDBPoolTB(tb testing.TB) *dbconnpool.DbSQLConnPool {
	tb.Helper()
	dir := tb.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	thumbsDBPath := filepath.Join(dir, "thumbs.db")
	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		tb.Fatalf("failed to create migrations source: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", d, "sqlite://"+filepath.ToSlash(dbPath))
	if err != nil {
		tb.Fatalf("failed to initialize migrate: %v", err)
	}
	tb.Cleanup(func() { _, _ = m.Close() })
	if migErr := m.Up(); migErr != nil && migErr != migrate.ErrNoChange {
		tb.Fatalf("failed to apply migrations: %v", err)
	}
	m2, err := migrations.NewThumbsMigrator(thumbsDBPath)
	if err != nil {
		tb.Fatalf("failed to create thumbs migrator: %v", err)
	}
	if thumbsErr := m2.Up(); thumbsErr != nil && thumbsErr != migrate.ErrNoChange {
		m2.Close()
		tb.Fatalf("failed to run thumbs migrations: %v", thumbsErr)
	}
	m2.Close()

	mmapSize := strconv.Itoa(39 * 1024 * 1024 * 1024)
	params := []string{
		"_cache_size=10240", "_pragma=cache(shared)", "_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)", "_pragma=busy_timeout(5000)", "_pragma=temp_store(memory)",
		"_pragma=foreign_keys(true)", "_pragma=mmap_size(" + mmapSize + ")", "_txlock=deferred",
	}
	dsn := filepath.ToSlash(dbPath) + "?" + strings.Join(params, "&")
	pool, err := dbconnpool.NewDbSQLConnPool(context.Background(), dsn, dbconnpool.Config{
		DriverName: "sqlite", MaxConnections: 10, MinIdleConnections: 1, ReadOnly: false,
		QueriesFunc:  gallerydb.NewCustomQueries,
		ThumbsDBPath: thumbsDBPath,
	})
	if err != nil {
		tb.Fatalf("failed to create test DB pool: %v", err)
	}
	tb.Cleanup(func() { pool.Close() })
	return pool
}

// createTestDBPoolInternal provisions a temporary SQLite database with migrations applied.
func createTestDBPoolInternal(t *testing.T) *dbconnpool.DbSQLConnPool {
	t.Helper()
	return createTestDBPoolTB(t)
}

func TestCheckCache_ReturnsStoredEntry_Internal(t *testing.T) {
	db := createTestDBPoolInternal(t)

	ctx := context.Background()
	entry := &HTTPCacheEntry{
		Key:         "check-key",
		Method:      "GET",
		Path:        "/check",
		Status:      200,
		ContentType: sql.NullString{String: "text/plain", Valid: true},
		Body:        []byte("hello"),
		CreatedAt:   time.Now().Unix(),
	}

	if err := StoreCacheEntry(ctx, db, entry); err != nil {
		t.Fatalf("failed to store entry: %v", err)
	}

	// Dummy submit function for internal tests (not used since we test internal methods directly)
	dummySubmit := func(entry *HTTPCacheEntry) {}
	mw := NewHTTPCacheMiddlewareForTest(db, CacheConfig{MaxTotalSize: 1}, nil, dummySubmit)
	got, err := mw.checkCache(ctx, "check-key")
	if err != nil {
		t.Fatalf("checkCache returned error: %v", err)
	}
	if got == nil {
		t.Fatal("checkCache returned nil entry")
	}
	if got.Key != "check-key" {
		t.Fatalf("checkCache key = %q, want check-key", got.Key)
	}
}

func TestHasCacheBypassDirective(t *testing.T) {
	tests := []struct {
		name         string
		cacheControl []string
		want         bool
	}{
		{"Empty", []string{""}, false},
		{"No Cache (simple)", []string{"no-cache"}, true},
		{"No Cache (case insensitive)", []string{"NO-CACHE"}, true},
		{"No Store", []string{"no-store"}, true},
		{"No Store (case insensitive)", []string{"No-Store"}, true},
		{"Max Age 0", []string{"max-age=0"}, true},
		{"Max Age 0 (case insensitive)", []string{"MAX-AGE=0"}, true},
		{"Max Age 0 (with whitespace)", []string{"max-age = 0"}, true},
		{"Compound Directives", []string{"public, no-cache"}, true},
		{"Compound Directives with Space", []string{"public ,  no-cache"}, true},
		{"Multiple header values", []string{"public", "no-cache"}, true},
		{"Wait-until-expiry", []string{"max-age=3600"}, false},
		{"Only proxy revalidate", []string{"proxy-revalidate"}, false},
		{"Bypass in second value", []string{"public", "max-age=0"}, true},
		{"Bypass with other params", []string{"no-cache=\"Set-Cookie\""}, true}, // RFC 7234: no-cache can have params
		{"Max age not zero", []string{"max-age=10"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasCacheBypassDirective(tt.cacheControl); got != tt.want {
				t.Errorf("hasCacheBypassDirective(%v) = %v, want %v", tt.cacheControl, got, tt.want)
			}
		})
	}
}

func TestEvictIfNeeded_EvictsWhenOverBudget_Internal(t *testing.T) {
	db := createTestDBPoolInternal(t)

	ctx := context.Background()
	entry := &HTTPCacheEntry{
		Key:           "evict-key",
		Method:        "GET",
		Path:          "/evict",
		Status:        200,
		ContentType:   sql.NullString{String: "text/plain", Valid: true},
		Body:          []byte("to-evict"),
		ContentLength: sql.NullInt64{Int64: 8, Valid: true},
		CreatedAt:     time.Now().Unix(),
	}

	if err := StoreCacheEntry(ctx, db, entry); err != nil {
		t.Fatalf("failed to store entry: %v", err)
	}

	// Dummy submit function for internal tests (not used since we test internal methods directly)
	dummySubmit := func(entry *HTTPCacheEntry) {}
	mw := NewHTTPCacheMiddlewareForTest(db, CacheConfig{MaxTotalSize: 1}, nil, dummySubmit)

	if _, err := mw.evictIfNeeded(ctx, 2); err != nil {
		t.Fatalf("evictIfNeeded returned error: %v", err)
	}

	got, err := GetCacheEntry(ctx, db, "evict-key")
	if err == nil && got != nil {
		t.Fatalf("expected entry to be evicted, but found key %q", got.Key)
	}
}

func TestStoreCacheEntryBatch_EmptySlice(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()
	if err := StoreCacheEntryBatch(ctx, db, nil); err != nil {
		t.Fatalf("StoreCacheEntryBatch(nil) should not error: %v", err)
	}
	if err := StoreCacheEntryBatch(ctx, db, []*HTTPCacheEntry{}); err != nil {
		t.Fatalf("StoreCacheEntryBatch(empty) should not error: %v", err)
	}
}

func TestStoreCacheEntryBatch_Success(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()
	now := time.Now().Unix()
	entries := []*HTTPCacheEntry{
		{Key: "batch-key-1", Method: "GET", Path: "/a", Status: 200, Body: []byte("1"), CreatedAt: now},
		{Key: "batch-key-2", Method: "GET", Path: "/b", Status: 200, Body: []byte("2"), CreatedAt: now},
		{Key: "batch-key-3", Method: "GET", Path: "/c", Status: 200, Body: []byte("3"), CreatedAt: now},
	}
	if err := StoreCacheEntryBatch(ctx, db, entries); err != nil {
		t.Fatalf("StoreCacheEntryBatch: %v", err)
	}
	for i, key := range []string{"batch-key-1", "batch-key-2", "batch-key-3"} {
		got, err := GetCacheEntry(ctx, db, key)
		if err != nil {
			t.Fatalf("GetCacheEntry(%q): %v", key, err)
		}
		if got == nil {
			t.Fatalf("GetCacheEntry(%q) = nil", key)
		}
		wantBody := string(rune('1' + i))
		if string(got.Body) != wantBody {
			t.Errorf("GetCacheEntry(%q).Body = %q, want %q", key, string(got.Body), wantBody)
		}
	}
}

func TestStoreCacheEntryBatch_TransactionRollback(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()
	now := time.Now().Unix()
	ctxCancel, cancel := context.WithCancel(ctx)
	cancel() // cancel before call so BeginTx or Get will see cancelled context
	entries := []*HTTPCacheEntry{
		{Key: "rollback-key-1", Method: "GET", Path: "/p1", Status: 200, Body: []byte("x"), CreatedAt: now},
		{Key: "rollback-key-2", Method: "GET", Path: "/p2", Status: 200, Body: []byte("y"), CreatedAt: now},
	}
	err := StoreCacheEntryBatch(ctxCancel, db, entries)
	if err == nil {
		t.Fatal("StoreCacheEntryBatch with cancelled context should return error")
	}
	// Verify no entries were written (transaction rolled back or never started)
	for _, key := range []string{"rollback-key-1", "rollback-key-2"} {
		got, err := GetCacheEntry(ctx, db, key)
		if err == nil && got != nil {
			t.Errorf("expected key %q to not exist after cancelled batch", key)
		}
	}
}

// TestStoreCacheEntryBatch_PartialFailure verifies that when the batch contains a nil entry,
// the function returns an error and no entries from the batch are written (all-or-nothing).
func TestStoreCacheEntryBatch_PartialFailure(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()
	now := time.Now().Unix()
	validEntry := &HTTPCacheEntry{
		Key: "partial-key-1", Method: "GET", Path: "/p1", Status: 200, Body: []byte("a"), CreatedAt: now,
	}
	// Slice with nil in the middle: should error and commit nothing
	entries := []*HTTPCacheEntry{
		{Key: "partial-key-0", Method: "GET", Path: "/p0", Status: 200, Body: []byte("x"), CreatedAt: now},
		nil,
		validEntry,
	}
	err := StoreCacheEntryBatch(ctx, db, entries)
	if err == nil {
		t.Fatal("StoreCacheEntryBatch with nil entry in slice should return error")
	}
	// Verify no entries from this batch were written
	for _, key := range []string{"partial-key-0", "partial-key-1"} {
		got, getErr := GetCacheEntry(ctx, db, key)
		if getErr == nil && got != nil {
			t.Errorf("expected key %q to not exist after partial failure batch, got entry", key)
		}
	}
}
