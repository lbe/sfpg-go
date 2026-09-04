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
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// =====================================================================
// Write batcher
// =====================================================================

func (s *InfrastructureService) buildWriteBatcher(ctx context.Context, maxBatchSize int, flushInterval time.Duration, deferDQueDrain bool, dqueMaxDiskBytes int64) (*writebatcher.WriteBatcher[BatchedWrite], error) {
	var cpcRw *dbconnpool.CpConn

	return writebatcher.New(ctx, writebatcher.Config[BatchedWrite]{
		MaxBatchSize:        maxBatchSize,
		MaxBatchBytes:       int64(8 * 1024 * 1024),
		FlushInterval:       flushInterval,
		ChannelSize:         4096,
		DQueDirPath:         s.dqueDirPath,
		DQueItemsPerSegment: 250,
		MaxDiskBytes:        dqueMaxDiskBytes,
		DeferDQueDrain:      deferDQueDrain,
		SizeFunc:            func(bw BatchedWrite) int64 { return bw.Size() },
		DropWithoutFlush:    s.dropFolderIndexBatch,
		BeginTx: func(ctx context.Context) (*sql.Tx, error) {
			if s.testSeams.OnBeginTx != nil {
				s.testSeams.OnBeginTx()
			}
			var getErr error
			cpcRw, getErr = s.dbRwPool.Get()
			if getErr != nil {
				return nil, getErr
			}
			s.batcherQueries = cpcRw.Queries
			return cpcRw.Conn.BeginTx(ctx, nil)
		},
		Flush: func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
			if s.testSeams.FlushBatchedWrites != nil {
				return s.testSeams.FlushBatchedWrites(ctx, tx, batch)
			}
			return s.flushBatchedWrites(ctx, tx, batch)
		},
		OnSuccess: func(batch []BatchedWrite) {
			if cpcRw != nil {
				if s.testSeams.OnPut != nil {
					s.testSeams.OnPut()
				}
				s.dbRwPool.Put(cpcRw)
				cpcRw = nil
			}
			s.batcherQueries = nil
			var totalAdded, entriesAdded int64
			for _, bw := range batch {
				if bw.CacheEntry != nil {
					totalAdded += int64(len(bw.CacheEntry.Body))
					entriesAdded++
				}
			}
			if totalAdded > 0 {
				s.cacheSizeBytes.Add(totalAdded)
			}
			if entriesAdded > 0 {
				s.cacheEntryCount.Add(entriesAdded)
			}
			s.maybeEvictCacheEntries(batch)
			// Decrement folder-index inflight for rows this process submitted
			// (matching current rebuild generation) BEFORE cleanup nils FolderIndex.
			// Stale leftover dque rows have a different generation and must not
			// decrement, or a rebuild wait could 30s-stall. Never below 0.
			for _, bw := range batch {
				if bw.FolderIndex != nil && bw.FolderIndex.Generation == s.folderIndexGeneration.Load() {
					if s.folderIndexInflight.Load() > 0 {
						s.folderIndexInflight.Add(-1)
					}
				}
			}
			cleanupBatchedWriteResources(batch)
		},
		OnError: func(err error, batch []BatchedWrite) {
			if cpcRw != nil {
				if s.testSeams.OnPut != nil {
					s.testSeams.OnPut()
				}
				s.dbRwPool.Put(cpcRw)
				cpcRw = nil
			}
			s.batcherQueries = nil
			var filesCount, cacheEntriesCount, folderIndexCount int
			for _, bw := range batch {
				switch {
				case bw.File != nil:
					filesCount++
				case bw.CacheEntry != nil:
					cacheEntriesCount++
				case bw.FolderIndex != nil:
					folderIndexCount++
				}
			}
			slog.Error("failed to flush unified batch",
				"err", err, "files", filesCount, "cache_entries", cacheEntriesCount, "folder_index", folderIndexCount)
			if cacheEntriesCount > 0 {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				s.resyncCacheSizeFromDB(ctx)
			}
		},
		OnAfterCommit:       s.postCommitMaintenance,
		MaintenanceInterval: 5 * time.Minute,
	})
}

// dropFolderIndexBatch is the DropWithoutFlush classifier. It returns true
// (drop before BeginTx) iff every item is a FolderIndex (no File, no Cache)
// and rebuild is inactive. Dest-missing stays in flushBatchedWrites after
// BeginTx. Does not use batcherQueries.
func (s *InfrastructureService) dropFolderIndexBatch(batch []BatchedWrite) bool {
	if !s.indexOnlyAndRebuildInactive(batch) {
		return false
	}
	slog.Info("writebatcher: dropping folder-index batch before BeginTx",
		"count", len(batch), "reason", "rebuild inactive")
	return true
}

// indexOnlyAndRebuildInactive is a shared helper for skip classification.
// Dest-exists is not checked here — it stays in flushBatchedWrites after
// BeginTx using batcherQueries.
func (s *InfrastructureService) indexOnlyAndRebuildInactive(batch []BatchedWrite) bool {
	if s.folderIndexRebuildActive.Load() {
		return false
	}
	for _, bw := range batch {
		if bw.FolderIndex == nil || bw.File != nil || bw.CacheEntry != nil {
			return false
		}
	}
	return true
}

// =====================================================================
// Batched write flush
// =====================================================================

func (s *InfrastructureService) flushBatchedWrites(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
	s.lastFlushWroteDML.Store(false)
	fileWrites := make([]*files.File, 0, len(batch))
	galleryCache := make([]*cachelite.HTTPCacheEntry, 0, len(batch))
	otherCache := make([]*cachelite.HTTPCacheEntry, 0, len(batch))
	folderIndexWrites := make([]*files.FolderIndexRow, 0, len(batch))

	for _, bw := range batch {
		switch {
		case bw.File != nil:
			fileWrites = append(fileWrites, bw.File)
		case bw.CacheEntry != nil:
			if strings.HasPrefix(bw.CacheEntry.Path, "/gallery/") {
				galleryCache = append(galleryCache, bw.CacheEntry)
			} else {
				otherCache = append(otherCache, bw.CacheEntry)
			}
		case bw.FolderIndex != nil:
			folderIndexWrites = append(folderIndexWrites, bw.FolderIndex)
		}
	}

	// FolderIndex rows are only flushed into file_folder_index_new when an
	// in-process rebuild is active and the dest table exists. Otherwise (or
	// when a row's generation is stale) they are skipped so a dque leftover
	// cannot PK-wedge the rebuild. File/Cache writes always proceed. The dest
	// existence check is a non-aborting gallerydb query (the catalog lookup lives in
	// gallerydb, NOT in infrastructure_batcher.go per G1/G3); a missing dest skips
	// index rows only and leaves the tx usable so File/Cache still commit.
	// Drop-all (index-only + rebuild inactive) uses the same classifier as
	// DropWithoutFlush so the predicates cannot diverge if this path is hit.
	if s.indexOnlyAndRebuildInactive(batch) {
		folderIndexWrites = nil
	}
	rebuildActive := s.folderIndexRebuildActive.Load()
	destExists := false
	if len(folderIndexWrites) > 0 && rebuildActive {
		var existsErr error
		destExists, existsErr = s.batcherQueries.FileFolderIndexNewExists(ctx)
		if existsErr != nil {
			return fmt.Errorf("check file_folder_index_new existence: %w", existsErr)
		}
	}

	if len(folderIndexWrites) > 0 && (!rebuildActive || !destExists) {
		reason := "rebuild inactive"
		if rebuildActive {
			reason = "dest table missing"
		}
		slog.Error("skipping folder index writes: "+reason,
			"count", len(folderIndexWrites), "rebuild_active", rebuildActive, "dest_exists", destExists)
		folderIndexWrites = nil
	}

	qtx := s.batcherQueries.WithTx(tx)
	imp := &gallerylib.Importer{Q: qtx, OnFolderCreated: s.OnFolderCreated}
	for _, f := range fileWrites {
		if err := files.WriteFileInTx(ctx, imp, f); err != nil {
			return fmt.Errorf("write file %s: %w", f.Path, err)
		}
	}
	for _, entry := range galleryCache {
		if err := cachelite.StoreCacheEntryInTx(ctx, tx, entry); err != nil {
			return fmt.Errorf("store gallery cache %s: %w", entry.Path, err)
		}
	}
	for _, entry := range otherCache {
		if err := cachelite.StoreCacheEntryInTx(ctx, tx, entry); err != nil {
			return fmt.Errorf("store cache %s: %w", entry.Path, err)
		}
	}
	// Collect matching-generation rows, then flush them in one prepared INSERT
	// on this transaction via CustomQueries.InsertFileFolderIndexNewRows (G2/G6).
	if rebuildActive && destExists && len(folderIndexWrites) > 0 {
		indexParams := make([]gallerydb.InsertFileFolderIndexNewParams, 0, len(folderIndexWrites))
		for _, row := range folderIndexWrites {
			if row.Generation != s.folderIndexGeneration.Load() {
				slog.Error("skipping stale folder index write",
					"file_id", row.FileID, "row_generation", row.Generation, "current_generation", s.folderIndexGeneration.Load())
				continue
			}
			indexParams = append(indexParams, gallerydb.InsertFileFolderIndexNewParams{
				FileID:     row.FileID,
				FolderID:   row.FolderID,
				ImageIndex: row.ImageIndex,
				ImageCount: row.ImageCount,
				PrevID:     row.PrevID,
				NextID:     row.NextID,
				FirstID:    row.FirstID,
				LastID:     row.LastID,
			})
		}
		if len(indexParams) > 0 {
			if err := qtx.InsertFileFolderIndexNewRows(ctx, indexParams); err != nil {
				return fmt.Errorf("insert folder index rows: %w", err)
			}
			s.lastFlushWroteDML.Store(true)
		}
	}
	if len(fileWrites) > 0 || len(galleryCache) > 0 || len(otherCache) > 0 {
		s.lastFlushWroteDML.Store(true)
	}
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
			"files", len(fileWrites), "gallery_cache", len(galleryCache),
			"other_cache", len(otherCache), "max_depth", maxDepth,
			"unique_dirs", len(uniqueDirs))
	}
	return nil
}
