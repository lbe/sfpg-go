package server

import (
	"github.com/lbe/sfpg-go/internal/cachelite"
)

// submitCacheWrite submits a cache entry to the unified batcher.
// Returns entry to pool if submission fails.
// This replaces the old cacheWriteQueue channel.
func (app *App) submitCacheWrite(entry *cachelite.HTTPCacheEntry) {
	app.InfrastructureService.submitCacheWrite(entry)
}
