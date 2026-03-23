package server

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
)

func testTableExists(ctx context.Context, app *App, tableName string) (bool, error) {
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

func TestIncrementETag_ClearsCache(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Pre-populate cache with an entry
	entry := &cachelite.HTTPCacheEntry{
		Key:           "GET:/gallery/1?|HX=false|HXTarget=|Theme=dark|gzip",
		Method:        "GET",
		Path:          "/gallery/1",
		Encoding:      "gzip",
		Status:        200,
		ETag:          sql.NullString{String: "\"old-etag-123\"", Valid: true},
		Body:          []byte("<html>test</html>"),
		ContentLength: sql.NullInt64{Int64: 19, Valid: true},
		CreatedAt:     time.Now().Unix(),
	}
	err := cachelite.StoreCacheEntry(app.ctx, app.dbRwPool, entry)
	if err != nil {
		t.Fatalf("failed to store cache entry: %v", err)
	}

	// Verify cache has entries before increment
	countBefore, err := cachelite.CountCacheEntries(app.ctx, app.dbRwPool)
	if err != nil {
		t.Fatalf("failed to count cache entries: %v", err)
	}
	if countBefore != 1 {
		t.Errorf("expected 1 cache entry before, got %d", countBefore)
	}

	// Increment ETag
	newETag, err := app.IncrementETag()
	if err != nil {
		t.Fatalf("IncrementETag failed: %v", err)
	}
	if newETag == "" {
		t.Error("expected non-empty ETag after increment")
	}

	// Verify cache is cleared after increment
	countAfter, err := cachelite.CountCacheEntries(app.ctx, app.dbRwPool)
	if err != nil {
		t.Fatalf("failed to count cache entries: %v", err)
	}
	if countAfter != 0 {
		t.Errorf("expected 0 cache entries after increment, got %d", countAfter)
	}

	staleExists, err := testTableExists(app.ctx, app, "http_cache_to_be_dropped")
	if err != nil {
		t.Fatalf("table existence check failed: %v", err)
	}
	if !staleExists {
		t.Fatal("expected stale table to exist after ETag rotation")
	}
}
