package cachelite

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestCheckCache_ReturnsStoredEntry_Internal(t *testing.T) {
	db := createTestDBPoolInternal(t)

	ctx := context.Background()
	entry := &HTTPCacheEntry{
		Key:         "check-key",
		Method:      "GET",
		Path:        "/check",
		Status:      200,
		ContentType: sql.NullString{String: "text/plain", Valid: true},
		Body:        []byte("<html><body>hello</body></html>"),
		CreatedAt:   time.Now().Unix(),
	}
	entry.ContentLength = sql.NullInt64{Int64: int64(len(entry.Body)), Valid: true}

	if err := StoreCacheEntry(ctx, db, entry); err != nil {
		t.Fatalf("failed to store entry: %v", err)
	}

	dummySubmit := func(entry *HTTPCacheEntry) {}
	mw := newHTTPCacheMiddlewareForTest(db, CacheConfig{MaxTotalSize: 1}, nil, dummySubmit)
	got, err := mw.checkCache(ctx, "check-key")
	if err != nil {
		t.Fatalf("checkCache returned error: %v", err)
	}
	if got == nil {
		t.Fatal("checkCache returned nil entry")
	}
	if got.Key != "check-key" {
		t.Fatalf("checkCache key = %q, want check-key", got.Key)
	}
}

func TestHasCacheBypassDirective(t *testing.T) {
	tests := []struct {
		name         string
		cacheControl []string
		want         bool
	}{
		{"Empty", []string{""}, false},
		{"No Cache (simple)", []string{"no-cache"}, true},
		{"No Cache (case insensitive)", []string{"NO-CACHE"}, true},
		{"No Store", []string{"no-store"}, true},
		{"No Store (case insensitive)", []string{"No-Store"}, true},
		{"Max Age 0", []string{"max-age=0"}, true},
		{"Max Age 0 (case insensitive)", []string{"MAX-AGE=0"}, true},
		{"Max Age 0 (with whitespace)", []string{"max-age = 0"}, true},
		{"Compound Directives", []string{"public, no-cache"}, true},
		{"Compound Directives with Space", []string{"public ,  no-cache"}, true},
		{"Multiple header values", []string{"public", "no-cache"}, true},
		{"Wait-until-expiry", []string{"max-age=3600"}, false},
		{"Only proxy revalidate", []string{"proxy-revalidate"}, false},
		{"Bypass in second value", []string{"public", "max-age=0"}, true},
		{"Bypass with other params", []string{"no-cache=\"Set-Cookie\""}, true},
		{"Max age not zero", []string{"max-age=10"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasCacheBypassDirective(tt.cacheControl); got != tt.want {
				t.Errorf("hasCacheBypassDirective(%v) = %v, want %v", tt.cacheControl, got, tt.want)
			}
		})
	}
}
