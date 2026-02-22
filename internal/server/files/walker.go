package files

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
	"github.com/lbe/sfpg-go/internal/queue"
)

// IsImageFile checks if a file has a common image file extension and is case-insensitive.
func IsImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".heic", ".heif", ".tif", ".tiff":
		return true
	}
	return false
}

// WalkDeps holds dependencies for WalkImageDir. Passed by the caller (e.g. App).
type WalkDeps struct {
	Wg             *sync.WaitGroup
	QSendersActive *atomic.Int64
	Ctx            context.Context
	ImagesDir      string
	Q              queue.Enqueuer[string]
}

// WalkImageDir recursively scans the images directory and enqueues image file
// paths for processing. It runs as a background goroutine; the caller should
// invoke it via `go files.WalkImageDir(deps)`.
func WalkImageDir(deps *WalkDeps) {
	deps.Wg.Add(1)
	defer deps.Wg.Done()

	imageRegex := regexp.MustCompile(`(?i)(?:jpe?g|gif|png)$`)

	deps.QSendersActive.Add(1)

	eg, ctx := errgroup.WithContext(deps.Ctx)

	walker := parallelwalkdir.NewWalker(
		parallelwalkdir.WithContext(ctx),
		parallelwalkdir.WithRegexpInclude(imageRegex),
		parallelwalkdir.WithSizeNotZero(),
	)

	resultsChan, errChan := walker.ParallelWalk(deps.ImagesDir)

	eg.Go(func() error {
		for file := range resultsChan {
			if err := deps.Q.Enqueue(file); err != nil {
				slog.Error("failed to enqueue file", "file", file, "err", err)
				return fmt.Errorf("failed to enqueue file %q: %w", file, err)
			}
		}
		return nil
	})

	eg.Go(func() error {
		for err := range errChan {
			slog.Warn("Error during directory walk", "err", err)
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		slog.Error("Error during parallel directory walk", "err", err)
	}

	if deps.Ctx.Err() != nil {
		slog.Debug("walkImageDir cancelled by context")
	}

	deps.QSendersActive.Add(-1)

	slog.Info("walkImageDir for all images Ended")
}
