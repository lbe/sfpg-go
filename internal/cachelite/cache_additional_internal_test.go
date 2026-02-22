package cachelite

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreCacheEntryInTx_Commits(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get connection: %v", err)
	}
	defer db.Put(cpc)

	tx, err := cpc.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	entry := &HTTPCacheEntry{
		Key:       "tx-key",
		Method:    "GET",
		Path:      "/tx",
		Status:    200,
		Body:      []byte("ok"),
		CreatedAt: time.Now().Unix(),
	}
	if err = StoreCacheEntryInTx(ctx, tx, entry); err != nil {
		_ = tx.Rollback()
		t.Fatalf("StoreCacheEntryInTx: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	got, err := GetCacheEntry(ctx, db, "tx-key")
	if err != nil {
		t.Fatalf("GetCacheEntry: %v", err)
	}
	if got == nil || got.Key != "tx-key" {
		t.Fatalf("unexpected entry: %#v", got)
	}
}

func TestStoreCacheEntryInTx_NilEntry(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get connection: %v", err)
	}
	defer db.Put(cpc)

	tx, err := cpc.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := StoreCacheEntryInTx(ctx, tx, nil); err != nil {
		_ = tx.Rollback()
		t.Fatalf("StoreCacheEntryInTx(nil): %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func TestDeleteCacheEntry_Removes(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	entry := &HTTPCacheEntry{
		Key:       "delete-key",
		Method:    "GET",
		Path:      "/delete",
		Status:    200,
		Body:      []byte("gone"),
		CreatedAt: time.Now().Unix(),
	}
	if err := StoreCacheEntry(ctx, db, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	if err := DeleteCacheEntry(ctx, db, "delete-key"); err != nil {
		t.Fatalf("DeleteCacheEntry: %v", err)
	}

	got, err := GetCacheEntry(ctx, db, "delete-key")
	if err == nil && got != nil {
		t.Fatalf("expected deleted entry, got %#v", got)
	}
}

func TestGetCacheSizeBytes_EmptyAndNonEmpty(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	size, err := GetCacheSizeBytes(ctx, db)
	if err != nil {
		t.Fatalf("GetCacheSizeBytes empty: %v", err)
	}
	if size != 0 {
		t.Fatalf("expected size 0, got %d", size)
	}

	entry := &HTTPCacheEntry{
		Key:           "size-key",
		Method:        "GET",
		Path:          "/size",
		Status:        200,
		Body:          []byte("12345"),
		ContentLength: sql.NullInt64{Int64: 5, Valid: true},
		CreatedAt:     time.Now().Unix(),
	}
	if err = StoreCacheEntry(ctx, db, entry); err != nil {
		t.Fatalf("StoreCacheEntry: %v", err)
	}

	size, err = GetCacheSizeBytes(ctx, db)
	if err != nil {
		t.Fatalf("GetCacheSizeBytes populated: %v", err)
	}
	if size < 5 {
		t.Fatalf("expected size >= 5, got %d", size)
	}
}

func TestGetCacheSizeBytes_Error(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := GetCacheSizeBytes(ctx, db); err == nil {
		t.Fatal("expected error after canceled context")
	}
}

func TestCleanupExpired_RemovesOnlyExpired(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	now := time.Now().Unix()
	expired := &HTTPCacheEntry{
		Key:       "expired-key",
		Method:    "GET",
		Path:      "/expired",
		Status:    200,
		Body:      []byte("old"),
		CreatedAt: now,
		ExpiresAt: sql.NullInt64{Int64: now - 10, Valid: true},
	}
	live := &HTTPCacheEntry{
		Key:       "live-key",
		Method:    "GET",
		Path:      "/live",
		Status:    200,
		Body:      []byte("new"),
		CreatedAt: now,
		ExpiresAt: sql.NullInt64{Int64: now + 3600, Valid: true},
	}
	if err := StoreCacheEntry(ctx, db, expired); err != nil {
		t.Fatalf("StoreCacheEntry expired: %v", err)
	}
	if err := StoreCacheEntry(ctx, db, live); err != nil {
		t.Fatalf("StoreCacheEntry live: %v", err)
	}

	if _, err := CleanupExpired(ctx, db); err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}

	got, err := GetCacheEntry(ctx, db, "expired-key")
	if err == nil && got != nil {
		t.Fatalf("expected expired entry removed, got %#v", got)
	}

	got, err = GetCacheEntry(ctx, db, "live-key")
	if err != nil || got == nil {
		t.Fatalf("expected live entry to remain, err=%v", err)
	}
}

func TestCacheConfig_IsCacheablePath(t *testing.T) {
	cfg := CacheConfig{}
	if !cfg.IsCacheablePath("/any") {
		t.Fatal("expected default config to allow any path")
	}

	cfg.CacheableRoutes = []string{"/gallery", "/image"}
	if !cfg.IsCacheablePath("/gallery/1") {
		t.Error("expected /gallery path to be cacheable")
	}
	if cfg.IsCacheablePath("/config") {
		t.Error("expected /config to be not cacheable")
	}
}

func TestHTTPCacheMiddleware_Config(t *testing.T) {
	db := createTestDBPoolInternal(t)
	cfg := CacheConfig{MaxTotalSize: 123}
	mw := NewHTTPCacheMiddleware(db, cfg, nil, nil)

	got := mw.Config()
	if got.MaxTotalSize != cfg.MaxTotalSize {
		t.Fatalf("Config MaxTotalSize = %d, want %d", got.MaxTotalSize, cfg.MaxTotalSize)
	}
}

func TestParseGalleryFolderID(t *testing.T) {
	id, ok := parseGalleryFolderID("/gallery/123")
	if !ok || id != 123 {
		t.Fatalf("expected valid folder id, got %d ok=%v", id, ok)
	}

	if _, ok := parseGalleryFolderID("/gallery/"); ok {
		t.Fatal("expected empty id to be invalid")
	}
	if _, ok := parseGalleryFolderID("/gallery/abc"); ok {
		t.Fatal("expected non-numeric id to be invalid")
	}
	if _, ok := parseGalleryFolderID("/image/1"); ok {
		t.Fatal("expected non-gallery path to be invalid")
	}
}

func TestGetSessionIDForPreload(t *testing.T) {
	db := createTestDBPoolInternal(t)
	cfg := CacheConfig{SessionCookieName: "session"}
	mw := NewHTTPCacheMiddleware(db, cfg, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	if got := mw.getSessionIDForPreload(req); got != "10.0.0.1:1234" {
		t.Fatalf("expected remote addr session id, got %q", got)
	}

	req.AddCookie(&http.Cookie{Name: "session", Value: "abc"})
	if got := mw.getSessionIDForPreload(req); got != "abc" {
		t.Fatalf("expected cookie session id, got %q", got)
	}
}

func TestMaybeTriggerGalleryPreload(t *testing.T) {
	db := createTestDBPoolInternal(t)

	called := make(chan struct{}, 1)
	cfg := CacheConfig{
		SessionCookieName:     "session",
		SkipPreloadWhenHeader: "X-Internal",
		SkipPreloadWhenValue:  "1",
		OnGalleryCacheHit: func(ctx context.Context, folderID int64, sessionID string, acceptEncoding string) {
			if folderID == 42 && sessionID == "sess" && acceptEncoding == "gzip" {
				called <- struct{}{}
			}
		},
	}
	mw := NewHTTPCacheMiddleware(db, cfg, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/gallery/42", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.AddCookie(&http.Cookie{Name: "session", Value: "sess"})
	mw.maybeTriggerGalleryPreload(context.Background(), req)

	select {
	case <-called:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected preload callback")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/gallery/42", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	req2.AddCookie(&http.Cookie{Name: "session", Value: "sess"})
	req2.Header.Set("X-Internal", "1")
	mw.maybeTriggerGalleryPreload(context.Background(), req2)

	select {
	case <-called:
		t.Fatal("expected preload callback to be skipped")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestEvictIfNeeded_Branches(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	mw := NewHTTPCacheMiddleware(db, CacheConfig{MaxTotalSize: 0}, nil, nil)
	freed, err := mw.evictIfNeeded(ctx, 10)
	if err != nil {
		t.Fatalf("evictIfNeeded no budget: %v", err)
	}
	if freed != 0 {
		t.Fatalf("expected freed 0, got %d", freed)
	}

	counter := &atomic.Int64{}
	counter.Store(5)
	mw = NewHTTPCacheMiddleware(db, CacheConfig{MaxTotalSize: 100}, counter, nil)
	freed, err = mw.evictIfNeeded(ctx, 10)
	if err != nil {
		t.Fatalf("evictIfNeeded with counter: %v", err)
	}
	if freed != 0 {
		t.Fatalf("expected no eviction, got %d", freed)
	}
}

func TestEvictIfNeeded_EvictError(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	counter := &atomic.Int64{}
	counter.Store(50)
	mw := NewHTTPCacheMiddleware(db, CacheConfig{MaxTotalSize: 1}, counter, nil)

	if _, err := mw.evictIfNeeded(ctx, 10); err == nil {
		t.Fatal("expected eviction error with canceled context")
	}
}

func TestMockCacheStore_DeleteEvictClearErrors(t *testing.T) {
	ctx := context.Background()
	store := NewMockCacheStore()

	store.DeleteErr = sql.ErrNoRows
	if err := store.Delete(ctx, "missing"); err == nil {
		t.Fatal("expected delete error")
	}

	store.EvictErr = sql.ErrNoRows
	if _, err := store.EvictLRU(ctx, 10); err == nil {
		t.Fatal("expected evict error")
	}

	store.ClearErr = sql.ErrNoRows
	if err := store.Clear(ctx); err == nil {
		t.Fatal("expected clear error")
	}

	store.EvictErr = nil
	store.ClearErr = nil
	store.Entries["a"] = &HTTPCacheEntry{Key: "a"}
	if _, err := store.EvictLRU(ctx, 1); err != nil {
		t.Fatalf("expected evict success, got %v", err)
	}
	if len(store.Entries) != 0 {
		t.Fatal("expected entries cleared after evict")
	}
}

func TestSQLiteCacheStore_Wrappers(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	store := NewSQLiteCacheStore(db)
	entry := &HTTPCacheEntry{
		Key:           "wrapper-key",
		Method:        "GET",
		Path:          "/wrap",
		Status:        200,
		Body:          []byte("wrap"),
		ContentLength: sql.NullInt64{Int64: 4, Valid: true},
		CreatedAt:     time.Now().Unix(),
	}

	if err := store.Store(ctx, entry); err != nil {
		t.Fatalf("Store: %v", err)
	}

	got, err := store.Get(ctx, "wrapper-key")
	if err != nil || got == nil {
		t.Fatalf("Get: %v", err)
	}

	if _, err := store.EvictLRU(ctx, 1); err != nil {
		t.Fatalf("EvictLRU: %v", err)
	}

	if _, err := store.SizeBytes(ctx); err != nil {
		t.Fatalf("SizeBytes: %v", err)
	}

	if err := store.Delete(ctx, "wrapper-key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := store.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}
}
