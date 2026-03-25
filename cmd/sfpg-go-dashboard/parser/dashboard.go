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
// The HTML must contain a #dashboard-container element with stat-title
// and stat-value elements following the DaisyUI stats pattern.
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

// extractStatValue extracts a stat value by its title from the container.
func extractStatValue(container *html.Node, statTitle string) string {
	statTitles := testutil.FindAllElements(container, func(n *html.Node) bool {
		classes := strings.Fields(testutil.GetAttr(n, "class"))
		isStatTitle := false
		for _, c := range classes {
			if c == "stat-title" {
				isStatTitle = true
				break
			}
		}
		return isStatTitle && strings.Contains(testutil.GetTextContent(n), statTitle)
	})

	for _, titleEl := range statTitles {
		parent := titleEl.Parent
		if parent == nil {
			continue
		}
		valueEl := testutil.FindElementByClass(parent, "stat-value")
		if valueEl != nil {
			return strings.TrimSpace(testutil.GetTextContent(valueEl))
		}
	}
	return ""
}

// extractMemoryStats extracts memory statistics into the metrics struct.
func extractMemoryStats(container *html.Node, m *DashboardMetrics) {
	m.Memory.Allocated = extractStatValue(container, "Allocated")
	m.Memory.HeapInUse = extractStatValue(container, "Heap In Use")
	m.Memory.HeapReleased = extractStatValue(container, "Heap Released")
	m.Memory.HeapObjects = extractStatValue(container, "Heap Objects")
}

// extractRuntimeStats extracts runtime statistics into the metrics struct.
func extractRuntimeStats(container *html.Node, m *DashboardMetrics) {
	m.Runtime.Goroutines = extractStatValue(container, "Goroutines")
	m.Runtime.CPUCount = extractStatValue(container, "CPU Count")
	m.Runtime.NextGC = extractStatValue(container, "Next GC")
	m.Runtime.Uptime = extractStatValue(container, "Uptime")
}

// extractWriteBatcherStats extracts write batcher statistics into the metrics struct.
func extractWriteBatcherStats(container *html.Node, m *DashboardMetrics) {
	m.WriteBatcher.Pending = extractStatValue(container, "Pending")
	m.WriteBatcher.TotalFlushed = extractStatValue(container, "Total Flushed")
	m.WriteBatcher.TotalErrors = extractStatValue(container, "Errors")
	m.WriteBatcher.BatchSize = extractStatValue(container, "Batch Size")

	// Extract channel size from stat-desc "of X"
	statDescs := testutil.FindAllElements(container, func(n *html.Node) bool {
		classes := strings.Fields(testutil.GetAttr(n, "class"))
		for _, c := range classes {
			if c == "stat-desc" {
				return true
			}
		}
		return false
	})

	for _, desc := range statDescs {
		text := testutil.GetTextContent(desc)
		if strings.Contains(text, "of") && desc.Parent != nil {
			titleEl := testutil.FindElementByClass(desc.Parent, "stat-title")
			if titleEl != nil && strings.Contains(testutil.GetTextContent(titleEl), "Pending") {
				parts := strings.Fields(text)
				if len(parts) >= 2 {
					m.WriteBatcher.ChannelSize = parts[1]
				}
			}
		}
	}
}

// extractWorkerPoolStats extracts worker pool statistics into the metrics struct.
func extractWorkerPoolStats(container *html.Node, m *DashboardMetrics) {
	m.WorkerPool.CompletedTasks = extractStatValue(container, "Completed Tasks")
	m.WorkerPool.Successful = extractStatValue(container, "Successful")
	m.WorkerPool.Failed = extractStatValue(container, "Failed")

	// Parse "Running Workers: X/Y" format
	runningValue := extractStatValue(container, "Running Workers")
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
	m.Queue.Utilization = extractStatValue(container, "Utilization")
	m.Queue.Available = extractStatValue(container, "Available")

	queuedValue := extractStatValue(container, "Queued Items")
	if queuedValue != "" {
		m.Queue.Queued = queuedValue
	}

	// Extract capacity from stat-desc "of X items"
	statDescs := testutil.FindAllElements(container, func(n *html.Node) bool {
		classes := strings.Fields(testutil.GetAttr(n, "class"))
		for _, c := range classes {
			if c == "stat-desc" {
				return true
			}
		}
		return false
	})

	for _, desc := range statDescs {
		text := testutil.GetTextContent(desc)
		if strings.Contains(text, "of") && strings.Contains(text, "items") && desc.Parent != nil {
			titleEl := testutil.FindElementByClass(desc.Parent, "stat-title")
			if titleEl != nil && strings.Contains(testutil.GetTextContent(titleEl), "Queued") {
				parts := strings.Fields(text)
				for i, p := range parts {
					if p == "of" && i+1 < len(parts) {
						m.Queue.Capacity = parts[i+1]
						break
					}
				}
			}
		}
	}
}

// extractFileProcessingStats extracts file processing statistics into the metrics struct.
func extractFileProcessingStats(container *html.Node, m *DashboardMetrics) {
	m.FileProcessing.TotalFound = extractStatValue(container, "Total Found")
	m.FileProcessing.Existing = extractStatValue(container, "Existing")
	m.FileProcessing.New = extractStatValue(container, "New")
	m.FileProcessing.Invalid = extractStatValue(container, "Invalid")
	m.FileProcessing.InFlight = extractStatValue(container, "In Flight")
}

// extractCachePreloadStats extracts cache preload statistics into the metrics struct.
func extractCachePreloadStats(container *html.Node, m *DashboardMetrics) {
	m.CachePreload.Scheduled = extractStatValue(container, "Scheduled")
	m.CachePreload.Completed = extractStatValue(container, "Completed")
	m.CachePreload.Failed = extractStatValue(container, "Failed")
	m.CachePreload.Skipped = extractStatValue(container, "Skipped")

	// Find "Cache Preload" card and check badge for Enabled/Disabled
	badges := testutil.FindAllElements(container, func(n *html.Node) bool {
		classes := testutil.GetAttr(n, "class")
		return strings.Contains(classes, "badge")
	})

	for _, badge := range badges {
		text := strings.TrimSpace(testutil.GetTextContent(badge))
		if text == "Enabled" || text == "Disabled" {
			parent := badge.Parent
			if parent != nil {
				titleEl := testutil.FindElementByClass(parent, "card-title")
				if titleEl != nil && strings.Contains(testutil.GetTextContent(titleEl), "Cache Preload") {
					m.CachePreload.IsEnabled = (text == "Enabled")
					break
				}
			}
		}
	}
}

// extractCacheBatchLoadStats extracts cache batch load statistics into the metrics struct.
// The Progress value is normalized to remove newlines, tabs, and extra whitespace.
func extractCacheBatchLoadStats(container *html.Node, m *DashboardMetrics) {
	m.CacheBatchLoad.Failed = extractStatValue(container, "Failed")
	m.CacheBatchLoad.Skipped = extractStatValue(container, "Skipped")

	// Extract and normalize progress value
	progressValue := extractStatValue(container, "Progress")
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

	// Extract total from stat-desc
	statDescs := testutil.FindAllElements(container, func(n *html.Node) bool {
		classes := strings.Fields(testutil.GetAttr(n, "class"))
		for _, c := range classes {
			if c == "stat-desc" {
				return true
			}
		}
		return false
	})

	for _, desc := range statDescs {
		text := testutil.GetTextContent(desc)
		if strings.Contains(text, "total") && desc.Parent != nil {
			titleEl := testutil.FindElementByClass(desc.Parent, "stat-title")
			if titleEl != nil && strings.Contains(testutil.GetTextContent(titleEl), "Progress") {
				parts := strings.Fields(text)
				if len(parts) >= 1 {
					m.CacheBatchLoad.Total = parts[0]
				}
			}
		}
	}

	// Find "Cache Batch Load" card and check badge for Running/Idle
	badges := testutil.FindAllElements(container, func(n *html.Node) bool {
		classes := testutil.GetAttr(n, "class")
		return strings.Contains(classes, "badge")
	})

	for _, badge := range badges {
		text := strings.TrimSpace(testutil.GetTextContent(badge))
		if text == "Running" || text == "Idle" {
			parent := badge.Parent
			if parent != nil {
				titleEl := testutil.FindElementByClass(parent, "card-title")
				if titleEl != nil && strings.Contains(testutil.GetTextContent(titleEl), "Cache Batch Load") {
					m.CacheBatchLoad.IsRunning = (text == "Running")
					break
				}
			}
		}
	}
}

// extractHTTPCacheStats extracts HTTP cache statistics into the metrics struct.
func extractHTTPCacheStats(container *html.Node, m *DashboardMetrics) {
	m.HTTPCache.Entries = extractStatValue(container, "Entries")
	m.HTTPCache.Size = extractStatValue(container, "Size")
	m.HTTPCache.MaxTotal = extractStatValue(container, "Max Total")
	m.HTTPCache.MaxEntry = extractStatValue(container, "Max Entry")
	m.HTTPCache.Utilization = extractStatValue(container, "Utilization")

	// Find "HTTP Cache" card and check badge for Enabled/Disabled
	badges := testutil.FindAllElements(container, func(n *html.Node) bool {
		classes := testutil.GetAttr(n, "class")
		return strings.Contains(classes, "badge")
	})

	for _, badge := range badges {
		text := strings.TrimSpace(testutil.GetTextContent(badge))
		if text == "Enabled" || text == "Disabled" {
			parent := badge.Parent
			if parent != nil {
				titleEl := testutil.FindElementByClass(parent, "card-title")
				if titleEl != nil && strings.Contains(testutil.GetTextContent(titleEl), "HTTP Cache") {
					m.HTTPCache.Enabled = (text == "Enabled")
					break
				}
			}
		}
	}
}
