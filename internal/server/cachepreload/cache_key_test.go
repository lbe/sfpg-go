package cachepreload

import (
	"net/http"
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite"
)

// TestGenerateCacheKey_MatchesMiddlewarePattern verifies that preload keys match middleware keys.
func TestGenerateCacheKey_MatchesMiddlewarePattern(t *testing.T) {
	params := cachelite.NewCacheKeyForPreload("/gallery/23", "v=20260201-01", "full")
	key := cachelite.NewCacheKey(params)
	if key != "GET:/gallery/23?v=20260201-01|Variant=full" {
		t.Errorf("GenerateCacheKey = %q, want GET:/gallery/23?v=20260201-01|Variant=full", key)
	}

	req, err := http.NewRequest("GET", "/gallery/23?v=20260201-01", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	middlewareKey := cachelite.NewCacheKey(cachelite.NewCacheKeyForRequest(req))
	if key != middlewareKey {
		t.Errorf("preload key %q != middleware key %q", key, middlewareKey)
	}
}

func TestGenerateCacheKey_EmptyEncodingDefaultsToIdentity(t *testing.T) {
	params := cachelite.NewCacheKeyForPreload("/gallery/1", "v=x", "full")
	key := cachelite.NewCacheKey(params)
	if key != "GET:/gallery/1?v=x|Variant=full" {
		t.Errorf("GenerateCacheKey with empty encoding = %q, want GET:/gallery/1?v=x|Variant=full", key)
	}
}

func TestGenerateCacheKey_WithQueryString(t *testing.T) {
	params := cachelite.NewCacheKeyForPreload("/info/folder/5", "v=20260201-02&foo=bar", "box_info")
	key := cachelite.NewCacheKey(params)
	expected := "GET:/info/folder/5?v=20260201-02|Variant=box_info"
	if key != expected {
		t.Errorf("GenerateCacheKey = %q, want %q", key, expected)
	}

	req, err := http.NewRequest("GET", "/info/folder/5?v=20260201-02&foo=bar", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	middlewareKey := cachelite.NewCacheKey(cachelite.NewCacheKeyForRequest(req))
	if key != middlewareKey {
		t.Errorf("preload key %q != middleware key %q", key, middlewareKey)
	}
}

func TestGenerateCacheKey_EmptyQuery(t *testing.T) {
	params := cachelite.NewCacheKeyForPreload("/gallery/1", "", "full")
	key := cachelite.NewCacheKey(params)
	if key != "GET:/gallery/1?|Variant=full" {
		t.Errorf("GenerateCacheKey with empty query = %q, want GET:/gallery/1?|Variant=full", key)
	}
}

// TestGenerateCacheKeyWithHX_ForInfoImage_MatchesBrowserRequest verifies that keys for
// HTMX info-box requests match what the middleware builds, so preloaded entries are
// found by real browser requests. Info images collapse to box_info variant.
func TestGenerateCacheKeyWithHX_ForInfoImage_MatchesBrowserRequest(t *testing.T) {
	params := cachelite.NewCacheKeyForPreload("/info/image/12", "v=20260202-01", "box_info")
	key := cachelite.NewCacheKey(params)
	expected := "GET:/info/image/12?v=20260202-01|Variant=box_info"
	if key != expected {
		t.Errorf("GenerateCacheKeyWithHX = %q, want %q", key, expected)
	}
}

// TestGenerateCacheKeyWithHX_ForLightbox_CanonicalTarget verifies that a preload key
// built with variant lightbox-ui matches what the middleware produces via
// NormalizedVariant for /lightbox/ paths (hxTarget ignored).
func TestGenerateCacheKeyWithHX_ForLightbox_CanonicalTarget(t *testing.T) {
	params := cachelite.NewCacheKeyForPreload("/lightbox/15", "v=20260202-01", "lightbox-ui")
	key := cachelite.NewCacheKey(params)
	expected := "GET:/lightbox/15?v=20260202-01|Variant=lightbox-ui"
	if key != expected {
		t.Errorf("Canonical lightbox cache key = %q, want %q", key, expected)
	}
}

// TestGenerateCacheKeyWithHX_ForInfoFolder_MatchesBrowserRequest verifies keys for
// info folder (box_info).
func TestGenerateCacheKeyWithHX_ForInfoFolder_MatchesBrowserRequest(t *testing.T) {
	params := cachelite.NewCacheKeyForPreload("/info/folder/10", "v=20260202-01", "box_info")
	key := cachelite.NewCacheKey(params)
	expected := "GET:/info/folder/10?v=20260202-01|Variant=box_info"
	if key != expected {
		t.Errorf("GenerateCacheKeyWithHX = %q, want %q", key, expected)
	}
}
