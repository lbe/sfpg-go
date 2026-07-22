// Package cachepreload provides cache preloading when folders are opened.
package cachepreload

import "context"

// PreloadService defines the interface for cache preloading operations.
// Handlers receive this interface via dependency injection (Option B pattern).
type PreloadService interface {
	// ScheduleFolderPreload schedules background cache preload for a folder.
	// sessionID is used for task cancellation when user navigates away.
	// Non-blocking (fire-and-forget).
	ScheduleFolderPreload(ctx context.Context, folderID int64, sessionID string)

	// SetEnabled dynamically enables or disables cache preloading.
	// When disabled, all pending tasks are cancelled.
	SetEnabled(enabled bool)

	// IsEnabled returns whether cache preloading is currently enabled.
	IsEnabled() bool
}
