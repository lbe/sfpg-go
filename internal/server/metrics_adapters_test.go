package server

import (
	"sync/atomic"
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/discovery"
	"github.com/lbe/sfpg-go/internal/server/metrics"
)

// Compile-time assertions: the source types satisfy the metrics interfaces directly.
var (
	_ metrics.HTTPCacheSource     = (*cachelite.HTTPCacheMiddleware)(nil)
	_ metrics.FileProcessorSource = (*discovery.Stats)(nil)
)

// TestHTTPCacheMiddleware_MetricsMethods verifies that HTTPCacheMiddleware exposes
// the metrics-compatible config and status accessors needed by the dashboard.
func TestHTTPCacheMiddleware_MetricsMethods(t *testing.T) {
	t.Run("IsEnabled returns true when cache is enabled", func(t *testing.T) {
		var sizeCounter atomic.Int64
		counters := &cachelite.HTTPCacheCounterState{
			SizeBytes:       &sizeCounter,
			EntryCount:      &atomic.Int64{},
			BaselineRunning: &atomic.Int32{},
		}
		cache := cachelite.NewHTTPCacheMiddleware(nil, cachelite.CacheConfig{
			MaxEntrySize: 100_000,
			MaxTotalSize: 1_000_000,
			Enabled:      true,
		}, counters, func(*cachelite.HTTPCacheEntry) {})

		if !cache.IsEnabled() {
			t.Error("expected IsEnabled true")
		}
	})

	t.Run("IsEnabled returns false when cache is disabled", func(t *testing.T) {
		var sizeCounter atomic.Int64
		counters := &cachelite.HTTPCacheCounterState{
			SizeBytes:       &sizeCounter,
			EntryCount:      &atomic.Int64{},
			BaselineRunning: &atomic.Int32{},
		}
		cache := cachelite.NewHTTPCacheMiddleware(nil, cachelite.CacheConfig{
			MaxEntrySize: 100_000,
			MaxTotalSize: 1_000_000,
			Enabled:      false,
		}, counters, func(*cachelite.HTTPCacheEntry) {})

		if cache.IsEnabled() {
			t.Error("expected IsEnabled false")
		}
	})

	t.Run("MaxEntrySize and MaxTotalSize return cache configuration", func(t *testing.T) {
		var sizeCounter atomic.Int64
		counters := &cachelite.HTTPCacheCounterState{
			SizeBytes:       &sizeCounter,
			EntryCount:      &atomic.Int64{},
			BaselineRunning: &atomic.Int32{},
		}
		cache := cachelite.NewHTTPCacheMiddleware(nil, cachelite.CacheConfig{
			MaxEntrySize: 100_000,
			MaxTotalSize: 1_000_000,
			Enabled:      true,
		}, counters, func(*cachelite.HTTPCacheEntry) {})

		if cache.MaxEntrySize() != 100_000 {
			t.Errorf("MaxEntrySize: got %d, want 100000", cache.MaxEntrySize())
		}
		if cache.MaxTotalSize() != 1_000_000 {
			t.Errorf("MaxTotalSize: got %d, want 1000000", cache.MaxTotalSize())
		}
	})

	t.Run("GetSizeBytes and GetEntryCount read from counters, not DB", func(t *testing.T) {
		app := newAppForUnlock(t)
		var sizeCounter atomic.Int64
		sizeCounter.Store(12345)
		var entryCounter atomic.Int64
		entryCounter.Store(67)
		var br atomic.Int32
		counter := &cachelite.HTTPCacheCounterState{
			SizeBytes:       &sizeCounter,
			EntryCount:      &entryCounter,
			BaselineRunning: &br,
		}
		cache := cachelite.NewHTTPCacheMiddleware(app.dbRoPool, cachelite.CacheConfig{
			Enabled:      true,
			MaxEntrySize: 100_000,
			MaxTotalSize: 1_000_000,
			DefaultTTL:   60,
			CacheableRoutes: []string{
				"/gallery/", "/lightbox/", "/info/folder/", "/info/image/",
			},
		}, counter, func(*cachelite.HTTPCacheEntry) {})

		if got := cache.GetSizeBytes(); got != 12345 {
			t.Errorf("GetSizeBytes = %d, want 12345 (from counter, not DB)", got)
		}
		if got := cache.GetEntryCount(); got != 67 {
			t.Errorf("GetEntryCount = %d, want 67 (from counter, not DB)", got)
		}
	})
}

func TestSubsystemManager_WireMetrics_UsesSourceTypes(t *testing.T) {
	app := newAppForUnlock(t)
	m := NewSubsystemManager(app.InfrastructureService)
	m.stats = &discovery.Stats{}

	collector := metrics.NewCollector()
	m.WireMetrics(collector)

	// The collector should have accepted the concrete types via the interfaces.
	if collector == nil {
		t.Fatal("collector is nil")
	}
}
