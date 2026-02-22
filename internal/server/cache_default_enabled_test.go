package server

import (
	"context"
	"testing"
)

// TestCacheEnabledByDefault verifies that HTTP cache is enabled by default
// (when EnableHTTPCache=true in config) without requiring explicit CLI flag.
// This is the TDD test for the cache initialization fix:
// Cache middleware should initialize when app.config.EnableHTTPCache is true,
// NOT when app.opt.EnableHTTPCache (CLI flag) is set.
func TestCacheEnabledByDefault(t *testing.T) {
	// Create app WITHOUT setting app.opt.EnableHTTPCache CLI flag
	// This simulates normal startup where cache should be enabled by default config
	app := CreateApp(t, false)

	// Load config (which has EnableHTTPCache=true by default)
	if err := app.loadConfig(); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Verify config has cache enabled
	if !app.config.EnableHTTPCache {
		t.Fatal("Expected EnableHTTPCache=true in default config")
	}

	// Apply config (this mimics app.applyConfig() from Run())
	app.applyConfig()

	// Initialize cache middleware after config is loaded (this mimics app.initializeHTTPCache() from Run())
	app.initializeHTTPCache()

	// Verify cache middleware is initialized
	if app.cacheMW == nil {
		t.Fatal("Expected cacheMW to be initialized when EnableHTTPCache=true in config")
	}

	// Verify the cache database table is accessible and functional
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get read-only connection: %v", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	// Check that cache table exists and has 0 entries (cache should be empty at start)
	initialCount, err := cpcRo.Queries.CountHttpCacheEntries(context.Background())
	if err != nil {
		t.Fatalf("failed to count cache entries: %v", err)
	}

	if initialCount != 0 {
		t.Errorf("expected 0 cache entries at start, got %d", initialCount)
	}
}
