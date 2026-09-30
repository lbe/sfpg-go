package writebatcher

import (
	"log/slog"
)

// Submit enqueues an item for inclusion in a future flush. It does not block:
// the item may be flushed later when the batch reaches MaxBatchSize, when
// FlushInterval elapses, or when Close is called.
//
// Submit returns nil on success. It returns ErrFull if the internal channel is
// at capacity (caller may retry or drop). It returns ErrClosed if the batcher
// has been closed or the context passed to New was cancelled.
//
// When a dque is configured and the channel is full, Submit overflows the item
// to the dque instead of returning ErrFull.
//
// Submit is safe to call concurrently from multiple goroutines.
func (wb *WriteBatcher[T]) Submit(item T) error {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if wb.closed.Load() {
		return ErrClosed
	}

	// Fast path: try to send to channel without blocking.
	// Never acquires overflowMu so TestSubmit_FastPath_NoOverflowMu passes.
	select {
	case wb.ch <- item:
		wb.pendingCount.Add(1)
		return nil
	case <-wb.ctx.Done():
		return ErrClosed
	default:
	}

	// Overflow path: channel is full. If dque is configured, enqueue there.
	if wb.dq != nil {
		// Check disk quota before enqueueing.
		if quota := wb.maxDiskBytes.Load(); quota > 0 {
			if currentBytes := wb.dq.DiskBytes(); currentBytes >= quota {
				return ErrQuotaExceeded
			}
		}

		pending := wb.pendingCount.Load()

		wb.overflowWG.Add(1)
		defer wb.overflowWG.Done()
		wb.overflowMu.Lock()
		defer wb.overflowMu.Unlock()
		copied := item // copy for dque enqueue
		if err := wb.dq.Enqueue(&copied); err != nil {
			slog.Error("writebatcher: dque enqueue failed",
				"err", err,
				"pending", pending)
			return ErrFull
		}
		wb.pendingCount.Add(1)
		wb.overflowCount.Add(1)

		slog.Debug("writebatcher: dque enqueue",
			"overflow_count", wb.overflowCount.Load(),
			"dque_size", wb.dq.Size(),
			"pending", wb.pendingCount.Load())

		// Periodic summary log every 1000 overflows to provide visibility
		// without flooding the logs on every single overflow.
		if cnt := wb.overflowCount.Load(); cnt%1000 == 0 {
			slog.Info("writebatcher: dque overflow",
				"overflow_count", cnt,
				"dque_size", wb.dq.Size(),
				"pending", wb.pendingCount.Load())
		}
		select {
		case wb.dqNotify <- struct{}{}:
		default:
		}
		return nil
	}

	return ErrFull
}
