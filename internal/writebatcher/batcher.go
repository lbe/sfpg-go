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
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/dque"
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

// OnAfterCommitFunc is called after a successful commit, before the next batch.
// No transaction is active, making it safe for operations like WAL checkpointing
// or PRAGMA optimize that require no active transactions.
// Receives the context, time of last WAL checkpoint, time of last PRAGMA optimize, and total committed count.
type OnAfterCommitFunc[T any] func(ctx context.Context, lastWalCheckpointTime time.Time, lastOptimizeTime time.Time, totalCommitted int64)

// Config holds all parameters for a WriteBatcher. BeginTx and Flush are
// required; other fields have defaults (MaxBatchSize 50, FlushInterval 200ms,
// ChannelSize 1024).
type Config[T any] struct {
	BeginTx             func(ctx context.Context) (*sql.Tx, error) // how to start a tx
	Flush               FlushFunc[T]                               // business logic
	OnError             OnErrorFunc[T]                             // called on flush failure (nil = log only)
	OnSuccess           OnSuccessFunc[T]                           // called after successful commit
	OnAfterCommit       OnAfterCommitFunc[T]                       // called after commit, no tx active
	MaxBatchSize        int                                        // flush at this count (default 50)
	FlushInterval       time.Duration                              // flush after this duration (default 200ms)
	MaintenanceInterval time.Duration                              // run OnAfterCommit periodically (0 = disabled)
	ChannelSize         int                                        // buffered channel capacity (default 1024)
	SizeFunc            func(T) int64                              // returns byte cost of an item (nil = size tracking disabled)
	MaxBatchBytes       int64                                      // flush when cumulative batch bytes >= this (0 = no byte limit)

	// DQueDirPath specifies a file system path for a durable queue (dque)
	// used as disk-backed overflow storage for crash recovery. When
	// non-empty, the batcher creates or opens a dque at this path.
	DQueDirPath string

	// DQueItemsPerSegment is the number of items per dque segment file.
	// When zero or negative, defaults to 250.
	DQueItemsPerSegment int

	// DQueTurbo enables dque turbo mode (skips fsync for throughput).
	// Turbo is always enabled when DQueDirPath is set; this field cannot disable it.
	DQueTurbo bool
}

// WriteBatcher collects items of type T and flushes them in batched transactions
// through a single background worker. A WriteBatcher must be created using New
// and should not be copied after first use. The zero value is not usable.
type WriteBatcher[T any] struct {
	cfg                   Config[T]
	ch                    chan T
	done                  chan struct{} // closed when worker exits
	ctx                   context.Context
	cancel                context.CancelFunc
	mu                    sync.Mutex
	closed                atomic.Bool
	pendingCount          atomic.Int64 // number of items not yet flushed (Submit +1, flush -len(batch))
	totalFlushed          atomic.Int64
	totalErrors           atomic.Int64
	totalCommitted        atomic.Int64 // total items successfully committed
	lastCommitTime        atomic.Value // time.Time of last successful commit
	lastWalCheckpointTime atomic.Value // time.Time of last WAL checkpoint
	lastOptimizeTime      atomic.Value // time.Time of last PRAGMA optimize

	overflowMu    sync.Mutex // guards overflowCount and dque enqueue path
	overflowCount atomic.Int64
	overflowWG    sync.WaitGroup // tracks in-flight overflow Submits for Close barrier

	dq       *dque.DQue[T]
	dqNotify chan struct{}
}

// Stats holds statistics about the WriteBatcher.
type Stats struct {
	ChannelSize   int
	MaxBatchSize  int
	FlushInterval time.Duration
	IsClosed      bool
	TotalFlushed  int64
	TotalErrors   int64
	OverflowCount int64
	DQueEnabled   bool
	DQueSize      int
}

// GetStats returns the current statistics of the WriteBatcher.
func (wb *WriteBatcher[T]) GetStats() Stats {
	isClosed := wb.closed.Load()

	var dqueSize int
	if wb.dq != nil {
		dqueSize = wb.dq.Size()
	}

	return Stats{
		ChannelSize:   wb.cfg.ChannelSize,
		MaxBatchSize:  wb.cfg.MaxBatchSize,
		FlushInterval: wb.cfg.FlushInterval,
		IsClosed:      isClosed,
		TotalFlushed:  wb.totalFlushed.Load(),
		TotalErrors:   wb.totalErrors.Load(),
		OverflowCount: wb.overflowCount.Load(),
		DQueEnabled:   wb.dq != nil,
		DQueSize:      dqueSize,
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

	// DQue overflow directory setup (pass by value so cfg is not mutated)
	dq, err := openDQue(cfg)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	wb := &WriteBatcher[T]{
		cfg:    cfg,
		ch:     make(chan T, cfg.ChannelSize),
		done:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	if dq != nil {
		wb.dq = dq
		wb.dqNotify = make(chan struct{}, 1)
		wb.pendingCount.Store(int64(dq.Size()))

		sz := dq.Size()
		if sz > 0 {
			slog.Info("writebatcher: recovering items from dque",
				"count", sz)
		}
	}

	// Start worker. The worker's drainDQueAll loop handles draining
	// dque items into batches on its first iteration.
	go wb.worker()

	return wb, nil
}

// openDQue creates or opens a dque at the configured path for crash recovery.
// Returns nil when DQueDirPath is empty. It also applies defaults for
// DQueItemsPerSegment (250 when <= 0) and DQueTurbo (always enabled).
func openDQue[T any](cfg Config[T]) (*dque.DQue[T], error) {
	if cfg.DQueDirPath == "" {
		return nil, nil
	}
	if cfg.DQueItemsPerSegment <= 0 {
		cfg.DQueItemsPerSegment = 250
	}

	if err := os.MkdirAll(cfg.DQueDirPath, 0755); err != nil {
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
		if wb.dq != nil {
			exit := wb.drainDQueAll(&batch, &batchBytes, flushTimer)
			if exit {
				return
			}
		}

		select {
		case item, ok := <-wb.ch:
			if !ok {
				// Channel closed: drain remaining dque items, flush batch, then exit.
				wb.drainRemaining(&batch, &batchBytes, "close", flushTimer)
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
				wb.cfg.OnAfterCommit(wb.ctx, lastWalCheckpoint, lastOptimize, totalCommitted)

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
			// Context cancelled: drain remaining items (dque + channel) and exit.
			wb.drainRemaining(&batch, &batchBytes, "shutdown", flushTimer)
			return
		}
	}
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
		// Check context cancellation
		select {
		case <-wb.ctx.Done():
			wb.drainRemaining(batch, batchBytes, "shutdown", flushTimer)
			return true
		default:
		}

		// Check flush timer to prevent starvation during long drain
		select {
		case <-flushTimer.C:
			if len(*batch) > 0 {
				wb.flush(wb.ctx, *batch, *batchBytes, "timeout")
				*batch = (*batch)[:0]
				*batchBytes = 0
			}
		default:
		}

		// Non-blocking channel receive (interleaving to prevent channel fill)
		select {
		case item, ok := <-wb.ch:
			if !ok {
				// Channel closed — drain remaining dque items and exit
				wb.drainRemaining(batch, batchBytes, "close", flushTimer)
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
			slog.Debug("writebatcher: dque dequeue",
				"drained", drained,
				"remaining", wb.dq.Size(),
				"overflow_total", wb.overflowCount.Load())
			if drained%logInterval == 0 {
				slog.Info("writebatcher: draining dque progress",
					"drained_so_far", drained,
					"remaining", wb.dq.Size(),
					"overflow_total", wb.overflowCount.Load())
			}
			*batch, *batchBytes = wb.appendAndManageTimer(wb.ctx, *batch, *batchBytes, *item, flushTimer)
			continue
		}

		// Both empty — exit drain phase
		return false
	}
}

// drainRemaining best-effort drains dque + channel on context cancellation or
// channel close. It first drains all dque items, then any remaining channel items,
// flushing the final batch before returning. Unlike the normal path, it does not
// manage the flush timer (timer resets are dead code during a tight drain loop).
// Uses a 10-second drain-specific timeout to ensure faster shutdown than the
// default 30-second flush timeout.
func (wb *WriteBatcher[T]) drainRemaining(batch *[]T, batchBytes *int64, reason string, flushTimer *time.Timer) {
	flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	drained := 0

	// Drain dque items first
	if wb.dq != nil {
		for wb.dq.Size() > 0 {
			item, err := wb.dq.Dequeue()
			if err != nil {
				if errors.Is(err, dque.ErrEmpty) {
					continue
				}
				break
			}
			wb.overflowCount.Add(-1)
			drained++
			slog.Debug("writebatcher: drainRemaining dque dequeue",
				"drained", drained,
				"remaining", wb.dq.Size(),
				"overflow_total", wb.overflowCount.Load())
			*batch = append(*batch, *item)
			if wb.cfg.SizeFunc != nil {
				*batchBytes += wb.cfg.SizeFunc(*item)
			}
			if len(*batch) >= wb.cfg.MaxBatchSize || (wb.cfg.MaxBatchBytes > 0 && *batchBytes >= wb.cfg.MaxBatchBytes) {
				wb.flush(flushCtx, *batch, *batchBytes, reason)
				*batch = (*batch)[:0]
				*batchBytes = 0
			}
		}
	}

	// Then drain any remaining items from channel
	for {
		select {
		case item, ok := <-wb.ch:
			if !ok {
				if len(*batch) > 0 {
					wb.flush(flushCtx, *batch, *batchBytes, reason)
					*batch = (*batch)[:0]
					*batchBytes = 0
				}
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
			if len(*batch) > 0 {
				wb.flush(flushCtx, *batch, *batchBytes, reason)
				*batch = (*batch)[:0]
				*batchBytes = 0
			}
			return
		}
	}
}

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

	tx, err := wb.cfg.BeginTx(flushCtx)
	if err != nil {
		wb.totalErrors.Add(1)
		wb.reEnqueueBatch(batch)
		if wb.cfg.OnError != nil {
			wb.cfg.OnError(err, copyBatch(batch))
		} else {
			slog.Error("writebatcher flush: BeginTx failed", "err", err, "batch_size", len(batch))
		}
		return
	}

	if err := wb.cfg.Flush(flushCtx, tx, batch); err != nil {
		wb.totalErrors.Add(1)
		if tx != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				slog.Warn("writebatcher flush: rollback after FlushFunc error", "err", rbErr)
			}
		}
		wb.reEnqueueBatch(batch)
		if wb.cfg.OnError != nil {
			wb.cfg.OnError(err, copyBatch(batch))
		} else {
			slog.Error("writebatcher flush: FlushFunc failed", "err", err, "batch_size", len(batch))
		}
		return
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			wb.totalErrors.Add(1)
			if rbErr := tx.Rollback(); rbErr != nil {
				slog.Warn("writebatcher flush: rollback after Commit error", "err", rbErr)
			}
			wb.reEnqueueBatch(batch)
			if wb.cfg.OnError != nil {
				wb.cfg.OnError(err, copyBatch(batch))
			} else {
				slog.Error("writebatcher flush: Commit failed", "err", err, "batch_size", len(batch))
			}
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
		wb.cfg.OnAfterCommit(wb.ctx, time.Time{}, time.Time{}, wb.totalCommitted.Load())
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

// PendingCount returns the number of items currently enqueued or in the current
// batch and not yet flushed. It is intended for completion checks (e.g. consider
// processing done only when PendingCount is zero in addition to worker in-flight).
func (wb *WriteBatcher[T]) PendingCount() int64 {
	return wb.pendingCount.Load()
}

// Close signals shutdown: it closes the input channel, waits for the worker to
// drain and flush any remaining items, cancels the context, then closes the
// dque overflow queue (if configured) and returns.
// After Close returns, all subsequent Submit calls return ErrClosed.
//
// Close is safe to call multiple times; after the first call it returns nil
// immediately without blocking.
func (wb *WriteBatcher[T]) Close() error {
	wb.mu.Lock()
	if wb.closed.Load() {
		wb.mu.Unlock()
		return nil
	}
	wb.closed.Store(true)
	close(wb.ch)
	wb.mu.Unlock()

	// Wait for any in-flight overflow Submits to complete before the
	// worker drains remaining items. After this returns, all overflowed
	// items are enqueued to the dque and will be drained by the worker.
	wb.overflowWG.Wait()

	<-wb.done
	wb.cancel()
	if wb.dq != nil {
		_ = wb.dq.Close()
	}
	return nil
}
