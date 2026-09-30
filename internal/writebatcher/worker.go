package writebatcher

import (
	"context"
	"log/slog"
	"time"
)

func (wb *WriteBatcher[T]) worker() {
	defer close(wb.done)

	batch := make([]T, 0, wb.cfg.MaxBatchSize)
	var batchBytes int64

	flushTimer := time.NewTimer(wb.cfg.FlushInterval)
	if !flushTimer.Stop() {
		<-flushTimer.C
	}
	defer flushTimer.Stop()

	// Maintenance timer for periodic tasks (WAL checkpoint, optimization).
	// Uses nil channel when disabled so the select case never fires.
	var maintenanceCh <-chan time.Time
	var maintenanceTimer *time.Timer
	if wb.cfg.MaintenanceInterval > 0 {
		maintenanceTimer = time.NewTimer(wb.cfg.MaintenanceInterval)
		maintenanceCh = maintenanceTimer.C
		defer maintenanceTimer.Stop()
	}

	for {
		// Phase 1: Drain dque (non-blocking) before blocking on main select.
		// This runs after every channel receive or dqNotify wake, interleaving
		// non-blocking channel receives to prevent channel fill (death spiral prevention).
		if wb.dq != nil && wb.dqueDrainEnabled.Load() {
			exit := wb.drainDQueAll(&batch, &batchBytes, flushTimer)
			if exit {
				return
			}
		}

		select {
		case item, ok := <-wb.ch:
			if !ok {
				wb.flushChannelAndExit(&batch, &batchBytes, "close", flushTimer)
				return
			}
			batch, batchBytes = wb.appendAndManageTimer(wb.ctx, batch, batchBytes, item, flushTimer)

		case <-flushTimer.C:
			if len(batch) > 0 {
				wb.flush(wb.ctx, batch, batchBytes, "timeout")
				batch = batch[:0]
				batchBytes = 0
			}

		case <-maintenanceCh:
			// Only run maintenance if enabled (interval > 0)
			if wb.cfg.MaintenanceInterval > 0 && wb.cfg.OnAfterCommit != nil {
				lastWalCheckpoint, _ := wb.lastWalCheckpointTime.Load().(time.Time)
				lastOptimize, _ := wb.lastOptimizeTime.Load().(time.Time)
				totalCommitted := wb.totalCommitted.Load()
				wb.cfg.OnAfterCommit(wb.ctx, lastWalCheckpoint, lastOptimize, totalCommitted, false)

				// Update both times after running maintenance callback
				now := time.Now()
				wb.lastWalCheckpointTime.Store(now)
				wb.lastOptimizeTime.Store(now)
				maintenanceTimer.Reset(wb.cfg.MaintenanceInterval)
			}

		case <-wb.dqNotify:
			// Woken by dqNotify — the drain loop at the top of the for loop
			// will drain dque items before blocking on select.

		case <-wb.ctx.Done():
			wb.flushChannelAndExit(&batch, &batchBytes, "shutdown", flushTimer)
			return
		}
	}
}

// stopFlushTimer safely stops a timer and drains its channel to prevent
// the timer from firing after Stop returns false (race condition between
// Stop and the select receiving from the timer's channel).
func (wb *WriteBatcher[T]) stopFlushTimer(flushTimer *time.Timer) {
	if !flushTimer.Stop() {
		select {
		case <-flushTimer.C:
		default:
		}
	}
}

// appendAndManageTimer appends item to batch, flushes if MaxBatchSize or
// MaxBatchBytes is reached, and manages the flush timer (reset on first
// item, stop on empty batch after flush).
func (wb *WriteBatcher[T]) appendAndManageTimer(ctx context.Context, batch []T, batchBytes int64, item T, flushTimer *time.Timer) ([]T, int64) {
	batch = append(batch, item)
	if wb.cfg.SizeFunc != nil {
		batchBytes += wb.cfg.SizeFunc(item)
	}
	if len(batch) >= wb.cfg.MaxBatchSize {
		wb.flush(ctx, batch, batchBytes, "size_limit")
		wb.stopFlushTimer(flushTimer)
		return batch[:0], 0
	}
	if wb.cfg.MaxBatchBytes > 0 && batchBytes >= wb.cfg.MaxBatchBytes {
		wb.flush(ctx, batch, batchBytes, "byte_limit")
		wb.stopFlushTimer(flushTimer)
		return batch[:0], 0
	}
	// First item in batch — start the flush timer
	if len(batch) == 1 {
		flushTimer.Reset(wb.cfg.FlushInterval)
	}
	return batch, batchBytes
}

// flushChannelAndExit flushes in-memory channel items and returns without
// draining dque. Persisted overflow items remain on disk for the next startup.
func (wb *WriteBatcher[T]) flushChannelAndExit(batch *[]T, batchBytes *int64, reason string, flushTimer *time.Timer) {
	wb.stopFlushTimer(flushTimer)

	if wb.dq != nil {
		if remaining := wb.dq.Size(); remaining > 0 {
			slog.Info("writebatcher: shutdown preserving dque items on disk",
				"remaining", remaining,
				"overflow_total", wb.overflowCount.Load(),
				"reason", reason)
		}
	}

	flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	drainChannel := func() {
		if reason == "close" {
			for {
				item, ok := <-wb.ch
				if !ok {
					return
				}
				*batch = append(*batch, item)
				if wb.cfg.SizeFunc != nil {
					*batchBytes += wb.cfg.SizeFunc(item)
				}
				if len(*batch) >= wb.cfg.MaxBatchSize || (wb.cfg.MaxBatchBytes > 0 && *batchBytes >= wb.cfg.MaxBatchBytes) {
					wb.flush(flushCtx, *batch, *batchBytes, reason)
					*batch = (*batch)[:0]
					*batchBytes = 0
				}
			}
		}
		for {
			select {
			case item, ok := <-wb.ch:
				if !ok {
					return
				}
				*batch = append(*batch, item)
				if wb.cfg.SizeFunc != nil {
					*batchBytes += wb.cfg.SizeFunc(item)
				}
				if len(*batch) >= wb.cfg.MaxBatchSize || (wb.cfg.MaxBatchBytes > 0 && *batchBytes >= wb.cfg.MaxBatchBytes) {
					wb.flush(flushCtx, *batch, *batchBytes, reason)
					*batch = (*batch)[:0]
					*batchBytes = 0
				}
			default:
				return
			}
		}
	}

	drainChannel()
	if len(*batch) > 0 {
		wb.flush(flushCtx, *batch, *batchBytes, reason)
	}
}
