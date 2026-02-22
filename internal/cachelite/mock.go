package cachelite

import (
	"context"
)

// MockCacheStore is a mock implementation of CacheStore for testing.
type MockCacheStore struct {
	Entries   map[string]*HTTPCacheEntry
	Size      int64
	GetErr    error
	StoreErr  error
	DeleteErr error
	EvictErr  error
	SizeErr   error
	ClearErr  error
}

// NewMockCacheStore creates a new mock cache store.
func NewMockCacheStore() *MockCacheStore {
	return &MockCacheStore{
		Entries: make(map[string]*HTTPCacheEntry),
	}
}

// Get retrieves a cache entry by key.
func (m *MockCacheStore) Get(ctx context.Context, key string) (*HTTPCacheEntry, error) {
	if m.GetErr != nil {
		return nil, m.GetErr
	}
	return m.Entries[key], nil
}

// Store saves a cache entry.
func (m *MockCacheStore) Store(ctx context.Context, entry *HTTPCacheEntry) error {
	if m.StoreErr != nil {
		return m.StoreErr
	}
	m.Entries[entry.Key] = entry
	return nil
}

// Delete removes a cache entry by key.
func (m *MockCacheStore) Delete(ctx context.Context, key string) error {
	if m.DeleteErr != nil {
		return m.DeleteErr
	}
	delete(m.Entries, key)
	return nil
}

// EvictLRU removes entries to free targetBytes (simplified mock implementation).
func (m *MockCacheStore) EvictLRU(ctx context.Context, targetBytes int64) (int64, error) {
	if m.EvictErr != nil {
		return 0, m.EvictErr
	}
	// Simple mock: just clear all entries and return 0 (not accurate but sufficient)
	m.Entries = make(map[string]*HTTPCacheEntry)
	return 0, nil
}

// SizeBytes returns the mock size.
func (m *MockCacheStore) SizeBytes(ctx context.Context) (int64, error) {
	if m.SizeErr != nil {
		return 0, m.SizeErr
	}
	return m.Size, nil
}

// Clear removes all cache entries.
func (m *MockCacheStore) Clear(ctx context.Context) error {
	if m.ClearErr != nil {
		return m.ClearErr
	}
	m.Entries = make(map[string]*HTTPCacheEntry)
	return nil
}

// Ensure MockCacheStore implements CacheStore
var _ CacheStore = (*MockCacheStore)(nil)
