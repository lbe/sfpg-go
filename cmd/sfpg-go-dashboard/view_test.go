package main

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/parser"
)

// dashboardModel builds a dashboard Model with fixed dimensions for view tests.
func dashboardModel(metrics *parser.DashboardMetrics) Model {
	return Model{
		metrics: metrics,
		width:   120,
		height:  80,
	}
}

// populatedMetrics returns DashboardMetrics with every card populated by
// distinct non-zero values so all card titles render and metric values do not
// collide as substrings on the same line (row-pairing assertions use card
// title strings only).
func populatedMetrics() *parser.DashboardMetrics {
	return &parser.DashboardMetrics{
		Version:     "0.14.1-test",
		LastUpdated: "17:32:31",
		Memory: parser.MemoryStats{
			Allocated:    "15.0 MiB",
			HeapInUse:    "8.0 MiB",
			HeapReleased: "2.0 MiB",
			HeapObjects:  "12345",
		},
		Runtime: parser.RuntimeStats{
			Goroutines: "57",
			CPUCount:   "8",
			NextGC:     "4.0 MiB",
			Uptime:     "3h22m",
		},
		Gallery: parser.GalleryStats{
			Present:        true,
			Folders:        "12",
			Images:         "99",
			ImagesSize:     "3.0 GiB",
			FirstDiscovery: "2026-03-01 00:00:00",
			LastDiscovery:  "2026-03-02 00:00:00",
		},
		FileProcessing: parser.FileProcessingStats{
			TotalFound: "9876",
			Existing:   "9000",
			New:        "800",
			Invalid:    "11",
			InFlight:   "65",
		},
		Queue: parser.QueueStats{Queued: "42"},
		WorkerPool: parser.WorkerPoolStats{
			RunningWorkers: "3",
			MaxWorkers:     "8",
			CompletedTasks: "1024",
			Successful:     "1019",
			Failed:         "5",
		},
		CachePreload: parser.CachePreloadStats{
			IsEnabled: true,
			Scheduled: "1",
			Completed: "1",
			Failed:    "0",
			Skipped:   "0",
		},
		CacheBatchLoad: parser.CacheBatchLoadStats{
			IsRunning: true,
			Loaded:    "77",
			Total:     "888",
			Progress:  "77/888",
			Failed:    "3",
			Skipped:   "25",
		},
		HTTPCache: parser.HTTPCacheStats{
			Enabled:     true,
			Entries:     "128",
			Size:        "64.0 MiB",
			MaxTotal:    "128.0 MiB",
			MaxEntry:    "8.0 MiB",
			Utilization: "50%",
		},
		WriteBatcher: parser.WriteBatcherStats{
			Pending:        "6",
			ChannelSize:    "100",
			TotalFlushed:   "2048",
			TotalErrors:    "2",
			BatchSize:      "64",
			DQueEnabled:    "Enabled",
			DQueSize:       "12",
			OverflowCount:  "5",
			DiskUsageBytes: "1.0 MiB",
			DiskQuotaBytes: "50.0 GiB",
		},
	}
}

// TestViewDashboard_TitleAndVersion shows the web dashboard title and version
// in the header and drops the old System Dashboard title. Version is its own
// centered zone, never glued to the title.
func TestViewDashboard_TitleAndVersion(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{Version: "0.14.1-test"})
	view := m.viewDashboard()
	assertPlainIncludes(t, view, "Performance & Health Dashboard")
	assertPlainIncludes(t, view, "Version 0.14.1-test")
	assertPlainExcludes(t, view, "Performance & Health Dashboard  Version")
	assertPlainExcludes(t, view, "System Dashboard")
}

// TestViewDashboard_HeaderThreeZone shows the three-zone header layout: title
// left, version centered, Live + last-updated time right. The version is its
// own zone, never glued to the title.
func TestViewDashboard_HeaderThreeZone(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{
		Version:     "0.14.1-test",
		LastUpdated: "17:32:31",
	})
	view := m.viewDashboard()
	assertPlainIncludes(t, view, "Performance & Health Dashboard")
	assertPlainIncludes(t, view, "Version 0.14.1-test")
	assertPlainIncludes(t, view, "Live 17:32:31")
	assertPlainExcludes(t, view, "Performance & Health Dashboard  Version")
	assertPlainAppearsBefore(t, view, "Performance & Health Dashboard", "Version 0.14.1-test")
	assertPlainAppearsBefore(t, view, "Version 0.14.1-test", "Live 17:32:31")
}

// TestViewDashboard_WriteBatcherDiskAndDQue shows dque status and disk
// usage/quota labeled pairs on the Write Batcher card. The DQue row uses the
// web label "DQue Overflow" with an Enabled/Off value.
func TestViewDashboard_WriteBatcherDiskAndDQue(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{
		WriteBatcher: parser.WriteBatcherStats{
			DQueEnabled:    "Enabled",
			DQueSize:       "12",
			OverflowCount:  "5",
			DiskUsageBytes: "1.0 MiB",
			DiskQuotaBytes: "50.0 GiB",
		},
	})
	view := m.viewDashboard()
	assertPlainIncludes(t, view, "DQue Overflow")
	assertPlainIncludes(t, view, "Enabled")
	assertPlainIncludes(t, view, "Disk Usage")
	assertPlainIncludes(t, view, "1.0 MiB")
	assertPlainIncludes(t, view, "Disk Quota")
	assertPlainIncludes(t, view, "50.0 GiB")
	assertPlainExcludes(t, view, "DQue:On")
	assertPlainExcludes(t, view, "DQue:Off")
}

// TestViewDashboard_CacheBatchLoadFields shows Loaded, Total, Progress,
// Failed, and Skipped as labeled fields on the Cache Batch Load card.
func TestViewDashboard_CacheBatchLoadFields(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{
		CacheBatchLoad: parser.CacheBatchLoadStats{
			Loaded:   "77",
			Total:    "888",
			Progress: "10/999",
			Failed:   "3",
			Skipped:  "25",
		},
	})
	view := m.viewDashboard()
	assertPlainIncludes(t, view, "Cache Batch Load")
	assertPlainIncludes(t, view, "Loaded: 77")
	assertPlainIncludes(t, view, "Total: 888")
	assertPlainIncludes(t, view, "Progress: 10/999")
	assertPlainIncludes(t, view, "Failed: 3")
	assertPlainIncludes(t, view, "Skipped: 25")
}

// TestViewDashboard_GalleryStats shows the Gallery Statistics card above the
// File Processing card with Folders, Images, and Images Size on one line and
// First/Last discovery dates on separate lines.
func TestViewDashboard_GalleryStats(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{
		Gallery: parser.GalleryStats{
			Present:        true,
			Folders:        "42",
			Images:         "99",
			ImagesSize:     "3.0 GiB",
			FirstDiscovery: "2026-03-01 00:00:00",
			LastDiscovery:  "2026-03-02 00:00:00",
		},
	})
	view := m.viewDashboard()
	assertPlainIncludes(t, view, "Gallery Statistics")
	assertPlainIncludes(t, view, "Folders")
	assertPlainIncludes(t, view, "42")
	assertPlainIncludes(t, view, "Images")
	assertPlainIncludes(t, view, "99")
	assertPlainIncludes(t, view, "Images Size")
	assertPlainIncludes(t, view, "3.0 GiB")
	// First/Last each on their own line, never "First: …  Last: …" on one line.
	assertPlainIncludes(t, view, "First: 2026-03-01 00:00:00")
	assertPlainIncludes(t, view, "Last: 2026-03-02 00:00:00")
	assertPlainExcludes(t, view, "First: 2026-03-01 00:00:00  Last:")
	assertPlainAppearsBefore(t, view, "Gallery Statistics", "File Processing")
}

// TestViewDashboard_GalleryLoading shows the loading state when the gallery
// card is not present.
func TestViewDashboard_GalleryLoading(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{})
	view := m.viewDashboard()
	assertPlainIncludes(t, view, "Gallery Statistics")
	assertPlainIncludes(t, view, "Loading gallery data...")
}

// TestViewDashboard_GalleryUnknownDates shows "unknown" for empty First/Last
// discovery dates, each on its own line.
func TestViewDashboard_GalleryUnknownDates(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{
		Gallery: parser.GalleryStats{
			Present: true,
			Folders: "42",
			Images:  "99",
		},
	})
	view := m.viewDashboard()
	assertPlainIncludes(t, view, "First: unknown")
	assertPlainIncludes(t, view, "Last: unknown")
	assertPlainExcludes(t, view, "First: unknown  Last:")
	assertPlainAppearsBefore(t, view, "Gallery Statistics", "File Processing")
}

// TestViewDashboard_RebuildErrorBanner shows the folder-index rebuild error
// banner under the header when the message is non-empty, with no ack control.
func TestViewDashboard_RebuildErrorBanner(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{
		FolderIndexRebuildError: "Manual discovery failed to rebuild the file's folder index. The live index is unchanged.",
	})
	view := m.viewDashboard()
	assertPlainIncludes(t, view, "Folder Index Rebuild Failed")
	assertPlainIncludes(t, view, "live index is unchanged")
	assertPlainExcludes(t, view, "Acknowledge")
}

// TestViewDashboard_RebuildErrorHidden omits the banner when the message is empty.
func TestViewDashboard_RebuildErrorHidden(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{})
	view := m.viewDashboard()
	assertPlainExcludes(t, view, "Folder Index Rebuild Failed")
}

// TestViewDashboard_SectionOrder renders every card title in the web section
// order: Memory, Runtime, Gallery Statistics, File Processing, Cache Preload,
// Cache Batch Load, HTTP Cache, Worker Pool, Write Batcher.
func TestViewDashboard_SectionOrder(t *testing.T) {
	m := dashboardModel(populatedMetrics())
	view := m.viewDashboard()
	assertPlainAppearsBefore(t, view, "Memory", "Runtime")
	assertPlainAppearsBefore(t, view, "Runtime", "Gallery Statistics")
	assertPlainAppearsBefore(t, view, "Gallery Statistics", "File Processing")
	assertPlainAppearsBefore(t, view, "File Processing", "Cache Preload")
	assertPlainAppearsBefore(t, view, "Cache Preload", "Cache Batch Load")
	assertPlainAppearsBefore(t, view, "Cache Batch Load", "HTTP Cache")
	assertPlainAppearsBefore(t, view, "HTTP Cache", "Worker Pool")
	assertPlainAppearsBefore(t, view, "Worker Pool", "Write Batcher")
}

// TestViewDashboard_QueuedInsideFileProcessing shows Queued Items as a field
// inside the File Processing card, never as a separate card.
func TestViewDashboard_QueuedInsideFileProcessing(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{Queue: parser.QueueStats{Queued: "42"}})
	view := m.viewDashboard()
	assertPlainIncludes(t, view, "Queued Items")
	assertPlainIncludes(t, view, "42")
	assertPlainAppearsBefore(t, view, "File Processing", "Queued Items")
	assertPlainAppearsBefore(t, view, "Queued Items", "Cache Preload")
	assertPlainExcludes(t, view, "│ Queued Items │")
}

// TestViewDashboard_WebLabels asserts every web field label appears on the TUI,
// with DQue row in the Enabled state and cache status flags off so the bare
// word "Enabled" can only come from the DQue Overflow row.
func TestViewDashboard_WebLabels(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{
		Memory:         parser.MemoryStats{Allocated: "15.0 MiB", HeapInUse: "8.0 MiB", HeapReleased: "2.0 MiB", HeapObjects: "12345"},
		Runtime:        parser.RuntimeStats{Goroutines: "57", CPUCount: "8", NextGC: "4.0 MiB", Uptime: "3h22m"},
		Gallery:        parser.GalleryStats{Present: true, Folders: "12", Images: "99", ImagesSize: "3.0 GiB", FirstDiscovery: "2026-03-01 00:00:00", LastDiscovery: "2026-03-02 00:00:00"},
		FileProcessing: parser.FileProcessingStats{TotalFound: "9876", Existing: "9000", New: "800", Invalid: "11", InFlight: "65"},
		Queue:          parser.QueueStats{Queued: "42"},
		WorkerPool:     parser.WorkerPoolStats{RunningWorkers: "3", MaxWorkers: "8", CompletedTasks: "1024", Successful: "1019", Failed: "5"},
		CachePreload:   parser.CachePreloadStats{IsEnabled: false, Scheduled: "1", Completed: "1", Failed: "0", Skipped: "0"},
		CacheBatchLoad: parser.CacheBatchLoadStats{IsRunning: true, Loaded: "77", Total: "888", Progress: "77/888", Failed: "3", Skipped: "25"},
		HTTPCache:      parser.HTTPCacheStats{Enabled: false, Entries: "128", Size: "64.0 MiB", MaxTotal: "128.0 MiB", MaxEntry: "8.0 MiB", Utilization: "50%"},
		WriteBatcher:   parser.WriteBatcherStats{Pending: "6", ChannelSize: "100", TotalFlushed: "2048", TotalErrors: "2", BatchSize: "64", DQueEnabled: "Enabled", DQueSize: "12", OverflowCount: "5", DiskUsageBytes: "1.0 MiB", DiskQuotaBytes: "50.0 GiB"},
	})
	view := m.viewDashboard()

	assertPlainIncludes(t, view, "Running Workers")
	assertPlainIncludes(t, view, "Completed Tasks")
	assertPlainIncludes(t, view, "Successful")
	assertPlainIncludes(t, view, "Failed")
	assertPlainIncludes(t, view, "Total Found")
	assertPlainIncludes(t, view, "Existing")
	assertPlainIncludes(t, view, "New")
	assertPlainIncludes(t, view, "Invalid")
	assertPlainIncludes(t, view, "In Flight")
	assertPlainIncludes(t, view, "Queued Items")
	assertPlainIncludes(t, view, "Total Flushed:")
	assertPlainIncludes(t, view, "Errors")
	assertPlainIncludes(t, view, "Batch Size")
	assertPlainIncludes(t, view, "Pending")
	assertPlainIncludes(t, view, "Disk Usage")
	assertPlainIncludes(t, view, "Disk Quota")
	assertPlainIncludes(t, view, "DQue Overflow")
	assertPlainIncludes(t, view, "Enabled")

	assertPlainExcludes(t, view, "DQue:On")
	assertPlainExcludes(t, view, "DQue:Off")
	assertPlainExcludes(t, view, "Running:")
}

// TestViewDashboard_DQueOff shows the DQue Overflow row in the Off state; with
// cache status flags off too, the word "Enabled" must not appear anywhere.
func TestViewDashboard_DQueOff(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{
		CachePreload: parser.CachePreloadStats{IsEnabled: false},
		HTTPCache:    parser.HTTPCacheStats{Enabled: false},
		WriteBatcher: parser.WriteBatcherStats{
			DQueEnabled:    "Off",
			DiskUsageBytes: "1.0 MiB",
			DiskQuotaBytes: "50.0 GiB",
		},
	})
	view := m.viewDashboard()
	assertPlainIncludes(t, view, "DQue Overflow")
	assertPlainIncludes(t, view, "Off")
	assertPlainExcludes(t, view, "DQue:On")
	assertPlainExcludes(t, view, "DQue:Off")
	assertPlainExcludes(t, view, "Enabled")
}

// TestViewDashboard_RowPairingWide pairs sibling cards on one row when the
// terminal is at least dashboardPairMinWidth wide.
func TestViewDashboard_RowPairingWide(t *testing.T) {
	m := dashboardModel(populatedMetrics())
	m.width = 120
	view := m.viewDashboard()
	assertPlainSameRow(t, view, "Memory", "Runtime")
	assertPlainSameRow(t, view, "Gallery Statistics", "File Processing")
	assertPlainSameRow(t, view, "Cache Preload", "Cache Batch Load")
	assertPlainSameRow(t, view, "Cache Batch Load", "HTTP Cache")
	assertPlainSameRow(t, view, "Worker Pool", "Write Batcher")
}

// TestViewDashboard_RowPairingNarrow stacks sibling cards vertically below
// dashboardPairMinWidth while preserving web section order.
func TestViewDashboard_RowPairingNarrow(t *testing.T) {
	m := dashboardModel(populatedMetrics())
	m.width = 40
	view := m.viewDashboard()

	assertPlainAppearsBefore(t, view, "Memory", "Runtime")
	assertPlainAppearsBefore(t, view, "Runtime", "Gallery Statistics")
	assertPlainAppearsBefore(t, view, "Gallery Statistics", "File Processing")
	assertPlainAppearsBefore(t, view, "File Processing", "Cache Preload")
	assertPlainAppearsBefore(t, view, "Cache Preload", "Cache Batch Load")
	assertPlainAppearsBefore(t, view, "Cache Batch Load", "HTTP Cache")
	assertPlainAppearsBefore(t, view, "HTTP Cache", "Worker Pool")
	assertPlainAppearsBefore(t, view, "Worker Pool", "Write Batcher")

	assertPlainNotSameRow(t, view, "Memory", "Runtime")
	assertPlainNotSameRow(t, view, "Gallery Statistics", "File Processing")
	assertPlainNotSameRow(t, view, "Cache Preload", "Cache Batch Load")
	assertPlainNotSameRow(t, view, "Cache Batch Load", "HTTP Cache")
	assertPlainNotSameRow(t, view, "Worker Pool", "Write Batcher")
}

// TestViewLogin_NetworkError shows the network error message.
func TestViewLogin_NetworkError(t *testing.T) {
	m := Model{
		authState:     authStatePrompting,
		usernameInput: textinput.New(),
		passwordInput: textinput.New(),
		err:           client.ErrNetworkError,
	}
	m.usernameInput.Focus()

	view := m.viewLogin()
	assertPlainIncludes(t, view, "Network error - cannot connect to server")
}
