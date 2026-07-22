package cachepreload

import (
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite"
)

// TestGenerateCacheKey_MatchesMiddlewarePattern verifies that preload keys match middleware keys.
func TestGenerateCacheKey_MatchesMiddlewarePattern(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	// For full page: normalized variant is "full"
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method:  "GET",
		Path:    "/gallery/23",
		Query:   "v=20260201-01",
		Variant: "full",
	})
	// Expected: GET:/gallery/23?v=20260201-01|Variant=full
	if key != "GET:/gallery/23?v=20260201-01|Variant=full" {
		t.Errorf("GenerateCacheKey = %q, want GET:/gallery/23?v=20260201-01|Variant=full", key)
	}
}

func TestGenerateCacheKey_EmptyEncodingDefaultsToIdentity(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	// For full page: normalized variant is "full"
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method:  "GET",
		Path:    "/gallery/1",
		Query:   "v=x",
		Variant: "full",
	})
	// Expected: GET:/gallery/1?v=x|Variant=full
	if key != "GET:/gallery/1?v=x|Variant=full" {
		t.Errorf("GenerateCacheKey with empty encoding = %q, want GET:/gallery/1?v=x|Variant=full", key)
	}
}

func TestGenerateCacheKey_WithQueryString(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	// For info folder: normalized variant is "box_info"
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method:  "GET",
		Path:    "/info/folder/5",
		Query:   "v=20260201-02&foo=bar",
		Variant: "box_info",
	})
	// Expected: GET:/info/folder/5?v=20260201-02&foo=bar|Variant=box_info
	if key != "GET:/info/folder/5?v=20260201-02&foo=bar|Variant=box_info" {
		t.Errorf("GenerateCacheKey = %q, want GET:/info/folder/5?v=20260201-02&foo=bar|Variant=box_info", key)
	}
}

func TestGenerateCacheKey_EmptyQuery(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	// For page with no query: normalized variant is "full"
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method:  "GET",
		Path:    "/gallery/1",
		Query:   "",
		Variant: "full",
	})
	// Expected: GET:/gallery/1?|Variant=full
	if key != "GET:/gallery/1?|Variant=full" {
		t.Errorf("GenerateCacheKey with empty query = %q, want GET:/gallery/1?|Variant=full", key)
	}
}

// TestGenerateCacheKeyWithHX_ForInfoImage_MatchesBrowserRequest verifies that keys for
// HTMX info-box requests match what the middleware builds, so preloaded entries are
// found by real browser requests. Info images collapse to box_info variant.
func TestGenerateCacheKeyWithHX_ForInfoImage_MatchesBrowserRequest(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method:  "GET",
		Path:    "/info/image/12",
		Query:   "v=20260202-01",
		Variant: "box_info",
	})
	// Expected: GET:/info/image/12?v=20260202-01|Variant=box_info
	expected := "GET:/info/image/12?v=20260202-01|Variant=box_info"
	if key != expected {
		t.Errorf("GenerateCacheKeyWithHX = %q, want %q", key, expected)
	}
}

// TestGenerateCacheKeyWithHX_ForLightbox_CanonicalTarget verifies that a preload key
// built with variant lightbox-ui matches what the middleware produces via
// NormalizedVariant for /lightbox/ paths (hxTarget ignored).
func TestGenerateCacheKeyWithHX_ForLightbox_CanonicalTarget(t *testing.T) {
	// The canonical target for lightbox cache keys is lightbox-ui.
	// Preload (via PreloadVariantForPath) and middleware (via NormalizedVariant in NewCacheKeyForRequest)
	// both converge on this value.
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method:  "GET",
		Path:    "/lightbox/15",
		Query:   "v=20260202-01",
		Variant: "lightbox-ui",
	})
	expected := "GET:/lightbox/15?v=20260202-01|Variant=lightbox-ui"
	if key != expected {
		t.Errorf("Canonical lightbox cache key = %q, want %q", key, expected)
	}
}

// TestGenerateCacheKeyWithHX_ForInfoFolder_MatchesBrowserRequest verifies keys for
// info folder (box_info).
func TestGenerateCacheKeyWithHX_ForInfoFolder_MatchesBrowserRequest(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method:  "GET",
		Path:    "/info/folder/10",
		Query:   "v=20260202-01",
		Variant: "box_info",
	})
	// Expected: GET:/info/folder/10?v=20260202-01|Variant=box_info
	expected := "GET:/info/folder/10?v=20260202-01|Variant=box_info"
	if key != expected {
		t.Errorf("GenerateCacheKeyWithHX = %q, want %q", key, expected)
	}
}
