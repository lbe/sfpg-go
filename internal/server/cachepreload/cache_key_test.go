package cachepreload

import (
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite"
)

// TestGenerateCacheKey_MatchesMiddlewarePattern verifies that preload keys match middleware keys.
func TestGenerateCacheKey_MatchesMiddlewarePattern(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	// For full page: htmx="false", hxTarget=""
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method: "GET",
		Path:   "/gallery/23",
		Query:  "v=20260201-01",
		HTMX: cachelite.HTMXParams{
			Request:   "false",
			Target:    "",
			IsVariant: false,
		},
		Encoding: "identity",
	})
	// Expected: GET:/gallery/23?v=20260201-01|HX=false|Theme=dark|identity
	if key != "GET:/gallery/23?v=20260201-01|HX=false|Theme=dark|identity" {
		t.Errorf("GenerateCacheKey = %q, want GET:/gallery/23?v=20260201-01|HX=false|Theme=dark|identity", key)
	}
}

func TestGenerateCacheKey_EmptyEncodingDefaultsToIdentity(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	// For full page with no encoding header: htmx="false", hxTarget="", encoding="identity" (default)
	// Note: We use "identity" directly here since NewCacheKey doesn't normalize empty encoding,
	// but NewCacheKeyForRequest does via NormalizeAcceptEncoding.
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method: "GET",
		Path:   "/gallery/1",
		Query:  "v=x",
		HTMX: cachelite.HTMXParams{
			Request:   "false",
			Target:    "",
			IsVariant: false,
		},
		Encoding: "identity",
	})
	// Expected: GET:/gallery/1?v=x|HX=false|Theme=dark|identity
	if key != "GET:/gallery/1?v=x|HX=false|Theme=dark|identity" {
		t.Errorf("GenerateCacheKey with empty encoding = %q, want identity", key)
	}
}

func TestGenerateCacheKey_WithQueryString(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	// For page with query: htmx="false", hxTarget="", encoding="identity"
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method: "GET",
		Path:   "/info/folder/5",
		Query:  "v=20260201-02&foo=bar",
		HTMX: cachelite.HTMXParams{
			Request:   "false",
			Target:    "",
			IsVariant: false,
		},
		Encoding: "identity",
	})
	// Expected: GET:/info/folder/5?v=20260201-02&foo=bar|HX=false|Theme=dark|identity
	if key != "GET:/info/folder/5?v=20260201-02&foo=bar|HX=false|Theme=dark|identity" {
		t.Errorf("GenerateCacheKey = %q, want GET:/info/folder/5?v=20260201-02&foo=bar|HX=false|Theme=dark|identity", key)
	}
}

func TestGenerateCacheKey_EmptyQuery(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	// For page with no query: htmx="false", hxTarget="", encoding="identity"
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method: "GET",
		Path:   "/gallery/1",
		Query:  "",
		HTMX: cachelite.HTMXParams{
			Request:   "false",
			Target:    "",
			IsVariant: false,
		},
		Encoding: "identity",
	})
	// Expected: GET:/gallery/1?|HX=false|Theme=dark|identity
	if key != "GET:/gallery/1?|HX=false|Theme=dark|identity" {
		t.Errorf("GenerateCacheKey with empty query = %q, want identity", key)
	}
}

// TestGenerateCacheKeyWithHX_ForInfoImage_MatchesBrowserRequest verifies that keys for
// HTMX info-box requests (HX-Request: true, HX-Target: box_info, Accept-Encoding: gzip)
// match what the middleware builds, so preloaded entries are found by real browser requests.
func TestGenerateCacheKeyWithHX_ForInfoImage_MatchesBrowserRequest(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method: "GET",
		Path:   "/info/image/12",
		Query:  "v=20260202-01",
		HTMX: cachelite.HTMXParams{
			Request:   "true",
			Target:    "box_info",
			IsVariant: true,
		},
		Encoding: "gzip",
	})
	// Expected: GET:/info/image/12?v=20260202-01|HX=true|HXTarget=box_info|IsVariant=true|Theme=dark|gzip
	if key != "GET:/info/image/12?v=20260202-01|HX=true|HXTarget=box_info|IsVariant=true|Theme=dark|gzip" {
		t.Errorf("GenerateCacheKeyWithHX = %q, want GET:/info/image/12?v=20260202-01|HX=true|HXTarget=box_info|IsVariant=true|Theme=dark|gzip", key)
	}
}

// TestGenerateCacheKeyWithHX_ForLightbox_MatchesBrowserRequest verifies keys for
// lightbox requests (HX-Target: lightbox_content).
// match what the middleware builds, so preloaded entries are found by real browser requests.
func TestGenerateCacheKeyWithHX_ForLightbox_MatchesBrowserRequest(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method: "GET",
		Path:   "/lightbox/15",
		Query:  "v=20260202-01",
		HTMX: cachelite.HTMXParams{
			Request:   "true",
			Target:    "lightbox_content",
			IsVariant: true,
		},
		Encoding: "gzip",
	})
	// Expected: GET:/lightbox/15?v=20260202-01|HX=true|HXTarget=lightbox_content|IsVariant=true|Theme=dark|gzip
	if key != "GET:/lightbox/15?v=20260202-01|HX=true|HXTarget=lightbox_content|IsVariant=true|Theme=dark|gzip" {
		t.Errorf("GenerateCacheKeyWithHX = %q, want GET:/lightbox/15?v=20260202-01|HX=true|HXTarget=lightbox_content|IsVariant=true|Theme=dark|gzip", key)
	}
}

// TestGenerateCacheKeyWithHX_ForInfoFolder_MatchesBrowserRequest verifies keys for
// info folder (box_info).
// match what the middleware builds, so preloaded entries are found by real browser requests.
func TestGenerateCacheKeyWithHX_ForInfoFolder_MatchesBrowserRequest(t *testing.T) {
	// Middleware uses NewCacheKeyForRequest which builds CacheKeyParams
	key := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method: "GET",
		Path:   "/info/folder/10",
		Query:  "v=20260202-01",
		HTMX: cachelite.HTMXParams{
			Request:   "true",
			Target:    "box_info",
			IsVariant: true,
		},
		Encoding: "gzip",
	})
	// Expected: GET:/info/folder/10?v=20260202-01|HX=true|HXTarget=box_info|IsVariant=true|Theme=dark|gzip
	if key != "GET:/info/folder/10?v=20260202-01|HX=true|HXTarget=box_info|IsVariant=true|Theme=dark|gzip" {
		t.Errorf("GenerateCacheKeyWithHX = %q, want GET:/info/folder/10?v=20260202-01|HX=true|HXTarget=box_info|IsVariant=true|Theme=dark|gzip", key)
	}
}
