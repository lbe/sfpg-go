package writebatcher

import (
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/lbe/sfpg-go/internal/dque"
)

var (
	// osMkdirAll is a testable hook for os.MkdirAll used by openDQue.
	osMkdirAll = os.MkdirAll
)

// dqueQueue is the minimal surface WriteBatcher needs from its durable queue.
// Production uses *dque.DQue[T]; tests may supply a mock implementation.
type dqueQueue[T any] interface {
	Size() int
	DiskBytes() int64
	Dequeue() (*T, error)
	Enqueue(item *T) error
	TurboOn() error
	Close() error
}

// StartDQueDrain begins draining persisted dque overflow items into batches.
// It is a no-op when dque is disabled or draining was not deferred.
func (wb *WriteBatcher[T]) StartDQueDrain() {
	if wb.dq == nil {
		return
	}
	if wb.dqueDrainEnabled.Swap(true) {
		return
	}
	if sz := wb.dq.Size(); sz > 0 {
		slog.Info("writebatcher: starting dque recovery drain", "count", sz)
	}
	select {
	case wb.dqNotify <- struct{}{}:
	default:
	}
}

// openDQue creates or opens a dque at the configured path for crash recovery.
// Returns nil when DQueDirPath is empty. It also applies defaults for
// DQueItemsPerSegment (default 250 when <= 0). Turbo mode is always enabled
// when DQueDirPath is set.
func openDQue[T any](cfg Config[T]) (dqueQueue[T], error) {
	if cfg.DQueDirPath == "" {
		return nil, nil
	}
	if cfg.DQueItemsPerSegment <= 0 {
		cfg.DQueItemsPerSegment = 250
	}

	if err := osMkdirAll(cfg.DQueDirPath, 0755); err != nil {
		return nil, err
	}

	dq, err := dque.NewOrOpen[T]("writebatcher", cfg.DQueDirPath, cfg.DQueItemsPerSegment)
	if err != nil {
		return nil, err
	}

	if err := dq.TurboOn(); err != nil {
		slog.Debug("writebatcher: dque turbo already on")
	}

	sz := dq.Size()
	slog.Info("writebatcher: dque overflow initialized",
		"dir", cfg.DQueDirPath,
		"items_per_segment", cfg.DQueItemsPerSegment,
		"turbo", true,
		"existing_items", sz)

	return dq, nil
}

// drainDQueAll non-blocking drains all available dque items, interleaving
// non-blocking channel receives to prevent channel fill (death spiral prevention).
// Returns true if the worker should exit (channel closed during drain).
func (wb *WriteBatcher[T]) drainDQueAll(batch *[]T, batchBytes *int64, flushTimer *time.Timer) bool {
	drained := 0
	logInterval := 10000
	defer func() {
		if drained > 0 {
			remaining := 0
			if wb.dq != nil {
				remaining = wb.dq.Size()
			}
			slog.Info("writebatcher: drained dque items",
				"count", drained,
				"remaining", remaining,
				"overflow_total", wb.overflowCount.Load())
		}
	}()
	for {
		if wb.closed.Load() {
			return false
		}

		// Check context cancellation
		select {
		case <-wb.ctx.Done():
			wb.flushChannelAndExit(batch, batchBytes, "shutdown", flushTimer)
			return true
		default:
		}

		// Non-blocking channel receive (interleaving to prevent channel fill)
		select {
		case item, ok := <-wb.ch:
			if !ok {
				wb.flushChannelAndExit(batch, batchBytes, "close", flushTimer)
				return true
			}
			*batch, *batchBytes = wb.appendAndManageTimer(wb.ctx, *batch, *batchBytes, item, flushTimer)
			continue
		default:
		}

		// Non-blocking dque dequeue
		if wb.dq.Size() > 0 {
			item, err := wb.dq.Dequeue()
			if err != nil {
				if errors.Is(err, dque.ErrEmpty) {
					continue // TOCTOU: item was consumed between Size() and Dequeue()
				}
				return false
			}
			wb.overflowCount.Add(-1)
			drained++
			if drained%logInterval == 0 {
				slog.Info("writebatcher: draining dque progress",
					"drained_so_far", drained,
					"remaining", wb.dq.Size(),
					"overflow_total", wb.overflowCount.Load())
			}
			*batch, *batchBytes = wb.appendAndManageTimer(wb.ctx, *batch, *batchBytes, *item, flushTimer)
			continue
		}

		// Both empty — exit drain phase. Flush leftover only if this
		// invocation dequeued at least one item (drain_end). Idle polls
		// (drained==0) leave the batch for the worker-main interval flush.
		if drained > 0 && len(*batch) > 0 {
			wb.flush(wb.ctx, *batch, *batchBytes, "drain_end")
			*batch = (*batch)[:0]
			*batchBytes = 0
			wb.stopFlushTimer(flushTimer)
		}
		return false
	}
}
