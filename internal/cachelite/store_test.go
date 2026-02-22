package cachelite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// TestMockCacheStore demonstrates fast testing without database setup.
// This test runs in microseconds vs hundreds of milliseconds for real DB tests.
func TestMockCacheStore(t *testing.T) {
	ctx := context.Background()
	store := NewMockCacheStore()

	// Test Store
	entry := &HTTPCacheEntry{
		Key:           "test-key",
		Method:        "GET",
		Path:          "/test",
		Body:          []byte("test body"),
		ContentType:   sql.NullString{String: "text/plain", Valid: true},
		ContentLength: sql.NullInt64{Int64: 9, Valid: true},
		Status:        200,
		CreatedAt:     time.Now().Unix(),
		ExpiresAt:     sql.NullInt64{Int64: time.Now().Add(time.Hour).Unix(), Valid: true},
	}

	if err := store.Store(ctx, entry); err != nil {
		t.Fatalf("store failed: %v", err)
	}

	// Test Get
	got, err := store.Get(ctx, "test-key")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected entry, got nil")
	}
	if string(got.Body) != "test body" {
		t.Errorf("expected body %q, got %q", "test body", string(got.Body))
	}

	// Test Delete
	if err := store.Delete(ctx, "test-key"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	got, _ = store.Get(ctx, "test-key")
	if got != nil {
		t.Error("expected nil after delete")
	}

	// Test Clear
	store.Store(ctx, entry)
	if err := store.Clear(ctx); err != nil {
		t.Fatalf("clear failed: %v", err)
	}
	if len(store.Entries) != 0 {
		t.Error("expected empty entries after clear")
	}
}

// TestMockCacheStore_Errors tests error handling without database.
func TestMockCacheStore_Errors(t *testing.T) {
	ctx := context.Background()
	store := NewMockCacheStore()

	store.GetErr = errors.New("get error")
	_, err := store.Get(ctx, "key")
	if err == nil {
		t.Error("expected error")
	}

	store.GetErr = nil
	store.StoreErr = errors.New("store error")
	err = store.Store(ctx, &HTTPCacheEntry{Key: "key"})
	if err == nil {
		t.Error("expected error")
	}
}

// TestMockCacheStore_SizeBytes tests the SizeBytes method.
func TestMockCacheStore_SizeBytes(t *testing.T) {
	ctx := context.Background()
	store := NewMockCacheStore()
	store.Size = 1024

	size, err := store.SizeBytes(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if size != 1024 {
		t.Errorf("expected size 1024, got %d", size)
	}

	// Test error
	store.SizeErr = errors.New("size error")
	_, err = store.SizeBytes(ctx)
	if err == nil {
		t.Error("expected error")
	}
}

// BenchmarkMockCacheStore vs real SQLite store shows performance difference.
func BenchmarkMockCacheStore(b *testing.B) {
	ctx := context.Background()
	store := NewMockCacheStore()
	entry := &HTTPCacheEntry{
		Key:       "bench-key",
		Body:      []byte("benchmark data"),
		Status:    200,
		CreatedAt: time.Now().Unix(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entry.Key = string(rune(i))
		store.Store(ctx, entry)
		store.Get(ctx, entry.Key)
	}
}
