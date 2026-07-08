package cachelite_test

import (
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	cachelite "github.com/lbe/sfpg-go/internal/cachelite"
)

func TestNewHTTPCacheMiddleware_ConstructsWithNonNilSubmit(t *testing.T) {
	var counter atomic.Int64
	submitted := make(chan *cachelite.HTTPCacheEntry, 1)
	submit := func(entry *cachelite.HTTPCacheEntry) {
		submitted <- entry
	}

	cfg := cachelite.CacheConfig{Enabled: true}
	mw := cachelite.NewHTTPCacheMiddleware(nil, cfg, &counter, submit)

	if got := mw.Config(); !reflect.DeepEqual(got, cfg) {
		t.Errorf("Config() = %+v, want %+v", got, cfg)
	}
	if !mw.IsEnabled() {
		t.Error("IsEnabled() = false, want true")
	}
}

func TestNewHTTPCacheMiddleware_NilSubmitPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil submitFunc")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "submitFunc is required in production") {
			t.Errorf("panic message = %q, want it to contain 'submitFunc is required in production'", msg)
		}
	}()

	_ = cachelite.NewHTTPCacheMiddleware(nil, cachelite.CacheConfig{}, nil, nil)
}

func TestHTTPCacheMiddleware_IsEnabled(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		want    bool
	}{
		{"enabled", true, true},
		{"disabled", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := cachelite.CacheConfig{Enabled: tt.enabled}
			mw := cachelite.NewHTTPCacheMiddlewareForTest(nil, cfg, nil, func(*cachelite.HTTPCacheEntry) {})
			if got := mw.IsEnabled(); got != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
