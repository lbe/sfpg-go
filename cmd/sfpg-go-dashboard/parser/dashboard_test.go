package parser

import (
	"strings"
	"testing"
)

// TestParseDashboard extracts metrics from dashboard HTML using ID-based extraction.
// Values are intentionally distinct to catch any field misattribution bugs.
func TestParseDashboard(t *testing.T) {
	// NOTE: Values are intentionally DIFFERENT across sections to catch bugs
	// where the parser might extract the wrong field (e.g., "Completed" from
	// Worker Pool instead of "Completed" from Cache Preload).
	html := `<!DOCTYPE html>
<html>
<body>
<div id="dashboard-container">
	<div id="last-updated">22:30:00</div>
	<div class="card">
		<div class="card-title">discovery</div>
		<div class="text-sm">discovery</div>
		<span class="badge">active</span>
	</div>
	<div class="card">
		<div class="card-title">cache_preload</div>
		<div class="text-sm">cache_preload</div>
		<span class="badge">active</span>
	</div>
	<!-- Memory Stats -->
	<div>
		<div class="stat-title">Allocated</div>
		<div id="mem-allocated" class="stat-value">15.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Heap In Use</div>
		<div id="mem-heap-in-use" class="stat-value">20.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Heap Released</div>
		<div id="mem-heap-released" class="stat-value">5.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Heap Objects</div>
		<div id="mem-heap-objects" class="stat-value">50,000</div>
	</div>
	<!-- Runtime Stats -->
	<div>
		<div class="stat-title">Goroutines</div>
		<div id="runtime-goroutines" class="stat-value">18</div>
	</div>
	<div>
		<div class="stat-title">CPU Count</div>
		<div id="runtime-cpu-count" class="stat-value">24</div>
	</div>
	<div>
		<div class="stat-title">Next GC</div>
		<div id="runtime-next-gc" class="stat-value">16.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Uptime</div>
		<div id="runtime-uptime" class="stat-value">1m30s</div>
	</div>
	<!-- Write Batcher Stats -->
	<div>
		<div class="stat-title">Pending</div>
		<div id="wb-pending" class="stat-value">0</div>
		<div id="wb-channel-size-desc" class="stat-desc">of 4,096</div>
	</div>
	<div>
		<div class="stat-title">Total Flushed</div>
		<div id="wb-flushed" class="stat-value">100</div>
	</div>
	<div>
		<div class="stat-title">Errors</div>
		<div id="wb-errors" class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Batch Size</div>
		<div id="wb-batch-size" class="stat-value">10,000</div>
	</div>
	<!-- Worker Pool Stats -->
	<div>
		<div class="stat-title">Running Workers</div>
		<div id="wp-running" class="stat-value">1/22</div>
	</div>
	<div>
		<div class="stat-title">Completed Tasks</div>
		<div id="wp-completed" class="stat-value">3,315</div>
	</div>
	<div>
		<div class="stat-title">Successful</div>
		<div id="wp-successful" class="stat-value">3,315</div>
	</div>
	<div>
		<div class="stat-title">Failed</div>
		<div id="wp-failed" class="stat-value">0</div>
	</div>
	<!-- Queue Stats -->
	<div>
		<div class="stat-title">Queued Items</div>
		<div id="queue-queued" class="stat-value">0</div>
		<div id="queue-capacity-desc" class="stat-desc">of 10,000 items</div>
	</div>
	<div>
		<div class="stat-title">Utilization</div>
		<div id="queue-utilization" class="stat-value">0%</div>
	</div>
	<div>
		<div class="stat-title">Available</div>
		<div id="queue-available" class="stat-value">10,000</div>
	</div>
	<!-- File Processing Stats - DISTINCT value: 3,314 -->
	<div>
		<div class="stat-title">Total Found</div>
		<div id="fp-total" class="stat-value">3,314</div>
	</div>
	<div>
		<div class="stat-title">Existing</div>
		<div id="fp-existing" class="stat-value">3,314</div>
	</div>
	<div>
		<div class="stat-title">New</div>
		<div id="fp-new" class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Invalid</div>
		<div id="fp-invalid" class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">In Flight</div>
		<div id="fp-inflight" class="stat-value">0</div>
	</div>
	<!-- Cache Preload - DISTINCT value: 2,500 for Completed, 50 for Skipped -->
	<div id="card-cache-preload" class="card-title">Cache Preload</div>
	<span id="preload-status" class="badge">Enabled</span>
	<div>
		<div class="stat-title">Scheduled</div>
		<div id="preload-scheduled" class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Completed</div>
		<div id="preload-completed" class="stat-value">2,500</div>
	</div>
	<div>
		<div class="stat-title">Failed</div>
		<div id="preload-failed" class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Skipped</div>
		<div id="preload-skipped" class="stat-value">50</div>
	</div>
	<!-- Cache Batch Load - DISTINCT value: 25 for Skipped -->
	<div id="card-cache-batch-load" class="card-title">Cache Batch Load</div>
	<span id="batch-status" class="badge">Idle</span>
	<div>
		<div class="stat-title">Progress</div>
		<div id="batch-progress" class="stat-value">0 / 0</div>
		<div id="batch-total-desc" class="stat-desc">100 total</div>
	</div>
	<div>
		<div class="stat-title">Failed</div>
		<div id="batch-failed" class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Skipped</div>
		<div id="batch-skipped" class="stat-value">25</div>
	</div>
	<!-- HTTP Cache - DISTINCT value: 5.0 MiB for Size -->
	<div id="card-http-cache" class="card-title">HTTP Cache</div>
	<span id="http-status" class="badge">Enabled</span>
	<div>
		<div class="stat-title">Entries</div>
		<div id="http-entries" class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Size</div>
		<div id="http-size" class="stat-value">5.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Max Total</div>
		<div id="http-max-total" class="stat-value">500.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Max Entry</div>
		<div id="http-max-entry" class="stat-value">10.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Utilization</div>
		<div id="http-utilization" class="stat-value">0%</div>
	</div>
</div>
</body>
</html>`

	metrics, err := ParseDashboard(strings.NewReader(html))
	if err != nil {
		t.Fatalf("ParseDashboard failed: %v", err)
	}

	// Verify last updated
	if metrics.LastUpdated != "22:30:00" {
		t.Errorf("LastUpdated = %q, want %q", metrics.LastUpdated, "22:30:00")
	}

	// Verify modules
	if len(metrics.Modules) != 2 {
		t.Fatalf("len(Modules) = %d, want 2", len(metrics.Modules))
	}
	if metrics.Modules[0].Name != "discovery" {
		t.Errorf("Modules[0].Name = %q, want %q", metrics.Modules[0].Name, "discovery")
	}
	if metrics.Modules[0].Status != "active" {
		t.Errorf("Modules[0].Status = %q, want %q", metrics.Modules[0].Status, "active")
	}

	// Verify memory stats
	if metrics.Memory.Allocated != "15.0 MiB" {
		t.Errorf("Memory.Allocated = %q, want %q", metrics.Memory.Allocated, "15.0 MiB")
	}
	if metrics.Memory.HeapInUse != "20.0 MiB" {
		t.Errorf("Memory.HeapInUse = %q, want %q", metrics.Memory.HeapInUse, "20.0 MiB")
	}
	if metrics.Memory.HeapReleased != "5.0 MiB" {
		t.Errorf("Memory.HeapReleased = %q, want %q", metrics.Memory.HeapReleased, "5.0 MiB")
	}
	if metrics.Memory.HeapObjects != "50,000" {
		t.Errorf("Memory.HeapObjects = %q, want %q", metrics.Memory.HeapObjects, "50,000")
	}

	// Verify runtime stats
	if metrics.Runtime.Goroutines != "18" {
		t.Errorf("Runtime.Goroutines = %q, want %q", metrics.Runtime.Goroutines, "18")
	}
	if metrics.Runtime.CPUCount != "24" {
		t.Errorf("Runtime.CPUCount = %q, want %q", metrics.Runtime.CPUCount, "24")
	}
	if metrics.Runtime.NextGC != "16.0 MiB" {
		t.Errorf("Runtime.NextGC = %q, want %q", metrics.Runtime.NextGC, "16.0 MiB")
	}
	if metrics.Runtime.Uptime != "1m30s" {
		t.Errorf("Runtime.Uptime = %q, want %q", metrics.Runtime.Uptime, "1m30s")
	}

	// Verify write batcher
	if metrics.WriteBatcher.Pending != "0" {
		t.Errorf("WriteBatcher.Pending = %q, want %q", metrics.WriteBatcher.Pending, "0")
	}
	if metrics.WriteBatcher.ChannelSize != "4,096" {
		t.Errorf("WriteBatcher.ChannelSize = %q, want %q", metrics.WriteBatcher.ChannelSize, "4,096")
	}
	if metrics.WriteBatcher.TotalFlushed != "100" {
		t.Errorf("WriteBatcher.TotalFlushed = %q, want %q", metrics.WriteBatcher.TotalFlushed, "100")
	}
	if metrics.WriteBatcher.TotalErrors != "0" {
		t.Errorf("WriteBatcher.TotalErrors = %q, want %q", metrics.WriteBatcher.TotalErrors, "0")
	}
	if metrics.WriteBatcher.BatchSize != "10,000" {
		t.Errorf("WriteBatcher.BatchSize = %q, want %q", metrics.WriteBatcher.BatchSize, "10,000")
	}

	// Verify worker pool
	if metrics.WorkerPool.RunningWorkers != "1" {
		t.Errorf("WorkerPool.RunningWorkers = %q, want %q", metrics.WorkerPool.RunningWorkers, "1")
	}
	if metrics.WorkerPool.MaxWorkers != "22" {
		t.Errorf("WorkerPool.MaxWorkers = %q, want %q", metrics.WorkerPool.MaxWorkers, "22")
	}
	if metrics.WorkerPool.CompletedTasks != "3,315" {
		t.Errorf("WorkerPool.CompletedTasks = %q, want %q", metrics.WorkerPool.CompletedTasks, "3,315")
	}
	if metrics.WorkerPool.Successful != "3,315" {
		t.Errorf("WorkerPool.Successful = %q, want %q", metrics.WorkerPool.Successful, "3,315")
	}
	if metrics.WorkerPool.Failed != "0" {
		t.Errorf("WorkerPool.Failed = %q, want %q", metrics.WorkerPool.Failed, "0")
	}

	// Verify queue
	if metrics.Queue.Queued != "0" {
		t.Errorf("Queue.Queued = %q, want %q", metrics.Queue.Queued, "0")
	}
	if metrics.Queue.Capacity != "10,000" {
		t.Errorf("Queue.Capacity = %q, want %q", metrics.Queue.Capacity, "10,000")
	}
	if metrics.Queue.Utilization != "0%" {
		t.Errorf("Queue.Utilization = %q, want %q", metrics.Queue.Utilization, "0%")
	}
	if metrics.Queue.Available != "10,000" {
		t.Errorf("Queue.Available = %q, want %q", metrics.Queue.Available, "10,000")
	}

	// Verify file processing - DISTINCT from Worker Pool Completed
	if metrics.FileProcessing.TotalFound != "3,314" {
		t.Errorf("FileProcessing.TotalFound = %q, want %q", metrics.FileProcessing.TotalFound, "3,314")
	}
	if metrics.FileProcessing.Existing != "3,314" {
		t.Errorf("FileProcessing.Existing = %q, want %q", metrics.FileProcessing.Existing, "3,314")
	}
	if metrics.FileProcessing.New != "0" {
		t.Errorf("FileProcessing.New = %q, want %q", metrics.FileProcessing.New, "0")
	}
	if metrics.FileProcessing.Invalid != "0" {
		t.Errorf("FileProcessing.Invalid = %q, want %q", metrics.FileProcessing.Invalid, "0")
	}
	if metrics.FileProcessing.InFlight != "0" {
		t.Errorf("FileProcessing.InFlight = %q, want %q", metrics.FileProcessing.InFlight, "0")
	}

	// Verify cache preload - CRITICAL: Must be 2,500 NOT 3,315 (Worker Pool Completed)
	if !metrics.CachePreload.IsEnabled {
		t.Error("CachePreload.IsEnabled = false, want true")
	}
	if metrics.CachePreload.Scheduled != "0" {
		t.Errorf("CachePreload.Scheduled = %q, want %q", metrics.CachePreload.Scheduled, "0")
	}
	// BUG FIX: This was extracting Worker Pool Completed (3,315) instead of Cache Preload Completed (2,500)
	if metrics.CachePreload.Completed != "2,500" {
		t.Errorf("CachePreload.Completed = %q, want %q (BUG: was extracting Worker Pool Completed)", metrics.CachePreload.Completed, "2,500")
	}
	if metrics.CachePreload.Failed != "0" {
		t.Errorf("CachePreload.Failed = %q, want %q", metrics.CachePreload.Failed, "0")
	}
	// BUG FIX: This was extracting Cache Batch Load Skipped (25) instead of Cache Preload Skipped (50)
	if metrics.CachePreload.Skipped != "50" {
		t.Errorf("CachePreload.Skipped = %q, want %q (BUG: was extracting Cache Batch Load Skipped)", metrics.CachePreload.Skipped, "50")
	}

	// Verify cache batch load
	if metrics.CacheBatchLoad.IsRunning {
		t.Error("CacheBatchLoad.IsRunning = true, want false")
	}
	if metrics.CacheBatchLoad.Progress == "" {
		t.Error("CacheBatchLoad.Progress is empty")
	}
	if metrics.CacheBatchLoad.Progress != "0/0" {
		t.Errorf("CacheBatchLoad.Progress = %q, want %q", metrics.CacheBatchLoad.Progress, "0/0")
	}
	if metrics.CacheBatchLoad.Total != "100" {
		t.Errorf("CacheBatchLoad.Total = %q, want %q", metrics.CacheBatchLoad.Total, "100")
	}
	if metrics.CacheBatchLoad.Failed != "0" {
		t.Errorf("CacheBatchLoad.Failed = %q, want %q", metrics.CacheBatchLoad.Failed, "0")
	}
	// Cache Batch Load Skipped should be 25, distinct from Cache Preload Skipped (50)
	if metrics.CacheBatchLoad.Skipped != "25" {
		t.Errorf("CacheBatchLoad.Skipped = %q, want %q", metrics.CacheBatchLoad.Skipped, "25")
	}

	// Verify HTTP cache
	if !metrics.HTTPCache.Enabled {
		t.Error("HTTPCache.Enabled = false, want true")
	}
	if metrics.HTTPCache.Entries != "0" {
		t.Errorf("HTTPCache.Entries = %q, want %q", metrics.HTTPCache.Entries, "0")
	}
	// BUG FIX: This was extracting Write Batcher Batch Size (10,000) instead of HTTP Cache Size (5.0 MiB)
	if metrics.HTTPCache.Size != "5.0 MiB" {
		t.Errorf("HTTPCache.Size = %q, want %q (BUG: was extracting Write Batcher Batch Size)", metrics.HTTPCache.Size, "5.0 MiB")
	}
	if metrics.HTTPCache.MaxTotal != "500.0 MiB" {
		t.Errorf("HTTPCache.MaxTotal = %q, want %q", metrics.HTTPCache.MaxTotal, "500.0 MiB")
	}
	if metrics.HTTPCache.MaxEntry != "10.0 MiB" {
		t.Errorf("HTTPCache.MaxEntry = %q, want %q", metrics.HTTPCache.MaxEntry, "10.0 MiB")
	}
	if metrics.HTTPCache.Utilization != "0%" {
		t.Errorf("HTTPCache.Utilization = %q, want %q", metrics.HTTPCache.Utilization, "0%")
	}
}

// TestParseDashboardNotFound returns error when dashboard container missing
func TestParseDashboardNotFound(t *testing.T) {
	html := `<!DOCTYPE html><html><body><div id="other">content</div></body></html>`

	_, err := ParseDashboard(strings.NewReader(html))
	if err != ErrDashboardNotFound {
		t.Errorf("ParseDashboard error = %v, want %v", err, ErrDashboardNotFound)
	}
}

// TestParseProgressWithWhitespace normalizes progress values with newlines/spaces
func TestParseProgressWithWhitespace(t *testing.T) {
	// This test verifies the progress value normalization
	// The HTML may have "0 /\n                0" which needs to be normalized to "0/0"
	html := `<!DOCTYPE html>
<html>
<body>
<div id="dashboard-container">
	<div id="last-updated">22:30:00</div>
	<div id="card-cache-batch-load" class="card-title">Cache Batch Load</div>
	<span id="batch-status" class="badge">Idle</span>
	<div>
		<div class="stat-title">Progress</div>
		<div id="batch-progress" class="stat-value">0 /
                0</div>
	</div>
</div>
</body>
</html>`

	metrics, err := ParseDashboard(strings.NewReader(html))
	if err != nil {
		t.Fatalf("ParseDashboard failed: %v", err)
	}

	// Progress should be normalized to "0/0" without extra whitespace
	progress := metrics.CacheBatchLoad.Progress
	if strings.Contains(progress, "\n") {
		t.Errorf("Progress contains newline: %q", progress)
	}
	if strings.Contains(progress, "  ") {
		t.Errorf("Progress contains double spaces: %q", progress)
	}
	if progress != "0/0" {
		t.Errorf("Progress = %q, want %q", progress, "0/0")
	}
}

// TestParseDashboardDisabledStates tests parsing when features are disabled
func TestParseDashboardDisabledStates(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<body>
<div id="dashboard-container">
	<div id="last-updated">22:30:00</div>
	<!-- Cache Preload Disabled -->
	<div id="card-cache-preload" class="card-title">Cache Preload</div>
	<span id="preload-status" class="badge">Disabled</span>
	<div>
		<div class="stat-title">Scheduled</div>
		<div id="preload-scheduled" class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Completed</div>
		<div id="preload-completed" class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Failed</div>
		<div id="preload-failed" class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Skipped</div>
		<div id="preload-skipped" class="stat-value">0</div>
	</div>
	<!-- Cache Batch Load Running -->
	<div id="card-cache-batch-load" class="card-title">Cache Batch Load</div>
	<span id="batch-status" class="badge">Running</span>
	<div>
		<div class="stat-title">Progress</div>
		<div id="batch-progress" class="stat-value">50/100</div>
	</div>
	<div>
		<div class="stat-title">Failed</div>
		<div id="batch-failed" class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Skipped</div>
		<div id="batch-skipped" class="stat-value">0</div>
	</div>
	<!-- HTTP Cache Disabled -->
	<div id="card-http-cache" class="card-title">HTTP Cache</div>
	<span id="http-status" class="badge">Disabled</span>
	<div>
		<div class="stat-title">Entries</div>
		<div id="http-entries" class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Size</div>
		<div id="http-size" class="stat-value">0 B</div>
	</div>
	<div>
		<div class="stat-title">Max Total</div>
		<div id="http-max-total" class="stat-value">0 B</div>
	</div>
	<div>
		<div class="stat-title">Max Entry</div>
		<div id="http-max-entry" class="stat-value">0 B</div>
	</div>
	<div>
		<div class="stat-title">Utilization</div>
		<div id="http-utilization" class="stat-value">0%</div>
	</div>
</div>
</body>
</html>`

	metrics, err := ParseDashboard(strings.NewReader(html))
	if err != nil {
		t.Fatalf("ParseDashboard failed: %v", err)
	}

	// Verify disabled states
	if metrics.CachePreload.IsEnabled {
		t.Error("CachePreload.IsEnabled = true, want false")
	}
	if !metrics.CacheBatchLoad.IsRunning {
		t.Error("CacheBatchLoad.IsRunning = false, want true")
	}
	if metrics.HTTPCache.Enabled {
		t.Error("HTTPCache.Enabled = true, want false")
	}

	// Verify progress parsing
	if metrics.CacheBatchLoad.Progress != "50/100" {
		t.Errorf("CacheBatchLoad.Progress = %q, want %q", metrics.CacheBatchLoad.Progress, "50/100")
	}
}
