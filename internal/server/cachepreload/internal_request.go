package cachepreload

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
)

// InternalPreloadHeader is set on internal preload requests so handlers can skip
// scheduling another preload (avoiding cascading preloads).
const InternalPreloadHeader = "X-SFPG-Internal-Preload"

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
		return fmt.Errorf("internal request config: handler is nil")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}

	// Set query string for cache key
	req.URL.RawQuery = fmt.Sprintf("v=%s", cfg.ETagVersion)

	// Set Accept-Encoding to match common client requests
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set(InternalPreloadHeader, "true")

	// Defensive check before use in case of use-after-put (e.g. pooled task run after reset).
	if cfg.Handler == nil {
		return fmt.Errorf("internal request config: handler is nil")
	}
	rec := httptest.NewRecorder()
	cfg.Handler.ServeHTTP(rec, req)

	// Check for success (cache middleware will store on 2xx)
	if rec.Code >= 400 {
		return fmt.Errorf("internal request failed: %d", rec.Code)
	}

	return nil
}

// MakeInternalRequestWithVariant makes an internal HTTP request with optional HX variant
// and Accept-Encoding so the stored cache key matches real browser requests.
// When hxTarget is non-empty, sets HX-Request: true and HX-Target: hxTarget; when empty,
// does not set HX headers (full-page style). encoding is always set on Accept-Encoding.
func MakeInternalRequestWithVariant(ctx context.Context, cfg InternalRequestConfig, path string, hxTarget string, encoding string) error {
	if cfg.Handler == nil {
		return fmt.Errorf("internal request config: handler is nil")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}

	req.URL.RawQuery = fmt.Sprintf("v=%s", cfg.ETagVersion)
	if encoding == "" {
		encoding = "identity"
	}
	req.Header.Set("Accept-Encoding", encoding)

	if hxTarget != "" {
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", hxTarget)
	}
	req.Header.Set(InternalPreloadHeader, "true")

	// Defensive check before use in case of use-after-put (e.g. pooled task run after reset).
	if cfg.Handler == nil {
		return fmt.Errorf("internal request config: handler is nil")
	}
	rec := httptest.NewRecorder()
	cfg.Handler.ServeHTTP(rec, req)

	if rec.Code >= 400 {
		return fmt.Errorf("internal request failed: %d", rec.Code)
	}

	return nil
}
