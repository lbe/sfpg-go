package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
)

// Style definitions for the dashboard UI using lipgloss.
// Colors use 256-color terminal palette indices.
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
		if m.err == client.ErrNetworkError {
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
// Layout:
//   - Header with title and timestamp (far right)
//   - Module status (single line)
//   - Memory and Runtime cards (side by side)
//   - Write Batcher card
//   - Worker Pool and File Queue cards (side by side)
//   - File Processing card
//   - Cache cards (Preload, Batch, HTTP - side by side)
//   - Footer with controls
func (m Model) viewDashboard() string {
	var b strings.Builder

	leftPart := " System Dashboard"
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

	spacing := headerWidth - lipgloss.Width(leftPart) - lipgloss.Width(rightPart) - 2
	if spacing < 1 {
		spacing = 1
	}

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("62")).
		Padding(0, 1).
		Width(headerWidth).
		Render(leftPart + strings.Repeat(" ", spacing) + rightPart)
	b.WriteString(header)
	b.WriteString("\n")

	b.WriteString(m.renderModules())
	b.WriteString(m.renderMemoryRuntime())
	b.WriteString(m.renderWriteBatcher())
	b.WriteString(m.renderWorkerPoolQueue())
	b.WriteString(m.renderFileProcessing())
	b.WriteString(m.renderCaches())

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

	maxScroll := len(lines) - visibleHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scrollY > maxScroll {
		m.scrollY = maxScroll
	}

	start := m.scrollY
	end := start + visibleHeight
	if end > len(lines) {
		end = len(lines)
	}

	visibleLines := lines[start:end]
	return strings.Join(visibleLines, "\n") + "\n"
}

// renderModules renders the module status section on a single line.
// Active modules show with a filled circle (●), inactive with empty (○).
func (m Model) renderModules() string {
	var b strings.Builder

	b.WriteString(titleInCardStyle.Render("Module Status: "))

	if len(m.metrics.Modules) == 0 {
		b.WriteString(dimStyle.Render("No modules registered"))
		b.WriteString("\n")
		return b.String()
	}

	for i, mod := range m.metrics.Modules {
		if i > 0 {
			b.WriteString("  ")
		}
		statusStyle := dimStyle
		statusIcon := "○"
		switch mod.Status {
		case "active":
			statusStyle = successStyle
			statusIcon = "●"
		case "recent":
			statusStyle = warningStyle
			statusIcon = "●"
		}

		b.WriteString(fmt.Sprintf("%s %s %s", statusIcon, mod.Name, statusStyle.Render(mod.Status)))
	}
	b.WriteString("\n")
	return b.String()
}

// renderMemoryRuntime renders Memory and Runtime cards side by side.
// Memory: Allocated, Heap In Use, Heap Released, Heap Objects
// Runtime: Goroutines, CPU Count, Next GC, Uptime
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

	row := lipgloss.JoinHorizontal(lipgloss.Top, memCard, "  ", runtimeCard)
	b.WriteString(row)
	b.WriteString("\n")

	return b.String()
}

// renderWriteBatcher renders the Write Batcher card.
// Shows: Pending/ChannelSize, Flushed, Errors, Batch Size
func (m Model) renderWriteBatcher() string {
	var b strings.Builder

	errorsStyle := valueStyle
	if m.metrics.WriteBatcher.TotalErrors != "0" {
		errorsStyle = errorStyle
	}

	content := fmt.Sprintf("%s: %s/%s  %s: %s  %s: %s  %s: %s",
		labelStyle.Render("Pending"), valueStyle.Render(m.metrics.WriteBatcher.Pending),
		valueStyle.Render(m.metrics.WriteBatcher.ChannelSize),
		labelStyle.Render("Flushed"), valueStyle.Render(m.metrics.WriteBatcher.TotalFlushed),
		labelStyle.Render("Errors"), errorsStyle.Render(m.metrics.WriteBatcher.TotalErrors),
		labelStyle.Render("Batch"), valueStyle.Render(m.metrics.WriteBatcher.BatchSize),
	)

	b.WriteString(cardStyle.Render(titleInCardStyle.Render("Write Batcher") + "\n" + content))
	b.WriteString("\n")

	return b.String()
}

// renderWorkerPoolQueue renders Worker Pool and File Queue cards side by side.
// Worker Pool: Running/Max, Completed, Successful, Failed
// File Queue: Queued/Capacity, Utilization, Available
func (m Model) renderWorkerPoolQueue() string {
	var b strings.Builder

	failedStyle := valueStyle
	if m.metrics.WorkerPool.Failed != "0" {
		failedStyle = errorStyle
	}

	poolContent := fmt.Sprintf("%s: %s/%s\n%s: %s\n%s: %s\n%s: %s",
		labelStyle.Render("Running"), valueStyle.Render(m.metrics.WorkerPool.RunningWorkers),
		valueStyle.Render(m.metrics.WorkerPool.MaxWorkers),
		labelStyle.Render("Completed"), valueStyle.Render(m.metrics.WorkerPool.CompletedTasks),
		labelStyle.Render("Successful"), successStyle.Render(m.metrics.WorkerPool.Successful),
		labelStyle.Render("Failed"), failedStyle.Render(m.metrics.WorkerPool.Failed),
	)

	queueContent := fmt.Sprintf("%s: %s/%s\n%s: %s\n%s: %s",
		labelStyle.Render("Queued"), valueStyle.Render(m.metrics.Queue.Queued),
		valueStyle.Render(m.metrics.Queue.Capacity),
		labelStyle.Render("Utilization"), valueStyle.Render(m.metrics.Queue.Utilization),
		labelStyle.Render("Available"), valueStyle.Render(m.metrics.Queue.Available),
	)

	poolCard := cardStyle.Render(titleInCardStyle.Render("Worker Pool") + "\n" + poolContent)
	queueCard := cardStyle.Render(titleInCardStyle.Render("File Queue") + "\n" + queueContent)

	row := lipgloss.JoinHorizontal(lipgloss.Top, poolCard, "  ", queueCard)
	b.WriteString(row)
	b.WriteString("\n")

	return b.String()
}

// renderFileProcessing renders the File Processing card.
// Shows: Total, Existing, New, Invalid, In Flight counts
func (m Model) renderFileProcessing() string {
	var b strings.Builder

	content := fmt.Sprintf("%s: %s  %s: %s  %s: %s  %s: %s  %s: %s",
		labelStyle.Render("Total"), valueStyle.Render(m.metrics.FileProcessing.TotalFound),
		labelStyle.Render("Existing"), warningStyle.Render(m.metrics.FileProcessing.Existing),
		labelStyle.Render("New"), successStyle.Render(m.metrics.FileProcessing.New),
		labelStyle.Render("Invalid"), errorStyle.Render(m.metrics.FileProcessing.Invalid),
		labelStyle.Render("In Flight"), valueStyle.Render(m.metrics.FileProcessing.InFlight),
	)

	b.WriteString(cardStyle.Render(titleInCardStyle.Render("File Processing") + "\n" + content))
	b.WriteString("\n")

	return b.String()
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

	batchContent := fmt.Sprintf("%s %s\n%s: %s\n%s: %s\n%s: %s",
		batchIcon, batchStatus,
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
	batchCard := cardStyle.Width(32).Render(titleInCardStyle.Render("Cache Batch") + "\n" + batchContent)
	httpCard := cardStyle.Width(34).Render(titleInCardStyle.Render("HTTP Cache") + "\n" + httpContent)

	row := lipgloss.JoinHorizontal(lipgloss.Top, preloadCard, " ", batchCard, " ", httpCard)
	b.WriteString(row)
	b.WriteString("\n")

	return b.String()
}
