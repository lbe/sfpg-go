package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/thumbnail"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

// emptyBacklogWait is how long a worker waits before looking at an empty
// backlog again.
const emptyBacklogWait = 100 * time.Millisecond

// work is the pool's worker: it takes files from the backlog until ctx is
// canceled, the pool tells it to stop, or the backlog is closed.
func (s *Service) work(ctx context.Context, wc workerpool.WorkerContext, queueLength func() int, _ int) error {
	for ctx.Err() == nil && !wc.ShouldIStop(queueLength()+1) {
		wc.AddSubmitted()
		w, err := s.claim(ctx)
		switch {
		case errors.Is(err, queue.ErrEmptyQueue):
			select {
			case <-ctx.Done():
			case <-time.After(emptyBacklogWait):
			}
			continue
		case errors.Is(err, queue.ErrClosedQueue):
			return nil
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return nil
		case err != nil:
			slog.Error("failed to dequeue file", "err", err)
			return nil
		}
		if s.importFile(ctx, w) {
			wc.AddSuccessful()
		} else {
			wc.AddFailed()
		}
		s.deps.Stats.InFlight.Add(-1)
		wc.AddCompleted()
	}
	return nil
}

// claim takes the next file from the backlog and counts it in flight, in one
// step under s.claimMu, so the drain never sees the file in neither place. A
// canceled worker takes nothing.
func (s *Service) claim(ctx context.Context) (PathWork, error) {
	s.claimMu.Lock()
	defer s.claimMu.Unlock()
	if err := ctx.Err(); err != nil {
		return PathWork{}, err
	}
	w, err := s.backlog.dequeue()
	if err == nil {
		s.deps.Stats.InFlight.Add(1)
	}
	return w, err
}

// importFile processes one queued file and submits the result for writing,
// or records the file as invalid. It reports whether the file was submitted.
func (s *Service) importFile(ctx context.Context, w PathWork) bool {
	fullPath := string(w.Path)
	path, err := s.cfg.RemovePrefix(s.cfg.NormalizedImagesDir, fullPath)
	if err != nil {
		slog.Error("invalid file path detected", "file", fullPath, "err", err)
		return false
	}
	f, err := s.deps.Processor.ProcessDiscoveryFile(ctx, path, w.MtimeUnix, w.SizeBytes)
	if err != nil {
		slog.Error("failed to process file", "file", fullPath, "err", err)
		// A canceled file is not invalid: the next run processes it again.
		if !canceled(ctx, err) {
			s.recordInvalid(ctx, fullPath, path, err)
		}
		return false
	}

	// Counted when processed, before the write, as dev does.
	s.deps.Stats.NewlyInserted.Add(1)
	if err := s.deps.Writer.SubmitFile(f); err != nil {
		slog.Error("submit file for write", "path", f.Path, "err", err)
		if f.Thumbnail != nil {
			thumbnail.PutBytesBuffer(f.Thumbnail)
			f.Thumbnail = nil
		}
		return false
	}
	if s.deps.OnFileInserted != nil {
		s.deps.OnFileInserted(w.SizeBytes)
	}
	return true
}

// recordInvalid records the file in invalid_files, so it is skipped while its
// mtime and size are unchanged. Nothing is recorded if ctx is canceled
// meanwhile.
func (s *Service) recordInvalid(ctx context.Context, fullPath, path string, processErr error) {
	info, err := os.Stat(fullPath)
	if ctx.Err() != nil {
		return
	}
	if err == nil {
		err = s.deps.Writer.SubmitInvalidFile(files.InvalidFile{
			Path:   path,
			Mtime:  info.ModTime().Unix(),
			Size:   info.Size(),
			Reason: files.InvalidReason(processErr),
		})
	} else {
		err = fmt.Errorf("stat %s: %w", fullPath, err)
	}
	if err != nil {
		slog.Error("record invalid file", "path", path, "err", err)
		return
	}
	s.deps.Stats.SkippedInvalid.Add(1)
}

// canceled reports whether err comes from canceling the work, not from the
// file.
func canceled(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
