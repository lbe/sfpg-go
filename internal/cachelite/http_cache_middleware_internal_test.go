package cachelite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"

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
	if migErr := m.Up(); migErr != nil && !errors.Is(migErr, migrate.ErrNoChange) {
		tb.Fatalf("failed to apply migrations: %v", err)
	}
	m2, err := migrations.NewThumbsMigrator(thumbsDBPath)
	if err != nil {
		tb.Fatalf("failed to create thumbs migrator: %v", err)
	}
	if thumbsErr := m2.Up(); thumbsErr != nil && !errors.Is(thumbsErr, migrate.ErrNoChange) {
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
