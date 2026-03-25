package parser

import (
	"strings"
	"testing"
)

// TestParseDashboard extracts metrics from dashboard HTML
func TestParseDashboard(t *testing.T) {
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
	<div>
		<div class="stat-title">Allocated</div>
		<div class="stat-value">15.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Heap In Use</div>
		<div class="stat-value">20.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Heap Released</div>
		<div class="stat-value">5.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Heap Objects</div>
		<div class="stat-value">50,000</div>
	</div>
	<div>
		<div class="stat-title">Goroutines</div>
		<div class="stat-value">18</div>
	</div>
	<div>
		<div class="stat-title">CPU Count</div>
		<div class="stat-value">24</div>
	</div>
	<div>
		<div class="stat-title">Next GC</div>
		<div class="stat-value">16.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Uptime</div>
		<div class="stat-value">1m30s</div>
	</div>
	<div>
		<div class="stat-title">Pending</div>
		<div class="stat-value">0</div>
		<div class="stat-desc">of 4,096</div>
	</div>
	<div>
		<div class="stat-title">Total Flushed</div>
		<div class="stat-value">100</div>
	</div>
	<div>
		<div class="stat-title">Errors</div>
		<div class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Batch Size</div>
		<div class="stat-value">10,000</div>
	</div>
	<div>
		<div class="stat-title">Running Workers</div>
		<div class="stat-value">1/22</div>
	</div>
	<div>
		<div class="stat-title">Completed Tasks</div>
		<div class="stat-value">3,315</div>
	</div>
	<div>
		<div class="stat-title">Successful</div>
		<div class="stat-value">3,315</div>
	</div>
	<div>
		<div class="stat-title">Failed</div>
		<div class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Queued Items</div>
		<div class="stat-value">0</div>
		<div class="stat-desc">of 10,000 items</div>
	</div>
	<div>
		<div class="stat-title">Utilization</div>
		<div class="stat-value">0%</div>
	</div>
	<div>
		<div class="stat-title">Available</div>
		<div class="stat-value">10,000</div>
	</div>
	<div>
		<div class="stat-title">Total Found</div>
		<div class="stat-value">3,314</div>
	</div>
	<div>
		<div class="stat-title">Existing</div>
		<div class="stat-value">3,314</div>
	</div>
	<div>
		<div class="stat-title">New</div>
		<div class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Invalid</div>
		<div class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">In Flight</div>
		<div class="stat-value">0</div>
	</div>
	<div>
		<div class="card-title">Cache Preload</div>
		<span class="badge">Enabled</span>
	</div>
	<div>
		<div class="stat-title">Scheduled</div>
		<div class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Completed</div>
		<div class="stat-value">3,315</div>
	</div>
	<div>
		<div class="stat-title">Failed</div>
		<div class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Skipped</div>
		<div class="stat-value">0</div>
	</div>
	<div>
		<div class="card-title">Cache Batch Load</div>
		<span class="badge">Idle</span>
	</div>
	<div>
		<div class="stat-title">Progress</div>
		<div class="stat-value">0 / 0</div>
	</div>
	<div>
		<div class="stat-title">Failed</div>
		<div class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Skipped</div>
		<div class="stat-value">0</div>
	</div>
	<div>
		<div class="card-title">HTTP Cache</div>
		<span class="badge">Enabled</span>
	</div>
	<div>
		<div class="stat-title">Entries</div>
		<div class="stat-value">0</div>
	</div>
	<div>
		<div class="stat-title">Size</div>
		<div class="stat-value">0 B</div>
	</div>
	<div>
		<div class="stat-title">Max Total</div>
		<div class="stat-value">500.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Max Entry</div>
		<div class="stat-value">10.0 MiB</div>
	</div>
	<div>
		<div class="stat-title">Utilization</div>
		<div class="stat-value">0%</div>
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

	// Verify runtime stats
	if metrics.Runtime.Goroutines != "18" {
		t.Errorf("Runtime.Goroutines = %q, want %q", metrics.Runtime.Goroutines, "18")
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

	// Verify worker pool
	if metrics.WorkerPool.RunningWorkers != "1" {
		t.Errorf("WorkerPool.RunningWorkers = %q, want %q", metrics.WorkerPool.RunningWorkers, "1")
	}
	if metrics.WorkerPool.MaxWorkers != "22" {
		t.Errorf("WorkerPool.MaxWorkers = %q, want %q", metrics.WorkerPool.MaxWorkers, "22")
	}

	// Verify queue
	if metrics.Queue.Queued != "0" {
		t.Errorf("Queue.Queued = %q, want %q", metrics.Queue.Queued, "0")
	}
	if metrics.Queue.Capacity != "10,000" {
		t.Errorf("Queue.Capacity = %q, want %q", metrics.Queue.Capacity, "10,000")
	}

	// Verify file processing
	if metrics.FileProcessing.TotalFound != "3,314" {
		t.Errorf("FileProcessing.TotalFound = %q, want %q", metrics.FileProcessing.TotalFound, "3,314")
	}

	// Verify cache preload
	if !metrics.CachePreload.IsEnabled {
		t.Error("CachePreload.IsEnabled = false, want true")
	}
	if metrics.CachePreload.Completed != "3,315" {
		t.Errorf("CachePreload.Completed = %q, want %q", metrics.CachePreload.Completed, "3,315")
	}

	// Verify cache batch load
	if metrics.CacheBatchLoad.IsRunning {
		t.Error("CacheBatchLoad.IsRunning = true, want false")
	}
	if metrics.CacheBatchLoad.Progress == "" {
		t.Error("CacheBatchLoad.Progress is empty")
	}

	// Verify HTTP cache
	if !metrics.HTTPCache.Enabled {
		t.Error("HTTPCache.Enabled = false, want true")
	}
	if metrics.HTTPCache.MaxTotal != "500.0 MiB" {
		t.Errorf("HTTPCache.MaxTotal = %q, want %q", metrics.HTTPCache.MaxTotal, "500.0 MiB")
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
	<div>
		<div class="card-title">Cache Batch Load</div>
		<span class="badge">Idle</span>
	</div>
	<div>
		<div class="stat-title">Progress</div>
		<div class="stat-value">0 /
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
}
