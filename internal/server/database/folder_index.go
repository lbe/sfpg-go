package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/tableswap"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// ErrFolderIndexRebuild wraps every rebuild failure except cancellation.
// The startup goroutine uses it to choose between shutting down and serving.
var ErrFolderIndexRebuild = errors.New("file_folder_index rebuild failed")

const (
	// rebuildChunkPairs is the least number of (file_id, folder_id) pairs the
	// rebuild reads at a time. A chunk ends at the next folder boundary.
	rebuildChunkPairs = 1 << 18
	// stallLimit is how long the rebuild waits for the batcher to make
	// progress before it gives up.
	stallLimit = 30 * time.Second
	// pollInterval is how often the rebuild checks the batcher's progress.
	pollInterval = 100 * time.Millisecond
)

// folderIndexRow is one row of file_folder_index. Generation ties it to one
// rebuild in this process, so a row queued on disk by an earlier process is
// skipped instead of colliding with today's rows.
type folderIndexRow struct {
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

// folderIndex is the rebuild state the flush shares.
type folderIndex struct {
	active   atomic.Bool // rows may be written to file_folder_index_new
	scanHeld atomic.Bool // the rebuild's read cursor is open; no WAL truncation

	// mu guards generation and inflight, which change together: a commit of
	// an earlier generation's rows must not count against the current one.
	mu         sync.Mutex
	generation int64 // the current rebuild
	inflight   int64 // rows of the current rebuild submitted and not yet committed
}

// bumpGeneration starts a new generation and returns it, with no rows in
// flight: rows of an earlier generation are skipped, not counted. It uses the
// clock, not a counter from 0, so it cannot match rows queued by an earlier
// process.
func (x *folderIndex) bumpGeneration() int64 {
	x.mu.Lock()
	defer x.mu.Unlock()
	next := time.Now().UnixNano()
	if next == 0 || next == x.generation {
		next = x.generation + 1
	}
	x.generation, x.inflight = next, 0
	return next
}

func (x *folderIndex) currentGeneration() int64 {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.generation
}

// pending returns the current rebuild's rows in flight.
func (x *folderIndex) pending() int64 {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.inflight
}

// addInflight adds delta to the rows in flight if generation is current. The
// count does not go below zero.
func (x *folderIndex) addInflight(generation, delta int64) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if generation == x.generation {
		x.inflight = max(x.inflight+delta, 0)
	}
}

// indexOnlyAndInactive reports whether batch holds only folder-index rows
// while no rebuild runs.
func (x *folderIndex) indexOnlyAndInactive(batch []batchedWrite) bool {
	if x.active.Load() {
		return false
	}
	for _, bw := range batch {
		if bw.FolderIndex == nil {
			return false
		}
	}
	return true
}

// dropBatch is the batcher's DropWithoutFlush: a batch of only folder-index
// rows while no rebuild runs is dropped before a transaction starts.
func (x *folderIndex) dropBatch(batch []batchedWrite) bool {
	if !x.indexOnlyAndInactive(batch) {
		return false
	}
	slog.Info("writebatcher: dropping folder-index batch before BeginTx",
		"count", len(batch), "reason", "rebuild inactive")
	return true
}

// rowsToWrite returns the batch's folder-index rows to insert: none unless a
// rebuild is active and its table exists, and only rows of its generation.
func (x *folderIndex) rowsToWrite(ctx context.Context, batch []batchedWrite, q *gallerydb.CustomQueries) ([]gallerydb.InsertFileFolderIndexNewParams, error) {
	var count int
	for _, bw := range batch {
		if bw.FolderIndex != nil {
			count++
		}
	}
	if count == 0 || x.indexOnlyAndInactive(batch) {
		return nil, nil
	}
	active := x.active.Load()
	destExists := false
	if active {
		var err error
		if destExists, err = q.FileFolderIndexNewExists(ctx); err != nil {
			return nil, fmt.Errorf("check file_folder_index_new existence: %w", err)
		}
	}
	if !active || !destExists {
		reason := "rebuild inactive"
		if active {
			reason = "dest table missing"
		}
		slog.Error("skipping folder index writes: "+reason,
			"count", count, "rebuild_active", active, "dest_exists", destExists)
		return nil, nil
	}
	generation := x.currentGeneration()
	rows := make([]gallerydb.InsertFileFolderIndexNewParams, 0, count)
	for _, bw := range batch {
		r := bw.FolderIndex
		if r == nil {
			continue
		}
		if r.Generation != generation {
			slog.Error("skipping stale folder index write",
				"file_id", r.FileID, "row_generation", r.Generation, "current_generation", generation)
			continue
		}
		rows = append(rows, gallerydb.InsertFileFolderIndexNewParams{
			FileID:     r.FileID,
			FolderID:   r.FolderID,
			ImageIndex: r.ImageIndex,
			ImageCount: r.ImageCount,
			PrevID:     r.PrevID,
			NextID:     r.NextID,
			FirstID:    r.FirstID,
			LastID:     r.LastID,
		})
	}
	return rows, nil
}

// committed counts the batch's rows of the current generation as done.
func (x *folderIndex) committed(batch []batchedWrite) {
	for _, bw := range batch {
		if bw.FolderIndex != nil {
			x.addInflight(bw.FolderIndex.Generation, -1)
		}
	}
}

// RebuildFolderIndex rebuilds file_folder_index from the files table into a
// new table, through the batcher, and swaps it in.
func (w *Writer) RebuildFolderIndex(ctx context.Context) error {
	return w.rebuildFolderIndex(ctx, rebuildChunkPairs)
}

// rebuildFolderIndex rebuilds the index, reading the files in chunks of at
// least chunkPairs pairs. A canceled rebuild returns the bare context error,
// not ErrFolderIndexRebuild: shutdown cancels it, and that is not a failure.
func (w *Writer) rebuildFolderIndex(ctx context.Context, chunkPairs int) error {
	err := w.buildFolderIndex(ctx, chunkPairs)
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// buildFolderIndex reads the files in chunks of at least chunkPairs pairs
// on the RO pool, each chunk ending at a folder boundary, so no folder is
// split. The read cursor is closed and its connection returned before the
// chunk's rows are submitted. After the last chunk it waits for the batcher
// to commit every row, then creates the indexes and swaps the tables. The RW
// connection is held only to clone and to swap, so the batcher can write.
func (w *Writer) buildFolderIndex(ctx context.Context, chunkPairs int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rw := w.rw
	cpc, err := rw.Get()
	if err != nil {
		return fmt.Errorf("%w: get RW connection: %w", ErrFolderIndexRebuild, err)
	}
	if err = tableswap.CloneEmpty(ctx, cpc.Conn, "file_folder_index"); err != nil {
		rw.Put(cpc)
		return fmt.Errorf("%w: clone empty file_folder_index: %w", ErrFolderIndexRebuild, err)
	}
	w.index.active.Store(true)
	generation := w.index.bumpGeneration()
	defer w.index.active.Store(false)
	rw.Put(cpc)

	started := time.Now()
	slog.Debug("file_folder_index_new population starting")
	if err = w.populateFolderIndex(ctx, chunkPairs, generation); err != nil {
		slog.Debug("file_folder_index_new population failed", "err", err, "elapsed", time.Since(started))
		return err
	}
	slog.Debug("file_folder_index_new population completed successfully", "elapsed", time.Since(started))

	if cpc, err = rw.Get(); err != nil {
		return fmt.Errorf("%w: get RW connection for swap: %w", ErrFolderIndexRebuild, err)
	}
	if err := tableswap.CreateIndexes(ctx, cpc.Conn, "file_folder_index"); err != nil {
		rw.Put(cpc)
		return fmt.Errorf("%w: create file_folder_index indexes on dest: %w", ErrFolderIndexRebuild, err)
	}
	if err := tableswap.Swap(ctx, cpc, rw.Put, "file_folder_index"); err != nil {
		return fmt.Errorf("%w: swap file_folder_index: %w", ErrFolderIndexRebuild, err)
	}
	return nil
}

// populateFolderIndex submits every row for generation and waits until the
// batcher has committed them.
func (w *Writer) populateFolderIndex(ctx context.Context, chunkPairs int, generation int64) error {
	pairs := make([][2]int64, 0, chunkPairs)
	for after := int64(math.MinInt64); ; {
		var more bool
		var err error
		if pairs, more, err = w.scanFolderIndexChunk(ctx, after, chunkPairs, pairs[:0]); err != nil {
			return err
		}
		if err := w.submitFolders(ctx, pairs, generation); err != nil {
			return err
		}
		if !more {
			break
		}
		after = pairs[len(pairs)-1][1]
	}
	if err := w.waitFolderIndexCommitted(ctx); err != nil {
		return fmt.Errorf("%w: wait for folder-index flush: %w", ErrFolderIndexRebuild, err)
	}
	return nil
}

// scanFolderIndexChunk appends to pairs the (file_id, folder_id) pairs with
// folder_id > after, stopping at the first row of a new folder once pairs
// holds chunkPairs, and reports whether rows remain. The cursor is closed and
// the connection returned before it returns.
func (w *Writer) scanFolderIndexChunk(ctx context.Context, after int64, chunkPairs int, pairs [][2]int64) ([][2]int64, bool, error) {
	ro := w.ro
	cpc, err := ro.Get()
	if err != nil {
		return nil, false, fmt.Errorf("%w: get RO connection: %w", ErrFolderIndexRebuild, err)
	}
	defer ro.Put(cpc)
	rows, err := cpc.Queries.QueryFilesForFolderIndexRebuild(ctx, after)
	if err != nil {
		return nil, false, fmt.Errorf("%w: select files for folder-index rebuild: %w", ErrFolderIndexRebuild, err)
	}
	// An open read cursor makes a TRUNCATE checkpoint wait, so maintenance
	// skips it while the cursor is open.
	w.index.scanHeld.Store(true)
	defer w.index.scanHeld.Store(false)
	defer rows.Close()

	for rows.Next() {
		var fileID, folderID int64
		if err := rows.Scan(&fileID, &folderID); err != nil {
			return nil, false, fmt.Errorf("%w: scan file row for folder-index rebuild: %w", ErrFolderIndexRebuild, err)
		}
		if len(pairs) >= chunkPairs && folderID != pairs[len(pairs)-1][1] {
			return pairs, true, nil
		}
		pairs = append(pairs, [2]int64{fileID, folderID})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("%w: iterate files for folder-index rebuild: %w", ErrFolderIndexRebuild, err)
	}
	return pairs, false, nil
}

// submitFolders submits the rows for pairs, which hold whole folders in
// folder order.
func (w *Writer) submitFolders(ctx context.Context, pairs [][2]int64, generation int64) error {
	for start := 0; start < len(pairs); {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := start + 1
		for end < len(pairs) && pairs[end][1] == pairs[start][1] {
			end++
		}
		if err := w.submitFolder(ctx, pairs[start:end], generation); err != nil {
			return fmt.Errorf("%w: %w", ErrFolderIndexRebuild, err)
		}
		start = end
	}
	return nil
}

// submitFolder submits one row per file of a folder, with its navigation
// columns.
func (w *Writer) submitFolder(ctx context.Context, folder [][2]int64, generation int64) error {
	n := len(folder)
	for i, pair := range folder {
		if err := ctx.Err(); err != nil {
			return err
		}
		row := folderIndexRow{
			FileID:     pair[0],
			FolderID:   pair[1],
			ImageIndex: int64(i + 1),
			ImageCount: int64(n),
			FirstID:    folder[0][0],
			LastID:     folder[n-1][0],
			Generation: generation,
		}
		if i > 0 {
			row.PrevID = sql.NullInt64{Int64: folder[i-1][0], Valid: true}
		}
		if i < n-1 {
			row.NextID = sql.NullInt64{Int64: folder[i+1][0], Valid: true}
		}
		if err := w.submitFolderIndexRow(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// submitFolderIndexRow submits row. While the batcher is full it retries on
// each poll, and gives up when the batcher's pending count has not fallen for
// stallLimit.
func (w *Writer) submitFolderIndexRow(ctx context.Context, row folderIndexRow) error {
	full := func(err error) bool {
		return errors.Is(err, writebatcher.ErrFull) || errors.Is(err, writebatcher.ErrQuotaExceeded)
	}
	err := w.submitCounted(row)
	if !full(err) {
		return err
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	prev := w.PendingCount()
	lastProgress := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		if err = w.submitCounted(row); !full(err) {
			return err
		}
		cur := w.PendingCount()
		if cur < prev {
			lastProgress = time.Now()
		} else if time.Since(lastProgress) >= stallLimit {
			return err
		}
		prev = cur
	}
}

// submitCounted submits row and counts it as in flight if it was accepted.
func (w *Writer) submitCounted(row folderIndexRow) error {
	w.index.addInflight(row.Generation, 1)
	if err := w.submit(batchedWrite{FolderIndex: &row}); err != nil {
		w.index.addInflight(row.Generation, -1)
		return err
	}
	return nil
}

// waitFolderIndexCommitted waits until every submitted row is committed. It
// gives up when the count has not changed for stallLimit.
func (w *Writer) waitFolderIndexCommitted(ctx context.Context) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	last := w.index.pending()
	lastChange := time.Now()
	for last != 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		if cur := w.index.pending(); cur != last {
			last, lastChange = cur, time.Now()
		} else if time.Since(lastChange) >= stallLimit {
			return fmt.Errorf("folder-index inflight stuck at %d for %s", cur, stallLimit)
		}
	}
	return nil
}
