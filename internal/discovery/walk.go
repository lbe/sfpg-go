package discovery

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"

	"golang.org/x/sync/errgroup"

	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
)

// imageName matches the file names discovery imports.
var imageName = regexp.MustCompile(`(?i)(?:jpe?g|gif|png)$`)

// outcome is what the walk does with a file it finds.
type outcome int

const (
	process              outcome = iota // queue it for a worker
	skipUnchangedValid                  // imported, and unchanged since
	skipUnchangedInvalid                // recorded invalid, and unchanged since
)

// decide classifies a file from what the database has for its name. An
// unchanged invalid record wins over a valid row.
func decide(st parallelwalkdir.DirEntState, inCatalog bool, mtime, size int64) outcome {
	switch {
	case !inCatalog:
		return process
	case st.InvalidPathValid && st.InvalidMtime == mtime && st.InvalidSize == size:
		return skipUnchangedInvalid
	case st.FileIDValid && st.FileMtimeValid && st.FileMtime == mtime &&
		st.FileSizeValid && st.FileSize == size && st.FileMD5Valid:
		return skipUnchangedValid
	}
	return process
}

// modified is the walk's per-file check: it counts the file and reports
// whether it needs processing.
func (s *Service) modified(_ []byte, info fs.FileInfo, st parallelwalkdir.DirEntState, inCatalog bool) (bool, error) {
	if info == nil {
		return false, errors.New("discovery: walk reported a file without FileInfo")
	}
	stats := s.deps.Stats
	stats.TotalFound.Add(1)
	switch decide(st, inCatalog, info.ModTime().Unix(), info.Size()) {
	case skipUnchangedValid:
		stats.AlreadyExisting.Add(1)
		return false, nil
	case skipUnchangedInvalid:
		stats.SkippedInvalid.Add(1)
		return false, nil
	}
	return true, nil
}

// walk walks the images directory and queues each new or changed image on the
// backlog. Paths are kept in store until the run's drain is done.
func (s *Service) walk(ctx context.Context, store *parallelwalkdir.WalkPathStore) {
	slog.Info("walkImageDir for all images Started", "dir", s.cfg.ImagesDir)
	s.senders.Add(1)
	defer s.senders.Add(-1)

	eg, ctx := errgroup.WithContext(ctx)
	walker := parallelwalkdir.NewWalker(
		parallelwalkdir.WithContext(ctx),
		parallelwalkdir.WithBasenameInclude(imageName),
		parallelwalkdir.WithSizeNotZero(),
		parallelwalkdir.WithWalkPathStore(store),
		parallelwalkdir.WithDirEntCatalog(s.deps.Catalog, s.modified),
	)
	results, errs := walker.ParallelWalk(s.cfg.ImagesDir)
	eg.Go(func() error {
		for f := range results {
			if err := s.backlog.enqueue(PathWork{Path: f.Path, MtimeUnix: f.ModTimeUnix, SizeBytes: f.SizeBytes}); err != nil {
				for range results {
				}
				return fmt.Errorf("failed to enqueue file %q: %w", string(f.Path), err)
			}
		}
		return nil
	})
	eg.Go(func() error {
		for err := range errs {
			slog.Warn("Error during directory walk", "err", err)
		}
		return nil
	})
	if err := eg.Wait(); err != nil {
		slog.Error("Error during parallel directory walk", "err", err)
	}
	slog.Info("walkImageDir for all images Ended")
}
