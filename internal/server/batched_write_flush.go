package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/thumbnail"
)

// flushBatchedWrites processes a unified batch within a transaction.
// It segregates by type, honors cache route strategy, handles cache eviction,
// and cleans up resources.
func (app *App) flushBatchedWrites(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
	fileWrites := make([]*files.File, 0, len(batch))
	galleryCache := make([]*cachelite.HTTPCacheEntry, 0, len(batch))
	otherCache := make([]*cachelite.HTTPCacheEntry, 0, len(batch))

	// Segregate by type and maintain route-based cache strategy
	for _, bw := range batch {
		switch {
		case bw.File != nil:
			fileWrites = append(fileWrites, bw.File)
		case bw.CacheEntry != nil:
			// Maintain existing route strategy: /gallery/ separate from others
			if strings.HasPrefix(bw.CacheEntry.Path, "/gallery/") {
				galleryCache = append(galleryCache, bw.CacheEntry)
			} else {
				otherCache = append(otherCache, bw.CacheEntry)
			}
		}
	}

	// Process files using the prepared queries threaded from BeginTx.
	// app.batcherQueries holds the CpConn's prepared *CustomQueries (set in
	// BeginTx); WithTx(tx) propagates all prepared statements onto the tx so
	// every statement reuses the already-compiled plan instead of recompiling
	// from raw SQL text on each call.
	//
	// ONE Importer is constructed per batch and reused across all files so its
	// intra-batch folder cache (UpsertPathChain) and tiled-dir set persist —
	// that is what eliminates the repeated per-segment GetFolderByPath queries
	// and the redundant folder-tile view queries for files sharing a directory.
	qtx := app.batcherQueries.WithTx(tx)
	imp := &gallerylib.Importer{Q: qtx}
	for _, f := range fileWrites {
		if err := files.WriteFileInTx(ctx, imp, f); err != nil {
			return fmt.Errorf("write file %s: %w", f.Path, err)
		}
		// Don't cleanup thumbnail here - done in OnError or after successful commit
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

	// Log batch path stats for performance analysis
	if len(fileWrites) > 0 {
		maxDepth := 0
		uniqueDirs := make(map[string]struct{}, len(fileWrites))
		for _, f := range fileWrites {
			dir := filepath.Dir(f.Path)
			uniqueDirs[dir] = struct{}{}
			if dir == "." {
				continue
			}
			depth := len(strings.FieldsFunc(dir, func(c rune) bool { return c == '/' }))
			if depth > maxDepth {
				maxDepth = depth
			}
		}
		slog.Debug("batched flush: file batch path stats",
			"files", len(fileWrites),
			"gallery_cache", len(galleryCache),
			"other_cache", len(otherCache),
			"max_depth", maxDepth,
			"unique_dirs", len(uniqueDirs))
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
