// Package main provides a terminal user interface (TUI) dashboard for monitoring
// sfpg-go server metrics in real-time.
//
// The dashboard displays system metrics including:
//   - Module status (discovery, cache_preload)
//   - Memory statistics (allocated, heap in use/released/objects)
//   - Runtime statistics (goroutines, CPU count, next GC, uptime)
//   - Write batcher metrics (pending, flushed, errors, batch size)
//   - Worker pool metrics (running, completed, successful, failed)
//   - File queue metrics (queued, utilization, available)
//   - File processing statistics (total, existing, new, invalid, in flight)
//   - Cache statistics (preload, batch load, HTTP cache)
//
// # Authentication
//
// Credentials can be provided via environment variables:
//
//	SFPG_USERNAME - Username for authentication
//	SFPG_PASSWORD - Password for authentication
//
// If credentials are not provided, an interactive login prompt is displayed.
//
// # Usage
//
//	sfpg-go-dashboard [options]
//
// Options:
//
//	-server      Server URL (default: http://localhost:8083)
//	-refresh     Auto-refresh interval (default: 5s)
//	-no-refresh  Disable auto-refresh
//	-help        Show help
//
// # Keyboard Controls
//
//	↑/↓ or k/j  Scroll content
//	r           Manual refresh
//	p           Pause/resume auto-refresh
//	Tab         Switch between username/password fields (login screen)
//	Enter       Submit credentials (login screen)
//	Esc         Quit (login screen)
//	q           Quit (dashboard)
//
// # Architecture
//
// The application follows the Bubble Tea Model-View-Update (MVU) pattern:
//   - Model: Contains all application state including metrics, auth state, and UI state
//   - View: Renders the model as a string for display
//   - Update: Handles messages and updates the model accordingly
//
// Sub-packages:
//   - client: HTTP client for communicating with the sfpg-go server
//   - config: Command-line and environment variable configuration parsing
//   - parser: HTML parsing for dashboard metrics extraction
package main
