package cachelite_test

import (
	"database/sql"
	"testing"

	cachelite "github.com/lbe/sfpg-go/internal/cachelite"
)

// TestNewCacheKey tests the cache key generation
func TestNewCacheKey(t *testing.T) {
	tests := []struct {
		name   string
		params cachelite.CacheKeyParams
		want   string
	}{
		{
			name: "GET request",
			params: cachelite.CacheKeyParams{
				Method:  "GET",
				Path:    "/api/data",
				Query:   "id=1",
				Variant: "full",
			},
			want: "GET:/api/data?id=1|Variant=full",
		},
		{
			name: "POST request",
			params: cachelite.CacheKeyParams{
				Method:  "POST",
				Path:    "/upload",
				Query:   "",
				Variant: "full",
			},
			want: "POST:/upload?|Variant=full",
		},
		{
			name: "HEAD request",
			params: cachelite.CacheKeyParams{
				Method:  "HEAD",
				Path:    "/status",
				Query:   "v=2",
				Variant: "full",
			},
			want: "HEAD:/status?v=2|Variant=full",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cachelite.NewCacheKey(tt.params)
			if got != tt.want {
				t.Errorf("NewCacheKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCanCacheResponse tests cache eligibility logic
func TestCanCacheResponse(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		cacheControl string
		setCookie    string
		want         bool
	}{
		{
			name:         "200 OK with cache",
			status:       200,
			cacheControl: "max-age=3600",
			want:         true,
		},
		{
			name:         "200 OK no-store",
			status:       200,
			cacheControl: "no-store",
			want:         false,
		},
		{
			name:         "404 Not Found",
			status:       404,
			cacheControl: "max-age=3600",
			want:         false,
		},
		{
			name:         "200 OK with no-store and other directives",
			status:       200,
			cacheControl: "public, no-store, max-age=3600",
			want:         false,
		},
		{
			name:         "500 Internal Server Error",
			status:       500,
			cacheControl: "max-age=3600",
			want:         false,
		},
		{
			name:         "200 OK empty cache control",
			status:       200,
			cacheControl: "",
			want:         true,
		},
		{
			name:         "200 OK with Set-Cookie",
			status:       200,
			cacheControl: "max-age=3600",
			setCookie:    "session=abc123; Path=/; HttpOnly",
			want:         false,
		},
		{
			name:         "200 OK no-store with Set-Cookie",
			status:       200,
			cacheControl: "no-store",
			setCookie:    "session=abc123; Path=/; HttpOnly",
			want:         false,
		},
		{
			name:         "200 OK empty set-cookie still cacheable",
			status:       200,
			cacheControl: "max-age=3600",
			setCookie:    "",
			want:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cachelite.CanCacheResponse(tt.status, tt.cacheControl, tt.setCookie)
			if got != tt.want {
				t.Errorf("CanCacheResponse() = %v, want %v (status=%d, cacheControl=%q, setCookie=%q)",
					got, tt.want, tt.status, tt.cacheControl, tt.setCookie)
			}
		})
	}
}

// TestHTTPCacheEntry_StructLayout validates the struct is correctly defined
func TestHTTPCacheEntry_StructLayout(t *testing.T) {
	//nolint:govet // Intentionally setting all fields to verify struct layout
	entry := &cachelite.HTTPCacheEntry{
		ID:            1,
		Key:           "test-key",
		Method:        "GET",
		Path:          "/test",
		QueryString:   sql.NullString{String: "id=1", Valid: true},
		Status:        200,
		ContentType:   sql.NullString{String: "text/html", Valid: true},
		CacheControl:  sql.NullString{String: "max-age=3600", Valid: true},
		ETag:          sql.NullString{String: `"abc123"`, Valid: true},
		LastModified:  sql.NullString{String: "Mon, 02 Jan 2024 12:00:00 GMT", Valid: true},
		Vary:          sql.NullString{String: "Accept-Encoding", Valid: true},
		Body:          []byte("test body"),
		ContentLength: sql.NullInt64{Int64: 9, Valid: true},
		CreatedAt:     1234567890,
		ExpiresAt:     sql.NullInt64{Int64: 1234571490, Valid: true},
	}

	if entry.ID != 1 {
		t.Errorf("Expected ID=1, got %d", entry.ID)
	}

	if entry.Key != "test-key" {
		t.Errorf("Expected Key='test-key', got %q", entry.Key)
	}

	if entry.Status != 200 {
		t.Errorf("Expected Status=200, got %d", entry.Status)
	}

	if !entry.ContentType.Valid {
		t.Error("Expected ContentType to be valid")
	}

	if entry.ContentType.String != "text/html" {
		t.Errorf("Expected ContentType='text/html', got %q", entry.ContentType.String)
	}

	if !entry.ExpiresAt.Valid {
		t.Error("Expected ExpiresAt to be valid")
	}

	if entry.ExpiresAt.Int64 != 1234571490 {
		t.Errorf("Expected ExpiresAt=1234571490, got %d", entry.ExpiresAt.Int64)
	}
}

// TestCacheConfig_DefaultValues tests cache configuration
func TestCacheConfig_DefaultValues(t *testing.T) {
	//nolint:govet // Intentionally setting all fields to verify struct layout
	config := &cachelite.CacheConfig{
		Enabled:         true,
		MaxEntrySize:    10 * 1024 * 1024,  // 10MB
		MaxTotalSize:    500 * 1024 * 1024, // 500MB
		DefaultTTL:      3600,              // 1 hour in seconds (note: should be time.Duration)
		CacheableRoutes: []string{"/dummy"},
	}

	if !config.Enabled {
		t.Error("Expected cache to be enabled")
	}

	if config.MaxEntrySize != 10*1024*1024 {
		t.Errorf("Expected MaxEntrySize=10MB, got %d", config.MaxEntrySize)
	}

	if config.MaxTotalSize != 500*1024*1024 {
		t.Errorf("Expected MaxTotalSize=500MB, got %d", config.MaxTotalSize)
	}
}
