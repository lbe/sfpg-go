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
func newBatcherAdapter(wb *writebatcher.WriteBatcher[BatchedWrite]) files.UnifiedBatcher {
	return &batcherAdapter{wb: wb}
}

// SubmitFile submits a File to the unified batcher.
func (ba *batcherAdapter) SubmitFile(file *files.File) error {
	bw := BatchedWrite{File: file}
	err := ba.wb.Submit(bw)
	if errors.Is(err, writebatcher.ErrFull) {
		slog.Warn("unified batcher full, dropping file write",
			"path", file.Path,
			"pending", ba.wb.PendingCount())
	}
	return err
}

// SubmitCache submits a cache entry to the unified batcher.
// Not part of files.UnifiedBatcher interface - used directly by server.
func (ba *batcherAdapter) SubmitCache(entry *cachelite.HTTPCacheEntry) error {
	bw := BatchedWrite{CacheEntry: entry}
	err := ba.wb.Submit(bw)
	if errors.Is(err, writebatcher.ErrFull) {
		slog.Warn("unified batcher full, dropping cache write",
			"path", entry.Path,
			"pending", ba.wb.PendingCount())
	}
	return err
}

// PendingCount returns the number of items not yet flushed.
func (ba *batcherAdapter) PendingCount() int64 {
	return ba.wb.PendingCount()
}
