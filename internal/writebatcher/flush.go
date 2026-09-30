package writebatcher

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/lbe/sfpg-go/internal/humanize"
)

func (wb *WriteBatcher[T]) flush(ctx context.Context, batch []T, batchBytes int64, reason string) {
	if len(batch) == 0 {
		return
	}
	t0 := time.Now()
	n := int64(len(batch))
	defer func() { wb.pendingCount.Add(-n) }()

	// Use a timeout context to prevent hanging during shutdown
	flushCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// DropWithoutFlush: skip BeginTx / Flush / Commit / OnAfterCommit when
	// the hook says this entire batch is discardable. OnSuccess still runs.
	if wb.cfg.DropWithoutFlush != nil && wb.cfg.DropWithoutFlush(batch) {
		wb.totalFlushed.Add(n)
		if wb.cfg.OnSuccess != nil {
			wb.cfg.OnSuccess(batch)
		}
		return
	}

	tx, err := wb.cfg.BeginTx(flushCtx)
	if err != nil {
		wb.totalErrors.Add(1)
		if wb.cfg.OnError != nil {
			wb.cfg.OnError(err, copyBatch(batch))
		} else {
			slog.Error("writebatcher flush: BeginTx failed", "err", err, "batch_size", len(batch))
		}
		wb.reEnqueueBatch(batch)
		return
	}

	if err := wb.cfg.Flush(flushCtx, tx, batch); err != nil {
		wb.totalErrors.Add(1)
		if tx != nil {
			if rbErr := rollbackTx(tx); rbErr != nil {
				slog.Warn("writebatcher flush: rollback after FlushFunc error", "err", rbErr)
			}
		}
		if wb.cfg.OnError != nil {
			wb.cfg.OnError(err, copyBatch(batch))
		} else {
			slog.Error("writebatcher flush: FlushFunc failed", "err", err, "batch_size", len(batch))
		}
		wb.reEnqueueBatch(batch)
		return
	}

	if tx != nil {
		if err := commitTx(tx); err != nil {
			wb.totalErrors.Add(1)
			if rbErr := rollbackTx(tx); rbErr != nil {
				slog.Warn("writebatcher flush: rollback after Commit error", "err", rbErr)
			}
			if wb.cfg.OnError != nil {
				wb.cfg.OnError(err, copyBatch(batch))
			} else {
				slog.Error("writebatcher flush: Commit failed", "err", err, "batch_size", len(batch))
			}
			wb.reEnqueueBatch(batch)
			return
		}
	}

	// Transaction successfully committed - update stats and run maintenance callback
	now := time.Now()
	txElapsed := now.Sub(t0)
	wb.totalFlushed.Add(n)
	wb.totalCommitted.Add(n)
	wb.lastCommitTime.Store(now)

	if wb.cfg.OnSuccess != nil {
		wb.cfg.OnSuccess(batch)
	}

	// Call OnAfterCommit for WAL checkpointing/optimization (no transaction active).
	// Pass zero times to skip time-based checks - only size-based checks run from flush.
	// Time-based checks are handled by the maintenance timer.
	if wb.cfg.OnAfterCommit != nil {
		wb.cfg.OnAfterCommit(wb.ctx, time.Time{}, time.Time{}, wb.totalCommitted.Load(), true)
	}

	totalElapsed := time.Since(t0)
	postCommitElapsed := totalElapsed - txElapsed
	slog.Debug("writebatcher flush: completed",
		"trigger", reason,
		"batch_size", len(batch),
		"batch_bytes", humanize.Comma(batchBytes).String(),
		"tx_elapsed", fmt.Sprintf("%v", txElapsed),
		"post_commit_elapsed", fmt.Sprintf("%v", postCommitElapsed),
		"elapsed", fmt.Sprintf("%v", totalElapsed))
}

// reEnqueueBatch re-submits a failed batch so items are not lost.
// Called from flush when BeginTx, FlushFunc, or Commit fails.
// Each item goes through Submit, which handles the channel fast path
// and dque overflow path. If the batcher is closed during re-enqueue,
// remaining items are lost.
func (wb *WriteBatcher[T]) reEnqueueBatch(batch []T) {
	for _, item := range batch {
		if err := wb.Submit(item); err != nil {
			slog.Warn("writebatcher: failed to re-enqueue item after batch error",
				"err", err)
			return
		}
	}
}

// copyBatch returns a new slice with the same contents as batch.
// This ensures OnError receives data that won't be overwritten by
// subsequent batch reuse.
func copyBatch[T any](batch []T) []T {
	cp := make([]T, len(batch))
	copy(cp, batch)
	return cp
}
