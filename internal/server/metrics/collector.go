// Package metrics provides centralized metrics collection for the dashboard.
// It aggregates data from various components like WriteBatcher, WorkerPool,
// CachePreload, and Go runtime statistics.
package metrics

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/lbe/sfpg-go/internal/humanize"
)

// WriteBatcherMetrics holds statistics from the WriteBatcher.
type WriteBatcherMetrics struct {
	PendingCount  int64     `json:"pending_count"`
	ChannelSize   int       `json:"channel_size"`
	MaxBatchSize  int       `json:"max_batch_size"`
	FlushInterval string    `json:"flush_interval"`
	IsClosed      bool      `json:"is_closed"`
	LastFlushTime time.Time `json:"last_flush_time"`
	TotalFlushed  int64     `json:"total_flushed"`
	TotalErrors   int64     `json:"total_errors"`
}

// WriteBatcherSource provides metrics from a WriteBatcher.
type WriteBatcherSource interface {
	PendingCount() int64
	GetStats() WriteBatcherStats
}

// WriteBatcherStats holds internal stats from WriteBatcher.
type WriteBatcherStats struct {
	ChannelSize   int
	MaxBatchSize  int
	FlushInterval time.Duration
	IsClosed      bool
	TotalFlushed  int64
	TotalErrors   int64
}

// WorkerPoolMetrics holds statistics from the worker pool.
type WorkerPoolMetrics struct {
	RunningWorkers  int64  `json:"running_workers"`
	SubmittedTasks  uint64 `json:"submitted_tasks"`
	WaitingTasks    uint64 `json:"waiting_tasks"`
	SuccessfulTasks uint64 `json:"successful_tasks"`
	FailedTasks     uint64 `json:"failed_tasks"`
	CompletedTasks  uint64 `json:"completed_tasks"`
	DroppedTasks    uint64 `json:"dropped_tasks"`
	MaxWorkers      int    `json:"max_workers"`
	MinWorkers      int    `json:"min_workers"`
}

// WorkerPoolSource provides metrics from a worker pool.
type WorkerPoolSource interface {
	GetStats() WorkerPoolStats
}

// WorkerPoolStats holds internal stats from WorkerPool.
type WorkerPoolStats struct {
	RunningWorkers  int64
	SubmittedTasks  uint64
	WaitingTasks    uint64
	SuccessfulTasks uint64
	FailedTasks     uint64
	CompletedTasks  uint64
	DroppedTasks    uint64
	MaxWorkers      int
	MinWorkers      int
}

// CachePreloadMetrics holds statistics from the cache preload manager.
type CachePreloadMetrics struct {
	TasksScheduled int64         `json:"tasks_scheduled"`
	TasksCompleted int64         `json:"tasks_completed"`
	TasksFailed    int64         `json:"tasks_failed"`
	TasksCancelled int64         `json:"tasks_cancelled"`
	TasksSkipped   int64         `json:"tasks_skipped"`
	TotalDuration  time.Duration `json:"total_duration"`
	IsEnabled      bool          `json:"is_enabled"`
}

// CachePreloadSnapshot holds a snapshot of cache preload metrics.
type CachePreloadSnapshot struct {
	TasksScheduled int64
	TasksCompleted int64
	TasksFailed    int64
	TasksCancelled int64
	TasksSkipped   int64
	TotalDuration  time.Duration
}

// CachePreloadSource provides metrics from the cache preload manager.
type CachePreloadSource interface {
	GetMetrics() CachePreloadSnapshot
	IsEnabled() bool
}

// FileProcessingMetrics holds statistics from the file processor.
type FileProcessingMetrics struct {
	TotalFound      uint64 `json:"total_found"`
	AlreadyExisting uint64 `json:"already_existing"`
	NewlyInserted   uint64 `json:"newly_inserted"`
	SkippedInvalid  uint64 `json:"skipped_invalid"`
	InFlight        int64  `json:"in_flight"`
}

// FileProcessorSource provides metrics from the file processor.
type FileProcessorSource interface {
	GetStats() FileProcessingMetrics
}

// ModuleStatus represents the status of a module.
type ModuleStatus struct {
	Name          string    `json:"name"`
	Status        string    `json:"status"` // "active", "idle", "recent"
	LastActiveAt  time.Time `json:"last_active_at"`
	ActivityCount int64     `json:"activity_count"`
}

// RuntimeMetrics holds Go runtime statistics.
type RuntimeMetrics struct {
	NumGoroutine    int           `json:"num_goroutine"`
	NumCPU          int           `json:"num_cpu"`
	NumCgoCall      int64         `json:"num_cgo_call"`
	MemAlloc        uint64        `json:"mem_alloc"`
	MemTotalAlloc   uint64        `json:"mem_total_alloc"`
	MemSys          uint64        `json:"mem_sys"`
	MemHeapAlloc    uint64        `json:"mem_heap_alloc"`
	MemHeapSys      uint64        `json:"mem_heap_sys"`
	MemHeapInuse    uint64        `json:"mem_heap_inuse"`
	MemHeapReleased uint64        `json:"mem_heap_released"`
	MemHeapObjects  uint64        `json:"mem_heap_objects"`
	GCSys           uint64        `json:"gc_sys"`
	LastGC          time.Time     `json:"last_gc"`
	NextGC          uint64        `json:"next_gc"`
	GCCPUFraction   float64       `json:"gc_cpu_fraction"`
	Uptime          time.Duration `json:"uptime"`
}

// HTTPCacheMetrics holds HTTP cache statistics.
type HTTPCacheMetrics struct {
	Enabled      bool  `json:"enabled"`
	SizeBytes    int64 `json:"size_bytes"`
	MaxEntrySize int64 `json:"max_entry_size"`
	MaxTotalSize int64 `json:"max_total_size"`
	EntryCount   int64 `json:"entry_count"`
}

// HTTPCacheSource provides metrics from the HTTP cache.
type HTTPCacheSource interface {
	IsEnabled() bool
	GetSizeBytes() int64
	GetEntryCount() int64
	GetConfig() HTTPCacheConfig
}

// HTTPCacheConfig holds HTTP cache configuration for metrics.
type HTTPCacheConfig struct {
	MaxEntrySize int64
	MaxTotalSize int64
}

// Snapshot holds a complete snapshot of all metrics.
type Snapshot struct {
	Timestamp      time.Time             `json:"timestamp"`
	Runtime        RuntimeMetrics        `json:"runtime"`
	WriteBatcher   WriteBatcherMetrics   `json:"writebatcher"`
	WorkerPool     WorkerPoolMetrics     `json:"worker_pool"`
	CachePreload   CachePreloadMetrics   `json:"cache_preload"`
	FileProcessing FileProcessingMetrics `json:"file_processing"`
	HTTPCache      HTTPCacheMetrics      `json:"http_cache"`
	Modules        []ModuleStatus        `json:"modules"`
	QueueLength    int                   `json:"queue_length"`
	QueueCapacity  int                   `json:"queue_capacity"`
}

// Collector aggregates metrics from various sources.
type Collector struct {
	mu               sync.RWMutex
	startTime        time.Time
	writeBatcher     WriteBatcherSource
	workerPool       WorkerPoolSource
	cachePreload     CachePreloadSource
	fileProcessor    FileProcessorSource
	httpCache        HTTPCacheSource
	queueLength      func() int
	queueCapacity    int
	moduleActivities map[string]*moduleActivity
}

// moduleActivity tracks activity for a module.
type moduleActivity struct {
	lastActiveAt  time.Time
	activityCount int64
	isActive      bool
}

// NewCollector creates a new metrics collector.
func NewCollector() *Collector {
	return &Collector{
		startTime:        time.Now(),
		moduleActivities: make(map[string]*moduleActivity),
	}
}

// SetWriteBatcher sets the WriteBatcher source.
func (c *Collector) SetWriteBatcher(src WriteBatcherSource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeBatcher = src
}

// SetWorkerPool sets the WorkerPool source.
func (c *Collector) SetWorkerPool(src WorkerPoolSource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workerPool = src
}

// SetCachePreload sets the CachePreload source.
func (c *Collector) SetCachePreload(src CachePreloadSource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cachePreload = src
}

// SetFileProcessor sets the FileProcessor source.
func (c *Collector) SetFileProcessor(src FileProcessorSource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fileProcessor = src
}

// SetHTTPCache sets the HTTP cache source.
func (c *Collector) SetHTTPCache(src HTTPCacheSource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.httpCache = src
}

// SetQueueInfo sets the queue information functions.
func (c *Collector) SetQueueInfo(length func() int, capacity int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queueLength = length
	c.queueCapacity = capacity
}

// RecordModuleActivity records activity for a module.
func (c *Collector) RecordModuleActivity(name string, isActive bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.moduleActivities[name] == nil {
		c.moduleActivities[name] = &moduleActivity{}
	}

	activity := c.moduleActivities[name]
	activity.lastActiveAt = time.Now()
	activity.activityCount++
	activity.isActive = isActive
}

// GetModuleStatuses returns the current status of all tracked modules.
func (c *Collector) GetModuleStatuses() []ModuleStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	statuses := make([]ModuleStatus, 0, len(c.moduleActivities))

	for name, activity := range c.moduleActivities {
		status := ModuleStatus{
			Name:          name,
			LastActiveAt:  activity.lastActiveAt,
			ActivityCount: activity.activityCount,
		}

		switch {
		case activity.isActive:
			status.Status = "active"
		case now.Sub(activity.lastActiveAt) < time.Hour:
			status.Status = "recent"
		default:
			status.Status = "idle"
		}

		statuses = append(statuses, status)
	}

	return statuses
}

// Collect gathers all metrics into a snapshot.
func (c *Collector) Collect(ctx context.Context) Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snapshot := Snapshot{
		Timestamp: time.Now(),
		Runtime:   c.collectRuntime(),
	}

	if c.writeBatcher != nil {
		stats := c.writeBatcher.GetStats()
		snapshot.WriteBatcher = WriteBatcherMetrics{
			PendingCount:  c.writeBatcher.PendingCount(),
			ChannelSize:   stats.ChannelSize,
			MaxBatchSize:  stats.MaxBatchSize,
			FlushInterval: stats.FlushInterval.String(),
			IsClosed:      stats.IsClosed,
			TotalFlushed:  stats.TotalFlushed,
			TotalErrors:   stats.TotalErrors,
		}
	}

	if c.workerPool != nil {
		stats := c.workerPool.GetStats()
		// Convert WorkerPoolStats to WorkerPoolMetrics (same field types)
		snapshot.WorkerPool = WorkerPoolMetrics(stats)
	}

	if c.cachePreload != nil {
		cpSnapshot := c.cachePreload.GetMetrics()
		snapshot.CachePreload = CachePreloadMetrics{
			TasksScheduled: cpSnapshot.TasksScheduled,
			TasksCompleted: cpSnapshot.TasksCompleted,
			TasksFailed:    cpSnapshot.TasksFailed,
			TasksCancelled: cpSnapshot.TasksCancelled,
			TasksSkipped:   cpSnapshot.TasksSkipped,
			TotalDuration:  cpSnapshot.TotalDuration,
			IsEnabled:      c.cachePreload.IsEnabled(),
		}
	}

	if c.fileProcessor != nil {
		snapshot.FileProcessing = c.fileProcessor.GetStats()
	}

	if c.httpCache != nil {
		cfg := c.httpCache.GetConfig()
		snapshot.HTTPCache = HTTPCacheMetrics{
			Enabled:      c.httpCache.IsEnabled(),
			SizeBytes:    c.httpCache.GetSizeBytes(),
			EntryCount:   c.httpCache.GetEntryCount(),
			MaxEntrySize: cfg.MaxEntrySize,
			MaxTotalSize: cfg.MaxTotalSize,
		}
	}

	if c.queueLength != nil {
		snapshot.QueueLength = c.queueLength()
		snapshot.QueueCapacity = c.queueCapacity
	}

	snapshot.Modules = c.GetModuleStatuses()

	return snapshot
}

// collectRuntime gathers Go runtime statistics.
func (c *Collector) collectRuntime() RuntimeMetrics {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	lastGC := time.Time{}
	if m.LastGC > 0 {
		lastGC = time.Unix(0, int64(m.LastGC))
	}

	return RuntimeMetrics{
		NumGoroutine:    runtime.NumGoroutine(),
		NumCPU:          runtime.NumCPU(),
		NumCgoCall:      runtime.NumCgoCall(),
		MemAlloc:        m.Alloc,
		MemTotalAlloc:   m.TotalAlloc,
		MemSys:          m.Sys,
		MemHeapAlloc:    m.HeapAlloc,
		MemHeapSys:      m.HeapSys,
		MemHeapInuse:    m.HeapInuse,
		MemHeapReleased: m.HeapReleased,
		MemHeapObjects:  m.HeapObjects,
		GCSys:           m.GCSys,
		LastGC:          lastGC,
		NextGC:          m.NextGC,
		GCCPUFraction:   m.GCCPUFraction,
		Uptime:          time.Since(c.startTime),
	}
}

// FormatBytes returns a human-readable byte string using IEC units (e.g., "1.5 KiB").
func FormatBytes(b uint64) string {
	if b < 1024 {
		return humanize.Comma(b).String() + " B"
	}
	return humanize.IBytes(int64(b)).WithPrecision(1).String()
}

// FormatBytesInt64 is like FormatBytes but takes an int64.
func FormatBytesInt64(b int64) string {
	if b < 0 {
		return "N/A"
	}
	return FormatBytes(uint64(b))
}

// FormatDuration returns a human-readable duration string.
func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	if d < time.Hour {
		return d.Round(time.Second).String()
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh %dm", hours, minutes)
}
