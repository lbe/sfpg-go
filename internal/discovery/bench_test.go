package discovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
	"github.com/lbe/sfpg-go/internal/queue"
)

// The walk benchmarks need a real images directory, as in production. They
// are skipped without one, and scripts/parallelwalkdir-bench-gate.sh runs them:
//
//	SFPG_WALK_BENCH_ROOT=/path/to/Images go test ./internal/discovery -bench=BenchmarkDiscoveryWalk_ -benchtime=1x -run=^$

func benchImagesRoot(b *testing.B) string {
	root := os.Getenv("SFPG_WALK_BENCH_ROOT")
	if root == "" {
		b.Skip("set SFPG_WALK_BENCH_ROOT")
	}
	return root
}

// benchWalk walks root with discovery's filters, without a catalog, and calls
// onReport for each image file. It returns the number of files.
func benchWalk(root string, onReport func(parallelwalkdir.ReportedFile)) int64 {
	w := parallelwalkdir.NewWalker(
		parallelwalkdir.WithBasenameInclude(imageName),
		parallelwalkdir.WithSizeNotZero(),
		parallelwalkdir.WithWalkPathStore(parallelwalkdir.NewWalkPathStore()),
	)
	results, errs := w.ParallelWalk(root)
	go func() {
		for range errs {
		}
	}()
	var n int64
	for f := range results {
		onReport(f)
		n++
	}
	return n
}

func workOf(f parallelwalkdir.ReportedFile) PathWork {
	return PathWork{Path: f.Path, MtimeUnix: f.ModTimeUnix, SizeBytes: f.SizeBytes}
}

// BenchmarkDiscoveryWalk_memoryEnqueue walks into an in-memory queue: the
// cost of the walk alone.
func BenchmarkDiscoveryWalk_memoryEnqueue(b *testing.B) {
	root := benchImagesRoot(b)
	q := queue.NewQueue[PathWork](1_000_000)
	for b.Loop() {
		n := benchWalk(root, func(f parallelwalkdir.ReportedFile) { _ = q.Enqueue(workOf(f)) })
		for {
			if _, err := q.Dequeue(); err != nil {
				break
			}
		}
		b.ReportMetric(float64(n), "files")
	}
}

// BenchmarkDiscoveryWalk_diskDqueEnqueueOnly walks into the disk backlog with
// nothing taking from it: the backlog's write path.
func BenchmarkDiscoveryWalk_diskDqueEnqueueOnly(b *testing.B) {
	root := benchImagesRoot(b)
	for b.Loop() {
		bl, err := openBacklog(filepath.Join(b.TempDir(), "discovery-dque"))
		if err != nil {
			b.Fatal(err)
		}
		n := benchWalk(root, func(f parallelwalkdir.ReportedFile) {
			if err := bl.enqueue(workOf(f)); err != nil {
				b.Fatal(err)
			}
		})
		if err := bl.close(); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(n), "files")
	}
}

// BenchmarkDiscoveryWalk_diskDqueWithDrainWorkers walks into the disk backlog
// while one worker per CPU takes from it, as discovery's workers do, without
// processing.
func BenchmarkDiscoveryWalk_diskDqueWithDrainWorkers(b *testing.B) {
	root := benchImagesRoot(b)
	for b.Loop() {
		bl, err := openBacklog(filepath.Join(b.TempDir(), "discovery-dque"))
		if err != nil {
			b.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		var taken atomic.Int64
		var wg sync.WaitGroup
		for range runtime.GOMAXPROCS(0) {
			wg.Go(func() {
				for ctx.Err() == nil {
					_, err := bl.dequeue()
					switch {
					case errors.Is(err, queue.ErrEmptyQueue):
						time.Sleep(emptyBacklogWait)
					case err != nil:
						return
					default:
						taken.Add(1)
					}
				}
			})
		}
		n := benchWalk(root, func(f parallelwalkdir.ReportedFile) {
			if err := bl.enqueue(workOf(f)); err != nil {
				b.Fatal(err)
			}
		})
		for taken.Load() < n {
			time.Sleep(50 * time.Millisecond)
		}
		cancel()
		wg.Wait()
		if err := bl.close(); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(n), "files")
		b.ReportMetric(float64(taken.Load()), "dequeued")
	}
}
