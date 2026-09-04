package server

import (
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// submitStallDefault is the duration a SubmitFolderIndex ErrFull retry waits
// for PendingCount to fall before giving up. 0 means submitStallDefaultMs.
const submitStallDefault = 30 * time.Second

// fileBatcher adapts the App-level *writebatcher.WriteBatcher[BatchedWrite] to
// the files.UnifiedBatcher contract owned by the files package. This inverts
// the previous dependency direction where a pass-through adapter lived in
// internal/server and was imported by writebatcher tests; now files owns the
// interface and server provides the thin wiring close to where the batcher is
// used, avoiding an import cycle.
type fileBatcher struct {
	wb            *writebatcher.WriteBatcher[BatchedWrite]
	inflight      *atomic.Int64
	rebuildActive *atomic.Bool
	rebuildScan   *atomic.Bool
	generation    *atomic.Int64
	submitStall   time.Duration // 0 means submitStallDefault (30s)
}

// newFileBatcher wraps a WriteBatcher[BatchedWrite] as a files.UnifiedBatcher.
// When wb is nil, the returned adapter's methods return ErrClosed instead of
// panicking, matching the previous nil-safe behavior. inflight tracks the
// number of FolderIndex rows submitted but not yet flushed during a rebuild;
// rebuildActive, rebuildScan, and generation mirror InfrastructureService fields
// so the adapter and the flush path share the same atomics. Pass nil atomics
// only for tests that do not exercise inflight/rebuild tracking.
func newFileBatcher(wb *writebatcher.WriteBatcher[BatchedWrite], inflight *atomic.Int64, rebuildActive *atomic.Bool, rebuildScan *atomic.Bool, generation *atomic.Int64) files.UnifiedBatcher {
	return &fileBatcher{
		wb:            wb,
		inflight:      inflight,
		rebuildActive: rebuildActive,
		rebuildScan:   rebuildScan,
		generation:    generation,
	}
}

// SetFolderIndexRebuildScanHeld mirrors the cursor-held flag on
// InfrastructureService so the WAL checkpoint gate and the rebuild share the
// same atomic.
func (fb *fileBatcher) SetFolderIndexRebuildScanHeld(held bool) {
	if fb.rebuildScan != nil {
		fb.rebuildScan.Store(held)
	}
}

// SubmitFile submits a File to the unified batcher.
func (fb *fileBatcher) SubmitFile(file *files.File) error {
	if fb.wb == nil {
		return writebatcher.ErrClosed
	}
	err := fb.wb.Submit(BatchedWrite{File: file})
	if errors.Is(err, writebatcher.ErrFull) {
		slog.Warn("unified batcher full, dropping file write",
			"path", file.Path,
			"pending", fb.wb.PendingCount())
	}
	return err
}

// PendingCount returns the number of items not yet flushed.
func (fb *fileBatcher) PendingCount() int64 {
	if fb.wb == nil {
		return 0
	}
	return fb.wb.PendingCount()
}

// FolderIndexInflight returns the number of folder-index rows submitted but not
// yet flushed, or 0 when inflight tracking is disabled.
func (fb *fileBatcher) FolderIndexInflight() int64 {
	if fb.inflight == nil {
		return 0
	}
	return fb.inflight.Load()
}

// SetFolderIndexRebuildActive mirrors the flag on InfrastructureService so flush
// gates FolderIndex INSERTs on the same atomic the rebuild controls.
func (fb *fileBatcher) SetFolderIndexRebuildActive(active bool) {
	if fb.rebuildActive != nil {
		fb.rebuildActive.Store(active)
	}
}

// BumpFolderIndexGeneration sets the rebuild generation to time.Now().UnixNano()
// (falling back to prev+1 when that is 0 or equal to the previous value) and
// returns the stored generation. It never Add(1)s from 0, so a fresh process
// using generation 1 cannot collide with leftover dque rows from a prior process.
func (fb *fileBatcher) BumpFolderIndexGeneration() int64 {
	if fb.generation == nil {
		return 0
	}
	prev := fb.generation.Load()
	next := time.Now().UnixNano()
	if next == 0 || next == prev {
		next = prev + 1
	}
	fb.generation.Store(next)
	return next
}

// submitFolderIndexOnce increments inflight, submits one FolderIndex row, and
// returns the error. On Submit error it performs a saturating undo (it never
// takes inflight below 0) and returns the error. Callers that retry call this
// again (which re-increments before the retry Submit).
func (fb *fileBatcher) submitFolderIndexOnce(row files.FolderIndexRow) error {
	if fb.inflight != nil {
		fb.inflight.Add(1)
	}
	if err := fb.wb.Submit(BatchedWrite{FolderIndex: &row}); err != nil {
		if fb.inflight != nil {
			if fb.inflight.Load() > 0 {
				fb.inflight.Add(-1)
			}
		}
		return err
	}
	return nil
}

// SubmitFolderIndex submits one FolderIndex row. Inflight is incremented before
// the Submit; on Submit error it is saturating-undone. On ErrFull /
// ErrQuotaExceeded it retries while PendingCount is falling (poll 100ms); if
// pending is unchanged for submitStall (default 30s) or the batcher is closed,
// it returns the error. The row is never dropped. No rebuild context is taken.
func (fb *fileBatcher) SubmitFolderIndex(row files.FolderIndexRow) error {
	if fb.wb == nil {
		return writebatcher.ErrClosed
	}
	if err := fb.submitFolderIndexOnce(row); err != nil {
		if errors.Is(err, writebatcher.ErrClosed) {
			return err
		}
		if errors.Is(err, writebatcher.ErrFull) || errors.Is(err, writebatcher.ErrQuotaExceeded) {
			stall := fb.submitStall
			if stall == 0 {
				stall = submitStallDefault
			}
			start := time.Now()
			prev := fb.wb.PendingCount()
			for {
				time.Sleep(100 * time.Millisecond)
				cur := fb.wb.PendingCount()
				if cur < prev {
					if err2 := fb.submitFolderIndexOnce(row); err2 != nil {
						if errors.Is(err2, writebatcher.ErrClosed) {
							return err2
						}
						if errors.Is(err2, writebatcher.ErrFull) || errors.Is(err2, writebatcher.ErrQuotaExceeded) {
							prev = cur
							continue
						}
						return err2
					}
					return nil
				}
				prev = cur
				if time.Since(start) >= stall {
					return err
				}
				continue
			}
		}
		return err
	}
	return nil
}
