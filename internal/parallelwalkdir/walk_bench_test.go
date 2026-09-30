package parallelwalkdir

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// benchWalkImageIncludePattern must match WalkImageDir imageRegex in files/walker.go.
const benchWalkImageIncludePattern = `(?i)(?:jpe?g|gif|png)$`

var benchWalkImageIncludeRegexp = regexp.MustCompile(benchWalkImageIncludePattern)

// benchDirEntUnchangedMap is shared by BenchmarkParallelWalkDiscoveryImagesDirEntUnchanged
// getMap (empty catalog: every file treated as unchanged via check).
var benchDirEntUnchangedMap = map[PathHash]DirEntState{}

// discoveryWalkFileBasenames lists every non-symlink file basename under
// SFPG_WALK_BENCH_ROOT — same cardinality as include checks on regular-file
// dentries during a discovery walk (one name per file in the tree).
var (
	discoveryWalkFileBasenames     [][]byte
	discoveryWalkFileBasenamesOnce sync.Once
)

func discoveryWalkFileBasenamesFromEnv() [][]byte {
	discoveryWalkFileBasenamesOnce.Do(func() {
		root := os.Getenv("SFPG_WALK_BENCH_ROOT")
		if root == "" {
			return
		}
		err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			name := d.Name()
			discoveryWalkFileBasenames = append(discoveryWalkFileBasenames, []byte(name))
			return nil
		})
		if err != nil {
			panic("SFPG_WALK_BENCH_ROOT walk: " + err.Error())
		}
	})
	return discoveryWalkFileBasenames
}

// BenchmarkParallelWalkDiscoveryImages walks a real gallery tree using the same
// filters as production discovery (WalkImageDir): jpg/jpeg/gif/png, non-zero size.
// It does not enqueue to dque or touch the database.
//
// Set SFPG_WALK_BENCH_ROOT to the images root, then run:
//
//	SFPG_WALK_BENCH_ROOT=/path/to/Images \
//	  go test ./internal/parallelwalkdir -bench=BenchmarkParallelWalkDiscoveryImages -timeout=0 -run=^$
//
// Use -benchtime=1x on very large trees for a single timed iteration.
// Omit SFPG_WALK_BENCH_ROOT in CI; the benchmark skips.
func BenchmarkParallelWalkDiscoveryImages(b *testing.B) {
	root := os.Getenv("SFPG_WALK_BENCH_ROOT")
	if root == "" {
		b.Skip("set SFPG_WALK_BENCH_ROOT to the images directory")
	}

	var fileCount atomic.Int64
	var errCount atomic.Int64

	for b.Loop() {
		fileCount.Store(0)
		errCount.Store(0)

		walker := NewWalker(
			WithBasenameInclude(benchWalkImageIncludeRegexp),
			WithSizeNotZero(),
			WithWalkPathStore(NewWalkPathStore()),
		)
		results, errs := walker.ParallelWalk(root)

		done := make(chan struct{})
		go func() {
			for range errs {
				errCount.Add(1)
			}
			close(done)
		}()

		for rf := range results {
			fileCount.Add(1)
			_ = rf.Path
			_ = rf.ModTimeUnix
			_ = rf.SizeBytes
		}
		<-done
	}

	b.ReportMetric(float64(fileCount.Load()), "files")
	b.ReportMetric(float64(errCount.Load()), "walk_errs")
	b.Logf("reported files=%d walk errors=%d", fileCount.Load(), errCount.Load())
}

// BenchmarkParallelWalkDiscoveryImagesDirEntUnchanged uses catalog hooks that skip
// every matched file (0 reported) on SFPG_WALK_BENCH_ROOT for allocs gating.
func BenchmarkParallelWalkDiscoveryImagesDirEntUnchanged(b *testing.B) {
	root := os.Getenv("SFPG_WALK_BENCH_ROOT")
	if root == "" {
		b.Skip("set SFPG_WALK_BENCH_ROOT to the images directory")
	}

	getMap := func(context.Context, []byte) (map[PathHash]DirEntState, error) {
		return benchDirEntUnchangedMap, nil
	}
	check := func(_ []byte, _ fs.FileInfo, _ DirEntState, _ bool) (bool, error) {
		return false, nil
	}

	var fileCount atomic.Int64
	var errCount atomic.Int64

	for b.Loop() {
		fileCount.Store(0)
		errCount.Store(0)

		walker := NewWalker(
			WithBasenameInclude(benchWalkImageIncludeRegexp),
			WithSizeNotZero(),
			WithWalkPathStore(NewWalkPathStore()),
			WithDirEntCatalog(getMap, check),
		)
		results, errs := walker.ParallelWalk(root)

		done := make(chan struct{})
		go func() {
			for range errs {
				errCount.Add(1)
			}
			close(done)
		}()

		for rf := range results {
			fileCount.Add(1)
			_ = rf.Path
			_ = rf.ModTimeUnix
			_ = rf.SizeBytes
		}
		<-done
	}

	b.ReportMetric(float64(fileCount.Load()), "files")
	b.ReportMetric(float64(errCount.Load()), "walk_errs")
	b.Logf("reported files=%d walk errors=%d", fileCount.Load(), errCount.Load())
}

// Include-filter benchmarks: one simulated walk per b.Loop (GOMAXPROCS workers,
// persistent pool, every file basename under SFPG_WALK_BENCH_ROOT). Compares
// production regexp include vs byte suffixes derived from that same pattern.

func BenchmarkIncludeFilter_stdlib_Regexp_PerWalk(b *testing.B) {
	names := discoveryWalkFileBasenamesFromEnv()
	if len(names) == 0 {
		b.Skip("set SFPG_WALK_BENCH_ROOT")
	}
	re := benchWalkImageIncludeRegexp
	benchmarkIncludeFilterParallelOneWalk(b, names, re.Match)
}

func BenchmarkIncludeFilter_Bytes_Discovery_PerWalk(b *testing.B) {
	names := discoveryWalkFileBasenamesFromEnv()
	if len(names) == 0 {
		b.Skip("set SFPG_WALK_BENCH_ROOT")
	}
	benchmarkIncludeFilterParallelOneWalk(b, names, basenameIncludeForRegexp(benchWalkImageIncludeRegexp))
}

func BenchmarkIncludeFilter_stdlib_WalkerInclude_PerWalk(b *testing.B) {
	names := discoveryWalkFileBasenamesFromEnv()
	if len(names) == 0 {
		b.Skip("set SFPG_WALK_BENCH_ROOT")
	}
	w := NewWalker(WithRegexpInclude(benchWalkImageIncludeRegexp))
	benchmarkIncludeFilterParallelOneWalk(b, names, w.includeBasenameMatchesBytes)
}

func BenchmarkIncludeFilter_Bytes_WalkerInclude_PerWalk(b *testing.B) {
	names := discoveryWalkFileBasenamesFromEnv()
	if len(names) == 0 {
		b.Skip("set SFPG_WALK_BENCH_ROOT")
	}
	w := NewWalker(WithBasenameInclude(benchWalkImageIncludeRegexp))
	benchmarkIncludeFilterParallelOneWalk(b, names, w.includeBasenameMatchesBytes)
}

func benchmarkIncludeFilterParallelOneWalk(b *testing.B, names [][]byte, match func([]byte) bool) {
	workers := runtime.GOMAXPROCS(0)
	n := len(names)
	b.ReportMetric(float64(n), "dentries/walk")

	var next atomic.Uint64
	begin := make(chan struct{}, workers)
	done := make(chan struct{}, workers)

	var poolWG sync.WaitGroup
	poolWG.Add(workers)
	for range workers {
		go func() {
			defer poolWG.Done()
			for range begin {
				for {
					i := next.Add(1) - 1
					if i >= uint64(n) {
						break
					}
					match(names[i])
				}
				done <- struct{}{}
			}
		}()
	}

	b.ResetTimer()
	for b.Loop() {
		next.Store(0)
		for range workers {
			begin <- struct{}{}
		}
		for range workers {
			<-done
		}
	}
	close(begin)
	poolWG.Wait()
}
