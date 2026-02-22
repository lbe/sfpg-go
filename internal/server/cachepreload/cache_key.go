package cachepreload

import "github.com/lbe/sfpg-go/internal/cachelite"

// GenerateCacheKey creates a cache key matching the HTTP cache middleware pattern.
// This ensures preloaded entries are found by real requests.
//
// The key format matches http_cache_middleware.go:
//
//	METHOD:/path?query|HX=false|HXTarget=|encoding
//
// Parameters:
//   - method: HTTP method (GET)
//   - path: URL path (e.g., /gallery/23)
//   - query: Full query string (e.g., v=20260201-01)
//   - encoding: Accept-Encoding value (default: "identity")
func GenerateCacheKey(method, path, query, encoding string) string {
	return GenerateCacheKeyWithHX(method, path, query, "false", "", encoding)
}

// GenerateCacheKeyWithHX creates a cache key matching the HTTP cache middleware pattern
// for a specific HX-Request / HX-Target / Accept-Encoding variant. Use this for preloading
// so stored entries match real browser requests (e.g. HX=true, HXTarget=box_info, gzip).
func GenerateCacheKeyWithHX(method, path, query, htmx, hxTarget, encoding string) string {
	if encoding == "" {
		encoding = "identity"
	}
	if htmx == "" {
		htmx = "false"
	}
	suffix := query + "|HX=" + htmx + "|HXTarget=" + hxTarget
	return cachelite.NewCacheKey(method, path, suffix, encoding)
}
