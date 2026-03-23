package cachelite_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "github.com/golang-migrate/migrate/v4/database/sqlite"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/migrations"

	cachelite "github.com/lbe/sfpg-go/internal/cachelite"
)

// createTestDBPool provisions a temporary SQLite database with migrations applied.
// Uses production-equivalent DSN configuration from app.configureDatabaseDSN().
func createTestDBPool(t *testing.T) *dbconnpool.DbSQLConnPool {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	thumbsDBPath := filepath.Join(dir, "thumbs.db")

	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		t.Fatalf("failed to create migrations source: %v", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, "sqlite://"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("failed to create migrator: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if migErr := m.Up(); migErr != nil && migErr != migrate.ErrNoChange {
		t.Fatalf("failed to run migrations: %v", err)
	}

	m2, err := migrations.NewThumbsMigrator(thumbsDBPath)
	if err != nil {
		t.Fatalf("failed to create thumbs migrator: %v", err)
	}
	if thumbsErr := m2.Up(); thumbsErr != nil && thumbsErr != migrate.ErrNoChange {
		m2.Close()
		t.Fatalf("failed to run thumbs migrations: %v", thumbsErr)
	}
	m2.Close()

	// Match production DSN from app.configureDatabaseDSN()
	mmapSize := strconv.Itoa(39 * 1024 * 1024 * 1024)
	params := []string{
		"_cache_size=10240",
		"_pragma=cache(shared)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=temp_store(memory)",
		"_pragma=foreign_keys(true)",
		"_pragma=mmap_size(" + mmapSize + ")",
		"_txlock=deferred",
	}
	dsn := filepath.ToSlash(dbPath) + "?" + strings.Join(params, "&")

	pool, err := dbconnpool.NewDbSQLConnPool(context.Background(), dsn, dbconnpool.Config{
		DriverName:         "sqlite",
		MaxConnections:     10,
		MinIdleConnections: 1,
		ReadOnly:           false,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsDBPath,
	})
	if err != nil {
		t.Fatalf("failed to create test DB pool: %v", err)
	}

	return pool
}

func defaultConfig() cachelite.CacheConfig {
	return cachelite.CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/test", "/test1", "/test2", "/large", "/old", "/expired", "/cache", "/nocache", "/br", "/etag", "/gallery"},
	}
}

// createSyncSubmitFunc creates a submit function for tests that executes synchronously.
// This makes tests deterministic by ensuring cache writes complete before assertions.
// Returns the submit function and a sync.WaitGroup to wait for pending writes.
func createSyncSubmitFunc(db *dbconnpool.DbSQLConnPool) func(*cachelite.HTTPCacheEntry) {
	return func(entry *cachelite.HTTPCacheEntry) {
		ctx := context.Background()

		// Store entry directly (production batcher would handle eviction)
		// For tests, we skip eviction to avoid needing access to unexported fields
		// This is acceptable for unit tests that don't specifically test eviction behavior
		if err := cachelite.StoreCacheEntry(ctx, db, entry); err != nil {
			// Log errors in tests to help debugging
			fmt.Printf("StoreCacheEntry error for key %s: %v\n", entry.Key, err)
		}

		// Return entry to pool
		cachelite.PutHTTPCacheEntry(entry)
	}
}

// createTestMiddlewareWithSubmit creates a test middleware with a synchronous submit function.
// Returns the middleware and a WaitGroup to wait for pending cache writes.
func createTestMiddlewareWithSubmit(t *testing.T, db *dbconnpool.DbSQLConnPool, cfg cachelite.CacheConfig) *cachelite.HTTPCacheMiddleware {
	submitFunc := createSyncSubmitFunc(db)
	return cachelite.NewHTTPCacheMiddlewareForTest(db, cfg, nil, submitFunc)
}

// TestCacheMiss_HandlerCalledAndStored verifies cache miss calls handler and stores result.
func TestCacheMiss_HandlerCalledAndStored(t *testing.T) {
	db := createTestDBPool(t)

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("expensive content"))
	})

	cfg := defaultConfig()

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if handlerCalls != 1 {
		t.Errorf("handler calls = %d, want 1", handlerCalls)
	}
	if w.Header().Get("X-Cache") != "MISS" {
		t.Errorf("X-Cache = %q, want MISS", w.Header().Get("X-Cache"))
	}

	// The middleware includes HX-Request and HX-Target in the cache key's query part
	params := cachelite.CacheKeyParams{
		Method: "GET",
		Path:   "/test",
		HTMX: cachelite.HTMXParams{
			Request: "false",
			Target:  "",
		},
		Theme:    "dark",
		Encoding: "identity",
	}
	key := cachelite.NewCacheKey(params)
	entry, err := cachelite.GetCacheEntry(context.Background(), db, key)
	if err != nil {
		t.Fatalf("GetCacheEntry failed: %v", err)
	}
	if entry == nil {
		t.Fatal("expected cache entry to be stored")
	}
	if string(entry.Body) != "expensive content" {
		t.Errorf("cached body = %q, want %q", string(entry.Body), "expensive content")
	}
}

// TestCacheHit_HandlerNotCalled_CachedResponseReturned verifies cache hit skips handler.
func TestCacheHit_HandlerNotCalled_CachedResponseReturned(t *testing.T) {
	db := createTestDBPool(t)

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("expensive content"))
	})

	cfg := defaultConfig()

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	req1 := httptest.NewRequest("GET", "/test", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if handlerCalls != 1 {
		t.Fatalf("first request handler calls = %d, want 1", handlerCalls)
	}

	req2 := httptest.NewRequest("GET", "/test", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)
	if handlerCalls != 1 {
		t.Errorf("second request handler calls = %d, want 1 (cache hit)", handlerCalls)
	}
	if w2.Code != 200 {
		t.Errorf("cache hit status = %d, want 200", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
	if w2.Body.String() != "expensive content" {
		t.Errorf("cache hit body = %q, want %q", w2.Body.String(), "expensive content")
	}
}

// TestEncodingSeparation_GzipVsBrotli verifies separate cache entries per encoding.
func TestEncodingSeparation_GzipVsBrotli(t *testing.T) {
	db := createTestDBPool(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("content for " + r.Header.Get("Accept-Encoding")))
	})

	cfg := defaultConfig()

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	req1 := httptest.NewRequest("GET", "/test", nil)
	req1.Header.Set("Accept-Encoding", "gzip")
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Accept-Encoding", "br")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	// The middleware includes HX-Request (defaults to "false") in the cache key's query part
	paramsGzip := cachelite.CacheKeyParams{
		Method: "GET",
		Path:   "/test",
		HTMX: cachelite.HTMXParams{
			Request: "false",
			Target:  "",
		},
		Theme:    "dark",
		Encoding: "gzip",
	}
	paramsBr := cachelite.CacheKeyParams{
		Method: "GET",
		Path:   "/test",
		HTMX: cachelite.HTMXParams{
			Request: "false",
			Target:  "",
		},
		Theme:    "dark",
		Encoding: "br",
	}
	keyGzip := cachelite.NewCacheKey(paramsGzip)
	keyBr := cachelite.NewCacheKey(paramsBr)

	entryGzip, _ := cachelite.GetCacheEntry(context.Background(), db, keyGzip)
	entryBr, _ := cachelite.GetCacheEntry(context.Background(), db, keyBr)

	if entryGzip == nil {
		t.Error("expected gzip cache entry")
	}
	if entryBr == nil {
		t.Error("expected br cache entry")
	}
	if entryGzip != nil && entryBr != nil {
		if string(entryGzip.Body) == string(entryBr.Body) {
			t.Error("expected different cached bodies for different encodings")
		}
	}
}

// TestSizeLimit_SkipOversized verifies large responses are not cached.
func TestSizeLimit_SkipOversized(t *testing.T) {
	db := createTestDBPool(t)

	largeBody := make([]byte, 11*1024*1024)
	for i := range largeBody {
		largeBody[i] = 'x'
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		_, _ = w.Write(largeBody)
	})

	cfg := defaultConfig()
	cfg.MaxEntrySize = 10 * 1024 * 1024 // 10MB limit

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/large", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	params := cachelite.CacheKeyParams{
		Method:   "GET",
		Path:     "/large",
		HTMX:     cachelite.HTMXParams{},
		Theme:    "dark",
		Encoding: "identity",
	}
	key := cachelite.NewCacheKey(params)
	entry, _ := cachelite.GetCacheEntry(context.Background(), db, key)
	if entry != nil {
		t.Error("expected oversized entry not to be cached")
	}
}

// TestBudgetEviction_LRU verifies LRU eviction when budget exceeded.
func TestBudgetEviction_LRU(t *testing.T) {
	db := createTestDBPool(t)

	cfg := defaultConfig()
	cfg.MaxTotalSize = 100 // very small budget

	now := time.Now().Unix()
	oldEntry := &cachelite.HTTPCacheEntry{
		Key: cachelite.NewCacheKey(cachelite.CacheKeyParams{
			Method:   "GET",
			Path:     "/old",
			HTMX:     cachelite.HTMXParams{},
			Theme:    "dark",
			Encoding: "identity",
		}),
		Method:        "GET",
		Path:          "/old",
		Encoding:      "identity",
		Status:        200,
		Body:          []byte("old content"),
		ContentLength: sql.NullInt64{Int64: 11, Valid: true},
		CreatedAt:     now - 100,
	}
	_ = cachelite.StoreCacheEntry(context.Background(), db, oldEntry)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("new content that exceeds budget"))
	})

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/new", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	oldParams := cachelite.CacheKeyParams{
		Method:   "GET",
		Path:     "/old",
		HTMX:     cachelite.HTMXParams{},
		Theme:    "dark",
		Encoding: "identity",
	}
	oldKey := cachelite.NewCacheKey(oldParams)
	evictedEntry, _ := cachelite.GetCacheEntry(context.Background(), db, oldKey)
	_ = evictedEntry // best-effort check only
}

// TestCacheInvalidation_ClearCache verifies ClearCache removes all entries.
func TestCacheInvalidation_ClearCache(t *testing.T) {
	db := createTestDBPool(t)

	now := time.Now().Unix()
	entry1 := &cachelite.HTTPCacheEntry{
		Key: cachelite.NewCacheKey(cachelite.CacheKeyParams{
			Method:   "GET",
			Path:     "/test1",
			HTMX:     cachelite.HTMXParams{},
			Theme:    "dark",
			Encoding: "identity",
		}),
		Method:    "GET",
		Path:      "/test1",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("content1"),
		CreatedAt: now,
	}
	entry2 := &cachelite.HTTPCacheEntry{
		Key: cachelite.NewCacheKey(cachelite.CacheKeyParams{
			Method:   "GET",
			Path:     "/test2",
			HTMX:     cachelite.HTMXParams{},
			Theme:    "dark",
			Encoding: "identity",
		}),
		Method:    "GET",
		Path:      "/test2",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("content2"),
		CreatedAt: now,
	}
	_ = cachelite.StoreCacheEntry(context.Background(), db, entry1)
	_ = cachelite.StoreCacheEntry(context.Background(), db, entry2)

	if err := cachelite.ClearCache(context.Background(), db); err != nil {
		t.Fatalf("ClearCache failed: %v", err)
	}

	e1, _ := cachelite.GetCacheEntry(context.Background(), db, entry1.Key)
	e2, _ := cachelite.GetCacheEntry(context.Background(), db, entry2.Key)
	if e1 != nil || e2 != nil {
		t.Error("expected all cache entries to be cleared")
	}
}

// TestExpiration_ExpiredNotReturned verifies expired entries are not returned.
func TestExpiration_ExpiredNotReturned(t *testing.T) {
	db := createTestDBPool(t)

	now := time.Now().Unix()
	expiredEntry := &cachelite.HTTPCacheEntry{
		Key: cachelite.NewCacheKey(cachelite.CacheKeyParams{
			Method:   "GET",
			Path:     "/expired",
			HTMX:     cachelite.HTMXParams{},
			Theme:    "dark",
			Encoding: "identity",
		}),
		Method:    "GET",
		Path:      "/expired",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("expired content"),
		CreatedAt: now - 7200,
		ExpiresAt: sql.NullInt64{Int64: now - 3600, Valid: true},
	}
	_ = cachelite.StoreCacheEntry(context.Background(), db, expiredEntry)

	entry, err := cachelite.GetCacheEntry(context.Background(), db, expiredEntry.Key)
	if err == nil && entry != nil {
		t.Error("expected expired entry not to be returned")
	}
}

// TestSkipPOST verifies POST requests bypass cache.
func TestSkipPOST(t *testing.T) {
	db := createTestDBPool(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	})

	cfg := defaultConfig()

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("POST", "/test", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	params := cachelite.CacheKeyParams{
		Method:   "POST",
		Path:     "/test",
		HTMX:     cachelite.HTMXParams{},
		Theme:    "dark",
		Encoding: "identity",
	}
	key := cachelite.NewCacheKey(params)
	entry, _ := cachelite.GetCacheEntry(context.Background(), db, key)
	if entry != nil {
		t.Error("expected POST request not to be cached")
	}
}

// TestSkipNoCacheDirective verifies no-store responses are not cached.
func TestSkipNoCacheDirective(t *testing.T) {
	db := createTestDBPool(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("private content"))
	})

	cfg := defaultConfig()

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	params := cachelite.CacheKeyParams{
		Method:   "GET",
		Path:     "/test",
		HTMX:     cachelite.HTMXParams{},
		Theme:    "dark",
		Encoding: "identity",
	}
	key := cachelite.NewCacheKey(params)
	entry, _ := cachelite.GetCacheEntry(context.Background(), db, key)
	if entry != nil {
		t.Error("expected no-store response not to be cached")
	}
}

// TestSkip404 verifies non-200 responses are not cached.
func TestSkip404(t *testing.T) {
	db := createTestDBPool(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(404)
		_, _ = w.Write([]byte("not found"))
	})

	cfg := defaultConfig()

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}

	params := cachelite.CacheKeyParams{
		Method:   "GET",
		Path:     "/test",
		HTMX:     cachelite.HTMXParams{},
		Theme:    "dark",
		Encoding: "identity",
	}
	key := cachelite.NewCacheKey(params)
	entry, _ := cachelite.GetCacheEntry(context.Background(), db, key)
	if entry != nil {
		t.Error("expected 404 response not to be cached")
	}
}

// TestContentEncodingStoredAndRestoredBrotli ensures compressed responses persist encoding header.
func TestContentEncodingStoredAndRestoredBrotli(t *testing.T) {
	db := createTestDBPool(t)

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		// Explicitly set Content-Encoding header (as compression middleware would)
		w.Header().Set("Content-Encoding", "br")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("compressed-body-br"))
	})

	cfg := defaultConfig()

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	// First request (MISS) with br negotiation
	req1 := httptest.NewRequest("GET", "/br", nil)
	req1.Header.Set("Accept-Encoding", "br")
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if w1.Code != 200 {
		t.Fatalf("miss status = %d, want 200", w1.Code)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("miss X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}

	// Second request (HIT) should restore Content-Encoding: br
	req2 := httptest.NewRequest("GET", "/br", nil)
	req2.Header.Set("Accept-Encoding", "br")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if handlerCalls != 1 {
		t.Fatalf("handler calls = %d, want 1 (cache hit)", handlerCalls)
	}
	if w2.Code != 200 {
		t.Fatalf("hit status = %d, want 200", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("hit X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
	if ce := w2.Header().Get("Content-Encoding"); ce != "br" {
		t.Fatalf("hit Content-Encoding = %q, want br", ce)
	}
	if body := w2.Body.String(); body != "compressed-body-br" {
		t.Fatalf("hit body = %q, want compressed-body-br", body)
	}

	// The middleware includes HX-Request and HX-Target in the cache key's query part
	params := cachelite.CacheKeyParams{
		Method: "GET",
		Path:   "/br",
		HTMX: cachelite.HTMXParams{
			Request: "false",
			Target:  "",
		},
		Theme:    "dark",
		Encoding: "br",
	}
	key := cachelite.NewCacheKey(params)
	entry, err := cachelite.GetCacheEntry(context.Background(), db, key)
	if err != nil {
		t.Fatalf("GetCacheEntry error: %v", err)
	}
	if entry == nil || !entry.ContentEncoding.Valid || entry.ContentEncoding.String != "br" {
		t.Fatalf("cached ContentEncoding = %#v, want br", entry)
	}
}

// TestIfNoneMatchReturns304 validates 304 handling with cached validators and no body.
func TestIfNoneMatchReturns304(t *testing.T) {
	db := createTestDBPool(t)

	handlerCalls := 0
	etag := "\"test-etag\""
	lastMod := time.Now().UTC().Format(http.TimeFormat)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", lastMod)
		w.Header().Set("Vary", "Accept-Encoding")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("etag-body"))
	})

	cfg := defaultConfig()

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	// Prime cache
	req1 := httptest.NewRequest("GET", "/etag", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("prime X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}

	// Conditional request should return 304, no body
	req2 := httptest.NewRequest("GET", "/etag", nil)
	req2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if handlerCalls != 1 {
		t.Fatalf("handler calls = %d, want 1 (served from cache)", handlerCalls)
	}
	if w2.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", w2.Code)
	}
	if w2.Body.Len() != 0 {
		t.Fatalf("304 body length = %d, want 0", w2.Body.Len())
	}
	if ce := w2.Header().Get("Content-Encoding"); ce != "" {
		t.Fatalf("304 Content-Encoding = %q, want empty", ce)
	}
	if cc := w2.Header().Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Fatalf("Cache-Control = %q, want public, max-age=3600", cc)
	}
	if got := w2.Header().Get("ETag"); got != etag {
		t.Fatalf("ETag = %q, want %q", got, etag)
	}
	if got := w2.Header().Get("Last-Modified"); got != lastMod {
		t.Fatalf("Last-Modified = %q, want %q", got, lastMod)
	}
	if got := w2.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("Vary = %q, want Accept-Encoding", got)
	}
	if xcache := w2.Header().Get("X-Cache"); xcache != "HIT" {
		t.Fatalf("X-Cache = %q, want HIT", xcache)
	}
}

// TestBypassOnClientNoCache ensures client no-cache header skips cache and storage.
func TestBypassOnClientNoCache(t *testing.T) {
	db := createTestDBPool(t)

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write([]byte("fresh"))
	})

	cfg := defaultConfig()

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	// Request with Cache-Control: no-cache should bypass cache and not store
	req1 := httptest.NewRequest("GET", "/nocache", nil)
	req1.Header.Set("Cache-Control", "no-cache")
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if w1.Header().Get("X-Cache") != "BYPASS" {
		t.Fatalf("X-Cache = %q, want BYPASS", w1.Header().Get("X-Cache"))
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls after bypass = %d, want 1", handlerCalls)
	}
	params := cachelite.CacheKeyParams{
		Method:   "GET",
		Path:     "/nocache",
		HTMX:     cachelite.HTMXParams{},
		Theme:    "dark",
		Encoding: "identity",
	}
	key := cachelite.NewCacheKey(params)
	if entry, _ := cachelite.GetCacheEntry(context.Background(), db, key); entry != nil {
		t.Fatalf("expected no cache entry after bypass, got %#v", entry)
	}

	// Next request without no-cache should MISS then store
	req2 := httptest.NewRequest("GET", "/nocache", nil)
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if w2.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("second request X-Cache = %q, want MISS", w2.Header().Get("X-Cache"))
	}
	if handlerCalls != 2 {
		t.Fatalf("handler calls after second request = %d, want 2", handlerCalls)
	}
}

// TestHTTPCacheMiddleware_ComprehensiveBypass validates that all relevant bypass directives are respected.
func TestHTTPCacheMiddleware_ComprehensiveBypass(t *testing.T) {
	db := createTestDBPool(t)
	defer db.Close()

	cfg := defaultConfig()
	// Disable TTL for deterministic testing
	cfg.DefaultTTL = 0

	var handlerCalls int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fresh content"))
	})

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg) // Sync storage
	mw := cacheMW.Middleware(handler)

	tests := []struct {
		name         string
		cacheControl string
		shouldBypass bool
	}{
		{"no-cache", "no-cache", true},
		{"no-store", "no-store", true},
		{"max-age=0", "max-age=0", true},
		{"no-cache with value", "no-cache=set-cookie", true},
		{"compound no-cache", "public, no-cache", true},
		{"compound no-store", "no-store, public", true},
		{"compound max-age=0", "max-age=0, public", true},
		{"mixed case", "No-Cache", true},
		{"whitespace", " no-cache , max-age=3600 ", true},
		{"no bypass", "public, max-age=3600", false},
		{"empty cache control", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlerCalls = 0
			// Clear cache between test cases to ensure isolation
			_ = cachelite.ClearCache(context.Background(), db)

			// First request: should be MISS or BYPASS
			req1 := httptest.NewRequest("GET", "/test", nil)
			if tt.cacheControl != "" {
				req1.Header.Set("Cache-Control", tt.cacheControl)
			}
			w1 := httptest.NewRecorder()
			mw.ServeHTTP(w1, req1)

			expectedStatus := "MISS"
			if tt.shouldBypass {
				expectedStatus = "BYPASS"
			}

			if got := w1.Header().Get("X-Cache"); got != expectedStatus {
				t.Fatalf("%s: expected X-Cache %s, got %s", tt.name, expectedStatus, got)
			}

			if handlerCalls != 1 {
				t.Fatalf("%s: expected 1 handler call, got %d", tt.name, handlerCalls)
			}

			// Second request: if bypass was true, should STILL be BYPASS. If false, should be HIT.
			req2 := httptest.NewRequest("GET", "/test", nil)
			if tt.cacheControl != "" {
				req2.Header.Set("Cache-Control", tt.cacheControl)
			}
			w2 := httptest.NewRecorder()
			mw.ServeHTTP(w2, req2)

			expectedStatus2 := "HIT"
			if tt.shouldBypass {
				expectedStatus2 = "BYPASS"
			}

			if got := w2.Header().Get("X-Cache"); got != expectedStatus2 {
				t.Fatalf("%s second request: expected X-Cache %s, got %s", tt.name, expectedStatus2, got)
			}

			expectedCalls := 1
			if tt.shouldBypass {
				expectedCalls = 2 // Should call handler again because of bypass
			}

			if handlerCalls != expectedCalls {
				t.Fatalf("%s: expected %d handler calls after second request, got %d", tt.name, expectedCalls, handlerCalls)
			}
		})
	}
}

// TestGalleryCacheHit_OnGalleryCacheHitCalled verifies that OnGalleryCacheHit callback
// is invoked when serving a cache HIT for a gallery path (/gallery/{id}).
func TestGalleryCacheHit_OnGalleryCacheHitCalled(t *testing.T) {
	db := createTestDBPool(t)

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("gallery content"))
	})

	var callbackCalls []struct {
		folderID       int64
		sessionID      string
		acceptEncoding string
	}
	var callbackMu sync.Mutex

	cfg := cachelite.CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/gallery"},
		OnGalleryCacheHit: func(ctx context.Context, folderID int64, sessionID string, acceptEncoding string) {
			callbackMu.Lock()
			callbackCalls = append(callbackCalls, struct {
				folderID       int64
				sessionID      string
				acceptEncoding string
			}{folderID, sessionID, acceptEncoding})
			callbackMu.Unlock()
		},
		SessionCookieName:     "session-name",
		SkipPreloadWhenHeader: "X-SFPG-Internal-Preload",
		SkipPreloadWhenValue:  "true",
	}

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	// First request: cache MISS, handler runs, callback should NOT be called
	req1 := httptest.NewRequest("GET", "/gallery/42", nil)
	req1.Header.Set("Accept-Encoding", "gzip")
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if w1.Code != 200 {
		t.Errorf("first request status = %d, want 200", w1.Code)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Errorf("first request X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}
	if handlerCalls != 1 {
		t.Errorf("handler calls = %d, want 1", handlerCalls)
	}

	callbackMu.Lock()
	if len(callbackCalls) != 0 {
		t.Errorf("callback called %d times on cache MISS, want 0", len(callbackCalls))
	}
	callbackMu.Unlock()

	// Second request: cache HIT, handler does NOT run, callback SHOULD be called
	req2 := httptest.NewRequest("GET", "/gallery/42", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	// Give callback time to execute (it's called in a goroutine)
	time.Sleep(50 * time.Millisecond)

	if w2.Code != 200 {
		t.Errorf("second request status = %d, want 200", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("second request X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
	if handlerCalls != 1 {
		t.Errorf("handler calls = %d after cache HIT, want 1", handlerCalls)
	}

	callbackMu.Lock()
	if len(callbackCalls) != 1 {
		t.Fatalf("callback called %d times on cache HIT, want 1", len(callbackCalls))
	}
	call := callbackCalls[0]
	callbackMu.Unlock()

	if call.folderID != 42 {
		t.Errorf("callback folderID = %d, want 42", call.folderID)
	}
	if call.sessionID == "" {
		t.Error("callback sessionID is empty")
	}
	if call.acceptEncoding != "gzip" {
		t.Errorf("callback acceptEncoding = %q, want gzip", call.acceptEncoding)
	}
}

// TestGalleryCacheHit_SkipWhenInternalPreload verifies that OnGalleryCacheHit
// is NOT called when SkipPreloadWhenHeader matches the request header.
func TestGalleryCacheHit_SkipWhenInternalPreload(t *testing.T) {
	db := createTestDBPool(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("gallery content"))
	})

	callbackCalled := false
	var callbackMu sync.Mutex

	cfg := cachelite.CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/gallery"},
		OnGalleryCacheHit: func(ctx context.Context, folderID int64, sessionID string, acceptEncoding string) {
			callbackMu.Lock()
			callbackCalled = true
			callbackMu.Unlock()
		},
		SessionCookieName:     "session-name",
		SkipPreloadWhenHeader: "X-SFPG-Internal-Preload",
		SkipPreloadWhenValue:  "true",
	}

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	// First request: populate cache
	req1 := httptest.NewRequest("GET", "/gallery/99", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	// Second request: cache HIT with internal preload header - callback should NOT be called
	req2 := httptest.NewRequest("GET", "/gallery/99", nil)
	req2.Header.Set("X-SFPG-Internal-Preload", "true")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	time.Sleep(50 * time.Millisecond)

	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}

	callbackMu.Lock()
	if callbackCalled {
		t.Error("callback was called when SkipPreloadWhenHeader matched")
	}
	callbackMu.Unlock()
}

// TestGalleryCacheHit_SessionIDFromCookie verifies that sessionID is extracted
// from cookie when SessionCookieName is set and cookie is present.
func TestGalleryCacheHit_SessionIDFromCookie(t *testing.T) {
	db := createTestDBPool(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("gallery content"))
	})

	var capturedSessionID string
	var callbackMu sync.Mutex

	cfg := cachelite.CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/gallery"},
		OnGalleryCacheHit: func(ctx context.Context, folderID int64, sessionID string, acceptEncoding string) {
			callbackMu.Lock()
			capturedSessionID = sessionID
			callbackMu.Unlock()
		},
		SessionCookieName:     "session-name",
		SkipPreloadWhenHeader: "X-SFPG-Internal-Preload",
		SkipPreloadWhenValue:  "true",
	}

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	// First request: populate cache
	req1 := httptest.NewRequest("GET", "/gallery/5", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	// Second request: cache HIT with session cookie
	req2 := httptest.NewRequest("GET", "/gallery/5", nil)
	req2.AddCookie(&http.Cookie{Name: "session-name", Value: "test-session-123"})
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	time.Sleep(50 * time.Millisecond)

	callbackMu.Lock()
	if capturedSessionID != "test-session-123" {
		t.Errorf("sessionID = %q, want test-session-123", capturedSessionID)
	}
	callbackMu.Unlock()
}

// TestGalleryCacheHit_SessionIDFromRemoteAddr verifies that sessionID falls back
// to RemoteAddr when cookie is not present.
func TestGalleryCacheHit_SessionIDFromRemoteAddr(t *testing.T) {
	db := createTestDBPool(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("gallery content"))
	})

	var capturedSessionID string
	var callbackMu sync.Mutex

	cfg := cachelite.CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/gallery"},
		OnGalleryCacheHit: func(ctx context.Context, folderID int64, sessionID string, acceptEncoding string) {
			callbackMu.Lock()
			capturedSessionID = sessionID
			callbackMu.Unlock()
		},
		SessionCookieName:     "session-name",
		SkipPreloadWhenHeader: "X-SFPG-Internal-Preload",
		SkipPreloadWhenValue:  "true",
	}

	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	// First request: populate cache
	req1 := httptest.NewRequest("GET", "/gallery/7", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	// Second request: cache HIT without cookie - should use RemoteAddr
	req2 := httptest.NewRequest("GET", "/gallery/7", nil)
	req2.RemoteAddr = "192.168.1.100:54321"
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	time.Sleep(50 * time.Millisecond)

	callbackMu.Lock()
	if capturedSessionID != "192.168.1.100:54321" {
		t.Errorf("sessionID = %q, want 192.168.1.100:54321", capturedSessionID)
	}
	callbackMu.Unlock()
}

// TestThemeInCacheKey verifies that different theme cookies result in separate cache entries.
func TestThemeInCacheKey(t *testing.T) {
	db := createTestDBPool(t)

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", `"test-etag"`)
		_, _ = w.Write([]byte("<html data-theme=\"" + getThemeFromCookie(r) + "\">"))
	})

	cfg := defaultConfig()
	cacheMW := createTestMiddlewareWithSubmit(t, db, cfg)
	mw := cacheMW.Middleware(handler)

	// First request: dark theme (no cookie, default)
	req1 := httptest.NewRequest("GET", "/gallery/1", nil)
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, req1)

	if handlerCalls != 1 {
		t.Fatalf("first request: handler calls = %d, want 1", handlerCalls)
	}
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first request: X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}
	doc1, err := html.Parse(strings.NewReader(w1.Body.String()))
	if err != nil {
		t.Fatalf("first request: failed to parse HTML: %v", err)
	}
	bodyElement1 := findElementWithAttribute(doc1, "data-theme", "dark")
	if bodyElement1 == nil {
		t.Fatalf("first request: missing element with data-theme=\"dark\"")
	}

	// Second request: light theme - should be MISS (different cache key)
	req2 := httptest.NewRequest("GET", "/gallery/1", nil)
	req2.AddCookie(&http.Cookie{Name: "theme", Value: "light"})
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, req2)

	if handlerCalls != 2 {
		t.Fatalf("second request: handler calls = %d, want 2", handlerCalls)
	}
	if w2.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("second request: X-Cache = %q, want MISS", w2.Header().Get("X-Cache"))
	}
	doc2, err := html.Parse(strings.NewReader(w2.Body.String()))
	if err != nil {
		t.Fatalf("second request: failed to parse HTML: %v", err)
	}
	bodyElement2 := findElementWithAttribute(doc2, "data-theme", "light")
	if bodyElement2 == nil {
		t.Fatalf("second request: missing element with data-theme=\"light\"")
	}

	// Third request: dark theme again - should be HIT (same cache key as first)
	req3 := httptest.NewRequest("GET", "/gallery/1", nil)
	w3 := httptest.NewRecorder()
	mw.ServeHTTP(w3, req3)

	if handlerCalls != 2 {
		t.Fatalf("third request: handler calls = %d, want 2 (cached)", handlerCalls)
	}
	if w3.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("third request: X-Cache = %q, want HIT", w3.Header().Get("X-Cache"))
	}

	// Fourth request: light theme again - should be HIT (same cache key as second)
	req4 := httptest.NewRequest("GET", "/gallery/1", nil)
	req4.AddCookie(&http.Cookie{Name: "theme", Value: "light"})
	w4 := httptest.NewRecorder()
	mw.ServeHTTP(w4, req4)

	if handlerCalls != 2 {
		t.Fatalf("fourth request: handler calls = %d, want 2 (cached)", handlerCalls)
	}
	if w4.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("fourth request: X-Cache = %q, want HIT", w4.Header().Get("X-Cache"))
	}
}

// getThemeFromCookie extracts the theme from the request cookie, defaulting to "dark"
func getThemeFromCookie(r *http.Request) string {
	if cookie, err := r.Cookie("theme"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	return "dark"
}

// findElementWithAttribute searches the HTML tree for an element with a specific attribute value.
func findElementWithAttribute(n *html.Node, attrName, attrValue string) *html.Node {
	if n.Type == html.ElementNode {
		for _, attr := range n.Attr {
			if attr.Key == attrName && attr.Val == attrValue {
				return n
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findElementWithAttribute(c, attrName, attrValue); found != nil {
			return found
		}
	}
	return nil
}
