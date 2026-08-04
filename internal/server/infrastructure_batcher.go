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
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// =====================================================================
// Write batcher
// =====================================================================

func (s *InfrastructureService) buildWriteBatcher(ctx context.Context, maxBatchSize int, flushInterval time.Duration, deferDQueDrain bool) (*writebatcher.WriteBatcher[BatchedWrite], error) {
	var cpcRw *dbconnpool.CpConn

	return writebatcher.New(ctx, writebatcher.Config[BatchedWrite]{
		MaxBatchSize:        maxBatchSize,
		MaxBatchBytes:       int64(8 * 1024 * 1024),
		FlushInterval:       flushInterval,
		ChannelSize:         4096,
		DQueDirPath:         s.dqueDirPath,
		DQueItemsPerSegment: 250,
		DeferDQueDrain:      deferDQueDrain,
		SizeFunc:            func(bw BatchedWrite) int64 { return bw.Size() },
		BeginTx: func(ctx context.Context) (*sql.Tx, error) {
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
			cleanupBatchedWriteResources(batch)
		},
		OnError: func(err error, batch []BatchedWrite) {
			if cpcRw != nil {
				s.dbRwPool.Put(cpcRw)
				cpcRw = nil
			}
			s.batcherQueries = nil
			var filesCount, cacheEntriesCount int
			for _, bw := range batch {
				switch {
				case bw.File != nil:
					filesCount++
				case bw.CacheEntry != nil:
					cacheEntriesCount++
				}
			}
			slog.Error("failed to flush unified batch",
				"err", err, "files", filesCount, "cache_entries", cacheEntriesCount)
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

// =====================================================================
// Batched write flush
// =====================================================================

func (s *InfrastructureService) flushBatchedWrites(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
	fileWrites := make([]*files.File, 0, len(batch))
	galleryCache := make([]*cachelite.HTTPCacheEntry, 0, len(batch))
	otherCache := make([]*cachelite.HTTPCacheEntry, 0, len(batch))

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
		}
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
