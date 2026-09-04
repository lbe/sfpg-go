package main

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/parser"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/tuiview"
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
	tuiview.AssertPlainIncludes(t, view, "Performance & Health Dashboard")
	tuiview.AssertPlainIncludes(t, view, "Version 0.14.1-test")
	tuiview.AssertPlainExcludes(t, view, "Performance & Health Dashboard  Version")
	tuiview.AssertPlainExcludes(t, view, "System Dashboard")
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
	tuiview.AssertPlainIncludes(t, view, "Performance & Health Dashboard")
	tuiview.AssertPlainIncludes(t, view, "Version 0.14.1-test")
	tuiview.AssertPlainIncludes(t, view, "Live 17:32:31")
	tuiview.AssertPlainExcludes(t, view, "Performance & Health Dashboard  Version")
	tuiview.AssertPlainAppearsBefore(t, view, "Performance & Health Dashboard", "Version 0.14.1-test")
	tuiview.AssertPlainAppearsBefore(t, view, "Version 0.14.1-test", "Live 17:32:31")
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
	tuiview.AssertPlainIncludes(t, view, "DQue Overflow")
	tuiview.AssertPlainIncludes(t, view, "Enabled")
	tuiview.AssertPlainIncludes(t, view, "Disk Usage")
	tuiview.AssertPlainIncludes(t, view, "1.0 MiB")
	tuiview.AssertPlainIncludes(t, view, "Disk Quota")
	tuiview.AssertPlainIncludes(t, view, "50.0 GiB")
	tuiview.AssertPlainExcludes(t, view, "DQue:On")
	tuiview.AssertPlainExcludes(t, view, "DQue:Off")
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
	tuiview.AssertPlainIncludes(t, view, "Cache Batch Load")
	tuiview.AssertPlainIncludes(t, view, "Loaded: 77")
	tuiview.AssertPlainIncludes(t, view, "Total: 888")
	tuiview.AssertPlainIncludes(t, view, "Progress: 10/999")
	tuiview.AssertPlainIncludes(t, view, "Failed: 3")
	tuiview.AssertPlainIncludes(t, view, "Skipped: 25")
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
	tuiview.AssertPlainIncludes(t, view, "Gallery Statistics")
	tuiview.AssertPlainIncludes(t, view, "Folders")
	tuiview.AssertPlainIncludes(t, view, "42")
	tuiview.AssertPlainIncludes(t, view, "Images")
	tuiview.AssertPlainIncludes(t, view, "99")
	tuiview.AssertPlainIncludes(t, view, "Images Size")
	tuiview.AssertPlainIncludes(t, view, "3.0 GiB")
	// First/Last each on their own line, never "First: …  Last: …" on one line.
	tuiview.AssertPlainIncludes(t, view, "First: 2026-03-01 00:00:00")
	tuiview.AssertPlainIncludes(t, view, "Last: 2026-03-02 00:00:00")
	tuiview.AssertPlainExcludes(t, view, "First: 2026-03-01 00:00:00  Last:")
	tuiview.AssertPlainAppearsBefore(t, view, "Gallery Statistics", "File Processing")
}

// TestViewDashboard_GalleryLoading shows the loading state when the gallery
// card is not present.
func TestViewDashboard_GalleryLoading(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{})
	view := m.viewDashboard()
	tuiview.AssertPlainIncludes(t, view, "Gallery Statistics")
	tuiview.AssertPlainIncludes(t, view, "Loading gallery data...")
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
	tuiview.AssertPlainIncludes(t, view, "First: unknown")
	tuiview.AssertPlainIncludes(t, view, "Last: unknown")
	tuiview.AssertPlainExcludes(t, view, "First: unknown  Last:")
	tuiview.AssertPlainAppearsBefore(t, view, "Gallery Statistics", "File Processing")
}

// TestViewDashboard_RebuildErrorBanner shows the folder-index rebuild error
// banner under the header when the message is non-empty, with no ack control.
func TestViewDashboard_RebuildErrorBanner(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{
		FolderIndexRebuildError: "Manual discovery failed to rebuild the file's folder index. The live index is unchanged.",
	})
	view := m.viewDashboard()
	tuiview.AssertPlainIncludes(t, view, "Folder Index Rebuild Failed")
	tuiview.AssertPlainIncludes(t, view, "live index is unchanged")
	tuiview.AssertPlainExcludes(t, view, "Acknowledge")
}

// TestViewDashboard_RebuildErrorHidden omits the banner when the message is empty.
func TestViewDashboard_RebuildErrorHidden(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{})
	view := m.viewDashboard()
	tuiview.AssertPlainExcludes(t, view, "Folder Index Rebuild Failed")
}

// TestViewDashboard_SectionOrder renders every card title in the web section
// order: Memory, Runtime, Gallery Statistics, File Processing, Cache Preload,
// Cache Batch Load, HTTP Cache, Worker Pool, Write Batcher.
func TestViewDashboard_SectionOrder(t *testing.T) {
	m := dashboardModel(populatedMetrics())
	view := m.viewDashboard()
	tuiview.AssertPlainAppearsBefore(t, view, "Memory", "Runtime")
	tuiview.AssertPlainAppearsBefore(t, view, "Runtime", "Gallery Statistics")
	tuiview.AssertPlainAppearsBefore(t, view, "Gallery Statistics", "File Processing")
	tuiview.AssertPlainAppearsBefore(t, view, "File Processing", "Cache Preload")
	tuiview.AssertPlainAppearsBefore(t, view, "Cache Preload", "Cache Batch Load")
	tuiview.AssertPlainAppearsBefore(t, view, "Cache Batch Load", "HTTP Cache")
	tuiview.AssertPlainAppearsBefore(t, view, "HTTP Cache", "Worker Pool")
	tuiview.AssertPlainAppearsBefore(t, view, "Worker Pool", "Write Batcher")
}

// TestViewDashboard_QueuedInsideFileProcessing shows Queued Items as a field
// inside the File Processing card, never as a separate card.
func TestViewDashboard_QueuedInsideFileProcessing(t *testing.T) {
	m := dashboardModel(&parser.DashboardMetrics{Queue: parser.QueueStats{Queued: "42"}})
	view := m.viewDashboard()
	tuiview.AssertPlainIncludes(t, view, "Queued Items")
	tuiview.AssertPlainIncludes(t, view, "42")
	tuiview.AssertPlainAppearsBefore(t, view, "File Processing", "Queued Items")
	tuiview.AssertPlainAppearsBefore(t, view, "Queued Items", "Cache Preload")
	tuiview.AssertPlainExcludes(t, view, "│ Queued Items │")
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

	tuiview.AssertPlainIncludes(t, view, "Running Workers")
	tuiview.AssertPlainIncludes(t, view, "Completed Tasks")
	tuiview.AssertPlainIncludes(t, view, "Successful")
	tuiview.AssertPlainIncludes(t, view, "Failed")
	tuiview.AssertPlainIncludes(t, view, "Total Found")
	tuiview.AssertPlainIncludes(t, view, "Existing")
	tuiview.AssertPlainIncludes(t, view, "New")
	tuiview.AssertPlainIncludes(t, view, "Invalid")
	tuiview.AssertPlainIncludes(t, view, "In Flight")
	tuiview.AssertPlainIncludes(t, view, "Queued Items")
	tuiview.AssertPlainIncludes(t, view, "Total Flushed:")
	tuiview.AssertPlainIncludes(t, view, "Errors")
	tuiview.AssertPlainIncludes(t, view, "Batch Size")
	tuiview.AssertPlainIncludes(t, view, "Pending")
	tuiview.AssertPlainIncludes(t, view, "Disk Usage")
	tuiview.AssertPlainIncludes(t, view, "Disk Quota")
	tuiview.AssertPlainIncludes(t, view, "DQue Overflow")
	tuiview.AssertPlainIncludes(t, view, "Enabled")

	tuiview.AssertPlainExcludes(t, view, "DQue:On")
	tuiview.AssertPlainExcludes(t, view, "DQue:Off")
	tuiview.AssertPlainExcludes(t, view, "Running:")
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
	tuiview.AssertPlainIncludes(t, view, "DQue Overflow")
	tuiview.AssertPlainIncludes(t, view, "Off")
	tuiview.AssertPlainExcludes(t, view, "DQue:On")
	tuiview.AssertPlainExcludes(t, view, "DQue:Off")
	tuiview.AssertPlainExcludes(t, view, "Enabled")
}

// TestViewDashboard_RowPairingWide pairs sibling cards on one row when the
// terminal is at least dashboardPairMinWidth wide.
func TestViewDashboard_RowPairingWide(t *testing.T) {
	m := dashboardModel(populatedMetrics())
	m.width = 120
	view := m.viewDashboard()
	tuiview.AssertPlainSameRow(t, view, "Memory", "Runtime")
	tuiview.AssertPlainSameRow(t, view, "Gallery Statistics", "File Processing")
	tuiview.AssertPlainSameRow(t, view, "Cache Preload", "Cache Batch Load")
	tuiview.AssertPlainSameRow(t, view, "Cache Batch Load", "HTTP Cache")
	tuiview.AssertPlainSameRow(t, view, "Worker Pool", "Write Batcher")
}

// TestViewDashboard_RowPairingNarrow stacks sibling cards vertically below
// dashboardPairMinWidth while preserving web section order.
func TestViewDashboard_RowPairingNarrow(t *testing.T) {
	m := dashboardModel(populatedMetrics())
	m.width = 40
	view := m.viewDashboard()

	tuiview.AssertPlainAppearsBefore(t, view, "Memory", "Runtime")
	tuiview.AssertPlainAppearsBefore(t, view, "Runtime", "Gallery Statistics")
	tuiview.AssertPlainAppearsBefore(t, view, "Gallery Statistics", "File Processing")
	tuiview.AssertPlainAppearsBefore(t, view, "File Processing", "Cache Preload")
	tuiview.AssertPlainAppearsBefore(t, view, "Cache Preload", "Cache Batch Load")
	tuiview.AssertPlainAppearsBefore(t, view, "Cache Batch Load", "HTTP Cache")
	tuiview.AssertPlainAppearsBefore(t, view, "HTTP Cache", "Worker Pool")
	tuiview.AssertPlainAppearsBefore(t, view, "Worker Pool", "Write Batcher")

	tuiview.AssertPlainNotSameRow(t, view, "Memory", "Runtime")
	tuiview.AssertPlainNotSameRow(t, view, "Gallery Statistics", "File Processing")
	tuiview.AssertPlainNotSameRow(t, view, "Cache Preload", "Cache Batch Load")
	tuiview.AssertPlainNotSameRow(t, view, "Cache Batch Load", "HTTP Cache")
	tuiview.AssertPlainNotSameRow(t, view, "Worker Pool", "Write Batcher")
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
	tuiview.AssertPlainIncludes(t, view, "Network error - cannot connect to server")
}
