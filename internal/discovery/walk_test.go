package discovery

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
	"github.com/lbe/sfpg-go/internal/server/metrics"
)

func TestWalkQueuesOnlyNonEmptyImages(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel string, size int) string {
		writeFile(t, dir, rel, make([]byte, size), testMtime)
		return filepath.Join(dir, filepath.FromSlash(rel))
	}
	want := []string{
		mustWrite("a.jpg", 10),
		mustWrite("B.JPEG", 1),
		mustWrite("c.PNG", 2),
		mustWrite("d.gif", 3),
		mustWrite("nested/e.png", 4),
	}
	// Skipped: empty, other extensions, and names that only contain an image
	// extension.
	mustWrite("zero.jpg", 0)
	mustWrite("nested/zero.png", 0)
	mustWrite("f.webp", 8)
	mustWrite("g.txt", 8)
	mustWrite("x.jpg.txt", 8)

	s := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: filepath.ToSlash(dir)}, Deps{
		Catalog: emptyCatalog,
	})
	s.walk(t.Context(), parallelwalkdir.NewWalkPathStore())

	got := queueContents(t, s.backlog)
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("queued paths = %q, want %q", got, want)
	}
}

func TestWalkSkipsCatalogedUnchangedFiles(t *testing.T) {
	dir := t.TempDir()
	cached := writeFile(t, dir, "cached.jpg", make([]byte, testSize), testMtime)
	stale := writeFile(t, dir, "stale.jpg", make([]byte, testSize), testMtime)

	// The catalog says cached.jpg is unchanged and stale.jpg has a new mtime.
	catalog := func(context.Context, []byte) (map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState, error) {
		return map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState{
			parallelwalkdir.HashPathBytes([]byte("cached.jpg")): {
				FileIDValid:    true,
				FileMtimeValid: true,
				FileMtime:      cached.ModTime().Unix(),
				FileSizeValid:  true,
				FileSize:       cached.Size(),
				FileMD5Valid:   true,
			},
			parallelwalkdir.HashPathBytes([]byte("stale.jpg")): {
				FileIDValid:    true,
				FileMtimeValid: true,
				FileMtime:      stale.ModTime().Unix() - 1,
				FileSizeValid:  true,
				FileSize:       stale.Size(),
				FileMD5Valid:   true,
			},
		}, nil
	}
	s := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: filepath.ToSlash(dir)}, Deps{
		Catalog: catalog,
	})
	s.walk(t.Context(), parallelwalkdir.NewWalkPathStore())

	want := []string{filepath.Join(dir, "stale.jpg")}
	if got := queueContents(t, s.backlog); !slices.Equal(got, want) {
		t.Errorf("queued paths = %q, want %q", got, want)
	}
	wantStats := metrics.FileProcessingMetrics{TotalFound: 2, AlreadyExisting: 1}
	if got := s.deps.Stats.GetStats(); got != wantStats {
		t.Errorf("counters = %+v, want %+v", got, wantStats)
	}
}

// emptyCatalog is a catalog with no rows: every file is new.
func emptyCatalog(context.Context, []byte) (map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState, error) {
	return map[parallelwalkdir.PathHash]parallelwalkdir.DirEntState{}, nil
}
