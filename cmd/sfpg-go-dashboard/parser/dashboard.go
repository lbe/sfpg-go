// Package parser provides HTML parsing for the sfpg-go dashboard page.
package parser

import (
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/lbe/sfpg-go/internal/testutil"
	"golang.org/x/net/html"
)

// ErrDashboardNotFound is returned when the dashboard-container element
// is not found in the HTML response. This typically indicates the response
// is not a valid dashboard page.
var ErrDashboardNotFound = errors.New("dashboard element not found in response")

// ModuleStatus represents the status of a registered module.
type ModuleStatus struct {
	// Name is the module identifier (e.g., "discovery", "cache_preload").
	Name string
	// Status is the current module state (e.g., "active", "recent", "idle").
	Status string
	// ActivityCount is the number of recent activities for this module.
	ActivityCount int
}

// MemoryStats contains memory allocation statistics from the Go runtime.
type MemoryStats struct {
	// Allocated is the currently allocated memory (e.g., "15.0 MiB").
	Allocated string
	// HeapInUse is the heap memory currently in use.
	HeapInUse string
	// HeapReleased is the heap memory released to the OS.
	HeapReleased string
	// HeapObjects is the number of allocated heap objects.
	HeapObjects string
}

// RuntimeStats contains Go runtime statistics.
type RuntimeStats struct {
	// Goroutines is the current number of goroutines.
	Goroutines string
	// CPUCount is the number of logical CPUs.
	CPUCount string
	// NextGC is the heap size target for the next GC cycle.
	NextGC string
	// Uptime is the server uptime duration.
	Uptime string
}

// WriteBatcherStats contains write batcher statistics.
type WriteBatcherStats struct {
	// Pending is the number of pending writes in the channel.
	Pending string
	// ChannelSize is the total channel capacity.
	ChannelSize string
	// TotalFlushed is the total number of flushed batches.
	TotalFlushed string
	// TotalErrors is the total number of write errors.
	TotalErrors string
	// BatchSize is the maximum batch size.
	BatchSize string
}

// WorkerPoolStats contains worker pool statistics.
type WorkerPoolStats struct {
	// RunningWorkers is the number of currently running workers.
	RunningWorkers string
	// MaxWorkers is the maximum number of workers.
	MaxWorkers string
	// CompletedTasks is the total number of completed tasks.
	CompletedTasks string
	// Successful is the number of successfully completed tasks.
	Successful string
	// Failed is the number of failed tasks.
	Failed string
}

// QueueStats contains file queue statistics.
type QueueStats struct {
	// Queued is the number of items currently in the queue.
	Queued string
	// Capacity is the maximum queue capacity.
	Capacity string
	// Utilization is the queue utilization percentage.
	Utilization string
	// Available is the number of available queue slots.
	Available string
}

// FileProcessingStats contains file processing statistics.
type FileProcessingStats struct {
	// TotalFound is the total number of files discovered.
	TotalFound string
	// Existing is the number of files that already exist in the database.
	Existing string
	// New is the number of new files to process.
	New string
	// Invalid is the number of invalid/unreadable files.
	Invalid string
	// InFlight is the number of files currently being processed.
	InFlight string
}

// CachePreloadStats contains cache preload statistics.
type CachePreloadStats struct {
	// IsEnabled indicates whether cache preload is enabled.
	IsEnabled bool
	// Scheduled is the number of scheduled preload tasks.
	Scheduled string
	// Completed is the number of completed preload tasks.
	Completed string
	// Failed is the number of failed preload tasks.
	Failed string
	// Skipped is the number of skipped preload tasks.
	Skipped string
}

// CacheBatchLoadStats contains cache batch load statistics.
type CacheBatchLoadStats struct {
	// IsRunning indicates whether a batch load is currently running.
	IsRunning bool
	// Progress shows the current progress as "current/total".
	// This value is normalized to remove whitespace/newlines.
	Progress string
	// Total is the total number of items to load.
	Total string
	// Failed is the number of failed batch loads.
	Failed string
	// Skipped is the number of skipped batch loads.
	Skipped string
}

// HTTPCacheStats contains HTTP cache statistics.
type HTTPCacheStats struct {
	// Enabled indicates whether HTTP caching is enabled.
	Enabled bool
	// Entries is the number of cached entries.
	Entries string
	// Size is the total cache size.
	Size string
	// MaxTotal is the maximum total cache size.
	MaxTotal string
	// MaxEntry is the maximum size per entry.
	MaxEntry string
	// Utilization is the cache utilization percentage.
	Utilization string
}

// DashboardMetrics contains all metrics extracted from the dashboard page.
type DashboardMetrics struct {
	// LastUpdated is the timestamp of the last update.
	LastUpdated string
	// Modules is the list of registered module statuses.
	Modules []ModuleStatus
	// Memory contains memory statistics.
	Memory MemoryStats
	// Runtime contains runtime statistics.
	Runtime RuntimeStats
	// WriteBatcher contains write batcher statistics.
	WriteBatcher WriteBatcherStats
	// WorkerPool contains worker pool statistics.
	WorkerPool WorkerPoolStats
	// Queue contains file queue statistics.
	Queue QueueStats
	// FileProcessing contains file processing statistics.
	FileProcessing FileProcessingStats
	// CachePreload contains cache preload statistics.
	CachePreload CachePreloadStats
	// CacheBatchLoad contains cache batch load statistics.
	CacheBatchLoad CacheBatchLoadStats
	// HTTPCache contains HTTP cache statistics.
	HTTPCache HTTPCacheStats
}

// ParseDashboard parses the dashboard HTML from the given reader and
// extracts all metrics into a DashboardMetrics struct.
//
// The HTML must contain a #dashboard-container element with stat-value
// elements that have specific IDs for each metric.
//
// Returns ErrDashboardNotFound if the dashboard container is not present.
//
// Example:
//
//	resp, err := http.Get("http://localhost:8083/dashboard")
//	if err != nil {
//	    return err
//	}
//	defer resp.Body.Close()
//
//	metrics, err := parser.ParseDashboard(resp.Body)
//	if errors.Is(err, parser.ErrDashboardNotFound) {
//	    // not a dashboard page
//	}
func ParseDashboard(r io.Reader) (*DashboardMetrics, error) {
	doc, err := testutil.ParseHTML(r)
	if err != nil {
		return nil, err
	}

	container := testutil.FindElementByID(doc, "dashboard-container")
	if container == nil {
		return nil, ErrDashboardNotFound
	}

	metrics := &DashboardMetrics{
		LastUpdated: extractLastUpdated(container),
		Modules:     extractModules(container),
	}

	extractMemoryStats(container, metrics)
	extractRuntimeStats(container, metrics)
	extractWriteBatcherStats(container, metrics)
	extractWorkerPoolStats(container, metrics)
	extractQueueStats(container, metrics)
	extractFileProcessingStats(container, metrics)
	extractCachePreloadStats(container, metrics)
	extractCacheBatchLoadStats(container, metrics)
	extractHTTPCacheStats(container, metrics)

	return metrics, nil
}

// extractLastUpdated extracts the last-updated timestamp from the container.
func extractLastUpdated(container *html.Node) string {
	el := testutil.FindElementByID(container, "last-updated")
	if el == nil {
		return ""
	}
	return strings.TrimSpace(testutil.GetTextContent(el))
}

// extractModules extracts all module status cards from the container.
func extractModules(container *html.Node) []ModuleStatus {
	var modules []ModuleStatus

	cards := testutil.FindAllElements(container, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "div" {
			return false
		}
		classes := strings.Fields(testutil.GetAttr(n, "class"))
		for _, c := range classes {
			if c == "card" {
				return true
			}
		}
		return false
	})

	for _, card := range cards {
		module := parseModuleCard(card)
		if module.Name != "" && module.Status != "" {
			modules = append(modules, module)
		}
	}

	return modules
}

// parseModuleCard parses a single module card element.
func parseModuleCard(card *html.Node) ModuleStatus {
	module := ModuleStatus{}

	nameEl := testutil.FindElementByClass(card, "text-sm")
	if nameEl != nil {
		module.Name = strings.TrimSpace(testutil.GetTextContent(nameEl))
	}

	badgeEl := testutil.FindElement(card, func(n *html.Node) bool {
		classes := testutil.GetAttr(n, "class")
		return strings.Contains(classes, "badge")
	})
	if badgeEl != nil {
		module.Status = strings.TrimSpace(testutil.GetTextContent(badgeEl))
	}

	activityEl := testutil.FindElement(card, func(n *html.Node) bool {
		text := testutil.GetTextContent(n)
		return strings.Contains(text, "Activity count:")
	})
	if activityEl != nil {
		text := testutil.GetTextContent(activityEl)
		text = strings.TrimPrefix(text, "Activity count:")
		text = strings.TrimSpace(text)
		text = strings.ReplaceAll(text, ",", "")
		if count, err := strconv.Atoi(text); err == nil {
			module.ActivityCount = count
		}
	}

	return module
}

// extractTextByID finds an element by ID and returns its trimmed text content.
func extractTextByID(container *html.Node, id string) string {
	el := testutil.FindElementByID(container, id)
	if el == nil {
		return ""
	}
	return strings.TrimSpace(testutil.GetTextContent(el))
}

// extractMemoryStats extracts memory statistics into the metrics struct.
func extractMemoryStats(container *html.Node, m *DashboardMetrics) {
	m.Memory.Allocated = extractTextByID(container, "mem-allocated")
	m.Memory.HeapInUse = extractTextByID(container, "mem-heap-in-use")
	m.Memory.HeapReleased = extractTextByID(container, "mem-heap-released")
	m.Memory.HeapObjects = extractTextByID(container, "mem-heap-objects")
}

// extractRuntimeStats extracts runtime statistics into the metrics struct.
func extractRuntimeStats(container *html.Node, m *DashboardMetrics) {
	m.Runtime.Goroutines = extractTextByID(container, "runtime-goroutines")
	m.Runtime.CPUCount = extractTextByID(container, "runtime-cpu-count")
	m.Runtime.NextGC = extractTextByID(container, "runtime-next-gc")
	m.Runtime.Uptime = extractTextByID(container, "runtime-uptime")
}

// extractWriteBatcherStats extracts write batcher statistics into the metrics struct.
func extractWriteBatcherStats(container *html.Node, m *DashboardMetrics) {
	m.WriteBatcher.Pending = extractTextByID(container, "wb-pending")
	m.WriteBatcher.TotalFlushed = extractTextByID(container, "wb-flushed")
	m.WriteBatcher.TotalErrors = extractTextByID(container, "wb-errors")
	m.WriteBatcher.BatchSize = extractTextByID(container, "wb-batch-size")

	// Extract channel size from wb-channel-size-desc "of X"
	descEl := testutil.FindElementByID(container, "wb-channel-size-desc")
	if descEl != nil {
		text := testutil.GetTextContent(descEl)
		// Format: "of X" where X is the number
		parts := strings.Fields(text)
		for i, p := range parts {
			if p == "of" && i+1 < len(parts) {
				m.WriteBatcher.ChannelSize = parts[i+1]
				break
			}
		}
	}
}

// extractWorkerPoolStats extracts worker pool statistics into the metrics struct.
func extractWorkerPoolStats(container *html.Node, m *DashboardMetrics) {
	m.WorkerPool.CompletedTasks = extractTextByID(container, "wp-completed")
	m.WorkerPool.Successful = extractTextByID(container, "wp-successful")
	m.WorkerPool.Failed = extractTextByID(container, "wp-failed")

	// Parse "Running Workers: X/Y" format
	runningValue := extractTextByID(container, "wp-running")
	if runningValue != "" {
		parts := strings.Split(runningValue, "/")
		if len(parts) >= 2 {
			m.WorkerPool.RunningWorkers = strings.TrimSpace(parts[0])
			m.WorkerPool.MaxWorkers = strings.TrimSpace(parts[1])
		} else {
			m.WorkerPool.RunningWorkers = runningValue
		}
	}
}

// extractQueueStats extracts file queue statistics into the metrics struct.
func extractQueueStats(container *html.Node, m *DashboardMetrics) {
	m.Queue.Queued = extractTextByID(container, "queue-queued")
	m.Queue.Utilization = extractTextByID(container, "queue-utilization")
	m.Queue.Available = extractTextByID(container, "queue-available")

	// Extract capacity from queue-capacity-desc "of X items"
	descEl := testutil.FindElementByID(container, "queue-capacity-desc")
	if descEl != nil {
		text := testutil.GetTextContent(descEl)
		// Format: "of X items"
		parts := strings.Fields(text)
		for i, p := range parts {
			if p == "of" && i+1 < len(parts) {
				m.Queue.Capacity = parts[i+1]
				break
			}
		}
	}
}

// extractFileProcessingStats extracts file processing statistics into the metrics struct.
func extractFileProcessingStats(container *html.Node, m *DashboardMetrics) {
	m.FileProcessing.TotalFound = extractTextByID(container, "fp-total")
	m.FileProcessing.Existing = extractTextByID(container, "fp-existing")
	m.FileProcessing.New = extractTextByID(container, "fp-new")
	m.FileProcessing.Invalid = extractTextByID(container, "fp-invalid")
	m.FileProcessing.InFlight = extractTextByID(container, "fp-inflight")
}

// extractCachePreloadStats extracts cache preload statistics into the metrics struct.
func extractCachePreloadStats(container *html.Node, m *DashboardMetrics) {
	m.CachePreload.Scheduled = extractTextByID(container, "preload-scheduled")
	m.CachePreload.Completed = extractTextByID(container, "preload-completed")
	m.CachePreload.Failed = extractTextByID(container, "preload-failed")
	m.CachePreload.Skipped = extractTextByID(container, "preload-skipped")

	// Check status badge
	statusEl := testutil.FindElementByID(container, "preload-status")
	if statusEl != nil {
		text := strings.TrimSpace(testutil.GetTextContent(statusEl))
		m.CachePreload.IsEnabled = (text == "Enabled")
	}
}

// extractCacheBatchLoadStats extracts cache batch load statistics into the metrics struct.
// The Progress value is normalized to remove newlines, tabs, and extra whitespace.
func extractCacheBatchLoadStats(container *html.Node, m *DashboardMetrics) {
	m.CacheBatchLoad.Failed = extractTextByID(container, "batch-failed")
	m.CacheBatchLoad.Skipped = extractTextByID(container, "batch-skipped")

	// Extract and normalize progress value
	progressValue := extractTextByID(container, "batch-progress")
	if progressValue != "" {
		// Normalize progress: remove newlines, tabs, collapse spaces
		progress := progressValue
		progress = strings.ReplaceAll(progress, "\n", "")
		progress = strings.ReplaceAll(progress, "\r", "")
		progress = strings.ReplaceAll(progress, "\t", "")
		for strings.Contains(progress, "  ") {
			progress = strings.ReplaceAll(progress, "  ", " ")
		}
		progress = strings.ReplaceAll(progress, " / ", "/")
		progress = strings.ReplaceAll(progress, " /", "/")
		progress = strings.ReplaceAll(progress, "/ ", "/")
		progress = strings.TrimSpace(progress)
		m.CacheBatchLoad.Progress = progress
	}

	// Extract total from batch-total-desc
	descEl := testutil.FindElementByID(container, "batch-total-desc")
	if descEl != nil {
		text := testutil.GetTextContent(descEl)
		// Format: "X total"
		parts := strings.Fields(text)
		if len(parts) >= 1 {
			m.CacheBatchLoad.Total = parts[0]
		}
	}

	// Check status badge
	statusEl := testutil.FindElementByID(container, "batch-status")
	if statusEl != nil {
		text := strings.TrimSpace(testutil.GetTextContent(statusEl))
		m.CacheBatchLoad.IsRunning = (text == "Running")
	}
}

// extractHTTPCacheStats extracts HTTP cache statistics into the metrics struct.
func extractHTTPCacheStats(container *html.Node, m *DashboardMetrics) {
	m.HTTPCache.Entries = extractTextByID(container, "http-entries")
	m.HTTPCache.Size = extractTextByID(container, "http-size")
	m.HTTPCache.MaxTotal = extractTextByID(container, "http-max-total")
	m.HTTPCache.MaxEntry = extractTextByID(container, "http-max-entry")
	m.HTTPCache.Utilization = extractTextByID(container, "http-utilization")

	// Check status badge
	statusEl := testutil.FindElementByID(container, "http-status")
	if statusEl != nil {
		text := strings.TrimSpace(testutil.GetTextContent(statusEl))
		m.HTTPCache.Enabled = (text == "Enabled")
	}
}
