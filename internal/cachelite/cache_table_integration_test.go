//go:build integration

package cachelite

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRotateCacheTable(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()
	now := time.Now().Unix()

	for i := 0; i < 3; i++ {
		path := fmt.Sprintf("/rotate/%d", i)
		key := NewCacheKey(CacheKeyParams{Method: "GET", Path: path, Theme: "dark", Encoding: "identity"})
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

	dropped, err := DropStaleCacheTableIfExists(ctx, db)
	if err != nil {
		t.Fatalf("DropStaleCacheTableIfExists first call: %v", err)
	}
	if !dropped {
		t.Fatal("expected DropStaleCacheTableIfExists to return true on first call")
	}

	dropped, err = DropStaleCacheTableIfExists(ctx, db)
	if err != nil {
		t.Fatalf("DropStaleCacheTableIfExists second call: %v", err)
	}
	if dropped {
		t.Fatal("expected DropStaleCacheTableIfExists to return false on second call")
	}
}

func TestDropStaleCacheTableIfExists_NoStale(t *testing.T) {
	db := createTestDBPoolInternal(t)
	ctx := context.Background()

	dropped, err := DropStaleCacheTableIfExists(ctx, db)
	if err != nil {
		t.Fatalf("DropStaleCacheTableIfExists: %v", err)
	}
	if dropped {
		t.Fatal("expected DropStaleCacheTableIfExists to return false when no stale table exists")
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
		key := NewCacheKey(CacheKeyParams{Method: "GET", Path: path, Theme: "dark", Encoding: "identity"})
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO http_cache (key, method, path, status, encoding, body, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, "block-key", "GET", "/block", 200, "identity", []byte("block"), time.Now().Unix()); err != nil {
		t.Fatalf("insert in holding tx: %v", err)
	}

	if err := RotateCacheTable(ctx, db); err == nil {
		t.Fatal("expected RotateCacheTable to return error while another transaction holds the lock")
	}
}

func TestDropStaleCacheTableIfExists_ClosedPoolReturnsError(t *testing.T) {
	db := createTestDBPoolInternal(t)
	db.Close()

	if _, err := DropStaleCacheTableIfExists(context.Background(), db); err == nil {
		t.Fatal("expected DropStaleCacheTableIfExists to return error with closed pool")
	}
}
