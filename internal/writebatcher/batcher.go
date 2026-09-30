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
// Close stops the worker and releases resources. In-memory channel items are
// flushed best-effort; the dque overflow queue is left on disk for recovery on
// the next process start (Close does not drain dque):
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
//
// # File layout
//
// Core types and New live here; lifecycle is split across dque.go (overflow
// queue), worker.go (batch loop), flush.go (transactions), submit.go, and
// close.go.
package writebatcher

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// commitTx is a testable hook for (*sql.Tx).Commit used by flush.
	commitTx = func(tx *sql.Tx) error { return tx.Commit() }

	// rollbackTx is a testable hook for (*sql.Tx).Rollback used by flush.
	rollbackTx = func(tx *sql.Tx) error { return tx.Rollback() }
)

// Sentinel errors returned by Submit.
var (
	ErrClosed        = errors.New("writebatcher: closed")
	ErrFull          = errors.New("writebatcher: channel full")
	ErrQuotaExceeded = errors.New("writebatcher: dque disk quota exceeded")
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
// Receives the context, time of last WAL checkpoint, time of last PRAGMA optimize, total committed count, and whether this call came from a flush (postFlush=true) or the maintenance timer (postFlush=false).
type OnAfterCommitFunc[T any] func(ctx context.Context, lastWalCheckpointTime time.Time, lastOptimizeTime time.Time, totalCommitted int64, postFlush bool)

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

	// MaxDiskBytes sets a maximum disk usage for the dque overflow queue.
	// When the dque directory exceeds this threshold, Submit returns
	// ErrQuotaExceeded instead of overflowing to disk. 0 means unlimited.
	MaxDiskBytes int64

	// DeferDQueDrain when true keeps persisted dque items on disk until
	// StartDQueDrain is called. Channel submits and flushes work normally.
	DeferDQueDrain bool

	// DropWithoutFlush is an optional hook called before BeginTx. If it
	// returns true for a batch, the entire batch is dropped without a
	// transaction: BeginTx, Flush, Commit, and OnAfterCommit are skipped.
	// OnSuccess is still called (if set) and PendingCount still decrements.
	// When nil or when it returns false, the batch proceeds to BeginTx.
	DropWithoutFlush func(batch []T) bool

	// testQueue is an optional override for the durable queue. When nil,
	// openDQue creates a real *dque.DQue[T]. Tests set this to inject
	// deterministic errors without touching the filesystem.
	testQueue dqueQueue[T]
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

	dq               dqueQueue[T]
	dqNotify         chan struct{}
	dqueDrainEnabled atomic.Bool

	// maxDiskBytes is the current dque disk quota in bytes (0 = unlimited).
	// It is initialized from Config.MaxDiskBytes in New and can be updated
	// at runtime via SetMaxDiskBytes (hot reload). Submit and GetStats read
	// this atomic rather than cfg.MaxDiskBytes so updates take effect without
	// copying the batcher.
	maxDiskBytes atomic.Int64
}

// Stats holds statistics about the WriteBatcher.
type Stats struct {
	ChannelSize   int           `json:"channel_size"`
	MaxBatchSize  int           `json:"max_batch_size"`
	FlushInterval time.Duration `json:"flush_interval"`
	IsClosed      bool          `json:"is_closed"`
	TotalFlushed  int64         `json:"total_flushed"`
	TotalErrors   int64         `json:"total_errors"`
	OverflowCount int64         `json:"overflow_count"`
	DQueEnabled   bool          `json:"dque_enabled"`
	DQueSize      int           `json:"dque_size"`
	DiskBytes     int64         `json:"disk_bytes"`
	MaxDiskBytes  int64         `json:"max_disk_bytes"`
}

// SetMaxDiskBytes updates the dque disk quota in bytes at runtime (0 = unlimited).
// The new value takes effect on subsequent Submit calls and is reflected in
// GetStats. It is safe to call concurrently with Submit.
func (wb *WriteBatcher[T]) SetMaxDiskBytes(n int64) {
	wb.maxDiskBytes.Store(n)
}

// GetStats returns the current statistics of the WriteBatcher.
func (wb *WriteBatcher[T]) GetStats() Stats {
	isClosed := wb.closed.Load()

	var dqueSize int
	var diskBytes int64
	if wb.dq != nil {
		dqueSize = wb.dq.Size()
		diskBytes = wb.dq.DiskBytes()
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
		DiskBytes:     diskBytes,
		MaxDiskBytes:  wb.maxDiskBytes.Load(),
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
	var dq dqueQueue[T]
	if cfg.testQueue != nil {
		dq = cfg.testQueue
	} else {
		var err error
		dq, err = openDQue(cfg)
		if err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	wb := &WriteBatcher[T]{
		cfg:    cfg,
		ch:     make(chan T, cfg.ChannelSize),
		done:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}
	wb.maxDiskBytes.Store(cfg.MaxDiskBytes)

	if dq != nil {
		wb.dq = dq
		wb.dqNotify = make(chan struct{}, 1)
		wb.pendingCount.Store(int64(dq.Size()))

		sz := dq.Size()
		if sz > 0 {
			if cfg.DeferDQueDrain {
				slog.Info("writebatcher: dque backlog pending drain",
					"count", sz)
			} else {
				slog.Info("writebatcher: recovering items from dque",
					"count", sz)
			}
		}
		if !cfg.DeferDQueDrain {
			wb.dqueDrainEnabled.Store(true)
		}
	}

	// Start worker. When DeferDQueDrain is set, dque recovery waits for StartDQueDrain.
	go wb.worker()

	return wb, nil
}

// PendingCount returns the number of items currently enqueued or in the current
// batch and not yet flushed. It is intended for completion checks (e.g. consider
// processing done only when PendingCount is zero in addition to worker in-flight).
func (wb *WriteBatcher[T]) PendingCount() int64 {
	return wb.pendingCount.Load()
}
