package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

// Bracket discovery walk wall time. Set SFPG_WALK_BENCH_ROOT to the gallery
// Images directory (same as production imagesDir). CI skips when unset.
//
//	go test ./internal/server -bench='BenchmarkDiscoveryWalk_' -benchtime=1x -timeout=0 -run=^$

func benchImagesRoot(b *testing.B) string {
	root := os.Getenv("SFPG_WALK_BENCH_ROOT")
	if root == "" {
		b.Skip("set SFPG_WALK_BENCH_ROOT")
	}
	return root
}

func discoveryWalkerOpts(maxFiles int64, store *parallelwalkdir.WalkPathStore, extra ...parallelwalkdir.Option) []parallelwalkdir.Option {
	imageRegex := regexp.MustCompile(`(?i)(?:jpe?g|gif|png)$`)
	opts := []parallelwalkdir.Option{
		parallelwalkdir.WithBasenameInclude(imageRegex),
		parallelwalkdir.WithSizeNotZero(),
		parallelwalkdir.WithWalkPathStore(store),
	}
	opts = append(opts, extra...)
	if maxFiles > 0 {
		opts = append(opts, parallelwalkdir.WithMaxReportedFiles(maxFiles))
	}
	return opts
}

func newDiscoveryWalker(maxFiles int64, store *parallelwalkdir.WalkPathStore) *parallelwalkdir.Walker {
	return parallelwalkdir.NewWalker(discoveryWalkerOpts(maxFiles, store)...)
}

// newDiscoveryWalkerWithDirEntCatalog matches files.WalkImageDir walker options when CatalogROPool is set.
func newDiscoveryWalkerWithDirEntCatalog(
	maxFiles int64,
	store *parallelwalkdir.WalkPathStore,
	catalogROPool *dbconnpool.DbSQLConnPool,
	normalizedImagesDir string,
) *parallelwalkdir.Walker {
	opts := discoveryWalkerOpts(maxFiles, store,
		parallelwalkdir.WithDirEntCatalog(
			files.NewDiscoveryDirEntMapFunc(catalogROPool, normalizedImagesDir, removeImagesDirPrefix),
			files.DiscoveryDirEntModifiedWithStats(nil),
		),
	)
	return parallelwalkdir.NewWalker(opts...)
}

func drainWalkErrs(errs <-chan error) func() int64 {
	var n atomic.Int64
	done := make(chan struct{})
	go func() {
		for range errs {
			n.Add(1)
		}
		close(done)
	}()
	return func() int64 {
		<-done
		return n.Load()
	}
}

// maxFiles 0 means no limit. When maxFiles > 0, the walker stops after that many reports.
func runParallelWalk(root string, maxFiles int64, onReport func(parallelwalkdir.ReportedFile)) (files int64, walkErrs int64) {
	store := parallelwalkdir.NewWalkPathStore()
	walker := newDiscoveryWalker(maxFiles, store)
	results, errs := walker.ParallelWalk(root)
	waitErrs := drainWalkErrs(errs)

	var count int64
	for rf := range results {
		if maxFiles > 0 && count >= maxFiles {
			continue // drain buffered reports after walk stop
		}
		count++
		onReport(rf)
	}
	return count, waitErrs()
}

func runParallelWalkWithDirEntCatalog(
	root string,
	maxFiles int64,
	catalogROPool *dbconnpool.DbSQLConnPool,
	normalizedImagesDir string,
	onReport func(parallelwalkdir.ReportedFile),
) (files int64, walkErrs int64) {
	store := parallelwalkdir.NewWalkPathStore()
	walker := newDiscoveryWalkerWithDirEntCatalog(maxFiles, store, catalogROPool, normalizedImagesDir)
	results, errs := walker.ParallelWalk(root)
	waitErrs := drainWalkErrs(errs)

	var count int64
	for rf := range results {
		if maxFiles > 0 && count >= maxFiles {
			continue
		}
		count++
		onReport(rf)
	}
	return count, waitErrs()
}

func countParallelWalk(b *testing.B, root string, maxFiles int64, onReport func(parallelwalkdir.ReportedFile)) (files int64, walkErrs int64) {
	return runParallelWalk(root, maxFiles, onReport)
}

// Walk + in-memory enqueue only (no disk dque, no workers).
func BenchmarkDiscoveryWalk_memoryEnqueue(b *testing.B) {
	root := benchImagesRoot(b)
	q := queue.NewQueue[files.DiscoveryPathWork](1_000_000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n, _ := countParallelWalk(b, root, 0, func(rf parallelwalkdir.ReportedFile) {
			_ = q.Enqueue(files.DiscoveryPathWork{
				Path:      rf.Path,
				MtimeUnix: rf.ModTimeUnix,
				SizeBytes: rf.SizeBytes,
			})
		})
		for {
			_, err := q.Dequeue()
			if errors.Is(err, queue.ErrEmptyQueue) {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(n), "files")
	}
}

// Walk + production disk discovery dque; nothing dequeues (measures enqueue write path only).
func BenchmarkDiscoveryWalk_diskDqueEnqueueOnly(b *testing.B) {
	root := benchImagesRoot(b)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := filepath.Join(b.TempDir(), "discovery-dque")
		q, err := newDiscoveryDQueAdapter(dir)
		if err != nil {
			b.Fatal(err)
		}

		n, _ := countParallelWalk(b, root, 0, func(rf parallelwalkdir.ReportedFile) {
			if err := q.Enqueue(files.DiscoveryPathWork{
				Path:      rf.Path,
				MtimeUnix: rf.ModTimeUnix,
				SizeBytes: rf.SizeBytes,
			}); err != nil {
				b.Fatal(err)
			}
		})
		q.Close()
		b.ReportMetric(float64(n), "files")
	}
}

// Walk + disk dque + N noop dequeue workers (same dequeue loop shape as discovery pool).
func BenchmarkDiscoveryWalk_diskDqueWithDrainWorkers(b *testing.B) {
	root := benchImagesRoot(b)
	workers := runtime.GOMAXPROCS(0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := filepath.Join(b.TempDir(), "discovery-dque")
		q, err := newDiscoveryDQueAdapter(dir)
		if err != nil {
			b.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		var dequeued atomic.Int64
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				for {
					if ctx.Err() != nil {
						return
					}
					_, err := q.Dequeue()
					if err != nil {
						if errors.Is(err, queue.ErrEmptyQueue) {
							time.Sleep(100 * time.Millisecond)
							continue
						}
						if errors.Is(err, queue.ErrClosedQueue) {
							return
						}
						return
					}
					dequeued.Add(1)
				}
			})
		}

		n, _ := countParallelWalk(b, root, 0, func(rf parallelwalkdir.ReportedFile) {
			if err := q.Enqueue(files.DiscoveryPathWork{
				Path:      rf.Path,
				MtimeUnix: rf.ModTimeUnix,
				SizeBytes: rf.SizeBytes,
			}); err != nil {
				b.Fatal(err)
			}
		})

		// Let drain workers empty the backlog (production waits similarly).
		deadline := time.Now().Add(2 * time.Hour)
		for time.Now().Before(deadline) {
			if q.IsEmpty() && dequeued.Load() >= n {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		cancel()
		wg.Wait()
		q.Close()

		b.ReportMetric(float64(n), "files")
		b.ReportMetric(float64(dequeued.Load()), "dequeued")
	}
}

const (
	benchSampleFileCount  = 500_000
	benchFullGalleryFiles = 15_666_608 // last full discovery run on 8084 gallery
)

// 500k samples: benchmarks only (not run by go test without -bench).
//
//	SFPG_WALK_BENCH_ROOT=/path/to/Images \
//	  go test ./internal/server -bench=BenchmarkDiscoveryWalk_sample500k -benchtime=1x -timeout=0 -run=^$

func BenchmarkDiscoveryWalk_sample500k_walkOnly(b *testing.B) {
	root := benchImagesRoot(b)
	scale := float64(benchFullGalleryFiles) / float64(benchSampleFileCount)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		n, walkErrs := runParallelWalk(root, benchSampleFileCount, func(parallelwalkdir.ReportedFile) {})
		elapsed := time.Since(start)
		if n != benchSampleFileCount {
			b.Fatalf("walk_only: expected %d files, got %d", benchSampleFileCount, n)
		}
		b.ReportMetric(float64(n), "files")
		b.Logf("walk_only sample=%d walk_errs=%d elapsed=%s extrap_full≈%s",
			n, walkErrs, elapsed, time.Duration(float64(elapsed)*scale))
	}
}

func BenchmarkDiscoveryWalk_sample500k_diskDque(b *testing.B) {
	root := benchImagesRoot(b)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := filepath.Join(b.TempDir(), "discovery-dque")
		q, err := newDiscoveryDQueAdapter(dir)
		if err != nil {
			b.Fatal(err)
		}

		start := time.Now()
		n, walkErrs := runParallelWalk(root, benchSampleFileCount, func(rf parallelwalkdir.ReportedFile) {
			if err := q.Enqueue(files.DiscoveryPathWork{
				Path:      rf.Path,
				MtimeUnix: rf.ModTimeUnix,
				SizeBytes: rf.SizeBytes,
			}); err != nil {
				b.Fatal(err)
			}
		})
		elapsed := time.Since(start)
		q.Close()

		if n != benchSampleFileCount {
			b.Fatalf("walk+disk_dque: expected %d files, got %d", benchSampleFileCount, n)
		}
		extrap := time.Duration(float64(elapsed) * float64(benchFullGalleryFiles) / float64(n))
		b.ReportMetric(float64(n), "files")
		b.Logf("walk+disk_dque sample=%d walk_errs=%d elapsed=%s extrap_full≈%s",
			n, walkErrs, elapsed, extrap)
	}
}

func BenchmarkDiscoveryWalk_sample500k_diskDqueWithDrainWorkers(b *testing.B) {
	root := benchImagesRoot(b)
	workers := runtime.GOMAXPROCS(0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := filepath.Join(b.TempDir(), "discovery-dque")
		q, err := newDiscoveryDQueAdapter(dir)
		if err != nil {
			b.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		var dequeued atomic.Int64
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				for {
					if ctx.Err() != nil {
						return
					}
					_, err := q.Dequeue()
					if err != nil {
						if errors.Is(err, queue.ErrEmptyQueue) {
							time.Sleep(100 * time.Millisecond)
							continue
						}
						if errors.Is(err, queue.ErrClosedQueue) {
							return
						}
						return
					}
					dequeued.Add(1)
				}
			})
		}

		start := time.Now()
		n, walkErrs := runParallelWalk(root, benchSampleFileCount, func(rf parallelwalkdir.ReportedFile) {
			if err := q.Enqueue(files.DiscoveryPathWork{
				Path:      rf.Path,
				MtimeUnix: rf.ModTimeUnix,
				SizeBytes: rf.SizeBytes,
			}); err != nil {
				b.Fatal(err)
			}
		})

		deadline := time.Now().Add(2 * time.Hour)
		for time.Now().Before(deadline) {
			if q.IsEmpty() && dequeued.Load() >= n {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		elapsed := time.Since(start)
		cancel()
		wg.Wait()
		q.Close()

		if n < benchSampleFileCount-100 || n > benchSampleFileCount {
			b.Fatalf("expected ~%d files, got %d", benchSampleFileCount, n)
		}
		if dequeued.Load() < n {
			b.Fatalf("dequeued %d < enqueued %d", dequeued.Load(), n)
		}
		extrap := time.Duration(float64(elapsed) * float64(benchFullGalleryFiles) / float64(n))
		b.ReportMetric(float64(n), "files")
		b.ReportMetric(float64(dequeued.Load()), "dequeued")
		b.Logf("walk+disk_dque+drain sample=%d walk_errs=%d dequeued=%d elapsed=%s extrap_full≈%s",
			n, walkErrs, dequeued.Load(), elapsed, extrap)
	}
}

// benchNoopBatcher satisfies files.UnifiedBatcher; existing-file discovery should not write.
type benchNoopBatcher struct{}

func (benchNoopBatcher) SubmitFile(*files.File) error                 { return nil }
func (benchNoopBatcher) SubmitFolderIndex(files.FolderIndexRow) error { return nil }
func (benchNoopBatcher) PendingCount() int64                          { return 0 }
func (benchNoopBatcher) FolderIndexInflight() int64                   { return 0 }
func (benchNoopBatcher) SetFolderIndexRebuildActive(bool)             {}
func (benchNoopBatcher) BumpFolderIndexGeneration() int64             { return 0 }
func (benchNoopBatcher) SetFolderIndexRebuildScanHeld(bool)           {}

func openBenchGalleryROPool(b *testing.B) (*dbconnpool.DbSQLConnPool, func()) {
	imagesDir := benchImagesRoot(b)
	galleryRoot := filepath.Dir(imagesDir)
	dbPath := os.Getenv("SFPG_WALK_BENCH_DB")
	if dbPath == "" {
		dbPath = filepath.Join(galleryRoot, "DB", "sfpg.db")
	}
	thumbsPath := filepath.Join(galleryRoot, "DB", "thumbs", "thumbs.db")
	if _, err := os.Stat(dbPath); err != nil {
		b.Fatalf("gallery db %s: %v", dbPath, err)
	}

	ctx := context.Background()
	dsn := "file:" + filepath.ToSlash(dbPath) + "?mode=ro&_pragma=busy_timeout(5000)"
	roPool, err := dbconnpool.NewDbSQLConnPool(ctx, dsn, dbconnpool.Config{
		DriverName:         "sqlite3",
		MaxConnections:     int64(runtime.GOMAXPROCS(0) + 2),
		MinIdleConnections: 1,
		ReadOnly:           true,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsPath,
	})
	if err != nil {
		b.Fatal(err)
	}
	return roPool, func() { _ = roPool.Close() }
}

func waitBenchFileProcessingDrain(
	ctx context.Context,
	q queue.Queuer[files.DiscoveryPathWork],
	qSendersActive *atomic.Int64,
	stats *files.ProcessingStats,
	processor files.FileProcessor,
) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if qSendersActive.Load() == 0 && q.Len() == 0 && stats.InFlight.Load() == 0 && processor.PendingWriteCount() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Legacy stage-A bench: parallel walk without DirEnt catalog (enqueues every image
// match), disk dque, and pool workers. No per-file discovery-state SQL on workers;
// walk supplies mtime/size only.
// Unchanged gallery fixture; useful baseline vs dirEntCatalog bench below.
func BenchmarkDiscoveryWalk_sample500k_existingFilesOnly(b *testing.B) {
	imagesDir := benchImagesRoot(b)
	normalizedImagesDir := filepath.ToSlash(imagesDir)
	roPool, closePool := openBenchGalleryROPool(b)
	defer closePool()

	processor := files.NewFileProcessor(roPool, nil, nil, imagesDir, benchNoopBatcher{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := filepath.Join(b.TempDir(), "discovery-dque")
		q, err := newDiscoveryDQueAdapter(dir)
		if err != nil {
			b.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		stats := &files.ProcessingStats{}
		pool := workerpool.NewPool(ctx, runtime.GOMAXPROCS(0), 0, 10*time.Second)
		poolDone := make(chan struct{})
		var qSendersActive atomic.Int64

		go func() {
			defer close(poolDone)
			pf := files.NewPoolFuncWithProcessor(
				processor, q, normalizedImagesDir, removeImagesDirPrefix, stats, nil,
			)
			pool.StartWorkerPool(pf, roPool, nil, q.Len)
		}()

		qSendersActive.Add(1)
		start := time.Now()
		n, walkErrs := runParallelWalk(imagesDir, benchSampleFileCount, func(rf parallelwalkdir.ReportedFile) {
			if err := q.Enqueue(files.DiscoveryPathWork{
				Path:      rf.Path,
				MtimeUnix: rf.ModTimeUnix,
				SizeBytes: rf.SizeBytes,
			}); err != nil {
				b.Fatal(err)
			}
		})
		qSendersActive.Add(-1)

		if err := waitBenchFileProcessingDrain(ctx, q, &qSendersActive, stats, processor); err != nil {
			b.Fatal(err)
		}
		elapsed := time.Since(start)
		cancel()
		<-poolDone
		q.Close()

		if n < benchSampleFileCount-100 || n > benchSampleFileCount {
			b.Fatalf("expected ~%d files, got %d", benchSampleFileCount, n)
		}
		extrap := time.Duration(float64(elapsed) * float64(benchFullGalleryFiles) / float64(n))
		b.ReportMetric(float64(n), "files")
		b.ReportMetric(float64(stats.AlreadyExisting.Load()), "already_existing")
		b.Logf("existing-only sample=%d walk_errs=%d already_existing=%d elapsed=%s extrap_full≈%s",
			n, walkErrs, stats.AlreadyExisting.Load(), elapsed, extrap)
	}
}

// Production-shaped stage-A bench with walk-time DirEnt catalog (WalkImageDir when
// CatalogROPool is set). Unchanged gallery fixture → ~0 enqueues; pool+dque still
// matches BenchmarkDiscoveryWalk_sample500k_existingFilesOnly.
func BenchmarkDiscoveryWalk_sample500k_existingFilesOnly_dirEntCatalog(b *testing.B) {
	imagesDir := benchImagesRoot(b)
	normalizedImagesDir := filepath.ToSlash(imagesDir)
	roPool, closePool := openBenchGalleryROPool(b)
	defer closePool()

	processor := files.NewFileProcessor(roPool, nil, nil, imagesDir, benchNoopBatcher{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := filepath.Join(b.TempDir(), "discovery-dque")
		q, err := newDiscoveryDQueAdapter(dir)
		if err != nil {
			b.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		stats := &files.ProcessingStats{}
		pool := workerpool.NewPool(ctx, runtime.GOMAXPROCS(0), 0, 10*time.Second)
		poolDone := make(chan struct{})
		var qSendersActive atomic.Int64

		go func() {
			defer close(poolDone)
			pf := files.NewPoolFuncWithProcessor(
				processor, q, normalizedImagesDir, removeImagesDirPrefix, stats, nil,
			)
			pool.StartWorkerPool(pf, roPool, nil, q.Len)
		}()

		qSendersActive.Add(1)
		start := time.Now()
		var enqueued int64
		n, walkErrs := runParallelWalkWithDirEntCatalog(
			imagesDir, benchSampleFileCount, roPool, normalizedImagesDir,
			func(rf parallelwalkdir.ReportedFile) {
				if err := q.Enqueue(files.DiscoveryPathWork{
					Path:      rf.Path,
					MtimeUnix: rf.ModTimeUnix,
					SizeBytes: rf.SizeBytes,
				}); err != nil {
					b.Fatal(err)
				}
				enqueued++
			},
		)
		qSendersActive.Add(-1)

		if err := waitBenchFileProcessingDrain(ctx, q, &qSendersActive, stats, processor); err != nil {
			b.Fatal(err)
		}
		elapsed := time.Since(start)
		cancel()
		<-poolDone
		q.Close()

		if n != enqueued {
			b.Fatalf("reported %d != enqueued %d", n, enqueued)
		}
		b.ReportMetric(float64(enqueued), "enqueued")
		b.Logf("dirEnt_catalog sample_cap=%d reported=%d walk_errs=%d elapsed=%s",
			benchSampleFileCount, n, walkErrs, elapsed)
	}
}
