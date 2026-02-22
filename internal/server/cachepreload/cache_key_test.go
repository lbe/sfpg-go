package cachepreload

import (
	"testing"

	"go.local/sfpg/internal/cachelite"
)

func TestGenerateCacheKey_MatchesMiddlewarePattern(t *testing.T) {
	// Middleware uses: NewCacheKey(method, path, query+"|HX="+htmx+"|HXTarget="+hxTarget, encoding)
	// For full page: htmx="false", hxTarget=""
	key := GenerateCacheKey("GET", "/gallery/23", "v=20260201-01", "identity")
	// Expected: GET:/gallery/23?v=20260201-01|HX=false|HXTarget=|identity
	if key != "GET:/gallery/23?v=20260201-01|HX=false|HXTarget=|identity" {
		t.Errorf("GenerateCacheKey = %q, want GET:/gallery/23?v=20260201-01|HX=false|HXTarget=|identity", key)
	}
}

func TestGenerateCacheKey_EmptyEncodingDefaultsToIdentity(t *testing.T) {
	key := GenerateCacheKey("GET", "/gallery/1", "v=x", "")
	if key != "GET:/gallery/1?v=x|HX=false|HXTarget=|identity" {
		t.Errorf("GenerateCacheKey with empty encoding = %q, want identity", key)
	}
}

func TestGenerateCacheKey_WithQueryString(t *testing.T) {
	key := GenerateCacheKey("GET", "/info/folder/5", "v=20260201-02&foo=bar", "identity")
	expected := "GET:/info/folder/5?v=20260201-02&foo=bar|HX=false|HXTarget=|identity"
	if key != expected {
		t.Errorf("GenerateCacheKey = %q, want %q", key, expected)
	}
}

func TestGenerateCacheKey_EmptyQuery(t *testing.T) {
	key := GenerateCacheKey("GET", "/gallery/1", "", "identity")
	if key != "GET:/gallery/1?|HX=false|HXTarget=|identity" {
		t.Errorf("GenerateCacheKey with empty query = %q", key)
	}
}

// TestGenerateCacheKeyWithHX_ForInfoImage_MatchesBrowserRequest verifies that keys for
// HTMX info-box requests (HX-Request: true, HX-Target: box_info, Accept-Encoding: gzip)
// match what the middleware builds, so preloaded entries are found by real browser requests.
func TestGenerateCacheKeyWithHX_ForInfoImage_MatchesBrowserRequest(t *testing.T) {
	key := GenerateCacheKeyWithHX("GET", "/info/image/12", "v=20260202-01", "true", "box_info", "gzip")
	expected := cachelite.NewCacheKey("GET", "/info/image/12", "v=20260202-01|HX=true|HXTarget=box_info", "gzip")
	if key != expected {
		t.Errorf("GenerateCacheKeyWithHX = %q, want %q", key, expected)
	}
}

// TestGenerateCacheKeyWithHX_ForLightbox_MatchesBrowserRequest verifies keys for
// lightbox requests (HX-Target: lightbox_content).
func TestGenerateCacheKeyWithHX_ForLightbox_MatchesBrowserRequest(t *testing.T) {
	key := GenerateCacheKeyWithHX("GET", "/lightbox/15", "v=20260202-01", "true", "lightbox_content", "gzip")
	expected := cachelite.NewCacheKey("GET", "/lightbox/15", "v=20260202-01|HX=true|HXTarget=lightbox_content", "gzip")
	if key != expected {
		t.Errorf("GenerateCacheKeyWithHX = %q, want %q", key, expected)
	}
}

// TestGenerateCacheKeyWithHX_ForInfoFolder_MatchesBrowserRequest verifies keys for
// info folder (box_info).
func TestGenerateCacheKeyWithHX_ForInfoFolder_MatchesBrowserRequest(t *testing.T) {
	key := GenerateCacheKeyWithHX("GET", "/info/folder/10", "v=20260202-01", "true", "box_info", "gzip")
	expected := cachelite.NewCacheKey("GET", "/info/folder/10", "v=20260202-01|HX=true|HXTarget=box_info", "gzip")
	if key != expected {
		t.Errorf("GenerateCacheKeyWithHX = %q, want %q", key, expected)
	}
}
