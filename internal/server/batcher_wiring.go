package server

import (
	"errors"
	"log/slog"

	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// fileBatcher adapts the App-level *writebatcher.WriteBatcher[BatchedWrite] to
// the files.UnifiedBatcher contract owned by the files package. This inverts
// the previous dependency direction where a pass-through adapter lived in
// internal/server and was imported by writebatcher tests; now files owns the
// interface and server provides the thin wiring close to where the batcher is
// used, avoiding an import cycle.
type fileBatcher struct {
	wb *writebatcher.WriteBatcher[BatchedWrite]
}

// newFileBatcher wraps a WriteBatcher[BatchedWrite] as a files.UnifiedBatcher.
// When wb is nil, the returned adapter's methods return ErrClosed instead of
// panicking, matching the previous nil-safe behavior.
func newFileBatcher(wb *writebatcher.WriteBatcher[BatchedWrite]) files.UnifiedBatcher {
	return &fileBatcher{wb: wb}
}

// SubmitFile submits a File to the unified batcher.
func (fb *fileBatcher) SubmitFile(file *files.File) error {
	if fb.wb == nil {
		return writebatcher.ErrClosed
	}
	err := fb.wb.Submit(BatchedWrite{File: file})
	if errors.Is(err, writebatcher.ErrFull) {
		slog.Warn("unified batcher full, dropping file write",
			"path", file.Path,
			"pending", fb.wb.PendingCount())
	}
	return err
}

// PendingCount returns the number of items not yet flushed.
func (fb *fileBatcher) PendingCount() int64 {
	if fb.wb == nil {
		return 0
	}
	return fb.wb.PendingCount()
}
