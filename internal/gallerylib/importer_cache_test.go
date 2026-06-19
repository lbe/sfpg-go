//go:build integration

package gallerylib_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
)

// countingQueries wraps a *gallerydb.CustomQueries and counts GetFolderByPath
// calls. It satisfies gallerylib.importerQueries (structurally) via embedded
// method promotion, overriding only GetFolderByPath. This lets us prove the
// intra-batch folder cache is actually CONSULTED (not just populated) by
// asserting that a second file in the same directory triggers zero additional
// GetFolderByPath round-trips.
type countingQueries struct {
	*gallerydb.CustomQueries
	getFolderByPathCalls int
}

func (c *countingQueries) GetFolderByPath(ctx context.Context, path string) (gallerydb.Folder, error) {
	c.getFolderByPathCalls++
	return c.CustomQueries.GetFolderByPath(ctx, path)
}

// TestUpsertPathChain_FolderCacheDedup is the regression guard for option A:
// the per-batch folder cache must turn repeated per-segment GetFolderByPath
// queries into a single resolution per distinct folder path. Without the cache,
// every file re-queries every path segment; with it, a second file in the same
// directory adds zero GetFolderByPath calls.
func TestUpsertPathChain_FolderCacheDedup(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	qtx := q.WithTx(tx)
	counter := &countingQueries{CustomQueries: qtx}
	imp := &gallerylib.Importer{Q: counter}

	dir := filepath.Join(string(filepath.Separator), "photos", "2025", "vacation")
	mk := func(name string) string { return filepath.Join(dir, name) }

	common := func(path string) (gallerydb.File, error) {
		return imp.UpsertPathChain(ctx, path,
			1700000000, 1024, "md5", 12345, 1920, 1080, "image/jpeg")
	}

	// First file resolves all 3 segments: /photos, /photos/2025, /photos/2025/vacation
	if _, err := common(mk("a.jpg")); err != nil {
		t.Fatalf("UpsertPathChain file1: %v", err)
	}
	afterFirst := counter.getFolderByPathCalls
	if afterFirst == 0 {
		t.Fatal("expected the first file to issue GetFolderByPath calls for its segments; got 0 (spy misconfigured?)")
	}

	// Second file in the SAME directory must add ZERO GetFolderByPath calls —
	// every segment is a cache hit.
	if _, err := common(mk("b.jpg")); err != nil {
		t.Fatalf("UpsertPathChain file2: %v", err)
	}
	if got := counter.getFolderByPathCalls - afterFirst; got != 0 {
		t.Errorf("second file in the same dir should be fully cache-served; got %d additional GetFolderByPath calls", got)
	}

	// Control: a file in a NEW sibling directory (/photos/2025/sunset) must
	// re-resolve only the new segment. /photos and /photos/2025 are cache hits,
	// /photos/2025/sunset is a miss → exactly 1 additional call.
	newDir := filepath.Join(string(filepath.Separator), "photos", "2025", "sunset")
	if _, err := imp.UpsertPathChain(ctx, filepath.Join(newDir, "c.jpg"),
		1700000000, 1024, "md5", 12345, 1920, 1080, "image/jpeg"); err != nil {
		t.Fatalf("UpsertPathChain file3 (new dir): %v", err)
	}
	if got := counter.getFolderByPathCalls - afterFirst; got != 1 {
		t.Errorf("control: new sibling dir should add exactly 1 GetFolderByPath call (the new segment); got %d", got)
	}
}
