package server

import (
	"log/slog"

	"go.local/sfpg/internal/cachelite"
)

// submitCacheWrite submits a cache entry to the unified batcher.
// Returns entry to pool if submission fails.
// This replaces the old cacheWriteQueue channel.
func (app *App) submitCacheWrite(entry *cachelite.HTTPCacheEntry) {
	if app.writeBatcher == nil {
		slog.Warn("unified batcher not available, dropping cache write", "path", entry.Path)
		cachelite.PutHTTPCacheEntry(entry)
		return
	}

	// Create adapter to access SubmitCache (not part of UnifiedBatcher interface)
	adapter := &batcherAdapter{wb: app.writeBatcher}

	if err := adapter.SubmitCache(entry); err != nil {
		slog.Debug("failed to submit cache write", "path", entry.Path, "err", err)
		// Return entry to pool on error
		cachelite.PutHTTPCacheEntry(entry)
		return
	}

	// On success, entry will be returned to pool after flush
	// (handled in flushBatchedWrites or OnError callback)
}
