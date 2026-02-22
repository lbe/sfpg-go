package metrics

import (
	"context"
	"testing"
	"time"
)

// mockWriteBatcher is a mock implementation of WriteBatcherSource for testing.
type mockWriteBatcher struct {
	pendingCount int64
	stats        WriteBatcherStats
}

func (m *mockWriteBatcher) PendingCount() int64 {
	return m.pendingCount
}

func (m *mockWriteBatcher) GetStats() WriteBatcherStats {
	return m.stats
}

// mockWorkerPool is a mock implementation of WorkerPoolSource for testing.
type mockWorkerPool struct {
	stats WorkerPoolStats
}

func (m *mockWorkerPool) GetStats() WorkerPoolStats {
	return m.stats
}

// mockCachePreload is a mock implementation of CachePreloadSource for testing.
type mockCachePreload struct {
	metrics   CachePreloadSnapshot
	isEnabled bool
}

func (m *mockCachePreload) GetMetrics() CachePreloadSnapshot {
	return m.metrics
}

func (m *mockCachePreload) IsEnabled() bool {
	return m.isEnabled
}

// mockFileProcessor is a mock implementation of FileProcessorSource for testing.
type mockFileProcessor struct {
	stats FileProcessingMetrics
}

func (m *mockFileProcessor) GetStats() FileProcessingMetrics {
	return m.stats
}

// mockHTTPCache is a mock implementation of HTTPCacheSource for testing.
type mockHTTPCache struct {
	enabled   bool
	sizeBytes int64
	config    HTTPCacheConfig
}

func (m *mockHTTPCache) IsEnabled() bool {
	return m.enabled
}

func (m *mockHTTPCache) GetSizeBytes() int64 {
	return m.sizeBytes
}

func (m *mockHTTPCache) GetConfig() HTTPCacheConfig {
	return m.config
}

func (m *mockHTTPCache) GetEntryCount() int64 {
	return 0
}

func TestNewCollector(t *testing.T) {
	c := NewCollector()
	if c == nil {
		t.Fatal("NewCollector() returned nil")
	}
	if c.moduleActivities == nil {
		t.Error("moduleActivities map not initialized")
	}
}

func TestCollector_SetWriteBatcher(t *testing.T) {
	c := NewCollector()
	mock := &mockWriteBatcher{pendingCount: 42}

	c.SetWriteBatcher(mock)

	if c.writeBatcher != mock {
		t.Error("WriteBatcher not set correctly")
	}
}

func TestCollector_SetWorkerPool(t *testing.T) {
	c := NewCollector()
	mock := &mockWorkerPool{stats: WorkerPoolStats{RunningWorkers: 5}}

	c.SetWorkerPool(mock)

	if c.workerPool != mock {
		t.Error("WorkerPool not set correctly")
	}
}

func TestCollector_SetCachePreload(t *testing.T) {
	c := NewCollector()
	mock := &mockCachePreload{isEnabled: true}

	c.SetCachePreload(mock)

	if c.cachePreload != mock {
		t.Error("CachePreload not set correctly")
	}
}

func TestCollector_SetFileProcessor(t *testing.T) {
	c := NewCollector()
	mock := &mockFileProcessor{stats: FileProcessingMetrics{TotalFound: 100}}

	c.SetFileProcessor(mock)

	if c.fileProcessor != mock {
		t.Error("FileProcessor not set correctly")
	}
}

func TestCollector_SetHTTPCache(t *testing.T) {
	c := NewCollector()
	mock := &mockHTTPCache{enabled: true, sizeBytes: 1024}

	c.SetHTTPCache(mock)

	if c.httpCache != mock {
		t.Error("HTTPCache not set correctly")
	}
}

func TestCollector_SetQueueInfo(t *testing.T) {
	c := NewCollector()
	lengthFunc := func() int { return 10 }
	capacity := 100

	c.SetQueueInfo(lengthFunc, capacity)

	if c.queueLength == nil {
		t.Error("queueLength not set")
	}
	if c.queueCapacity != capacity {
		t.Errorf("queueCapacity = %d, want %d", c.queueCapacity, capacity)
	}
}

func TestCollector_RecordModuleActivity(t *testing.T) {
	c := NewCollector()

	// Record activity for a module
	c.RecordModuleActivity("discovery", true)

	// Check module was recorded
	if _, exists := c.moduleActivities["discovery"]; !exists {
		t.Error("module activity not recorded")
	}

	// Record more activity
	c.RecordModuleActivity("discovery", false)

	activity := c.moduleActivities["discovery"]
	if activity.activityCount != 2 {
		t.Errorf("activityCount = %d, want 2", activity.activityCount)
	}
	if activity.isActive != false {
		t.Error("isActive should be false")
	}
}

func TestCollector_GetModuleStatuses(t *testing.T) {
	c := NewCollector()

	// Record activities with different states
	c.RecordModuleActivity("discovery", true)
	c.RecordModuleActivity("cache_preload", false)
	c.RecordModuleActivity("old_module", false)

	// Manually set old_module's last active time to more than an hour ago
	c.moduleActivities["old_module"].lastActiveAt = time.Now().Add(-2 * time.Hour)

	statuses := c.GetModuleStatuses()

	if len(statuses) != 3 {
		t.Fatalf("expected 3 module statuses, got %d", len(statuses))
	}

	// Check status mapping
	statusMap := make(map[string]string)
	for _, s := range statuses {
		statusMap[s.Name] = s.Status
	}

	if statusMap["discovery"] != "active" {
		t.Errorf("discovery status = %s, want active", statusMap["discovery"])
	}
	if statusMap["cache_preload"] != "recent" {
		t.Errorf("cache_preload status = %s, want recent", statusMap["cache_preload"])
	}
	if statusMap["old_module"] != "idle" {
		t.Errorf("old_module status = %s, want idle", statusMap["old_module"])
	}
}

func TestCollector_Collect(t *testing.T) {
	c := NewCollector()
	ctx := context.Background()

	// Set up mocks
	wb := &mockWriteBatcher{
		pendingCount: 10,
		stats: WriteBatcherStats{
			ChannelSize:   100,
			MaxBatchSize:  50,
			FlushInterval: 200 * time.Millisecond,
			IsClosed:      false,
			TotalFlushed:  1000,
			TotalErrors:   5,
		},
	}
	c.SetWriteBatcher(wb)

	wp := &mockWorkerPool{
		stats: WorkerPoolStats{
			RunningWorkers:  3,
			SubmittedTasks:  100,
			WaitingTasks:    5,
			SuccessfulTasks: 95,
			FailedTasks:     5,
			CompletedTasks:  100,
			DroppedTasks:    0,
			MaxWorkers:      10,
			MinWorkers:      2,
		},
	}
	c.SetWorkerPool(wp)

	cp := &mockCachePreload{
		metrics: CachePreloadSnapshot{
			TasksScheduled: 50,
			TasksCompleted: 45,
			TasksFailed:    3,
			TasksCancelled: 2,
			TasksSkipped:   10,
			TotalDuration:  5 * time.Second,
		},
		isEnabled: true,
	}
	c.SetCachePreload(cp)

	fp := &mockFileProcessor{
		stats: FileProcessingMetrics{
			TotalFound:      1000,
			AlreadyExisting: 800,
			NewlyInserted:   150,
			SkippedInvalid:  50,
			InFlight:        5,
		},
	}
	c.SetFileProcessor(fp)

	hc := &mockHTTPCache{
		enabled:   true,
		sizeBytes: 1024 * 1024,
		config: HTTPCacheConfig{
			MaxEntrySize: 1024 * 1024,
			MaxTotalSize: 100 * 1024 * 1024,
		},
	}
	c.SetHTTPCache(hc)

	c.SetQueueInfo(func() int { return 25 }, 100)

	// Record some module activity
	c.RecordModuleActivity("discovery", true)

	// Collect metrics
	snapshot := c.Collect(ctx)

	// Verify timestamp is set
	if snapshot.Timestamp.IsZero() {
		t.Error("Timestamp not set")
	}

	// Verify WriteBatcher metrics
	if snapshot.WriteBatcher.PendingCount != 10 {
		t.Errorf("WriteBatcher.PendingCount = %d, want 10", snapshot.WriteBatcher.PendingCount)
	}
	if snapshot.WriteBatcher.ChannelSize != 100 {
		t.Errorf("WriteBatcher.ChannelSize = %d, want 100", snapshot.WriteBatcher.ChannelSize)
	}

	// Verify WorkerPool metrics
	if snapshot.WorkerPool.RunningWorkers != 3 {
		t.Errorf("WorkerPool.RunningWorkers = %d, want 3", snapshot.WorkerPool.RunningWorkers)
	}
	if snapshot.WorkerPool.SubmittedTasks != 100 {
		t.Errorf("WorkerPool.SubmittedTasks = %d, want 100", snapshot.WorkerPool.SubmittedTasks)
	}

	// Verify CachePreload metrics
	if snapshot.CachePreload.TasksScheduled != 50 {
		t.Errorf("CachePreload.TasksScheduled = %d, want 50", snapshot.CachePreload.TasksScheduled)
	}
	if !snapshot.CachePreload.IsEnabled {
		t.Error("CachePreload.IsEnabled should be true")
	}

	// Verify FileProcessing metrics
	if snapshot.FileProcessing.TotalFound != 1000 {
		t.Errorf("FileProcessing.TotalFound = %d, want 1000", snapshot.FileProcessing.TotalFound)
	}

	// Verify HTTPCache metrics
	if !snapshot.HTTPCache.Enabled {
		t.Error("HTTPCache.Enabled should be true")
	}
	if snapshot.HTTPCache.SizeBytes != 1024*1024 {
		t.Errorf("HTTPCache.SizeBytes = %d, want %d", snapshot.HTTPCache.SizeBytes, 1024*1024)
	}

	// Verify queue info
	if snapshot.QueueLength != 25 {
		t.Errorf("QueueLength = %d, want 25", snapshot.QueueLength)
	}
	if snapshot.QueueCapacity != 100 {
		t.Errorf("QueueCapacity = %d, want 100", snapshot.QueueCapacity)
	}

	// Verify modules
	if len(snapshot.Modules) != 1 {
		t.Errorf("len(Modules) = %d, want 1", len(snapshot.Modules))
	}
}

func TestCollector_Collect_RuntimeMetrics(t *testing.T) {
	c := NewCollector()
	ctx := context.Background()

	snapshot := c.Collect(ctx)

	// Runtime metrics should always be present
	if snapshot.Runtime.NumGoroutine < 1 {
		t.Error("NumGoroutine should be at least 1")
	}
	if snapshot.Runtime.NumCPU < 1 {
		t.Error("NumCPU should be at least 1")
	}
	if snapshot.Runtime.Uptime < 0 {
		t.Error("Uptime should be non-negative")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    uint64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
	}

	for _, tt := range tests {
		result := FormatBytes(tt.bytes)
		if result != tt.expected {
			t.Errorf("FormatBytes(%d) = %s, want %s", tt.bytes, result, tt.expected)
		}
	}
}

func TestFormatBytesInt64(t *testing.T) {
	if FormatBytesInt64(-1) != "N/A" {
		t.Error("FormatBytesInt64(-1) should return N/A")
	}
	if FormatBytesInt64(1024) != "1.0 KiB" {
		t.Error("FormatBytesInt64(1024) should return 1.0 KiB")
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{500 * time.Millisecond, "500ms"},
		{5 * time.Second, "5s"},
		{90 * time.Second, "1m30s"},
	}

	for _, tt := range tests {
		result := FormatDuration(tt.d)
		if result != tt.expected {
			t.Errorf("FormatDuration(%v) = %s, want %s", tt.d, result, tt.expected)
		}
	}
}

func TestCollector_Collect_WithNilSources(t *testing.T) {
	c := NewCollector()
	ctx := context.Background()

	// Don't set any sources - should not panic
	snapshot := c.Collect(ctx)

	// Should still have runtime metrics
	if snapshot.Runtime.NumGoroutine == 0 {
		t.Error("Should have runtime metrics even with nil sources")
	}

	// Other metrics should be zero values
	if snapshot.WriteBatcher.PendingCount != 0 {
		t.Error("WriteBatcher should be zero value when source is nil")
	}
}
