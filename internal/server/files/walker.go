package files

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
	"github.com/lbe/sfpg-go/internal/queue"
)

// WalkDeps holds dependencies for WalkImageDir. Passed by the caller (e.g. App).
type WalkDeps struct {
	Wg             *sync.WaitGroup
	QSendersActive *atomic.Int64
	Ctx            context.Context
	ImagesDir      string
	Q              queue.Enqueuer[DiscoveryPathWork]
	// WalkPathStore pins reported path arena bytes until discovery drain completes.
	WalkPathStore       *parallelwalkdir.WalkPathStore
	NormalizedImagesDir string
	RemoveImagesPrefix  func(normalizedImagesDir, path string) (string, error)
	// CatalogROPool is required for walk-time DirEnt catalog. Nil logs Error and returns without walking.
	CatalogROPool *dbconnpool.DbSQLConnPool
	// Stats receives walk-time TotalFound / AlreadyExisting / SkippedInvalid updates.
	Stats *ProcessingStats
}

// WalkImageDir scans the images directory using parallelwalkdir's bounded worker
// pool (default parallelism runtime.GOMAXPROCS(0); no WithMaxWorkers or
// WithDirChCapacity in production). Directory workers submit subdirs to an
// unbounded schedule queue; a feeder goroutine alone sends dirWork to dirCh.
// Each ReportedFile on results is mapped to DiscoveryPathWork (path, mtime, size
// from the walk) and enqueued on deps.Q for discovery file workers; workers
// copy walk metadata only; workers do not re-Stat for modification checks. TriggerDiscovery sets
// WalkPathStore on deps, keeps walkDeps alive through waitForFileProcessingDrain,
// then resets the store; ParallelWalk resets the store at walk start only when
// the backlog is empty. Tests may leave WalkPathStore nil to allocate one here.
func WalkImageDir(deps *WalkDeps) {
	slog.Info("walkImageDir for all images Started", "dir", deps.ImagesDir)
	deps.Wg.Add(1)
	defer deps.Wg.Done()

	if deps.CatalogROPool == nil {
		slog.Error("WalkImageDir requires CatalogROPool", "dir", deps.ImagesDir)
		return
	}

	imageRegex := regexp.MustCompile(`(?i)(?:jpe?g|gif|png)$`)

	deps.QSendersActive.Add(1)

	if deps.WalkPathStore == nil {
		deps.WalkPathStore = parallelwalkdir.NewWalkPathStore()
	}

	eg, ctx := errgroup.WithContext(deps.Ctx)

	walkerOpts := []parallelwalkdir.Option{
		parallelwalkdir.WithContext(ctx),
		parallelwalkdir.WithBasenameInclude(imageRegex),
		parallelwalkdir.WithSizeNotZero(),
		parallelwalkdir.WithWalkPathStore(deps.WalkPathStore),
		parallelwalkdir.WithDirEntCatalog(
			NewDiscoveryDirEntMapFunc(deps.CatalogROPool, deps.NormalizedImagesDir, deps.RemoveImagesPrefix),
			DiscoveryDirEntModifiedWithStats(deps.Stats),
		),
	}
	walker := parallelwalkdir.NewWalker(walkerOpts...)

	resultsChan, errChan := walker.ParallelWalk(deps.ImagesDir)

	eg.Go(func() error {
		for reported := range resultsChan {
			work := DiscoveryPathWork{
				Path:      reported.Path,
				MtimeUnix: reported.ModTimeUnix,
				SizeBytes: reported.SizeBytes,
			}
			if err := enqueueWithBackpressure(ctx, deps.Q, work); err != nil {
				slog.Error("failed to enqueue file", "file", string(reported.Path), "err", err)
				drainReportedFiles(resultsChan)
				return fmt.Errorf("failed to enqueue file %q: %w", string(reported.Path), err)
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

func drainReportedFiles(results <-chan parallelwalkdir.ReportedFile) {
	for range results {
	}
}

// enqueueWithBackpressure enqueues discovery work into the backlog with
// backpressure. If the queue is full (ErrQueueFull), it polls with a short
// delay until space becomes available or the context is cancelled.
func enqueueWithBackpressure(ctx context.Context, q queue.Enqueuer[DiscoveryPathWork], work DiscoveryPathWork) error {
	for {
		err := q.Enqueue(work)
		if err == nil {
			return nil
		}
		if errors.Is(err, queue.ErrQueueFull) {
			// Backpressure: wait for space, but check context cancellation.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}
		// Non-recoverable error (closed queue, etc.)
		return err
	}
}
