package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/tableswap"
)

var _ tableswap.DB = (*sql.Conn)(nil) // REQUIRED gate — if this fails to compile, STOP

// ErrFolderIndexRebuild is returned (wrapped) by RebuildFileFolderIndex for any
// failure other than context cancellation. The startup goroutine uses it to
// decide between Shutdown and keeping the server serving.
var ErrFolderIndexRebuild = errors.New("file_folder_index rebuild failed")

// FolderIndexRow is one materialized navigation row for file_folder_index.
// It carries a Generation that ties it to a specific in-process rebuild so a
// dque leftover from a previous process (with a different generation) cannot
// PK-wedge the rebuild's INSERTs on startup.
type FolderIndexRow struct {
	FileID     int64
	FolderID   int64
	ImageIndex int64
	ImageCount int64
	PrevID     sql.NullInt64
	NextID     sql.NullInt64
	FirstID    int64
	LastID     int64
	Generation int64
}

// RebuildFileFolderIndex materializes file_folder_index from the files table
// without a gallery-wide window query. It streams (file_id, folder_id) pairs on
// the RO pool into a pre-sized [][2]int64, closes the cursor, releases the RO
// conn, then computes per-folder navigation columns in Go and submits each row
// via SubmitFolderIndex to the unified batcher (which INSERTs into
// file_folder_index_new). After the batcher flushes (inflight reaches 0), it
// CreateIndexes and Swaps on a fresh RW conn. Swap DROP TABLEs
// file_folder_index_to_be_dropped before it returns.
//
// The RW conn is Put immediately after CloneEmpty, so Submit and
// the inflight wait never hold a write lock (the batcher and Swap need it free).
func RebuildFileFolderIndex(ctx context.Context, rw, ro *dbconnpool.DbSQLConnPool, batcher UnifiedBatcher) error {
	if batcher == nil {
		return fmt.Errorf("%w: unified batcher is nil", ErrFolderIndexRebuild)
	}
	if ro == nil {
		return fmt.Errorf("%w: RO pool is nil", ErrFolderIndexRebuild)
	}
	// A canceled rebuild is benign (shutdown/HMR), not a fatal rebuild failure:
	// return the bare ctx error so callers do not treat it as ErrFolderIndexRebuild.
	if err := ctx.Err(); err != nil {
		return err
	}

	// Phase 1 — clone dest on a short-lived RW conn, then release it.
	cpc, connErr := rw.Get()
	if connErr != nil {
		return fmt.Errorf("%w: failed to get RW connection: %w", ErrFolderIndexRebuild, connErr)
	}
	if err := tableswap.CloneEmpty(ctx, cpc.Conn, "file_folder_index"); err != nil {
		rw.Put(cpc)
		return fmt.Errorf("%w: clone empty file_folder_index: %w", ErrFolderIndexRebuild, err)
	}
	// Mark rebuild active and bump a fresh generation so a dque leftover from a
	// prior process (different generation) cannot PK-wedge today's INSERTs.
	batcher.SetFolderIndexRebuildActive(true)
	generation := batcher.BumpFolderIndexGeneration()
	defer batcher.SetFolderIndexRebuildActive(false)
	rw.Put(cpc)

	popStarted := time.Now()
	slog.Debug("file_folder_index_new population starting")
	failPop := func(err error) error {
		slog.Debug("file_folder_index_new population failed", "err", err, "elapsed", time.Since(popStarted))
		return err
	}

	// Phase 2 — materialize the ordered (file_id, folder_id) scan into a pre-sized
	// [][2]int64 on the RO pool, close the cursor, release the RO conn, then
	// SubmitFolderIndex per folder. The WAL TRUNCATE skip (scanHeld) applies only
	// while the cursor is open, never across Submit.
	roCpc, roErr := ro.Get()
	if roErr != nil {
		return failPop(fmt.Errorf("%w: failed to get RO connection: %w", ErrFolderIndexRebuild, roErr))
	}
	// abort Puts the RO conn. It is valid only while the conn is still held (COUNT,
	// Query, and scan errors). After ro.Put below, never call abort (a second Put
	// would enqueue the same *CpConn on the idle channel).
	abort := func(perr error) error {
		ro.Put(roCpc)
		return perr
	}

	count, cntErr := roCpc.Queries.CountFilesForFolderIndexRebuild(ctx)
	if cntErr != nil {
		// COUNT is QueryRow; do not set scanHeld for it.
		return failPop(abort(fmt.Errorf("%w: count files for folder-index rebuild: %w", ErrFolderIndexRebuild, cntErr)))
	}
	var pairs [][2]int64
	if count > 0 {
		pairs = make([][2]int64, 0, int(count))
		// Stream every folder-bearing file ordered so Go can compute per-folder
		// navigation columns in one pass. The SQL lives in sqlc/queries; it is a
		// prepared, streaming *sql.Rows custom method on Queries (files table exists at
		// pool init). It reads the RO pool so the RW conn is free for the batcher and
		// the final CreateIndexes/Swap.
		rows, qErr := roCpc.Queries.QueryFilesForFolderIndexRebuild(ctx)
		if qErr != nil {
			// Query failed: never set scan-held; abort with the flag still false.
			return failPop(abort(fmt.Errorf("%w: select files for folder-index rebuild: %w", ErrFolderIndexRebuild, qErr)))
		}
		// Gate WAL TRUNCATE checkpoint on the open RO cursor only, not the whole
		// rebuild-active window. Set after the Query succeeds so an aborted Query
		// never leaves the flag stuck true.
		batcher.SetFolderIndexRebuildScanHeld(true)
		for rows.Next() {
			if ctx.Err() != nil {
				rows.Close()
				batcher.SetFolderIndexRebuildScanHeld(false)
				return failPop(abort(ctx.Err()))
			}
			var fileID, folderID int64
			if scanErr := rows.Scan(&fileID, &folderID); scanErr != nil {
				rows.Close()
				batcher.SetFolderIndexRebuildScanHeld(false)
				return failPop(abort(fmt.Errorf("%w: scan file row for folder-index rebuild: %w", ErrFolderIndexRebuild, scanErr)))
			}
			pairs = append(pairs, [2]int64{fileID, folderID})
		}
		if rerr := rows.Err(); rerr != nil {
			// Scan failed: close the cursor, clear the flag, then Put RO. No writeFolder
			// (the scan did not complete).
			rows.Close()
			batcher.SetFolderIndexRebuildScanHeld(false)
			return failPop(abort(fmt.Errorf("%w: iterate files for folder-index rebuild: %w", ErrFolderIndexRebuild, rerr)))
		}
		rows.Close()
	}
	// Cursor closed (or never opened). Clear the scan-held flag and release the RO
	// conn once, before any Submit. abort is invalid from here on.
	batcher.SetFolderIndexRebuildScanHeld(false)
	ro.Put(roCpc)

	// Walk the materialized pairs in order, grouping by folder_id. writeFolder
	// takes no context; on ctx cancellation return the bare error with no further
	// writeFolder.
	var (
		currentFolder int64 = -1
		ids           []int64
	)
	for _, pair := range pairs {
		folderID := pair[1]
		if folderID != currentFolder && len(ids) > 0 {
			if ctx.Err() != nil {
				return failPop(ctx.Err())
			}
			if werr := writeFolder(batcher, currentFolder, ids, generation); werr != nil {
				return failPop(mapRebuildError(ctx, werr))
			}
			ids = ids[:0]
		}
		currentFolder = folderID
		ids = append(ids, pair[0])
	}
	if len(ids) > 0 {
		if ctx.Err() != nil {
			return failPop(ctx.Err())
		}
		if werr := writeFolder(batcher, currentFolder, ids, generation); werr != nil {
			return failPop(mapRebuildError(ctx, werr))
		}
	}

	// Phase 3 — wait for the batcher to flush all folder-index rows, then swap.
	if waitErr := waitFolderIndexInflight(ctx, batcher, 0); waitErr != nil {
		if ctx.Err() != nil {
			return failPop(ctx.Err())
		}
		return failPop(fmt.Errorf("%w: wait for folder-index flush: %w", ErrFolderIndexRebuild, waitErr))
	}
	slog.Debug("file_folder_index_new population completed successfully", "elapsed", time.Since(popStarted))

	swapCpc, swapErr := rw.Get()
	if swapErr != nil {
		return fmt.Errorf("%w: failed to get RW connection for swap: %w", ErrFolderIndexRebuild, swapErr)
	}
	if err := tableswap.CreateIndexes(ctx, swapCpc.Conn, "file_folder_index"); err != nil {
		rw.Put(swapCpc)
		return fmt.Errorf("%w: create file_folder_index indexes on dest: %w", ErrFolderIndexRebuild, err)
	}
	if err := tableswap.Swap(ctx, swapCpc, rw.Put, "file_folder_index"); err != nil {
		return fmt.Errorf("%w: swap file_folder_index: %w", ErrFolderIndexRebuild, err)
	}
	return nil
}

// writeFolder computes the per-folder navigation columns for ids and submits one
// FolderIndexRow per file via batcher.SubmitFolderIndex (no context). On Submit
// error it returns immediately so the caller can abort without waiting or swapping.
func writeFolder(batcher UnifiedBatcher, folderID int64, ids []int64, generation int64) error {
	n := int64(len(ids))
	for i, fileID := range ids {
		var prevID, nextID sql.NullInt64
		if i > 0 {
			prevID = sql.NullInt64{Int64: ids[i-1], Valid: true}
		}
		if i < len(ids)-1 {
			nextID = sql.NullInt64{Int64: ids[i+1], Valid: true}
		}
		row := FolderIndexRow{
			FileID:     fileID,
			FolderID:   folderID,
			ImageIndex: int64(i + 1),
			ImageCount: n,
			PrevID:     prevID,
			NextID:     nextID,
			FirstID:    ids[0],
			LastID:     ids[n-1],
			Generation: generation,
		}
		if err := batcher.SubmitFolderIndex(row); err != nil {
			return err
		}
	}
	return nil
}

// waitFolderIndexInflight polls batcher.FolderIndexInflight until it reaches 0,
// honoring ctx. stall is the maximum time the inflight count may remain
// unchanged before giving up; stall == 0 means the default 30s. It is only
// called after every writeFolder call has succeeded.
func waitFolderIndexInflight(ctx context.Context, batcher UnifiedBatcher, stall time.Duration) error {
	if stall == 0 {
		stall = 30 * time.Second
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	last := batcher.FolderIndexInflight()
	lastChange := time.Now()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		cur := batcher.FolderIndexInflight()
		now := time.Now()
		if cur == 0 {
			return nil
		}
		if cur != last {
			last = cur
			lastChange = now
		} else if now.Sub(lastChange) >= stall {
			return fmt.Errorf("folder-index inflight stuck at %d for %s", cur, stall)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// mapRebuildError converts a writeFolder/SubmitFolderIndex error into the
// sentinel, unless the rebuild context was cancelled (in which case the bare
// ctx.Err is returned so startup does not Shutdown on a benign cancel).
func mapRebuildError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: %w", ErrFolderIndexRebuild, err)
}
