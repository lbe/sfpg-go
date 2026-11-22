package database

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// Batcher settings.
const (
	startBatchSize      = 10000
	startFlushInterval  = 50 * time.Millisecond
	maxBatchBytes       = 8 << 20
	batcherChannelSize  = 4096
	maintenanceInterval = 5 * time.Minute
)

// Writer sends the gallery's writes through one batcher: processed files,
// invalid files, HTTP cache entries and folder-index rows. Each batch is one
// transaction. Writes that do not fit in memory overflow to a queue on disk,
// <main db>-dque, and survive a restart.
type Writer struct {
	dqueDir         string
	walPath         string
	onFolderCreated func()
	onCacheStored   func(bytes, entries int64)
	onCacheFailed   func()

	rw, ro *dbconnpool.DbSQLConnPool

	mu      sync.RWMutex // guards batcher and closed; submits hold it for reading
	batcher *writebatcher.WriteBatcher[batchedWrite]
	closed  bool

	index            folderIndex
	wroteDML         atomic.Bool  // the last flush wrote a row
	optimizeInterval atomic.Int64 // periodic PRAGMA optimize, in nanoseconds; 0 is 1 h
	lastOptimize     time.Time    // last periodic PRAGMA optimize; used by the batcher's goroutine
}

// WriterConfig is what a Writer needs from the server.
type WriterConfig struct {
	Paths          DatabasePaths // main database: queue directory and WAL file
	RW, RO         *dbconnpool.DbSQLConnPool
	MaxDiskBytes   int64 // overflow queue quota; 0 is unlimited
	DeferDQueDrain bool  // keep queued writes on disk until StartDQueDrain

	// Optional callbacks. They run on the batcher's goroutine, also while
	// Close waits for it, so they must not call the Writer.
	OnFolderCreated func()                     // once per folder a batch creates
	OnCacheStored   func(bytes, entries int64) // after a batch with cache entries commits
	OnCacheFailed   func()                     // after a batch with cache entries fails
}

// NewWriter starts a Writer: up to 10000 writes or 8 MiB per batch, flushed
// at least every 50 ms. Canceling ctx does not stop it: Close does, after
// writing what is in memory, so a write submitted during shutdown is either
// written or dropped, never left in a stopped batcher.
func NewWriter(ctx context.Context, cfg WriterConfig) (*Writer, error) {
	w := &Writer{
		dqueDir:         cfg.Paths.Main + "-dque",
		walPath:         cfg.Paths.Main + "-wal",
		onFolderCreated: cfg.OnFolderCreated,
		onCacheStored:   cfg.OnCacheStored,
		onCacheFailed:   cfg.OnCacheFailed,
	}
	w.rw, w.ro = cfg.RW, cfg.RO
	// The first periodic optimize waits a full interval.
	w.lastOptimize = time.Now()
	b, err := w.newBatcher(ctx, startBatchSize, startFlushInterval, cfg.DeferDQueDrain, cfg.MaxDiskBytes)
	if err != nil {
		return nil, err
	}
	w.batcher = b
	slog.Info("unified WriteBatcher initialized",
		"max_batch_size", startBatchSize, "max_batch_bytes", maxBatchBytes,
		"flush_interval_ms", startFlushInterval.Milliseconds(), "channel_size", batcherChannelSize,
		"dque_dir", w.dqueDir, "dque_enabled", true, "dque_drain_deferred", cfg.DeferDQueDrain)
	return w, nil
}

func (w *Writer) newBatcher(ctx context.Context, batchSize int, flushInterval time.Duration, deferDQueDrain bool, maxDiskBytes int64) (*writebatcher.WriteBatcher[batchedWrite], error) {
	f := &flush{w: w}
	return writebatcher.New(context.WithoutCancel(ctx), writebatcher.Config[batchedWrite]{
		MaxBatchSize:        batchSize,
		MaxBatchBytes:       maxBatchBytes,
		FlushInterval:       flushInterval,
		ChannelSize:         batcherChannelSize,
		DQueDirPath:         w.dqueDir,
		DQueItemsPerSegment: 250,
		MaxDiskBytes:        maxDiskBytes,
		DeferDQueDrain:      deferDQueDrain,
		SizeFunc:            batchedWrite.size,
		DropWithoutFlush:    w.index.dropBatch,
		BeginTx:             f.begin,
		Flush:               f.write,
		OnSuccess:           f.succeeded,
		OnError:             f.failed,
		OnAfterCommit:       w.maintain,
		MaintenanceInterval: maintenanceInterval,
	})
}

// submit queues bw, or returns writebatcher.ErrClosed after Close.
func (w *Writer) submit(bw batchedWrite) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		return writebatcher.ErrClosed
	}
	return w.batcher.Submit(bw)
}

// SubmitFile queues a processed file.
func (w *Writer) SubmitFile(f *files.File) error {
	err := w.submit(batchedWrite{File: f})
	if errors.Is(err, writebatcher.ErrFull) {
		slog.Warn("unified batcher full, dropping file write", "path", f.Path, "pending", w.PendingCount())
	}
	return err
}

// SubmitInvalidFile queues a file the processor could not import.
func (w *Writer) SubmitInvalidFile(f files.InvalidFile) error {
	return w.submit(batchedWrite{InvalidFile: &f})
}

// SubmitCacheEntry queues e. If the Writer is closed or full, e goes back to
// the cache entry pool and the write is dropped.
func (w *Writer) SubmitCacheEntry(e *cachelite.HTTPCacheEntry) {
	err := w.submit(batchedWrite{CacheEntry: e})
	if err == nil {
		return
	}
	if errors.Is(err, writebatcher.ErrFull) {
		slog.Warn("unified batcher full, dropping cache write", "path", e.Path, "pending", w.PendingCount())
	}
	slog.Debug("failed to submit cache write", "path", e.Path, "err", err)
	cachelite.PutHTTPCacheEntry(e)
}

// PendingCount returns the number of writes queued and not yet flushed.
func (w *Writer) PendingCount() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.batcher.PendingCount()
}

// StartDQueDrain starts writing the queued overflow from a previous run.
func (w *Writer) StartDQueDrain() {
	w.mu.RLock()
	defer w.mu.RUnlock()
	w.batcher.StartDQueDrain()
}

// SetMaxDiskBytes sets the overflow queue quota; 0 is unlimited.
func (w *Writer) SetMaxDiskBytes(n int64) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	w.batcher.SetMaxDiskBytes(n)
}

// SetOptimizeInterval sets how often PRAGMA optimize runs; 0 is 1 h.
func (w *Writer) SetOptimizeInterval(d time.Duration) {
	w.optimizeInterval.Store(int64(d))
}

// Close stops the batcher after flushing the writes in memory. Writes
// submitted after Close are dropped. The overflow queue stays on disk for
// the next start.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.batcher.Close()
}

// GetStats returns the current batcher's statistics.
func (w *Writer) GetStats() writebatcher.Stats {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.batcher.GetStats()
}
