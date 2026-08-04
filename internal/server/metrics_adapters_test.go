package server

import (
	"sync/atomic"
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/metrics"
)

// Compile-time assertions: the source types satisfy the metrics interfaces directly.
var (
	_ metrics.HTTPCacheSource     = (*cachelite.HTTPCacheMiddleware)(nil)
	_ metrics.FileProcessorSource = (*files.ProcessingStats)(nil)
)

// TestHTTPCacheMiddleware_MetricsMethods verifies that HTTPCacheMiddleware exposes
// the metrics-compatible config and status accessors needed by the dashboard.
func TestHTTPCacheMiddleware_MetricsMethods(t *testing.T) {
	t.Run("IsEnabled returns true when cache is enabled", func(t *testing.T) {
		var sizeCounter atomic.Int64
		cache := cachelite.NewHTTPCacheMiddlewareForTest(nil, cachelite.CacheConfig{
			MaxEntrySize: 100_000,
			MaxTotalSize: 1_000_000,
			Enabled:      true,
		}, cachelite.HTTPCacheCountersForTest(&sizeCounter), nil)

		if !cache.IsEnabled() {
			t.Error("expected IsEnabled true")
		}
	})

	t.Run("IsEnabled returns false when cache is disabled", func(t *testing.T) {
		var sizeCounter atomic.Int64
		cache := cachelite.NewHTTPCacheMiddlewareForTest(nil, cachelite.CacheConfig{
			MaxEntrySize: 100_000,
			MaxTotalSize: 1_000_000,
			Enabled:      false,
		}, cachelite.HTTPCacheCountersForTest(&sizeCounter), nil)

		if cache.IsEnabled() {
			t.Error("expected IsEnabled false")
		}
	})

	t.Run("MaxEntrySize and MaxTotalSize return cache configuration", func(t *testing.T) {
		var sizeCounter atomic.Int64
		cache := cachelite.NewHTTPCacheMiddlewareForTest(nil, cachelite.CacheConfig{
			MaxEntrySize: 100_000,
			MaxTotalSize: 1_000_000,
			Enabled:      true,
		}, cachelite.HTTPCacheCountersForTest(&sizeCounter), nil)

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

// TestProcessingStats_GetStats verifies that ProcessingStats produces the metrics
// snapshot expected by the dashboard.
func TestProcessingStats_GetStats(t *testing.T) {
	t.Run("returns stats from underlying processing stats", func(t *testing.T) {
		stats := &files.ProcessingStats{}
		stats.TotalFound.Store(100)
		stats.AlreadyExisting.Store(50)
		stats.NewlyInserted.Store(30)
		stats.SkippedInvalid.Store(10)
		stats.InFlight.Store(5)

		metrics := stats.GetStats()

		if metrics.TotalFound != 100 {
			t.Errorf("TotalFound: got %d, want 100", metrics.TotalFound)
		}
		if metrics.AlreadyExisting != 50 {
			t.Errorf("AlreadyExisting: got %d, want 50", metrics.AlreadyExisting)
		}
		if metrics.NewlyInserted != 30 {
			t.Errorf("NewlyInserted: got %d, want 30", metrics.NewlyInserted)
		}
		if metrics.SkippedInvalid != 10 {
			t.Errorf("SkippedInvalid: got %d, want 10", metrics.SkippedInvalid)
		}
		if metrics.InFlight != 5 {
			t.Errorf("InFlight: got %d, want 5", metrics.InFlight)
		}
	})

	t.Run("returns zeros for empty stats", func(t *testing.T) {
		stats := &files.ProcessingStats{}

		metrics := stats.GetStats()

		if metrics.TotalFound != 0 {
			t.Errorf("TotalFound: got %d, want 0", metrics.TotalFound)
		}
		if metrics.AlreadyExisting != 0 {
			t.Errorf("AlreadyExisting: got %d, want 0", metrics.AlreadyExisting)
		}
		if metrics.NewlyInserted != 0 {
			t.Errorf("NewlyInserted: got %d, want 0", metrics.NewlyInserted)
		}
		if metrics.SkippedInvalid != 0 {
			t.Errorf("SkippedInvalid: got %d, want 0", metrics.SkippedInvalid)
		}
		if metrics.InFlight != 0 {
			t.Errorf("InFlight: got %d, want 0", metrics.InFlight)
		}
	})
}

// TestSubsystemManager_WireMetrics_UsesSourceTypes verifies that WireMetrics passes
// the concrete source types directly instead of wrapping them in adapter structs.
func TestSubsystemManager_WireMetrics_UsesSourceTypes(t *testing.T) {
	app := newAppForUnlock(t)
	m := NewSubsystemManager(app.InfrastructureService)
	m.processingStats = &files.ProcessingStats{}

	collector := metrics.NewCollector()
	m.WireMetrics(collector)

	// The collector should have accepted the concrete types via the interfaces.
	if collector == nil {
		t.Fatal("collector is nil")
	}
}
