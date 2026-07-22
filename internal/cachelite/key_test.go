package cachelite

import (
	"net/http"
	"strings"
	"testing"
)

func TestCacheKey_Consistency(t *testing.T) {
	// Same parameters should generate same key regardless of source
	requestParams := CacheKeyParams{
		Method:  "GET",
		Path:    "/gallery/123",
		Query:   "v=1",
		Variant: "gallery-content",
	}

	preloadParams := CacheKeyParams{
		Method:  "GET",
		Path:    "/gallery/123",
		Query:   "v=1",
		Variant: "gallery-content",
	}

	middlewareKey := NewCacheKey(requestParams)
	preloadKey := NewCacheKey(preloadParams)

	if middlewareKey != preloadKey {
		t.Errorf("Cache keys don't match:\nMiddleware: %s\nPreload: %s", middlewareKey, preloadKey)
	}
}

func TestCacheKey_Variant(t *testing.T) {
	// Variant name should be included
	params := CacheKeyParams{
		Method:  "GET",
		Path:    "/gallery/123",
		Query:   "",
		Variant: "gallery-content",
	}

	key := NewCacheKey(params)
	if !strings.Contains(key, "|Variant=gallery-content") {
		t.Errorf("Cache key missing variant component in key: %s", key)
	}
}

func TestNewCacheKeyForRequest(t *testing.T) {
	req, err := http.NewRequest("GET", "/gallery/123?v=1", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "gallery-content")

	params := NewCacheKeyForRequest(req)

	if params.Method != "GET" {
		t.Errorf("Expected Method GET, got %s", params.Method)
	}
	if params.Path != "/gallery/123" {
		t.Errorf("Expected Path /gallery/123, got %s", params.Path)
	}
	if params.Query != "v=1" {
		t.Errorf("Expected Query v=1, got %s", params.Query)
	}
	if params.Variant != "gallery-content" {
		t.Errorf("Expected Variant gallery-content, got %s", params.Variant)
	}
	// Encoding is no longer part of CacheKeyParams.
}

func TestNewCacheKeyForPreload(t *testing.T) {
	params := NewCacheKeyForPreload("/gallery/123", "v=1", "gallery-content")

	if params.Method != "GET" {
		t.Errorf("Expected Method GET, got %s", params.Method)
	}
	if params.Path != "/gallery/123" {
		t.Errorf("Expected Path /gallery/123, got %s", params.Path)
	}
	if params.Variant != "gallery-content" {
		t.Errorf("Expected Variant gallery-content, got %s", params.Variant)
	}
}

func TestNewCacheKeyForRequest_InfoFullAndHTMXShareKey(t *testing.T) {
	// Full page request to /info/image/1 (no HTMX)
	reqA, err := http.NewRequest("GET", "/info/image/1", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}

	// HTMX request to same path
	reqB, err := http.NewRequest("GET", "/info/image/1", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	reqB.Header.Set("HX-Request", "true")
	reqB.Header.Set("HX-Target", "box_info")

	keyA := NewCacheKey(NewCacheKeyForRequest(reqA))
	keyB := NewCacheKey(NewCacheKeyForRequest(reqB))

	if keyA != keyB {
		t.Errorf("info full and HTMX requests produced different cache keys:\n  %s\n  %s", keyA, keyB)
	}
}

func TestNewCacheKeyForRequest_LightboxTargetsShareKey(t *testing.T) {
	// Request with HX-Target: lightbox_content
	reqA, err := http.NewRequest("GET", "/lightbox/1", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	reqA.Header.Set("HX-Request", "true")
	reqA.Header.Set("HX-Target", "lightbox_content")

	// Request with HX-Target: lightbox-ui
	reqB, err := http.NewRequest("GET", "/lightbox/1", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	reqB.Header.Set("HX-Request", "true")
	reqB.Header.Set("HX-Target", "lightbox-ui")

	keyA := NewCacheKey(NewCacheKeyForRequest(reqA))
	keyB := NewCacheKey(NewCacheKeyForRequest(reqB))

	if keyA != keyB {
		t.Errorf("lightbox_content and lightbox-ui produced different cache keys:\n  %s\n  %s", keyA, keyB)
	}

	// Both should have variant lightbox-ui
	paramsA := NewCacheKeyForRequest(reqA)
	paramsB := NewCacheKeyForRequest(reqB)
	if paramsA.Variant != "lightbox-ui" {
		t.Errorf("NewCacheKeyForRequest with target=lightbox_content: got Variant=%q, want lightbox-ui", paramsA.Variant)
	}
	if paramsB.Variant != "lightbox-ui" {
		t.Errorf("NewCacheKeyForRequest with target=lightbox-ui: got Variant=%q, want lightbox-ui", paramsB.Variant)
	}
}

func TestNewCacheKeyForRequest_GalleryFullAndPartialDistinct(t *testing.T) {
	// Full page request to /gallery/1 (no HTMX) → variant full
	reqA, err := http.NewRequest("GET", "/gallery/1", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}

	// HTMX request to same path → variant gallery-content
	reqB, err := http.NewRequest("GET", "/gallery/1", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	reqB.Header.Set("HX-Request", "true")
	reqB.Header.Set("HX-Target", "gallery-content")

	keyA := NewCacheKey(NewCacheKeyForRequest(reqA))
	keyB := NewCacheKey(NewCacheKeyForRequest(reqB))

	if keyA == keyB {
		t.Errorf("gallery full and partial requests produced the same cache key:\n  %s", keyA)
	}
}

func TestNormalizedVariant(t *testing.T) {
	tests := []struct {
		path      string
		hxRequest string
		hxTarget  string
		want      string
	}{
		{"/info/image/", "true", "box_info", "box_info"},
		{"/info/image/", "false", "", "box_info"},
		{"/info/folder/", "false", "", "box_info"},
		{"/lightbox/1", "true", "lightbox_content", "lightbox-ui"},
		{"/lightbox/", "true", "lightbox-ui", "lightbox-ui"},
		{"/lightbox/", "false", "", "lightbox-ui"},
		{"/gallery/", "true", "gallery-content", "gallery-content"},
		{"/gallery/", "false", "", "full"},
		{"/gallery/", "true", "some-other-target", "full"},
		{"/other/", "false", "", "full"},
		{"/other/", "true", "some-target", "full"},
	}

	for _, tt := range tests {
		got := NormalizedVariant(tt.path, tt.hxRequest, tt.hxTarget)
		if got != tt.want {
			t.Errorf("NormalizedVariant(%q, %q, %q) = %q, want %q", tt.path, tt.hxRequest, tt.hxTarget, got, tt.want)
		}
	}
}

func TestPreloadVariantForPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/info/image/", "box_info"},
		{"/info/folder/", "box_info"},
		{"/lightbox/", "lightbox-ui"},
		{"/gallery/", "gallery-content"},
		{"/other/", "full"},
	}

	for _, tt := range tests {
		got := PreloadVariantForPath(tt.path)
		if got != tt.want {
			t.Errorf("PreloadVariantForPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestNewCacheKey_NoEncoding(t *testing.T) {
	// Different encoding values should produce the same cache key
	paramsWithGzip := CacheKeyParams{
		Method:  "GET",
		Path:    "/test",
		Query:   "",
		Variant: "full",
	}
	paramsWithBr := CacheKeyParams{
		Method:  "GET",
		Path:    "/test",
		Query:   "",
		Variant: "full",
	}

	keyGzip := NewCacheKey(paramsWithGzip)
	keyBr := NewCacheKey(paramsWithBr)

	if keyGzip != keyBr {
		t.Errorf("Expected same key for different encodings:\n  gzip: %s\n  br:   %s", keyGzip, keyBr)
	}

	// Verify no encoding component in the key
	if strings.Contains(keyGzip, "|gzip") || strings.Contains(keyGzip, "|br") || strings.Contains(keyGzip, "|identity") {
		t.Errorf("Cache key should not contain encoding, got: %s", keyGzip)
	}
}
