// Package writebatcher provides a generic, transaction-batching write serializer
// for database operations. It collects items of any type T from multiple
// concurrent goroutines and flushes them in batched transactions through a
// single background worker, eliminating write contention on single-writer
// databases like SQLite.
//
// This package simplifies batched database writes by:
//   - Serializing all writes through one goroutine (no lock contention)
//   - Batching items into single transactions (fewer round-trips)
//   - Triggering flushes by count or timeout (configurable latency vs throughput)
//   - Providing a generic API over any item type T
//
// # Usage
//
// Create a batcher with a BeginTx function and a FlushFunc:
//
//	wb, err := writebatcher.New[MyItem](ctx, writebatcher.Config[MyItem]{
//	    BeginTx: func(ctx context.Context) (*sql.Tx, error) {
//	        return db.BeginTx(ctx, nil)
//	    },
//	    Flush: func(ctx context.Context, tx *sql.Tx, batch []MyItem) error {
//	        for _, item := range batch {
//	            if _, err := tx.ExecContext(ctx, "INSERT ...", item.Val); err != nil {
//	                return err
//	            }
//	        }
//	        return nil
//	    },
//	    OnError:      func(err error, batch []MyItem) { log.Println(err) },
//	    MaxBatchSize: 50,
//	})
//
// Submit items from any goroutine:
//
//	if err := wb.Submit(item); err != nil {
//	    // ErrFull (channel at capacity) or ErrClosed (batcher shut down)
//	}
//
// Close flushes remaining items and releases resources:
//
//	wb.Close()
//
// # Flush Triggers
//
// A flush occurs when any of these conditions is met:
//   - The batch reaches MaxBatchSize items (default 50)
//   - FlushInterval elapses since the first item entered the current batch (default 200ms)
//   - The batch's cumulative size (via SizeFunc) reaches MaxBatchBytes (when SizeFunc and MaxBatchBytes > 0)
//   - Close() is called
//
// # Transaction Lifecycle
//
// The batcher calls BeginTx, passes the *sql.Tx to FlushFunc, then calls
// Commit on success or Rollback on failure. FlushFunc should only execute
// SQL statements -- it must not call Commit or Rollback itself.
//
// # Thread Safety
//
// Submit is safe for concurrent use by multiple goroutines. Close is safe
// to call multiple times. All other methods are internal to the worker goroutine.
package writebatcher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/humanize"
)

// Sentinel errors returned by Submit.
var (
	ErrClosed = errors.New("writebatcher: closed")
	ErrFull   = errors.New("writebatcher: channel full")
)

// FlushFunc executes the batch within the provided transaction. The batcher
// calls BeginTx before Flush and Commit or Rollback after; FlushFunc must only
// run SQL statements and must not call Commit or Rollback. The batch slice is
// valid only for the duration of the call; do not retain it.
type FlushFunc[T any] func(ctx context.Context, tx *sql.Tx, batch []T) error

// OnErrorFunc is called when a flush fails (after Rollback). The batch is a
// copy and is safe to retain or use for retry logic. If OnError is nil, the
// batcher logs the error with slog.Error instead.
type OnErrorFunc[T any] func(err error, batch []T)

// OnSuccessFunc is called after a successful flush and commit. The batch is
// passed as a slice; the caller must not retain it as it may be reused.
type OnSuccessFunc[T any] func(batch []T)

// Config holds all parameters for a WriteBatcher. BeginTx and Flush are
// required; other fields have defaults (MaxBatchSize 50, FlushInterval 200ms,
// ChannelSize 1024).
type Config[T any] struct {
	BeginTx       func(ctx context.Context) (*sql.Tx, error) // how to start a tx
	Flush         FlushFunc[T]                               // business logic
	OnError       OnErrorFunc[T]                             // called on flush failure (nil = log only)
	OnSuccess     OnSuccessFunc[T]                           // called after successful commit
	MaxBatchSize  int                                        // flush at this count (default 50)
	FlushInterval time.Duration                              // flush after this duration (default 200ms)
	ChannelSize   int                                        // buffered channel capacity (default 1024)
	SizeFunc      func(T) int64                              // returns byte cost of an item (nil = size tracking disabled)
	MaxBatchBytes int64                                      // flush when cumulative batch bytes >= this (0 = no byte limit)
}

// WriteBatcher collects items of type T and flushes them in batched transactions
// through a single background worker. A WriteBatcher must be created using New
// and should not be copied after first use. The zero value is not usable.
type WriteBatcher[T any] struct {
	cfg          Config[T]
	ch           chan T
	done         chan struct{} // closed when worker exits
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	closed       bool
	pendingCount atomic.Int64 // number of items not yet flushed (Submit +1, flush -len(batch))
	totalFlushed atomic.Int64
	totalErrors  atomic.Int64
}

// Stats holds statistics about the WriteBatcher.
type Stats struct {
	ChannelSize   int
	MaxBatchSize  int
	FlushInterval time.Duration
	IsClosed      bool
	TotalFlushed  int64
	TotalErrors   int64
}

// GetStats returns the current statistics of the WriteBatcher.
func (wb *WriteBatcher[T]) GetStats() Stats {
	wb.mu.Lock()
	isClosed := wb.closed
	wb.mu.Unlock()

	return Stats{
		ChannelSize:   wb.cfg.ChannelSize,
		MaxBatchSize:  wb.cfg.MaxBatchSize,
		FlushInterval: wb.cfg.FlushInterval,
		IsClosed:      isClosed,
		TotalFlushed:  wb.totalFlushed.Load(),
		TotalErrors:   wb.totalErrors.Load(),
	}
}

// New creates a WriteBatcher for type T and starts its background worker.
//
// BeginTx must start a new transaction; it is called by the worker for each flush.
// Flush is called with that transaction and the current batch; it must execute
// the SQL (e.g. INSERT/UPSERT) and return. OnError is optional; if nil, errors
// are logged with slog. MaxBatchSize, FlushInterval, and ChannelSize use
// defaults when zero or negative (50, 200ms, 1024).
//
// SizeFunc and MaxBatchBytes are optional. If both are set (MaxBatchBytes > 0 and
// SizeFunc non-nil), the batcher tracks cumulative batch size and flushes when
// the total reaches MaxBatchBytes. If MaxBatchBytes is 0, size tracking runs but
// never triggers a flush.
//
// The worker runs until the context is cancelled or the input channel is closed.
// The caller must call Close to shut down the batcher and release resources;
// closing the context without calling Close leaves the channel open.
//
// New returns an error if BeginTx or Flush is nil.
func New[T any](ctx context.Context, cfg Config[T]) (*WriteBatcher[T], error) {
	if cfg.BeginTx == nil {
		return nil, errors.New("writebatcher: BeginTx is required")
	}
	if cfg.Flush == nil {
		return nil, errors.New("writebatcher: Flush is required")
	}

	if cfg.MaxBatchBytes > 0 && cfg.SizeFunc == nil {
		return nil, errors.New("writebatcher: MaxBatchBytes requires SizeFunc")
	}

	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = 50
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 200 * time.Millisecond
	}
	if cfg.ChannelSize <= 0 {
		cfg.ChannelSize = 1024
	}

	ctx, cancel := context.WithCancel(ctx)
	wb := &WriteBatcher[T]{
		cfg:    cfg,
		ch:     make(chan T, cfg.ChannelSize),
		done:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	go wb.worker()

	return wb, nil
}

// appendAndMaybeFlush appends item to batch, updates batchBytes if SizeFunc is set,
// and flushes when MaxBatchSize or MaxBatchBytes is reached. Returns the updated
// batch and batchBytes (batch may be reset to empty after a flush).
func (wb *WriteBatcher[T]) appendAndMaybeFlush(ctx context.Context, batch []T, batchBytes int64, item T) ([]T, int64) {
	batch = append(batch, item)
	if wb.cfg.SizeFunc != nil {
		batchBytes += wb.cfg.SizeFunc(item)
	}
	if len(batch) >= wb.cfg.MaxBatchSize ||
		(wb.cfg.MaxBatchBytes > 0 && batchBytes >= wb.cfg.MaxBatchBytes) {
		slog.Debug("writebatcher: flush triggered by batch size or byte limit", "batch_size", len(batch), "batch_bytes",
			humanize.Comma(batchBytes).String())
		wb.flush(ctx, batch)
		return batch[:0], 0
	}
	return batch, batchBytes
}

func (wb *WriteBatcher[T]) worker() {
	defer close(wb.done)

	batch := make([]T, 0, wb.cfg.MaxBatchSize)
	var batchBytes int64

	timer := time.NewTimer(wb.cfg.FlushInterval)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		select {
		case item, ok := <-wb.ch:
			if !ok {
				if len(batch) > 0 {
					wb.flush(wb.ctx, batch)
				}
				return
			}

			batch, batchBytes = wb.appendAndMaybeFlush(wb.ctx, batch, batchBytes, item)
			if len(batch) == 0 {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			} else if len(batch) == 1 {
				timer.Reset(wb.cfg.FlushInterval)
			}

		case <-timer.C:
			if len(batch) > 0 {
				wb.flush(wb.ctx, batch)
				batch = batch[:0]
				batchBytes = 0
			}

		case <-wb.ctx.Done():
			// Shutdown requested. Drain remaining items from channel.
			for {
				select {
				case item, ok := <-wb.ch:
					if !ok {
						wb.flush(wb.ctx, batch)
						return
					}
					batch, batchBytes = wb.appendAndMaybeFlush(wb.ctx, batch, batchBytes, item)
				default:
					wb.flush(wb.ctx, batch)
					return
				}
			}
		}
	}
}

func (wb *WriteBatcher[T]) flush(ctx context.Context, batch []T) {
	if len(batch) == 0 {
		return
	}
	t0 := time.Now()
	n := int64(len(batch))
	defer func() { wb.pendingCount.Add(-n) }()

	// Use a timeout context to prevent hanging during shutdown
	flushCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tx, err := wb.cfg.BeginTx(flushCtx)
	if err != nil {
		wb.totalErrors.Add(1)
		if wb.cfg.OnError != nil {
			wb.cfg.OnError(err, copyBatch(batch))
		} else {
			slog.Error("writebatcher flush: BeginTx failed", "err", err, "batch_size", len(batch))
		}
		return
	}
	if tx == nil {
		wb.totalErrors.Add(1)
		nilTxErr := errors.New("writebatcher: BeginTx returned nil tx without error")
		if wb.cfg.OnError != nil {
			wb.cfg.OnError(nilTxErr, copyBatch(batch))
		} else {
			slog.Error("writebatcher flush: BeginTx returned nil tx", "batch_size", len(batch))
		}
		return
	}

	if err := wb.cfg.Flush(flushCtx, tx, batch); err != nil {
		wb.totalErrors.Add(1)
		_ = tx.Rollback()
		if wb.cfg.OnError != nil {
			wb.cfg.OnError(err, copyBatch(batch))
		} else {
			slog.Error("writebatcher flush: FlushFunc failed", "err", err, "batch_size", len(batch))
		}
		return
	}

	if err := tx.Commit(); err != nil {
		wb.totalErrors.Add(1)
		_ = tx.Rollback()
		if wb.cfg.OnError != nil {
			wb.cfg.OnError(err, copyBatch(batch))
		} else {
			slog.Error("writebatcher flush: Commit failed", "err", err, "batch_size", len(batch))
		}
		return
	}

	wb.totalFlushed.Add(n)
	if wb.cfg.OnSuccess != nil {
		wb.cfg.OnSuccess(batch)
	}
	slog.Debug("writebatcher flush: completed", "batch_size", len(batch), "elapsed", fmt.Sprintf("%v", time.Since(t0)))
}

// copyBatch returns a new slice with the same contents as batch.
// This ensures OnError receives data that won't be overwritten by
// subsequent batch reuse.
func copyBatch[T any](batch []T) []T {
	cp := make([]T, len(batch))
	copy(cp, batch)
	return cp
}

// Submit enqueues an item for inclusion in a future flush. It does not block:
// the item may be flushed later when the batch reaches MaxBatchSize, when
// FlushInterval elapses, or when Close is called.
//
// Submit returns nil on success. It returns ErrFull if the internal channel is
// at capacity (caller may retry or drop). It returns ErrClosed if the batcher
// has been closed or the context passed to New was cancelled.
//
// Submit is safe to call concurrently from multiple goroutines.
func (wb *WriteBatcher[T]) Submit(item T) error {
	wb.mu.Lock()
	if wb.closed {
		wb.mu.Unlock()
		return ErrClosed
	}
	wb.mu.Unlock()

	select {
	case wb.ch <- item:
		wb.pendingCount.Add(1)
		return nil
	case <-wb.ctx.Done():
		return ErrClosed
	default:
		return ErrFull
	}
}

// PendingCount returns the number of items currently enqueued or in the current
// batch and not yet flushed. It is intended for completion checks (e.g. consider
// processing done only when PendingCount is zero in addition to worker in-flight).
func (wb *WriteBatcher[T]) PendingCount() int64 {
	return wb.pendingCount.Load()
}

// Close signals shutdown: it closes the input channel, waits for the worker to
// drain and flush any remaining items, then cancels the context and returns.
// After Close returns, all subsequent Submit calls return ErrClosed.
//
// Close is safe to call multiple times; after the first call it returns nil
// immediately without blocking.
func (wb *WriteBatcher[T]) Close() error {
	wb.mu.Lock()
	if wb.closed {
		wb.mu.Unlock()
		return nil
	}
	wb.closed = true
	close(wb.ch)
	wb.mu.Unlock()

	<-wb.done
	wb.cancel()
	return nil
}
