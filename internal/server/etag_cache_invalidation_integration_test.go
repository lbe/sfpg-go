//go:build integration

package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/ui"
)

func tableExists(ctx context.Context, app *App, tableName string) (bool, error) {
	cpc, err := app.dbRwPool.Get()
	if err != nil {
		return false, fmt.Errorf("failed to get rw pool connection: %w", err)
	}
	defer app.dbRwPool.Put(cpc)

	row := cpc.Conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`, tableName)
	var exists int64
	if err := row.Scan(&exists); err != nil {
		return false, fmt.Errorf("scan table exists query failed: %w", err)
	}
	return exists == 1, nil
}

func waitForTableExistence(t *testing.T, ctx context.Context, app *App, tableName string, wantExists bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		exists, err := tableExists(ctx, app, tableName)
		if err != nil {
			t.Fatalf("tableExists(%s): %v", tableName, err)
		}
		if exists == wantExists {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	final, err := tableExists(ctx, app, tableName)
	if err != nil {
		t.Fatalf("tableExists(%s): %v", tableName, err)
	}
	t.Fatalf("timed out waiting for table %s existence=%v, got %v", tableName, wantExists, final)
}

// TestETagIncrement_InvalidatesHTTPCache verifies that when ConfigIncrementETag
// is called, the HTTP cache is cleared so stale responses are not served.
func TestETagIncrement_InvalidatesHTTPCache(t *testing.T) {
	opt := getopt.Opt{}
	opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}
	opt.SessionSecret = getopt.OptString{String: "test-secret", IsSet: true}
	app := CreateAppWithOpt(t, false, opt)
	defer app.Shutdown()

	ctx := app.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	// Populate HTTP cache with an entry
	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/test", Encoding: "identity"}),
		Method:    "GET",
		Path:      "/gallery/test",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("cached content before etag increment"),
		CreatedAt: now,
	}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	// Verify entry exists
	stored, err := cachelite.GetCacheEntry(ctx, app.dbRwPool, entry.Key)
	if err != nil || stored == nil {
		t.Fatalf("expected cache entry to exist before increment, err=%v", err)
	}

	// Call ConfigIncrementETag via handler (simulates user clicking "Increment ETag")
	h := app.configHandlers
	if h == nil {
		t.Fatal("app.configHandlers is nil")
	}
	formData := strings.NewReader("csrf_token=valid-token")
	req := httptest.NewRequest("POST", "/config/increment-etag", formData)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	addAuthToRequest(t, h.SessionManager, req)

	h.ConfigIncrementETag(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("ConfigIncrementETag status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	// Verify cache was cleared (GetCacheEntry returns nil, sql.ErrNoRows when not found)
	storedAfter, err := cachelite.GetCacheEntry(ctx, app.dbRwPool, entry.Key)
	if err != nil && err != sql.ErrNoRows {
		t.Fatalf("GetCacheEntry after increment: %v", err)
	}
	if storedAfter != nil {
		t.Error("expected HTTP cache to be cleared after ETag increment, but entry still exists")
	}

	rotatedExists, err := tableExists(ctx, app, "http_cache_to_be_dropped")
	if err != nil {
		t.Fatalf("tableExists(http_cache_to_be_dropped): %v", err)
	}
	if !rotatedExists {
		t.Fatal("expected rotated stale cache table http_cache_to_be_dropped to exist after ETag invalidation")
	}
}

// TestApplyConfig_InvalidatesCacheWhenETagChanges verifies that applyConfig clears
// the HTTP cache when the ETag version in config differs from the current UI cache version.
func TestApplyConfig_InvalidatesCacheWhenETagChanges(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	ctx := app.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	// Set UI cache version to "old", config to "new" so they differ
	ui.SetCacheVersion("etag-old")
	app.configMu.Lock()
	if app.config == nil {
		app.configMu.Unlock()
		t.Fatal("app.config is nil")
	}
	app.config.ETagVersion = "etag-new"
	app.configMu.Unlock()

	// Populate HTTP cache
	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/x", Encoding: "identity"}),
		Method:    "GET",
		Path:      "/gallery/x",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("stale content"),
		CreatedAt: now,
	}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	stored, _ := cachelite.GetCacheEntry(ctx, app.dbRwPool, entry.Key)
	if stored == nil {
		t.Fatal("expected cache entry to exist before applyConfig")
	}

	app.applyConfig()

	storedAfter, err := cachelite.GetCacheEntry(ctx, app.dbRwPool, entry.Key)
	if err != nil && err != sql.ErrNoRows {
		t.Fatalf("GetCacheEntry after applyConfig: %v", err)
	}
	if storedAfter != nil {
		t.Error("expected HTTP cache to be cleared when ETag changed in applyConfig, but entry still exists")
	}

	rotatedExists, err := tableExists(ctx, app, "http_cache_to_be_dropped")
	if err != nil {
		t.Fatalf("tableExists(http_cache_to_be_dropped): %v", err)
	}
	if !rotatedExists {
		t.Fatal("expected rotated stale cache table http_cache_to_be_dropped to exist after applyConfig ETag invalidation")
	}
}

// TestApplyConfig_DoesNotInvalidateWhenETagUnchanged verifies that applyConfig
// does NOT clear the HTTP cache when the ETag version is unchanged.
func TestApplyConfig_DoesNotInvalidateWhenETagUnchanged(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	ctx := app.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	sameETag := "etag-unchanged"
	ui.SetCacheVersion(sameETag)
	app.configMu.Lock()
	if app.config == nil {
		app.configMu.Unlock()
		t.Fatal("app.config is nil")
	}
	app.config.ETagVersion = sameETag
	app.configMu.Unlock()

	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/y", Encoding: "identity"}),
		Method:    "GET",
		Path:      "/gallery/y",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("valid content"),
		CreatedAt: now,
	}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	app.applyConfig()

	storedAfter, _ := cachelite.GetCacheEntry(ctx, app.dbRwPool, entry.Key)
	if storedAfter == nil {
		t.Error("expected HTTP cache to NOT be cleared when ETag unchanged, but entry was removed")
	}
}

// TestApplyConfig_DoesNotInvalidateOnStartup verifies that applyConfig does NOT
// clear the HTTP cache when the in-memory cache version is empty (e.g. after reboot).
// Cache should only be cleared when ETag changes from a previously known value.
func TestApplyConfig_DoesNotInvalidateOnStartup(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	ctx := app.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	// Simulate fresh process: in-memory UI cache version not set (empty)
	ui.SetCacheVersion("")
	app.configMu.Lock()
	if app.config == nil {
		app.configMu.Unlock()
		t.Fatal("app.config is nil")
	}
	app.config.ETagVersion = "v1-from-db"
	app.configMu.Unlock()

	// Populate HTTP cache (e.g. from previous run)
	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/z", Encoding: "identity"}),
		Method:    "GET",
		Path:      "/gallery/z",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("cached from before reboot"),
		CreatedAt: now,
	}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	app.applyConfig()

	storedAfter, err := cachelite.GetCacheEntry(ctx, app.dbRwPool, entry.Key)
	if err != nil && err != sql.ErrNoRows {
		t.Fatalf("GetCacheEntry after applyConfig: %v", err)
	}
	if storedAfter == nil {
		t.Error("expected HTTP cache to persist across startup when ETag unchanged (oldETag empty); entry was cleared")
	}
}

// TestETagIncrementIntegration verifies end-to-end cache invalidation through
// the HTTP router. It makes actual requests through the full middleware stack,
// verifies cache creation, calls IncrementETag directly, and confirms cache
// clearing and fresh cache creation on subsequent requests.
func TestETagIncrementIntegration(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	ctx := app.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	// Pre-populate cache with an entry (simulating a cached response)
	now := time.Now().Unix()
	entry := &cachelite.HTTPCacheEntry{
		Key:       cachelite.NewCacheKey(cachelite.CacheKeyParams{Method: "GET", Path: "/gallery/1", Encoding: "identity"}),
		Method:    "GET",
		Path:      "/gallery/1",
		Encoding:  "identity",
		Status:    200,
		Body:      []byte("cached content before etag increment"),
		CreatedAt: now,
	}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	// Verify cache entry exists
	countBefore, err := cachelite.CountCacheEntries(ctx, app.dbRwPool)
	if err != nil {
		t.Fatalf("failed to count cache entries: %v", err)
	}
	if countBefore == 0 {
		t.Error("expected cache entry to be created")
	}

	// Increment ETag
	newETag, err := app.IncrementETag()
	if err != nil {
		t.Fatalf("IncrementETag failed: %v", err)
	}
	if newETag == "" {
		t.Error("expected non-empty ETag from IncrementETag")
	}

	// Verify cache is cleared
	countAfter, err := cachelite.CountCacheEntries(ctx, app.dbRwPool)
	if err != nil {
		t.Fatalf("failed to count cache entries: %v", err)
	}
	if countAfter != 0 {
		t.Errorf("expected cache to be cleared, got %d entries", countAfter)
	}

	// Verify new ETag is stored in config
	cfg, err := app.configService.Load(ctx)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.ETagVersion != newETag {
		t.Errorf("expected ETag version %s in config, got %s", newETag, cfg.ETagVersion)
	}
}

// TestWalkImageDir_DropsStaleCacheTable verifies deferred stale cache table cleanup
// after discovery completes.
func TestWalkImageDir_DropsStaleCacheTable(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	ctx := app.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get rw pool connection: %v", err)
	}
	_, err = cpc.Conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS http_cache_to_be_dropped (id INTEGER PRIMARY KEY, body BLOB)`)
	app.dbRwPool.Put(cpc)
	if err != nil {
		t.Fatalf("failed to create http_cache_to_be_dropped: %v", err)
	}

	beforeExists, err := tableExists(ctx, app, "http_cache_to_be_dropped")
	if err != nil {
		t.Fatalf("tableExists before walkImageDir: %v", err)
	}
	if !beforeExists {
		t.Fatal("expected stale table to exist before walkImageDir")
	}

	app.walkImageDir()
	waitForTableExistence(t, ctx, app, "http_cache_to_be_dropped", false)
}
