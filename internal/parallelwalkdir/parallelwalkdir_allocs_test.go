//go:build !race

package parallelwalkdir

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestParallelWalk_ReportedPathAllocs checks that a no-match include regex does not
// increase per-walk allocations versus an empty directory (baseline walk overhead only).
// Skipped in race builds: -race instrumentation makes AllocsPerRun comparisons flaky.
func TestParallelWalk_ReportedPathAllocs(t *testing.T) {
	noMatch := regexp.MustCompile(`^nomatch$`)
	emptyDir := t.TempDir()
	tree := t.TempDir()
	for i := range 8 {
		if err := os.WriteFile(filepath.Join(tree, fmt.Sprintf("f%d.dat", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	drainWalk := func(root string) {
		walker := NewWalker(WithRegexpInclude(noMatch), WithMaxWorkers(1))
		results, errs := walker.ParallelWalk(root)
		for range results {
		}
		for range errs {
		}
	}

	for range 10 {
		drainWalk(emptyDir)
		drainWalk(tree)
	}

	emptyAllocs := testing.AllocsPerRun(5, func() { drainWalk(emptyDir) })
	filteredAllocs := testing.AllocsPerRun(5, func() { drainWalk(tree) })

	if filteredAllocs > emptyAllocs {
		t.Fatalf("filtered allocs/op %v > empty dir %v", filteredAllocs, emptyAllocs)
	}
}
