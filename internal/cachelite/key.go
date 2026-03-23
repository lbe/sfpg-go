package cachelite

import (
	"net/http"
	"strings"
)

// CacheKeyParams defines all parameters for cache key generation.
// Used by both middleware (NewCacheKeyForRequest) and preload/batch (NewCacheKeyForPreload).
type CacheKeyParams struct {
	Method   string     // HTTP method (e.g. "GET")
	Path     string     // URL path (e.g. "/gallery/1")
	Query    string     // Raw query string (e.g. "v=1")
	HTMX     HTMXParams // HTMX request/target/variant for partial vs full page
	Theme    string     // Theme cookie value (default "dark")
	Encoding string     // Normalized Accept-Encoding (e.g. "gzip", "identity")
}

// HTMXParams holds HTMX-specific parameters for cache keys.
type HTMXParams struct {
	Request   string // "true" or "false"
	Target    string // e.g., "gallery-content", "box_info"
	IsVariant bool   // true when using variant (not full page)
}

// NewCacheKey generates a consistent cache key from parameters.
// Format: "METHOD:/path?query|HX=X|HXTarget=Y|IsVariant=true|Theme=dark|encoding"
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

	// Add HTMX parameters consistently
	if params.HTMX.Request != "" {
		builder.WriteString("|HX=")
		builder.WriteString(params.HTMX.Request)
	}
	if params.HTMX.Target != "" {
		builder.WriteString("|HXTarget=")
		builder.WriteString(params.HTMX.Target)
	}
	if params.HTMX.IsVariant {
		builder.WriteString("|IsVariant=true")
	}

	// Add theme (default to "dark" if not specified)
	theme := params.Theme
	if theme == "" {
		theme = "dark"
	}
	builder.WriteString("|Theme=")
	builder.WriteString(theme)

	// Add encoding
	builder.WriteByte('|')
	builder.WriteString(params.Encoding)

	return builder.String()
}

// NewCacheKeyForRequest builds CacheKeyParams from an http.Request.
// Used by middleware.
func NewCacheKeyForRequest(r *http.Request, theme string) CacheKeyParams {
	htmx := r.Header.Get("HX-Request")
	if htmx == "" {
		htmx = "false"
	}

	return CacheKeyParams{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
		HTMX: HTMXParams{
			Request:   htmx,
			Target:    r.Header.Get("HX-Target"),
			IsVariant: htmx != "false",
		},
		Theme:    theme,
		Encoding: NormalizeAcceptEncoding(r.Header.Get("Accept-Encoding")),
	}
}

// NewCacheKeyForPreload builds CacheKeyParams for preload.
// Used by preload and batch load.
func NewCacheKeyForPreload(path, query, encoding string, theme string, useVariant bool) CacheKeyParams {
	params := CacheKeyParams{
		Method:   "GET",
		Path:     path,
		Query:    query,
		HTMX:     HTMXParams{},
		Theme:    theme,
		Encoding: encoding,
	}

	if useVariant {
		hxTarget, _ := VariantForPath(path)
		params.HTMX = HTMXParams{
			Request:   "true",
			Target:    hxTarget,
			IsVariant: true,
		}
	}

	return params
}

// VariantForPath returns (hxTarget, defaultEncoding) for preload.
// Copied from cachepreload variant logic for consistency.
func VariantForPath(path string) (hxTarget string, defaultEncoding string) {
	if path == "" {
		return "", ""
	}
	switch {
	case strings.HasPrefix(path, "/info/image/") || strings.HasPrefix(path, "/info/folder/"):
		return "box_info", "gzip"
	case strings.HasPrefix(path, "/lightbox/"):
		return "lightbox_content", "gzip"
	case strings.HasPrefix(path, "/gallery/"):
		return "gallery-content", "gzip"
	default:
		return "", "identity"
	}
}
