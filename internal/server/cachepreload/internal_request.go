package cachepreload

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/lbe/sfpg-go/internal/gensyncpool"
)

// InternalPreloadHeader is set on internal preload requests so handlers can skip
// scheduling another preload (avoiding cascading preloads).
const InternalPreloadHeader = "X-SFPG-Internal-Preload"

// DiscardingResponseWriter is a ResponseWriter that discards the body immediately
// to avoid buffering large responses during cache warming.
type DiscardingResponseWriter struct {
	statusCode int
	header     http.Header
}

// NewDiscardingResponseWriter creates a new DiscardingResponseWriter.
func NewDiscardingResponseWriter() *DiscardingResponseWriter {
	return &DiscardingResponseWriter{
		statusCode: http.StatusOK, // Default 200
		header:     make(http.Header),
	}
}

func (w *DiscardingResponseWriter) Header() http.Header {
	return w.header
}

func (w *DiscardingResponseWriter) WriteHeader(code int) {
	w.statusCode = code
}

func (w *DiscardingResponseWriter) Write(b []byte) (int, error) {
	// Discard body immediately - no buffering
	return len(b), nil
}

func (w *DiscardingResponseWriter) StatusCode() int {
	return w.statusCode
}

// requestPool reuses http.Request instances to reduce allocations during cache preload.
var requestPool = gensyncpool.New(
	func() *http.Request {
		return &http.Request{
			Method: http.MethodGet,
			Header: make(http.Header),
		}
	},
	func(req *http.Request) {
		// Reset all fields to zero values for reuse
		req.URL = nil
		req.Proto = ""
		req.ProtoMajor = 0
		req.ProtoMinor = 0
		req.Header = make(http.Header)
		req.Body = nil
		req.ContentLength = 0
		req.TransferEncoding = nil
		req.Close = false
		req.Host = ""
		req.Form = nil
		req.PostForm = nil
		req.MultipartForm = nil
		req.Trailer = nil
		req.RemoteAddr = ""
		req.RequestURI = ""
		req.TLS = nil
		req.Response = nil
	},
)

// responseWriterPool reuses DiscardingResponseWriter instances.
var responseWriterPool = gensyncpool.New(
	func() *DiscardingResponseWriter {
		return &DiscardingResponseWriter{
			statusCode: http.StatusOK,
			header:     make(http.Header),
		}
	},
	func(w *DiscardingResponseWriter) {
		w.statusCode = http.StatusOK
		// Clear all header keys
		for k := range w.header {
			delete(w.header, k)
		}
	},
)

// InternalRequestConfig holds dependencies for making internal HTTP requests.
type InternalRequestConfig struct {
	// Handler is the HTTP handler wrapped with full middleware chain
	// (cache middleware, compression middleware, etc.)
	Handler http.Handler

	// ETagVersion for cache key query string
	ETagVersion string
}

// MakeInternalRequest makes an internal HTTP request to warm the cache.
// The request goes through the full middleware chain to ensure proper cache entries.
//
// IMPORTANT: Does NOT set HX-Request header - this ensures full-page cache entries
// are created (partials have Cache-Control: no-store and won't be cached).
func MakeInternalRequest(ctx context.Context, cfg InternalRequestConfig, path string) error {
	if cfg.Handler == nil {
		return fmt.Errorf("internal request config: handler is required")
	}

	// Get request from pool (zero allocations after warmup)
	req := requestPool.Get()
	defer requestPool.Put(req)

	// Build URL
	u, err := url.Parse(path + "?v=" + cfg.ETagVersion)
	if err != nil {
		return fmt.Errorf("failed to parse URL: %w", err)
	}
	req.URL = u

	// Update context (may allocate depending on context chain)
	req = req.WithContext(ctx)

	// Set headers (reusing existing header map, no allocations)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set(InternalPreloadHeader, "true")

	// Call handler directly with discarding ResponseWriter
	rw := responseWriterPool.Get()
	defer responseWriterPool.Put(rw)
	cfg.Handler.ServeHTTP(rw, req)

	// Check for success (cache middleware will store on 2xx)
	if rw.StatusCode() >= 400 {
		return fmt.Errorf("internal request failed: %d", rw.StatusCode())
	}

	return nil
}

// MakeInternalRequestWithVariant makes an internal HTTP request with optional HX variant
// and Accept-Encoding so the stored cache key matches real browser requests.
// When hxTarget is non-empty, sets HX-Request: true and HX-Target: hxTarget; when empty,
// does not set HX headers (full-page style). encoding is always set on Accept-Encoding.
func MakeInternalRequestWithVariant(ctx context.Context, cfg InternalRequestConfig, path string, hxTarget string, encoding string) error {
	if cfg.Handler == nil {
		return fmt.Errorf("internal request config: handler is required")
	}

	// Get request from pool (zero allocations after warmup)
	req := requestPool.Get()
	defer requestPool.Put(req)

	// Build URL
	u, err := url.Parse(path + "?v=" + cfg.ETagVersion)
	if err != nil {
		return fmt.Errorf("failed to parse URL: %w", err)
	}
	req.URL = u

	// Update context (may allocate depending on context chain)
	req = req.WithContext(ctx)

	// Set encoding (default to identity if not specified)
	if encoding == "" {
		encoding = "identity"
	}
	req.Header.Set("Accept-Encoding", encoding)

	// Set HTMX headers if hxTarget provided
	if hxTarget != "" {
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", hxTarget)
	}
	req.Header.Set(InternalPreloadHeader, "true")

	// Call handler directly with discarding ResponseWriter
	rw := responseWriterPool.Get()
	defer responseWriterPool.Put(rw)
	cfg.Handler.ServeHTTP(rw, req)

	if rw.StatusCode() >= 400 {
		return fmt.Errorf("internal request failed: %d", rw.StatusCode())
	}

	return nil
}
