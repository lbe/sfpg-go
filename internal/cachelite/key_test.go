package cachelite

import (
	"net/http"
	"strings"
	"testing"
)

func TestCacheKey_Consistency(t *testing.T) {
	// Same parameters should generate same key regardless of source
	requestParams := CacheKeyParams{
		Method: "GET",
		Path:   "/gallery/123",
		Query:  "v=1",
		HTMX: HTMXParams{
			Request:   "true",
			Target:    "gallery-content",
			IsVariant: true,
		},
		Theme:    "dark",
		Encoding: "gzip",
	}

	preloadParams := CacheKeyParams{
		Method: "GET",
		Path:   "/gallery/123",
		Query:  "v=1",
		HTMX: HTMXParams{
			Request:   "true",
			Target:    "gallery-content",
			IsVariant: true,
		},
		Theme:    "dark",
		Encoding: "gzip",
	}

	middlewareKey := NewCacheKey(requestParams)
	preloadKey := NewCacheKey(preloadParams)

	if middlewareKey != preloadKey {
		t.Errorf("Cache keys don't match:\nMiddleware: %s\nPreload: %s", middlewareKey, preloadKey)
	}
}

func TestCacheKey_HTMXVariants(t *testing.T) {
	// All HX parameters should be included
	params := CacheKeyParams{
		Method: "GET",
		Path:   "/gallery/123",
		Query:  "",
		HTMX: HTMXParams{
			Request:   "true",
			Target:    "gallery-content",
			IsVariant: true,
		},
		Theme:    "dark",
		Encoding: "gzip",
	}

	key := NewCacheKey(params)
	expectedComponents := []string{"HX=true", "HXTarget=gallery-content", "IsVariant=true", "Theme=dark", "|gzip"}

	for _, component := range expectedComponents {
		if !strings.Contains(key, component) {
			t.Errorf("Cache key missing component %s in key: %s", component, key)
		}
	}
}

func TestNewCacheKeyForRequest(t *testing.T) {
	req, _ := http.NewRequest("GET", "/gallery/123?v=1", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "gallery-content")
	req.Header.Set("Accept-Encoding", "gzip")

	params := NewCacheKeyForRequest(req, "dark")

	if params.Method != "GET" {
		t.Errorf("Expected Method GET, got %s", params.Method)
	}
	if params.Path != "/gallery/123" {
		t.Errorf("Expected Path /gallery/123, got %s", params.Path)
	}
	if params.Query != "v=1" {
		t.Errorf("Expected Query v=1, got %s", params.Query)
	}
	if params.HTMX.Request != "true" {
		t.Errorf("Expected HTMX.Request true, got %s", params.HTMX.Request)
	}
	if params.HTMX.Target != "gallery-content" {
		t.Errorf("Expected HTMX.Target gallery-content, got %s", params.HTMX.Target)
	}
	if !params.HTMX.IsVariant {
		t.Error("Expected IsVariant true")
	}
	if params.Theme != "dark" {
		t.Errorf("Expected Theme dark, got %s", params.Theme)
	}
	if params.Encoding != "gzip" {
		t.Errorf("Expected Encoding gzip, got %s", params.Encoding)
	}
}

func TestNewCacheKeyForPreload(t *testing.T) {
	params := NewCacheKeyForPreload("/gallery/123", "v=1", "gzip", "dark", true)

	if params.Method != "GET" {
		t.Errorf("Expected Method GET, got %s", params.Method)
	}
	if params.Path != "/gallery/123" {
		t.Errorf("Expected Path /gallery/123, got %s", params.Path)
	}
	if params.HTMX.Request != "true" {
		t.Errorf("Expected HTMX.Request true, got %s", params.HTMX.Request)
	}
	if params.HTMX.Target != "gallery-content" {
		t.Errorf("Expected HTMX.Target gallery-content, got %s", params.HTMX.Target)
	}
}

func TestVariantForPath(t *testing.T) {
	tests := []struct {
		path         string
		wantHxTarget string
		wantEncoding string
	}{
		{"/info/image/", "box_info", "gzip"},
		{"/info/folder/", "box_info", "gzip"},
		{"/lightbox/", "lightbox_content", "gzip"},
		{"/gallery/", "gallery-content", "gzip"},
		{"/other/", "", "identity"},
	}

	for _, tt := range tests {
		hxTarget, encoding := VariantForPath(tt.path)
		if hxTarget != tt.wantHxTarget {
			t.Errorf("VariantForPath(%q) hxTarget = %q, want %q", tt.path, hxTarget, tt.wantHxTarget)
		}
		if encoding != tt.wantEncoding {
			t.Errorf("VariantForPath(%q) encoding = %q, want %q", tt.path, encoding, tt.wantEncoding)
		}
	}
}

func TestNewCacheKey_DefaultTheme(t *testing.T) {
	params := CacheKeyParams{
		Method:   "GET",
		Path:     "/test",
		Query:    "",
		HTMX:     HTMXParams{},
		Theme:    "", // empty theme
		Encoding: "gzip",
	}

	key := NewCacheKey(params)
	if !strings.Contains(key, "Theme=dark") {
		t.Errorf("Expected default theme dark, got key: %s", key)
	}
}
