//go:build integration

package cachelite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

func TestRotateCacheTable(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()
	now := time.Now().Unix()

	for i := 0; i < 3; i++ {
		path := fmt.Sprintf("/rotate/%d", i)
		key := NewCacheKey(CacheKeyParams{Method: "GET", Path: path})
		entry := &HTTPCacheEntry{
			Key:       key,
			Method:    "GET",
			Path:      path,
			Status:    200,
			Body:      []byte(fmt.Sprintf("rotating-%d", i)),
			CreatedAt: now,
		}
		if err := StoreCacheEntry(ctx, db, entry); err != nil {
			t.Fatalf("StoreCacheEntry: %v", err)
		}
	}

	if err := RotateCacheTable(ctx, db); err != nil {
		t.Fatalf("RotateCacheTable: %v", err)
	}

	count, err := CountCacheEntries(ctx, db)
	if err != nil {
		t.Fatalf("CountCacheEntries after rotation: %v", err)
	}
	if count != 0 {
		t.Fatalf("CountCacheEntries after rotation = %d, want 0", count)
	}

	assertTableGone(t, db, "http_cache_to_be_dropped")

	// After rotate, active http_cache must carry explicit indexes covering all columns.
	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer db.Put(cpc)
	activeHasHTTPCacheIndexes(t, ctx, cpc.Conn)
}

func assertTableGone(t *testing.T, db *dbconnpool.DbSQLConnPool, name string) {
	t.Helper()
	cpc, err := db.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer db.Put(cpc)
	var n int64
	if err := cpc.Conn.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, name).Scan(&n); err != nil {
		t.Fatalf("exists %s: %v", name, err)
	}
	if n != 0 {
		t.Fatalf("expected %s gone after Swap, still present", name)
	}
}

func activeHasHTTPCacheIndexes(t *testing.T, ctx context.Context, conn *sql.Conn) {
	t.Helper()
	rows, err := conn.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='http_cache' AND name NOT LIKE 'sqlite_autoindex_%'`)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("indexes err: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("expected at least one explicit index on http_cache")
	}
	found := map[string]bool{
		"key": false, "path": false, "created_at": false, "expires_at": false, "content_length": false,
	}
	for _, name := range names {
		info, err := conn.QueryContext(ctx, "PRAGMA index_info("+name+")")
		if err != nil {
			t.Fatalf("index_info %s: %v", name, err)
		}
		for info.Next() {
			var seqno, cid int
			var col string
			if err := info.Scan(&seqno, &cid, &col); err != nil {
				info.Close()
				t.Fatalf("scan index_info: %v", err)
			}
			if _, ok := found[col]; ok {
				found[col] = true
			}
		}
		info.Close()
	}
	var missing []string
	for col, ok := range found {
		if !ok {
			missing = append(missing, col)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("http_cache missing explicit index covering %v; indexes=%v", missing, names)
	}
}

func TestCountCacheEntries(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()
	now := time.Now().Unix()

	count, err := CountCacheEntries(ctx, db)
	if err != nil {
		t.Fatalf("CountCacheEntries empty: %v", err)
	}
	if count != 0 {
		t.Fatalf("empty CountCacheEntries = %d, want 0", count)
	}

	for i := 0; i < 3; i++ {
		path := fmt.Sprintf("/count/%d", i)
		key := NewCacheKey(CacheKeyParams{Method: "GET", Path: path})
		entry := &HTTPCacheEntry{
			Key:       key,
			Method:    "GET",
			Path:      path,
			Status:    200,
			Body:      []byte("counting"),
			CreatedAt: now,
		}
		if err := StoreCacheEntry(ctx, db, entry); err != nil {
			t.Fatalf("StoreCacheEntry: %v", err)
		}
	}

	count, err = CountCacheEntries(ctx, db)
	if err != nil {
		t.Fatalf("CountCacheEntries populated: %v", err)
	}
	if count != 3 {
		t.Fatalf("CountCacheEntries = %d, want 3", count)
	}

	if err := ClearCache(ctx, db); err != nil {
		t.Fatalf("ClearCache: %v", err)
	}

	count, err = CountCacheEntries(ctx, db)
	if err != nil {
		t.Fatalf("CountCacheEntries after clear: %v", err)
	}
	if count != 0 {
		t.Fatalf("CountCacheEntries after clear = %d, want 0", count)
	}
}

func TestRotateCacheTable_ClosedPoolReturnsError(t *testing.T) {
	db := createTestDBPoolInternal(t)
	db.Close()

	if err := RotateCacheTable(context.Background(), db); err == nil {
		t.Fatal("expected RotateCacheTable to return error with closed pool")
	}
}

func TestRotateCacheTable_CanceledContextReturnsError(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := RotateCacheTable(ctx, db); err == nil {
		t.Fatal("expected RotateCacheTable to return error with canceled context")
	}
}

func TestRotateCacheTable_ConcurrentTransactionCausesError(t *testing.T) {
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
	defer tx.Rollback()

	// Hold a write lock by inserting in the open transaction, then try to rotate.
	if _, err := tx.ExecContext(ctx, `INSERT INTO http_cache (key, method, path, status, body, created_at) VALUES (?, ?, ?, ?, ?, ?)`, "block-key", "GET", "/block", 200, []byte("block"), time.Now().Unix()); err != nil {
		t.Fatalf("insert in holding tx: %v", err)
	}

	if err := RotateCacheTable(ctx, db); err == nil {
		t.Fatal("expected RotateCacheTable to return error while another transaction holds the lock")
	}
}
