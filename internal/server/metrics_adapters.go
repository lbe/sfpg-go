package server

import (
	"go.local/sfpg/internal/cachelite"
	"go.local/sfpg/internal/server/cachepreload"
	"go.local/sfpg/internal/server/files"
	"go.local/sfpg/internal/server/metrics"
	"go.local/sfpg/internal/workerpool"
	"go.local/sfpg/internal/writebatcher"
)

// writeBatcherAdapter adapts *writebatcher.WriteBatcher to metrics.WriteBatcherSource.
// It exposes statistics from the unified write batcher for the metrics dashboard.
type writeBatcherAdapter struct {
	wb *writebatcher.WriteBatcher[BatchedWrite]
}

func (a *writeBatcherAdapter) PendingCount() int64 {
	return a.wb.PendingCount()
}

func (a *writeBatcherAdapter) GetStats() metrics.WriteBatcherStats {
	stats := a.wb.GetStats()
	return metrics.WriteBatcherStats{
		ChannelSize:   stats.ChannelSize,
		MaxBatchSize:  stats.MaxBatchSize,
		FlushInterval: stats.FlushInterval,
		IsClosed:      stats.IsClosed,
		TotalFlushed:  stats.TotalFlushed,
		TotalErrors:   stats.TotalErrors,
	}
}

// workerPoolAdapter adapts *workerpool.Pool to metrics.WorkerPoolSource.
// It exposes worker pool statistics for the metrics dashboard.
type workerPoolAdapter struct {
	pool *workerpool.Pool
}

func (a *workerPoolAdapter) GetStats() metrics.WorkerPoolStats {
	stats := a.pool.GetStats()
	return metrics.WorkerPoolStats{
		RunningWorkers:  stats.RunningWorkers,
		SubmittedTasks:  stats.SubmittedTasks,
		WaitingTasks:    stats.WaitingTasks,
		SuccessfulTasks: stats.SuccessfulTasks,
		FailedTasks:     stats.FailedTasks,
		CompletedTasks:  stats.CompletedTasks,
		DroppedTasks:    stats.DroppedTasks,
		MaxWorkers:      stats.MaxWorkers,
		MinWorkers:      stats.MinWorkers,
	}
}

// cachePreloadAdapter adapts *cachepreload.PreloadManager to metrics.CachePreloadSource.
// It exposes cache preload metrics for the metrics dashboard.
type cachePreloadAdapter struct {
	pm *cachepreload.PreloadManager
}

func (a *cachePreloadAdapter) GetMetrics() metrics.CachePreloadSnapshot {
	m := a.pm.GetMetrics()
	return metrics.CachePreloadSnapshot{
		TasksScheduled: m.TasksScheduled,
		TasksCompleted: m.TasksCompleted,
		TasksFailed:    m.TasksFailed,
		TasksCancelled: m.TasksCancelled,
		TasksSkipped:   m.TasksSkipped,
		TotalDuration:  m.TotalDuration,
	}
}

func (a *cachePreloadAdapter) IsEnabled() bool {
	return a.pm.IsEnabled()
}

// httpCacheAdapter adapts *cachelite.HTTPCacheMiddleware to metrics.HTTPCacheSource.
// It exposes HTTP cache statistics and configuration for the metrics dashboard.
type httpCacheAdapter struct {
	cache *cachelite.HTTPCacheMiddleware
}

func (a *httpCacheAdapter) IsEnabled() bool {
	return a.cache.IsEnabled()
}

func (a *httpCacheAdapter) GetSizeBytes() int64 {
	return a.cache.GetSizeBytes()
}

func (a *httpCacheAdapter) GetEntryCount() int64 {
	return a.cache.GetEntryCount()
}

func (a *httpCacheAdapter) GetConfig() metrics.HTTPCacheConfig {
	cfg := a.cache.Config()
	return metrics.HTTPCacheConfig{
		MaxEntrySize: cfg.MaxEntrySize,
		MaxTotalSize: cfg.MaxTotalSize,
	}
}

// fileProcessorAdapter adapts *files.ProcessingStats to metrics.FileProcessorSource.
// It exposes file processing statistics for the metrics dashboard.
type fileProcessorAdapter struct {
	stats *files.ProcessingStats
}

func (a *fileProcessorAdapter) GetStats() metrics.FileProcessingMetrics {
	return metrics.FileProcessingMetrics{
		TotalFound:      a.stats.TotalFound.Load(),
		AlreadyExisting: a.stats.AlreadyExisting.Load(),
		NewlyInserted:   a.stats.NewlyInserted.Load(),
		SkippedInvalid:  a.stats.SkippedInvalid.Load(),
		InFlight:        a.stats.InFlight.Load(),
	}
}
