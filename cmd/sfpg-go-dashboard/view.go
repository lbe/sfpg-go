package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
)

// Style definitions for the dashboard UI using lipgloss.
// Colors use 256-color terminal palette indices.
//
// dashboardPairMinWidth is the minimum terminal width at which sibling cards
// are joined on one row via lipgloss.JoinHorizontal; below it they stack
// vertically while preserving web section order.
const dashboardPairMinWidth = 100

var (
	// headerStyle styles the top header bar with dark blue background.
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("62")).
			Padding(0, 1)

	// titleInCardStyle styles card titles with cyan color.
	titleInCardStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("86"))

	// cardStyle provides rounded border boxes for content sections.
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)

	// labelStyle styles field labels with gray color.
	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	// valueStyle styles field values with bright white color.
	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15"))

	// successStyle styles positive status indicators with green color.
	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("42"))

	// warningStyle styles warning indicators with orange color.
	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214"))

	// errorStyle styles error indicators with red color.
	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	// dimStyle styles dimmed/secondary text with dark gray color.
	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	// footerStyle styles the bottom control hints.
	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	// inputStyle styles text input fields with normal border.
	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)

	// focusedStyle styles focused text input fields with highlighted border.
	focusedStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("86")).
			Padding(0, 1)

	// errorBoxStyle styles error message boxes with red border and text.
	errorBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("196")).
			Foreground(lipgloss.Color("196")).
			Padding(0, 1)
)

// View renders the current model state as a string for display.
// It returns different views based on the application state:
//   - Goodbye message when quitting
//   - Login form when prompting for credentials
//   - Connecting message during initial load
//   - Dashboard with metrics when authenticated
func (m Model) View() string {
	if m.quitting {
		return "Goodbye!\n"
	}

	if m.authState == authStatePrompting || m.authState == authStateAuthenticating {
		return m.viewLogin()
	}

	if m.metrics == nil && m.loading {
		return "Connecting to " + m.serverURL + "...\n"
	}

	if m.metrics == nil {
		return "Loading dashboard...\n"
	}

	return m.viewDashboard()
}

// viewLogin renders the login form with username/password inputs.
// Displays authentication errors if present and shows control hints.
func (m Model) viewLogin() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render(" SFPG-Go Dashboard - Login "))
	b.WriteString("\n\n")

	if m.err != nil {
		errMsg := "Authentication failed"
		if errors.Is(m.err, client.ErrNetworkError) {
			errMsg = "Network error - cannot connect to server"
		}
		b.WriteString(errorBoxStyle.Render(" " + errMsg + " "))
		b.WriteString("\n\n")
	}

	usernameStyle := inputStyle
	passwordStyle := inputStyle
	if m.focusPassword {
		passwordStyle = focusedStyle
	} else {
		usernameStyle = focusedStyle
	}

	b.WriteString(labelStyle.Render("Username: "))
	b.WriteString("\n")
	b.WriteString(usernameStyle.Render(m.usernameInput.View()))
	b.WriteString("\n\n")

	b.WriteString(labelStyle.Render("Password: "))
	b.WriteString("\n")
	b.WriteString(passwordStyle.Render(m.passwordInput.View()))
	b.WriteString("\n\n")

	if m.loading {
		b.WriteString(dimStyle.Render("Authenticating..."))
	} else {
		b.WriteString(dimStyle.Render("[Tab] Switch field  [Enter] Login  [Esc] Quit"))
	}
	b.WriteString("\n")

	return b.String()
}

// viewDashboard renders the main dashboard with all metrics sections.
// Layout (section order matches the web dashboard):
//   - Header bar with three zones: title left, version centered,
//     Live/Paused/Refreshing + last-updated time right
//   - Memory and Runtime cards (paired when wide)
//   - Gallery Statistics and File Processing cards (paired when wide;
//     Queued Items is a field inside File Processing)
//   - Cache cards: Cache Preload, Cache Batch Load, HTTP Cache (paired when wide)
//   - Worker Pool and Write Batcher cards (paired when wide)
//   - Footer with controls
func (m Model) viewDashboard() string {
	var b strings.Builder

	leftPart := " Performance & Health Dashboard"

	var centerPart string
	if v := strings.TrimSpace(m.metrics.Version); v != "" {
		centerPart = "Version " + v
	}

	var rightPart string
	switch {
	case m.loading:
		rightPart = "Refreshing... "
	case m.paused:
		rightPart = "Paused "
	default:
		rightPart = "Live " + m.metrics.LastUpdated + " "
	}

	headerWidth := m.width
	if headerWidth < 40 {
		headerWidth = 80
	}

	// Bar content area measured between the header's two horizontal padding cells.
	barWidth := headerWidth - 2
	leftW := lipgloss.Width(leftPart)
	centerW := lipgloss.Width(centerPart)
	rightW := lipgloss.Width(rightPart)

	// Start the center zone so its visual center lands on the bar's middle,
	// keeping at least one cell clear of the left-aligned title. The right
	// zone stays right-aligned via the trailing gap.
	centerStart := max((barWidth-centerW)/2, leftW+1)
	gapLeft := max(centerStart-leftW, 1)
	gapRight := max(barWidth-(centerStart+centerW)-rightW, 1)

	b.WriteString(headerStyle.Width(headerWidth).Render(
		leftPart + strings.Repeat(" ", gapLeft) + centerPart + strings.Repeat(" ", gapRight) + rightPart,
	))
	b.WriteString("\n")

	if msg := strings.TrimSpace(m.metrics.FolderIndexRebuildError); msg != "" {
		b.WriteString(errorBoxStyle.Render(" Folder Index Rebuild Failed\n " + msg + " "))
		b.WriteString("\n")
	}

	b.WriteString(m.renderMemoryRuntime())
	b.WriteString(m.renderGalleryFileProcessing())
	b.WriteString(m.renderCaches())
	b.WriteString(m.renderWorkerWriteBatcher())

	if m.err != nil {
		b.WriteString(errorBoxStyle.Render(" " + m.err.Error() + " "))
		b.WriteString("\n")
	}

	controls := "[r] Refresh"
	if m.autoRefresh {
		if m.paused {
			controls += "  [p] Resume"
		} else {
			controls += "  [p] Pause"
		}
	}
	controls += "  [↑/↓] Scroll  [q] Quit"
	b.WriteString(footerStyle.Render(controls))
	b.WriteString("\n")

	content := b.String()
	lines := strings.Split(content, "\n")

	visibleHeight := m.height - 1
	if visibleHeight < 5 {
		visibleHeight = 20
	}

	if m.scrollY < 0 {
		m.scrollY = 0
	}

	maxScroll := max(len(lines)-visibleHeight, 0)
	if m.scrollY > maxScroll {
		m.scrollY = maxScroll
	}

	start := m.scrollY
	end := min(start+visibleHeight, len(lines))

	visibleLines := lines[start:end]
	return strings.Join(visibleLines, "\n") + "\n"
}

// renderMemoryRuntime renders Memory and Runtime cards together.
// Memory: Allocated, Heap In Use, Heap Released, Heap Objects
// Runtime: Goroutines, CPU Count, Next GC, Uptime
// Cards are paired side by side when m.width >= dashboardPairMinWidth, else
// stacked preserving web order.
func (m Model) renderMemoryRuntime() string {
	var b strings.Builder

	memContent := fmt.Sprintf("%s: %s\n%s: %s\n%s: %s\n%s: %s",
		labelStyle.Render("Allocated"), valueStyle.Render(m.metrics.Memory.Allocated),
		labelStyle.Render("Heap In Use"), valueStyle.Render(m.metrics.Memory.HeapInUse),
		labelStyle.Render("Heap Released"), valueStyle.Render(m.metrics.Memory.HeapReleased),
		labelStyle.Render("Heap Objects"), valueStyle.Render(m.metrics.Memory.HeapObjects),
	)

	runtimeContent := fmt.Sprintf("%s: %s\n%s: %s\n%s: %s\n%s: %s",
		labelStyle.Render("Goroutines"), valueStyle.Render(m.metrics.Runtime.Goroutines),
		labelStyle.Render("CPU Count"), valueStyle.Render(m.metrics.Runtime.CPUCount),
		labelStyle.Render("Next GC"), valueStyle.Render(m.metrics.Runtime.NextGC),
		labelStyle.Render("Uptime"), valueStyle.Render(m.metrics.Runtime.Uptime),
	)

	memCard := cardStyle.Render(titleInCardStyle.Render("Memory") + "\n" + memContent)
	runtimeCard := cardStyle.Render(titleInCardStyle.Render("Runtime") + "\n" + runtimeContent)

	if m.width >= dashboardPairMinWidth {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, memCard, "  ", runtimeCard))
	} else {
		b.WriteString(memCard)
		b.WriteString("\n")
		b.WriteString(runtimeCard)
	}
	b.WriteString("\n")

	return b.String()
}

// renderGalleryFileProcessing renders Gallery Statistics and File Processing
// cards together, matching the web section order. File Processing carries its
// five stats plus Queued Items inside the card (six fields). Cards are paired
// side by side when m.width >= dashboardPairMinWidth, else stacked preserving
// web order.
func (m Model) renderGalleryFileProcessing() string {
	var b strings.Builder

	galleryCard := m.renderGalleryCard()

	fpContent := fmt.Sprintf("%s: %s  %s: %s  %s: %s  %s: %s  %s: %s  %s: %s",
		labelStyle.Render("Total Found"), valueStyle.Render(m.metrics.FileProcessing.TotalFound),
		labelStyle.Render("Existing"), warningStyle.Render(m.metrics.FileProcessing.Existing),
		labelStyle.Render("New"), successStyle.Render(m.metrics.FileProcessing.New),
		labelStyle.Render("Invalid"), errorStyle.Render(m.metrics.FileProcessing.Invalid),
		labelStyle.Render("In Flight"), valueStyle.Render(m.metrics.FileProcessing.InFlight),
		labelStyle.Render("Queued Items"), valueStyle.Render(m.metrics.Queue.Queued),
	)
	fpCard := cardStyle.Render(titleInCardStyle.Render("File Processing") + "\n" + fpContent)

	if m.width >= dashboardPairMinWidth {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, galleryCard, "  ", fpCard))
	} else {
		b.WriteString(galleryCard)
		b.WriteString("\n")
		b.WriteString(fpCard)
	}
	b.WriteString("\n")

	return b.String()
}

// renderGalleryCard renders the Gallery Statistics card alone (used inside
// renderGalleryFileProcessing). Shows Folders, Images, Images Size on one
// line; First and Last discovery dates each on their own line. Presents a
// loading state when the web card has no data yet (Present false).
func (m Model) renderGalleryCard() string {
	var content string
	if !m.metrics.Gallery.Present {
		content = dimStyle.Render("Loading gallery data...")
	} else {
		g := m.metrics.Gallery
		content = fmt.Sprintf("%s: %s  %s: %s  %s: %s\n%s: %s\n%s: %s",
			labelStyle.Render("Folders"), valueStyle.Render(g.Folders),
			labelStyle.Render("Images"), valueStyle.Render(g.Images),
			labelStyle.Render("Images Size"), valueStyle.Render(g.ImagesSize),
			labelStyle.Render("First"), valueStyle.Render(dashOrUnknown(g.FirstDiscovery)),
			labelStyle.Render("Last"), valueStyle.Render(dashOrUnknown(g.LastDiscovery)),
		)
	}
	return cardStyle.Render(titleInCardStyle.Render("Gallery Statistics") + "\n" + content)
}

// renderWorkerWriteBatcher renders Worker Pool and Write Batcher cards
// together, matching the web section order. Cards are paired side by side
// when m.width >= dashboardPairMinWidth, else stacked preserving web order.
// Worker Pool: Running Workers, Completed Tasks, Successful, Failed
// Write Batcher: Pending, Total Flushed, Errors, Batch Size, DQue Overflow
// (Enabled/Off), Disk Usage, Disk Quota
func (m Model) renderWorkerWriteBatcher() string {
	var b strings.Builder

	failedStyle := valueStyle
	if m.metrics.WorkerPool.Failed != "0" {
		failedStyle = errorStyle
	}

	poolContent := fmt.Sprintf("%s: %s/%s\n%s: %s\n%s: %s\n%s: %s",
		labelStyle.Render("Running Workers"), valueStyle.Render(m.metrics.WorkerPool.RunningWorkers),
		valueStyle.Render(m.metrics.WorkerPool.MaxWorkers),
		labelStyle.Render("Completed Tasks"), valueStyle.Render(m.metrics.WorkerPool.CompletedTasks),
		labelStyle.Render("Successful"), successStyle.Render(m.metrics.WorkerPool.Successful),
		labelStyle.Render("Failed"), failedStyle.Render(m.metrics.WorkerPool.Failed),
	)
	poolCard := cardStyle.Render(titleInCardStyle.Render("Worker Pool") + "\n" + poolContent)

	errorsStyle := valueStyle
	if m.metrics.WriteBatcher.TotalErrors != "0" {
		errorsStyle = errorStyle
	}

	dqueValue := "Off"
	if m.metrics.WriteBatcher.DQueEnabled == "Enabled" {
		dqueValue = "Enabled"
	}

	batcherContent := fmt.Sprintf("%s: %s/%s  %s: %s  %s: %s  %s: %s  %s: %s  %s: %s  %s: %s",
		labelStyle.Render("Pending"), valueStyle.Render(m.metrics.WriteBatcher.Pending),
		valueStyle.Render(m.metrics.WriteBatcher.ChannelSize),
		labelStyle.Render("Total Flushed"), valueStyle.Render(m.metrics.WriteBatcher.TotalFlushed),
		labelStyle.Render("Errors"), errorsStyle.Render(m.metrics.WriteBatcher.TotalErrors),
		labelStyle.Render("Batch Size"), valueStyle.Render(m.metrics.WriteBatcher.BatchSize),
		labelStyle.Render("DQue Overflow"), valueStyle.Render(dqueValue),
		labelStyle.Render("Disk Usage"), valueStyle.Render(m.metrics.WriteBatcher.DiskUsageBytes),
		labelStyle.Render("Disk Quota"), valueStyle.Render(m.metrics.WriteBatcher.DiskQuotaBytes),
	)
	batcherCard := cardStyle.Render(titleInCardStyle.Render("Write Batcher") + "\n" + batcherContent)

	if m.width >= dashboardPairMinWidth {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, poolCard, "  ", batcherCard))
	} else {
		b.WriteString(poolCard)
		b.WriteString("\n")
		b.WriteString(batcherCard)
	}
	b.WriteString("\n")

	return b.String()
}

// dashOrUnknown returns "unknown" for empty/blank strings, used for the
// Gallery First/Last discovery dates when the web card omits them.
func dashOrUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

// renderCaches renders Cache Preload, Cache Batch, and HTTP Cache cards side by side.
// Cache Preload: Enabled status, Scheduled, Completed, Failed, Skipped
// Cache Batch: Running status, Progress (normalized), Failed, Skipped
// HTTP Cache: Enabled status, Entries, Size, Max Total, Max Entry, Utilization
func (m Model) renderCaches() string {
	var b strings.Builder

	preloadIcon := "○"
	preloadStatus := dimStyle.Render("Disabled")
	if m.metrics.CachePreload.IsEnabled {
		preloadIcon = "●"
		preloadStatus = successStyle.Render("Enabled")
	}

	preloadFailedStyle := valueStyle
	if m.metrics.CachePreload.Failed != "0" {
		preloadFailedStyle = errorStyle
	}

	preloadContent := fmt.Sprintf("%s %s\n%s: %s\n%s: %s\n%s: %s\n%s: %s",
		preloadIcon, preloadStatus,
		labelStyle.Render("Scheduled"), valueStyle.Render(m.metrics.CachePreload.Scheduled),
		labelStyle.Render("Completed"), successStyle.Render(m.metrics.CachePreload.Completed),
		labelStyle.Render("Failed"), preloadFailedStyle.Render(m.metrics.CachePreload.Failed),
		labelStyle.Render("Skipped"), valueStyle.Render(m.metrics.CachePreload.Skipped),
	)

	batchIcon := "○"
	batchStatus := dimStyle.Render("Idle")
	if m.metrics.CacheBatchLoad.IsRunning {
		batchIcon = "●"
		batchStatus = successStyle.Render("Running")
	}

	batchFailedStyle := valueStyle
	if m.metrics.CacheBatchLoad.Failed != "0" {
		batchFailedStyle = errorStyle
	}

	// Normalize progress: remove newlines, tabs, collapse spaces
	// This handles HTML that may have "0 /\n                0" format
	progress := m.metrics.CacheBatchLoad.Progress
	if progress == "" {
		progress = "0/0"
	}
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

	batchContent := fmt.Sprintf("%s %s\n%s: %s\n%s: %s\n%s: %s\n%s: %s\n%s: %s",
		batchIcon, batchStatus,
		labelStyle.Render("Loaded"), valueStyle.Render(m.metrics.CacheBatchLoad.Loaded),
		labelStyle.Render("Total"), valueStyle.Render(m.metrics.CacheBatchLoad.Total),
		labelStyle.Render("Progress"), progress,
		labelStyle.Render("Failed"), batchFailedStyle.Render(m.metrics.CacheBatchLoad.Failed),
		labelStyle.Render("Skipped"), valueStyle.Render(m.metrics.CacheBatchLoad.Skipped),
	)

	httpIcon := "○"
	httpStatus := dimStyle.Render("Disabled")
	if m.metrics.HTTPCache.Enabled {
		httpIcon = "●"
		httpStatus = successStyle.Render("Enabled")
	}

	httpContent := fmt.Sprintf("%s %s\n%s: %s\n%s: %s\n%s: %s\n%s: %s\n%s: %s",
		httpIcon, httpStatus,
		labelStyle.Render("Entries"), valueStyle.Render(m.metrics.HTTPCache.Entries),
		labelStyle.Render("Size"), valueStyle.Render(m.metrics.HTTPCache.Size),
		labelStyle.Render("Max Total"), valueStyle.Render(m.metrics.HTTPCache.MaxTotal),
		labelStyle.Render("Max Entry"), valueStyle.Render(m.metrics.HTTPCache.MaxEntry),
		labelStyle.Render("Utilization"), valueStyle.Render(m.metrics.HTTPCache.Utilization),
	)

	preloadCard := cardStyle.Width(28).Render(titleInCardStyle.Render("Cache Preload") + "\n" + preloadContent)
	batchCard := cardStyle.Width(32).Render(titleInCardStyle.Render("Cache Batch Load") + "\n" + batchContent)
	httpCard := cardStyle.Width(34).Render(titleInCardStyle.Render("HTTP Cache") + "\n" + httpContent)

	if m.width >= dashboardPairMinWidth {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, preloadCard, " ", batchCard, " ", httpCard))
	} else {
		b.WriteString(preloadCard)
		b.WriteString("\n")
		b.WriteString(batchCard)
		b.WriteString("\n")
		b.WriteString(httpCard)
	}
	b.WriteString("\n")

	return b.String()
}
