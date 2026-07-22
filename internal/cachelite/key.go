package cachelite

import (
	"net/http"
	"strconv"
	"strings"
)

// CacheKeyFormatVersion is the current version of the HTTP cache key format.
// Bump when the caching layer changes key encoding, encoding suffixes, or
// body format such that old rows in http_cache must be invalidated on upgrade.
//
// Version 1: initial format (Encoding field separate, |Theme= and
// |gzip|br|identity suffixes in key strings).
// Version 2: Encoding removed from key; all cache entries are encoding-independent.
// Version 3: |HX=, |HXTarget=, |IsVariant= removed; normalized |Variant= only.
// Info/lightbox full + HTMX share one key; gallery keeps two variants.
const CacheKeyFormatVersion = 3

// CacheKeyFormatVersionString is the string representation of CacheKeyFormatVersion
// for storage in the config KV table.
var CacheKeyFormatVersionString = strconv.Itoa(CacheKeyFormatVersion)

// CacheKeyParams defines all parameters for cache key generation.
// Used by both middleware (NewCacheKeyForRequest) and preload/batch (NewCacheKeyForPreload).
type CacheKeyParams struct {
	Method  string // HTTP method (e.g. "GET")
	Path    string // URL path (e.g. "/gallery/1")
	Query   string // Raw query string (e.g. "v=1")
	Variant string // Variant name: "full", "gallery-content", "box_info", "lightbox-ui"
}

// NormalizedVariant returns the normalized cache variant for a request path
// with optional HTMX context. Info and lightbox paths collapse to one variant
// regardless of HTMX headers; gallery paths distinguish full vs gallery-content.
//
// For /lightbox/ paths, variant is always "lightbox-ui" regardless of HX-Target
// (including the browser value "lightbox_content"). Collapse is by path prefix,
// not by rewriting headers in NewCacheKeyForRequest.
func NormalizedVariant(path, hxRequest, hxTarget string) string {
	switch {
	case strings.HasPrefix(path, "/gallery/"):
		if hxRequest == "true" && hxTarget == "gallery-content" {
			return "gallery-content"
		}
		return "full"
	case strings.HasPrefix(path, "/info/"):
		return "box_info"
	case strings.HasPrefix(path, "/lightbox/"):
		return "lightbox-ui"
	default:
		return "full"
	}
}

// PreloadVariantForPath returns the variant to use when preloading or batch-warming
// a cache entry for the given path.
func PreloadVariantForPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/gallery/"):
		return "gallery-content"
	case strings.HasPrefix(path, "/info/"):
		return "box_info"
	case strings.HasPrefix(path, "/lightbox/"):
		return "lightbox-ui"
	default:
		return "full"
	}
}

// NewCacheKey generates a consistent cache key from parameters.
// Format: "METHOD:/path?query|Variant=<name>"
// All cache entries are encoding-independent — the key never varies by Accept-Encoding.
func NewCacheKey(params CacheKeyParams) string {
	var builder strings.Builder
	builder.WriteString(params.Method)
	builder.WriteByte(':')
	builder.WriteString(params.Path)
	builder.WriteByte('?')

	// Add query with cache-busting version
	if params.Query != "" {
		builder.WriteString(params.Query)
	}

	// Add variant suffix
	if params.Variant != "" {
		builder.WriteString("|Variant=")
		builder.WriteString(params.Variant)
	}

	return builder.String()
}

// NewCacheKeyForRequest builds CacheKeyParams from an http.Request.
// Used by middleware.
// The variant is determined from the request path, HX-Request, and HX-Target
// headers via NormalizedVariant.
func NewCacheKeyForRequest(r *http.Request) CacheKeyParams {
	htmx := r.Header.Get("HX-Request")
	if htmx == "" {
		htmx = "false"
	}

	target := r.Header.Get("HX-Target")
	variant := NormalizedVariant(r.URL.Path, htmx, target)

	return CacheKeyParams{
		Method:  r.Method,
		Path:    r.URL.Path,
		Query:   r.URL.RawQuery,
		Variant: variant,
	}
}

// NewCacheKeyForPreload builds CacheKeyParams for preload or batch load.
// Used by preload and batch load.
func NewCacheKeyForPreload(path, query, variant string) CacheKeyParams {
	return CacheKeyParams{
		Method:  "GET",
		Path:    path,
		Query:   query,
		Variant: variant,
	}
}
