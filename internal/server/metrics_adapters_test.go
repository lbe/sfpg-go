package server

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"go.local/sfpg/internal/cachelite"
	"go.local/sfpg/internal/server/cachepreload"
	"go.local/sfpg/internal/server/files"
	"go.local/sfpg/internal/workerpool"
	"go.local/sfpg/internal/writebatcher"
)

// TestWriteBatcherAdapter_GetStats verifies GetStats behavior.
func TestWriteBatcherAdapter_GetStats(t *testing.T) {
	t.Run("returns stats from underlying batcher", func(t *testing.T) {
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 5,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		defer wb.Close()

		adapter := &writeBatcherAdapter{wb: wb}

		stats := adapter.GetStats()

		if stats.ChannelSize != 10 {
			t.Errorf("ChannelSize: got %d, want 10", stats.ChannelSize)
		}
		if stats.MaxBatchSize != 5 {
			t.Errorf("MaxBatchSize: got %d, want 5", stats.MaxBatchSize)
		}
	})
}

// TestWorkerPoolAdapter_GetStats verifies GetStats behavior.
func TestWorkerPoolAdapter_GetStats(t *testing.T) {
	t.Run("returns stats from underlying pool", func(t *testing.T) {
		// NewPool(ctx, maxWorkers, minWorkers, maxIdleTime)
		pool := workerpool.NewPool(nil, 5, 2, 10*time.Second)

		adapter := &workerPoolAdapter{pool: pool}

		stats := adapter.GetStats()

		if stats.MaxWorkers != 5 {
			t.Errorf("MaxWorkers: got %d, want 5", stats.MaxWorkers)
		}
		if stats.MinWorkers != 2 {
			t.Errorf("MinWorkers: got %d, want 2", stats.MinWorkers)
		}
	})
}

// TestCachePreloadAdapter_GetMetrics verifies GetMetrics behavior.
func TestCachePreloadAdapter_GetMetrics(t *testing.T) {
	t.Run("returns zero metrics when manager has no metrics configured", func(t *testing.T) {
		pm := cachepreload.NewPreloadManager([]string{"/test/"}, false)
		defer pm.Shutdown()

		adapter := &cachePreloadAdapter{pm: pm}

		metrics := adapter.GetMetrics()

		if metrics.TasksScheduled != 0 {
			t.Errorf("TasksScheduled: got %d, want 0", metrics.TasksScheduled)
		}
		if metrics.TasksCompleted != 0 {
			t.Errorf("TasksCompleted: got %d, want 0", metrics.TasksCompleted)
		}
		if metrics.TasksFailed != 0 {
			t.Errorf("TasksFailed: got %d, want 0", metrics.TasksFailed)
		}
		if metrics.TasksCancelled != 0 {
			t.Errorf("TasksCancelled: got %d, want 0", metrics.TasksCancelled)
		}
		if metrics.TasksSkipped != 0 {
			t.Errorf("TasksSkipped: got %d, want 0", metrics.TasksSkipped)
		}
		if metrics.TotalDuration != 0 {
			t.Errorf("TotalDuration: got %v, want 0", metrics.TotalDuration)
		}
	})

	t.Run("returns configured metrics", func(t *testing.T) {
		pm := cachepreload.NewPreloadManager([]string{"/test/"}, false)
		defer pm.Shutdown()

		preloadMetrics := &cachepreload.PreloadMetrics{}
		preloadMetrics.TasksScheduled.Store(10)
		preloadMetrics.TasksCompleted.Store(8)
		preloadMetrics.TasksFailed.Store(1)
		preloadMetrics.TasksCancelled.Store(1)
		preloadMetrics.TasksSkipped.Store(2)
		preloadMetrics.TotalDuration.Store(5_000_000) // 5ms

		cfg := cachepreload.PreloadConfig{Metrics: preloadMetrics}
		pm.Configure(cfg)

		adapter := &cachePreloadAdapter{pm: pm}

		metrics := adapter.GetMetrics()

		if metrics.TasksScheduled != 10 {
			t.Errorf("TasksScheduled: got %d, want 10", metrics.TasksScheduled)
		}
		if metrics.TasksCompleted != 8 {
			t.Errorf("TasksCompleted: got %d, want 8", metrics.TasksCompleted)
		}
		if metrics.TasksFailed != 1 {
			t.Errorf("TasksFailed: got %d, want 1", metrics.TasksFailed)
		}
		if metrics.TasksCancelled != 1 {
			t.Errorf("TasksCancelled: got %d, want 1", metrics.TasksCancelled)
		}
		if metrics.TasksSkipped != 2 {
			t.Errorf("TasksSkipped: got %d, want 2", metrics.TasksSkipped)
		}
		if metrics.TotalDuration != 5*time.Millisecond {
			t.Errorf("TotalDuration: got %v, want 5ms", metrics.TotalDuration)
		}
	})
}

// TestCachePreloadAdapter_IsEnabled verifies IsEnabled behavior.
func TestCachePreloadAdapter_IsEnabled(t *testing.T) {
	t.Run("returns true when enabled", func(t *testing.T) {
		pm := cachepreload.NewPreloadManager([]string{"/test/"}, true)
		defer pm.Shutdown()

		adapter := &cachePreloadAdapter{pm: pm}

		if !adapter.IsEnabled() {
			t.Error("expected IsEnabled true")
		}
	})

	t.Run("returns false when disabled", func(t *testing.T) {
		pm := cachepreload.NewPreloadManager([]string{"/test/"}, false)
		defer pm.Shutdown()

		adapter := &cachePreloadAdapter{pm: pm}

		if adapter.IsEnabled() {
			t.Error("expected IsEnabled false")
		}
	})

	t.Run("reflects enable/disable changes", func(t *testing.T) {
		pm := cachepreload.NewPreloadManager([]string{"/test/"}, false)
		defer pm.Shutdown()

		adapter := &cachePreloadAdapter{pm: pm}

		if adapter.IsEnabled() {
			t.Error("expected initial IsEnabled false")
		}

		pm.SetEnabled(true)

		if !adapter.IsEnabled() {
			t.Error("expected IsEnabled true after SetEnabled(true)")
		}

		pm.SetEnabled(false)

		if adapter.IsEnabled() {
			t.Error("expected IsEnabled false after SetEnabled(false)")
		}
	})
}

// TestHTTPCacheAdapter_IsEnabled verifies IsEnabled behavior.
func TestHTTPCacheAdapter_IsEnabled(t *testing.T) {
	t.Run("returns true when cache is enabled", func(t *testing.T) {
		var sizeCounter atomic.Int64
		cache := cachelite.NewHTTPCacheMiddleware(nil, cachelite.CacheConfig{
			MaxEntrySize: 100_000,
			MaxTotalSize: 1_000_000,
			Enabled:      true,
		}, &sizeCounter, nil)

		adapter := &httpCacheAdapter{cache: cache}

		if !adapter.IsEnabled() {
			t.Error("expected IsEnabled true")
		}
	})

	t.Run("returns false when cache is disabled", func(t *testing.T) {
		var sizeCounter atomic.Int64
		cache := cachelite.NewHTTPCacheMiddleware(nil, cachelite.CacheConfig{
			MaxEntrySize: 100_000,
			MaxTotalSize: 1_000_000,
			Enabled:      false,
		}, &sizeCounter, nil)

		adapter := &httpCacheAdapter{cache: cache}

		if adapter.IsEnabled() {
			t.Error("expected IsEnabled false")
		}
	})
}

// TestHTTPCacheAdapter_GetSizeBytes requires database - skipped for unit tests.
// Integration tests cover this functionality.
func TestHTTPCacheAdapter_GetSizeBytes(t *testing.T) {
	t.Skip("requires database connection - covered by integration tests")
}

// TestHTTPCacheAdapter_GetEntryCount requires database - skipped for unit tests.
// Integration tests cover this functionality.
func TestHTTPCacheAdapter_GetEntryCount(t *testing.T) {
	t.Skip("requires database connection - covered by integration tests")
}

// TestHTTPCacheAdapter_GetConfig verifies GetConfig behavior.
func TestHTTPCacheAdapter_GetConfig(t *testing.T) {
	t.Run("returns cache configuration", func(t *testing.T) {
		var sizeCounter atomic.Int64
		cache := cachelite.NewHTTPCacheMiddleware(nil, cachelite.CacheConfig{
			MaxEntrySize: 100_000,
			MaxTotalSize: 1_000_000,
			Enabled:      true,
		}, &sizeCounter, nil)

		adapter := &httpCacheAdapter{cache: cache}

		config := adapter.GetConfig()

		if config.MaxEntrySize != 100_000 {
			t.Errorf("MaxEntrySize: got %d, want 100000", config.MaxEntrySize)
		}
		if config.MaxTotalSize != 1_000_000 {
			t.Errorf("MaxTotalSize: got %d, want 1000000", config.MaxTotalSize)
		}
	})
}

// TestFileProcessorAdapter_GetStats verifies GetStats behavior.
func TestFileProcessorAdapter_GetStats(t *testing.T) {
	t.Run("returns stats from underlying processing stats", func(t *testing.T) {
		stats := &files.ProcessingStats{}
		stats.TotalFound.Store(100)
		stats.AlreadyExisting.Store(50)
		stats.NewlyInserted.Store(30)
		stats.SkippedInvalid.Store(10)
		stats.InFlight.Store(5)

		adapter := &fileProcessorAdapter{stats: stats}

		metrics := adapter.GetStats()

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

		adapter := &fileProcessorAdapter{stats: stats}

		metrics := adapter.GetStats()

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

// Test helper for WriteBatcher - we need to expose the internal batcher for testing
// This is a simplified version that mimics the real WriteBatcher's GetStats method

// TestWriteBatcher is a test double for writebatcher.WriteBatcher
type TestWriteBatcher struct {
	channelSize  int
	maxBatchSize int
}

// NewTestWriteBatcher creates a test batcher
func NewTestWriteBatcher(channelSize, maxBatchSize int) *TestWriteBatcher {
	return &TestWriteBatcher{
		channelSize:  channelSize,
		maxBatchSize: maxBatchSize,
	}
}

// Close implements the close method
func (wb *TestWriteBatcher) Close() {}

// GetStats returns mock stats
func (wb *TestWriteBatcher) GetStats() writebatcher.Stats {
	return writebatcher.Stats{
		ChannelSize:  wb.channelSize,
		MaxBatchSize: wb.maxBatchSize,
	}
}

// Note: The writebatcher package exports Stats type, so we can use it directly
// No need for a test helper anymore
