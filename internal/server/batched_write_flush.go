package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/thumbnail"
)

// flushBatchedWrites processes a unified batch within a transaction.
// It segregates by type, honors cache route strategy, handles cache eviction,
// and cleans up resources.
func (app *App) flushBatchedWrites(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
	fileWrites := make([]*files.File, 0, len(batch))
	invalidFileWrites := make([]gallerydb.UpsertInvalidFileParams, 0, len(batch))
	galleryCache := make([]*cachelite.HTTPCacheEntry, 0, len(batch))
	otherCache := make([]*cachelite.HTTPCacheEntry, 0, len(batch))

	// Segregate by type and maintain route-based cache strategy
	for _, bw := range batch {
		switch {
		case bw.File != nil:
			fileWrites = append(fileWrites, bw.File)
		case bw.InvalidFile != nil:
			invalidFileWrites = append(invalidFileWrites, *bw.InvalidFile)
		case bw.CacheEntry != nil:
			// Maintain existing route strategy: /gallery/ separate from others
			if strings.HasPrefix(bw.CacheEntry.Path, "/gallery/") {
				galleryCache = append(galleryCache, bw.CacheEntry)
			} else {
				otherCache = append(otherCache, bw.CacheEntry)
			}
		}
	}

	// Process files
	for _, f := range fileWrites {
		if err := files.WriteFileInTx(ctx, tx, f); err != nil {
			return fmt.Errorf("write file %s: %w", f.Path, err)
		}
		// Don't cleanup thumbnail here - done in OnError or after successful commit
	}

	// Process invalid files
	q := gallerydb.New(tx)
	for _, params := range invalidFileWrites {
		if err := q.UpsertInvalidFile(ctx, params); err != nil {
			return fmt.Errorf("upsert invalid file %s: %w", params.Path, err)
		}
	}

	// Process gallery cache entries (maintain individual semantics even though batched)
	for _, entry := range galleryCache {
		if err := cachelite.StoreCacheEntryInTx(ctx, tx, entry); err != nil {
			return fmt.Errorf("store gallery cache %s: %w", entry.Path, err)
		}
		// Update size counter for new entry
		if entry.ContentLength.Valid {
			app.cacheSizeBytes.Add(entry.ContentLength.Int64)
		}
	}

	// Process other cache entries
	for _, entry := range otherCache {
		if err := cachelite.StoreCacheEntryInTx(ctx, tx, entry); err != nil {
			return fmt.Errorf("store cache %s: %w", entry.Path, err)
		}
		// Update size counter for new entry
		if entry.ContentLength.Valid {
			app.cacheSizeBytes.Add(entry.ContentLength.Int64)
		}
	}

	return nil
}

// cleanupBatchedWriteResources returns pooled resources to pools and clears references.
// Called from OnError callback and after successful flush.
func cleanupBatchedWriteResources(batch []BatchedWrite) {
	for i := range batch {
		bw := &batch[i]
		if bw.File != nil && bw.File.Thumbnail != nil {
			thumbnail.PutBytesBuffer(bw.File.Thumbnail)
			bw.File.Thumbnail = nil
		}
		if bw.CacheEntry != nil {
			cachelite.PutHTTPCacheEntry(bw.CacheEntry)
			bw.CacheEntry = nil
		}
		bw.File = nil
		bw.InvalidFile = nil
	}
}

// maybeEvictCacheEntries checks if cache eviction is needed after successful flush.
// Called from OnSuccess callback (outside of transaction to avoid deadlocks).
func (app *App) maybeEvictCacheEntries(batch []BatchedWrite) {
	// Check if any cache entries were written
	hasCacheEntries := false
	for _, bw := range batch {
		if bw.CacheEntry != nil {
			hasCacheEntries = true
			break
		}
	}
	if !hasCacheEntries || app.cacheMW == nil {
		return
	}

	cfg := app.cacheMW.Config()
	if cfg.MaxTotalSize <= 0 {
		return
	}

	// Check if eviction is needed (outside transaction to avoid deadlocks)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	currentSize, err := cachelite.GetCacheSizeBytes(ctx, app.dbRwPool)
	if err != nil {
		slog.Warn("failed to get cache size for eviction check", "err", err)
		return
	}

	if currentSize > cfg.MaxTotalSize {
		targetFree := currentSize - cfg.MaxTotalSize
		// Add some buffer to avoid constant eviction
		targetFree += cfg.MaxTotalSize / 10
		freed, err := cachelite.EvictLRU(ctx, app.dbRwPool, targetFree)
		if err != nil {
			slog.Warn("cache eviction failed", "err", err, "target", targetFree)
		} else {
			slog.Debug("cache eviction completed", "freed", freed, "target", targetFree)
			// Update size counter to reflect eviction
			if freed > 0 {
				app.cacheSizeBytes.Add(-freed)
			}
		}
	}
}
