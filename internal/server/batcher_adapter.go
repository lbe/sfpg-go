package server

import (
	"errors"
	"log/slog"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// batchWriter is the minimal interface batcherAdapter needs from WriteBatcher.
// Using an interface here allows tests to inject a fake without a live worker goroutine.
type batchWriter interface {
	Submit(BatchedWrite) error
	PendingCount() int64
}

// batcherAdapter adapts WriteBatcher[BatchedWrite] to files.UnifiedBatcher interface.
// This breaks the circular dependency between server and files packages.
type batcherAdapter struct {
	wb batchWriter
}

// newBatcherAdapter creates an adapter for the unified WriteBatcher.
// When wb is nil, it returns an adapter whose methods return ErrClosed
// instead of panicking.
func newBatcherAdapter(wb *writebatcher.WriteBatcher[BatchedWrite]) files.UnifiedBatcher {
	if wb == nil {
		return &batcherAdapter{wb: nil}
	}
	return &batcherAdapter{wb: wb}
}

// submit is a shared helper that combines the nil-check, Submit, and
// ErrFull-logging pattern used by SubmitFile and SubmitCache.
func (ba *batcherAdapter) submit(bw BatchedWrite, kind, path string) error {
	if ba.wb == nil {
		return writebatcher.ErrClosed
	}
	err := ba.wb.Submit(bw)
	if errors.Is(err, writebatcher.ErrFull) {
		slog.Warn("unified batcher full, dropping "+kind,
			"path", path,
			"pending", ba.wb.PendingCount())
	}
	return err
}

// SubmitFile submits a File to the unified batcher.
func (ba *batcherAdapter) SubmitFile(file *files.File) error {
	return ba.submit(BatchedWrite{File: file}, "file write", file.Path)
}

// SubmitCache submits a cache entry to the unified batcher.
// Not part of files.UnifiedBatcher interface - used directly by server.
func (ba *batcherAdapter) SubmitCache(entry *cachelite.HTTPCacheEntry) error {
	return ba.submit(BatchedWrite{CacheEntry: entry}, "cache write", entry.Path)
}

// PendingCount returns the number of items not yet flushed.
func (ba *batcherAdapter) PendingCount() int64 {
	if ba.wb == nil {
		return 0
	}
	return ba.wb.PendingCount()
}
