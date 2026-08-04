//go:build integration

package cachelite

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// createTestDBPoolInDir creates a migrated SQLite pool in an explicit directory.
// Used when a test needs multiple pools pointing at the same database file.
// Set readOnly=true to open a read-only pool for error-path testing.
func createTestDBPoolInDir(t *testing.T, dir string, readOnly bool) *dbconnpool.DbSQLConnPool {
	t.Helper()

	dbPath := filepath.Join(dir, "test.db")
	thumbsDBPath := filepath.Join(dir, "thumbs.db")

	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		t.Fatalf("failed to create migrations source: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", d, "sqlite://"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("failed to initialize migrate: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })
	if migErr := m.Up(); migErr != nil && !errors.Is(migErr, migrate.ErrNoChange) {
		t.Fatalf("failed to apply migrations: %v", migErr)
	}

	m2, err := migrations.NewThumbsMigrator(thumbsDBPath)
	if err != nil {
		t.Fatalf("failed to create thumbs migrator: %v", err)
	}
	if thumbsErr := m2.Up(); thumbsErr != nil && !errors.Is(thumbsErr, migrate.ErrNoChange) {
		m2.Close()
		t.Fatalf("failed to run thumbs migrations: %v", thumbsErr)
	}
	m2.Close()

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
		ReadOnly:           readOnly,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsDBPath,
	})
	if err != nil {
		t.Fatalf("failed to create test DB pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func defaultIntegrationConfig() CacheConfig {
	return CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,
		MaxTotalSize:    500 * 1024 * 1024,
		DefaultTTL:      time.Hour,
		CacheableRoutes: []string{"/test", "/gallery"},
	}
}

func TestNewHTTPCacheMiddleware_StoresEntryAsync(t *testing.T) {
	db := createTestDBPoolInternal(t)

	var wg sync.WaitGroup
	var submitErr error
	submit := func(entry *HTTPCacheEntry) {
		defer wg.Done()
		if err := StoreCacheEntry(context.Background(), db, entry); err != nil {
			submitErr = err
		}
		PutHTTPCacheEntry(entry)
	}

	cfg := defaultIntegrationConfig()
	mw := NewHTTPCacheMiddleware(db, cfg, nil, submit)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>async content</body></html>"))
	})

	wg.Add(1)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	mw.Middleware(handler).ServeHTTP(w, req)

	wg.Wait()
	if submitErr != nil {
		t.Fatalf("submit failed: %v", submitErr)
	}

	params := CacheKeyParams{
		Method:  "GET",
		Path:    "/test",
		Variant: "full",
	}
	entry, err := GetCacheEntry(context.Background(), db, NewCacheKey(params))
	if err != nil {
		t.Fatalf("GetCacheEntry failed: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry to be stored asynchronously")
	}
	if len(entry.Body) == 0 {
		t.Error("cached body is empty")
	}
}

func TestHTTPCacheMiddleware_SetOnGalleryCacheHit(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>gallery content</body></html>"))
	})

	var callbackCalls []struct {
		folderID  int64
		sessionID string
	}
	var callbackMu sync.Mutex
	callbackDone := make(chan struct{}, 1)

	cfg := CacheConfig{
		Enabled:               true,
		MaxEntrySize:          10 * 1024 * 1024,
		MaxTotalSize:          500 * 1024 * 1024,
		DefaultTTL:            time.Hour,
		CacheableRoutes:       []string{"/gallery"},
		SessionCookieName:     "session-name",
		SkipPreloadWhenHeader: "X-SFPG-Internal-Preload",
		SkipPreloadWhenValue:  "true",
	}

	mw := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	mw.SetOnGalleryCacheHit(func(ctx context.Context, folderID int64, sessionID string) {
		callbackMu.Lock()
		callbackCalls = append(callbackCalls, struct {
			folderID  int64
			sessionID string
		}{folderID, sessionID})
		callbackMu.Unlock()
		callbackDone <- struct{}{}
	})

	router := mw.Middleware(handler)

	req1 := httptest.NewRequest(http.MethodGet, "/gallery/42", nil)
	req1.Header.Set("Accept-Encoding", "gzip")
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first request X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}

	req2 := httptest.NewRequest(http.MethodGet, "/gallery/42", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	req2.AddCookie(&http.Cookie{Name: "session-name", Value: "test-session-123"})
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second request X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}

	// The callback fires in a goroutine; wait for it deterministically.
	select {
	case <-callbackDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OnGalleryCacheHit callback")
	}

	callbackMu.Lock()
	defer callbackMu.Unlock()
	if len(callbackCalls) != 1 {
		t.Fatalf("callback calls = %d, want 1", len(callbackCalls))
	}
	call := callbackCalls[0]
	if call.folderID != 42 {
		t.Errorf("folderID = %d, want 42", call.folderID)
	}
	if call.sessionID != "test-session-123" {
		t.Errorf("sessionID = %q, want test-session-123", call.sessionID)
	}
}

// createSyncSubmitFuncForIntegration returns a synchronous submit function for integration tests.
func createSyncSubmitFuncForIntegration(t *testing.T, db *dbconnpool.DbSQLConnPool) func(*HTTPCacheEntry) {
	t.Helper()
	return func(entry *HTTPCacheEntry) {
		ctx := context.Background()
		if err := StoreCacheEntry(ctx, db, entry); err != nil {
			t.Logf("StoreCacheEntry error for key %s: %v", entry.Key, err)
		}
		PutHTTPCacheEntry(entry)
	}
}

func TestHTTPCacheMiddleware_UpdatePool(t *testing.T) {
	dir := t.TempDir()
	pool1 := createTestDBPoolInDir(t, dir, false)
	pool2 := createTestDBPoolInDir(t, dir, false)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>pooled content</body></html>"))
	})

	cfg := defaultIntegrationConfig()
	mw := NewHTTPCacheMiddlewareForTest(pool1, cfg, nil, createSyncSubmitFuncForIntegration(t, pool1))
	router := mw.Middleware(handler)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first request X-Cache = %q, want MISS", w1.Header().Get("X-Cache"))
	}

	mw.UpdatePool(pool2)

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second request X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
	if w2.Body.Len() == 0 {
		t.Error("second request body is empty")
	}
}

func TestHTTPCacheMiddleware_UpdatePool_NilIgnored(t *testing.T) {
	db := createTestDBPoolInternal(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>nil pool content</body></html>"))
	})

	cfg := defaultIntegrationConfig()
	mw := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
	router := mw.Middleware(handler)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	mw.UpdatePool(nil)

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("X-Cache after nil UpdatePool = %q, want HIT", w2.Header().Get("X-Cache"))
	}
}

func TestHTTPCacheMiddleware_GetSizeBytes(t *testing.T) {
	db := createTestDBPoolInternal(t)
	cfg := defaultIntegrationConfig()

	t.Run("uncalibrated returns -1", func(t *testing.T) {
		var sizeBytes atomic.Int64
		counter := &HTTPCacheCounterState{
			SizeBytes:  &sizeBytes,
			EntryCount: &atomic.Int64{},
			// BaselineRunning left nil
		}
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, counter, createSyncSubmitFuncForIntegration(t, db))
		if got := mw.GetSizeBytes(); got != -1 {
			t.Fatalf("uncalibrated GetSizeBytes = %d, want -1", got)
		}
	})

	t.Run("calibrated seeded value without rows", func(t *testing.T) {
		var sizeBytes atomic.Int64
		var br atomic.Int32
		sizeBytes.Store(42)
		counter := &HTTPCacheCounterState{
			SizeBytes:       &sizeBytes,
			EntryCount:      &atomic.Int64{},
			BaselineRunning: &br,
		}
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, counter, createSyncSubmitFuncForIntegration(t, db))
		// No rows inserted into DB — value comes from atomics only.
		if got := mw.GetSizeBytes(); got != 42 {
			t.Fatalf("calibrated GetSizeBytes = %d, want 42", got)
		}
	})

	t.Run("running baseline returns -1 when size is 0", func(t *testing.T) {
		var sizeBytes atomic.Int64
		var br atomic.Int32
		br.Store(1) // baseline running, size is 0
		counter := &HTTPCacheCounterState{
			SizeBytes:       &sizeBytes,
			EntryCount:      &atomic.Int64{},
			BaselineRunning: &br,
		}
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, counter, createSyncSubmitFuncForIntegration(t, db))
		if got := mw.GetSizeBytes(); got != -1 {
			t.Fatalf("running baseline GetSizeBytes = %d, want -1 (N/A)", got)
		}
	})

	t.Run("running baseline returns value when size > 0", func(t *testing.T) {
		var sizeBytes atomic.Int64
		var br atomic.Int32
		sizeBytes.Store(500)
		br.Store(1) // baseline running, size > 0
		counter := &HTTPCacheCounterState{
			SizeBytes:       &sizeBytes,
			EntryCount:      &atomic.Int64{},
			BaselineRunning: &br,
		}
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, counter, createSyncSubmitFuncForIntegration(t, db))
		if got := mw.GetSizeBytes(); got != 500 {
			t.Fatalf("running baseline GetSizeBytes = %d, want 500", got)
		}
	})

	t.Run("not running returns 0 when size is 0", func(t *testing.T) {
		var sizeBytes atomic.Int64
		var br atomic.Int32
		// br is 0 (not running), sizeBytes is 0
		counter := &HTTPCacheCounterState{
			SizeBytes:       &sizeBytes,
			EntryCount:      &atomic.Int64{},
			BaselineRunning: &br,
		}
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, counter, createSyncSubmitFuncForIntegration(t, db))
		if got := mw.GetSizeBytes(); got != 0 {
			t.Fatalf("not running GetSizeBytes = %d, want 0", got)
		}
	})

	t.Run("closed pool still returns seeded value", func(t *testing.T) {
		var sizeBytes atomic.Int64
		var br atomic.Int32
		sizeBytes.Store(99)
		counter := &HTTPCacheCounterState{
			SizeBytes:       &sizeBytes,
			EntryCount:      &atomic.Int64{},
			BaselineRunning: &br,
		}
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, counter, createSyncSubmitFuncForIntegration(t, db))
		db.Close()
		if got := mw.GetSizeBytes(); got != 99 {
			t.Fatalf("closed pool GetSizeBytes = %d, want 99 (getters must not touch DB)", got)
		}
	})

	t.Run("nil counters returns -1", func(t *testing.T) {
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
		if got := mw.GetSizeBytes(); got != -1 {
			t.Fatalf("nil counters GetSizeBytes = %d, want -1", got)
		}
	})
}

func TestHTTPCacheMiddleware_GetEntryCount(t *testing.T) {
	db := createTestDBPoolInternal(t)
	cfg := defaultIntegrationConfig()

	t.Run("uncalibrated returns -1", func(t *testing.T) {
		var entryCount atomic.Int64
		counter := &HTTPCacheCounterState{
			SizeBytes:  &atomic.Int64{},
			EntryCount: &entryCount,
			// BaselineRunning left nil
		}
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, counter, createSyncSubmitFuncForIntegration(t, db))
		if got := mw.GetEntryCount(); got != -1 {
			t.Fatalf("uncalibrated GetEntryCount = %d, want -1", got)
		}
	})

	t.Run("calibrated seeded value without rows", func(t *testing.T) {
		var entryCount atomic.Int64
		var br atomic.Int32
		entryCount.Store(7)
		counter := &HTTPCacheCounterState{
			SizeBytes:       &atomic.Int64{},
			EntryCount:      &entryCount,
			BaselineRunning: &br,
		}
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, counter, createSyncSubmitFuncForIntegration(t, db))
		// No rows inserted into DB — value comes from atomics only.
		if got := mw.GetEntryCount(); got != 7 {
			t.Fatalf("calibrated GetEntryCount = %d, want 7", got)
		}
	})

	t.Run("running baseline returns -1 when entry count is 0", func(t *testing.T) {
		var entryCount atomic.Int64
		var br atomic.Int32
		br.Store(1) // baseline running, entry count is 0
		counter := &HTTPCacheCounterState{
			SizeBytes:       &atomic.Int64{},
			EntryCount:      &entryCount,
			BaselineRunning: &br,
		}
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, counter, createSyncSubmitFuncForIntegration(t, db))
		if got := mw.GetEntryCount(); got != -1 {
			t.Fatalf("running baseline GetEntryCount = %d, want -1 (N/A)", got)
		}
	})

	t.Run("running baseline returns value when entry count > 0", func(t *testing.T) {
		var entryCount atomic.Int64
		var br atomic.Int32
		entryCount.Store(25)
		br.Store(1) // baseline running, entry count > 0
		counter := &HTTPCacheCounterState{
			SizeBytes:       &atomic.Int64{},
			EntryCount:      &entryCount,
			BaselineRunning: &br,
		}
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, counter, createSyncSubmitFuncForIntegration(t, db))
		if got := mw.GetEntryCount(); got != 25 {
			t.Fatalf("running baseline GetEntryCount = %d, want 25", got)
		}
	})

	t.Run("not running returns 0 when entry count is 0", func(t *testing.T) {
		var entryCount atomic.Int64
		var br atomic.Int32
		// br is 0 (not running), entryCount is 0
		counter := &HTTPCacheCounterState{
			SizeBytes:       &atomic.Int64{},
			EntryCount:      &entryCount,
			BaselineRunning: &br,
		}
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, counter, createSyncSubmitFuncForIntegration(t, db))
		if got := mw.GetEntryCount(); got != 0 {
			t.Fatalf("not running GetEntryCount = %d, want 0", got)
		}
	})

	t.Run("closed pool still returns seeded value", func(t *testing.T) {
		var entryCount atomic.Int64
		var br atomic.Int32
		entryCount.Store(3)
		counter := &HTTPCacheCounterState{
			SizeBytes:       &atomic.Int64{},
			EntryCount:      &entryCount,
			BaselineRunning: &br,
		}
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, counter, createSyncSubmitFuncForIntegration(t, db))
		db.Close()
		if got := mw.GetEntryCount(); got != 3 {
			t.Fatalf("closed pool GetEntryCount = %d, want 3 (getters must not touch DB)", got)
		}
	})

	t.Run("nil counters returns -1", func(t *testing.T) {
		mw := NewHTTPCacheMiddlewareForTest(db, cfg, nil, createSyncSubmitFuncForIntegration(t, db))
		if got := mw.GetEntryCount(); got != -1 {
			t.Fatalf("nil counters GetEntryCount = %d, want -1", got)
		}
	})
}
