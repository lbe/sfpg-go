//go:build integration

package files

import (
	"context"
	"database/sql"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/thumbnail"
)

// writeSpy wraps a tx-bound *gallerydb.CustomQueries and counts the three
// queries whose round-trips options B1/B2/E eliminate. It satisfies
// gallerylib.importerQueries via embedded method promotion, overriding only the
// counted methods. The real CustomQueries underneath keeps UpsertPathChain and
// the thumbnail upsert functional, so these tests exercise real DB behavior
// while observing which queries the optimization skipped.
type writeSpy struct {
	*gallerydb.CustomQueries
	getThumbnailExistsViewByIDCalls    int
	getFolderTileExistsViewByPathCalls int
	deleteInvalidFileByPathCalls       int
}

func (s *writeSpy) GetThumbnailExistsViewByID(ctx context.Context, id int64) (bool, error) {
	s.getThumbnailExistsViewByIDCalls++
	return s.CustomQueries.GetThumbnailExistsViewByID(ctx, id)
}

func (s *writeSpy) GetFolderTileExistsViewByPath(ctx context.Context, p string) (bool, error) {
	s.getFolderTileExistsViewByPathCalls++
	return s.CustomQueries.GetFolderTileExistsViewByPath(ctx, p)
}

func (s *writeSpy) DeleteInvalidFileByPath(ctx context.Context, p string) error {
	s.deleteInvalidFileByPathCalls++
	return s.CustomQueries.DeleteInvalidFileByPath(ctx, p)
}

// newSpyImporter builds an Importer backed by a tx-bound spy on the given RW
// pool's prepared queries. Returns the spy (for assertions) and the tx (caller
// commits/rolls back). The connection is returned to the pool on test cleanup.
func newSpyImporter(t *testing.T, ctx context.Context, rwPool *dbconnpool.DbSQLConnPool) (*gallerylib.Importer, *writeSpy, *sql.Tx) {
	t.Helper()
	cpc, err := rwPool.Get()
	if err != nil {
		t.Fatalf("rwPool.Get: %v", err)
	}
	t.Cleanup(func() { rwPool.Put(cpc) })
	tx, err := cpc.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	qtx := cpc.Queries.WithTx(tx)
	spy := &writeSpy{CustomQueries: qtx}
	return &gallerylib.Importer{Q: spy}, spy, tx
}

// thumbFile builds a File with a small in-memory thumbnail buffer drawn from the
// shared pool, so WriteFileInTx can return it to the pool correctly.
func thumbFile(t *testing.T, p string, exists bool, hadInvalid bool) *File {
	t.Helper()
	buf := thumbnail.GetBytesBuffer()
	if _, err := buf.WriteString("thumbnail-bytes"); err != nil {
		t.Fatalf("write thumb buffer: %v", err)
	}
	return &File{
		Path:            p,
		Exists:          exists,
		HadInvalidEntry: hadInvalid,
		Thumbnail:       buf,
		File: gallerydb.File{
			Mtime:     sql.NullInt64{Int64: 1700000000, Valid: true},
			SizeBytes: sql.NullInt64{Int64: 1024, Valid: true},
			MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
		},
	}
}

// --- B1: skip GetThumbnailExistsViewByID when the file is brand new ---

// TestWriteFileInTx_B1_SkipsThumbnailViewForNewFile proves B1: when f.Exists is
// false the file row is new, so a thumbnail cannot pre-exist (FK), and the
// 3-table JOIN view query is skipped entirely.
func TestWriteFileInTx_B1_SkipsThumbnailViewForNewFile(t *testing.T) {
	_, rwPool, _, ctx := createTestPoolsAndDir(t)
	imp, spy, tx := newSpyImporter(t, ctx, rwPool)
	defer tx.Rollback()

	f := thumbFile(t, path.Join("d1", "new.jpg"), false /*exists*/, false)
	if err := WriteFileInTx(ctx, imp, f); err != nil {
		t.Fatalf("WriteFileInTx: %v", err)
	}
	if spy.getThumbnailExistsViewByIDCalls != 0 {
		t.Errorf("new file (Exists=false) must not query the thumbnail view; got %d calls",
			spy.getThumbnailExistsViewByIDCalls)
	}
}

// TestWriteFileInTx_B1_QueriesThumbnailViewForExistingFile proves the
// correctness guard for B1: when f.Exists is true (re-import of a pre-existing
// file) the view IS queried, so an already-stored thumbnail is detected and not
// re-written.
func TestWriteFileInTx_B1_QueriesThumbnailViewForExistingFile(t *testing.T) {
	_, rwPool, _, ctx := createTestPoolsAndDir(t)
	imp, spy, tx := newSpyImporter(t, ctx, rwPool)
	defer tx.Rollback()

	f := thumbFile(t, path.Join("d1", "existing.jpg"), true /*exists*/, false)
	if err := WriteFileInTx(ctx, imp, f); err != nil {
		t.Fatalf("WriteFileInTx: %v", err)
	}
	if spy.getThumbnailExistsViewByIDCalls != 1 {
		t.Errorf("re-import (Exists=true) must query the thumbnail view exactly once; got %d calls",
			spy.getThumbnailExistsViewByIDCalls)
	}
}

// --- B2: skip tile view + chain for a dir already tiled this batch ---

// TestWriteFileInTx_B2_SkipsTileForAlreadyTiledDir proves B2: after the first
// file in a directory updates the folder-tile chain, the second file in the
// SAME directory (via the same batch Importer) skips the tile view query and
// the chain update.
func TestWriteFileInTx_B2_SkipsTileForAlreadyTiledDir(t *testing.T) {
	_, rwPool, _, ctx := createTestPoolsAndDir(t)
	imp, spy, tx := newSpyImporter(t, ctx, rwPool)
	defer tx.Rollback()

	dir := "album"
	f1 := thumbFile(t, path.Join(dir, "a.jpg"), false, false)
	if err := WriteFileInTx(ctx, imp, f1); err != nil {
		t.Fatalf("WriteFileInTx f1: %v", err)
	}
	afterFirst := spy.getFolderTileExistsViewByPathCalls
	if afterFirst == 0 {
		t.Fatal("first file in a dir should query the tile view; got 0 (spy misconfigured?)")
	}

	// Second file in the SAME dir, same Importer → dir is already tiled this batch.
	f2 := thumbFile(t, path.Join(dir, "b.jpg"), false, false)
	if err := WriteFileInTx(ctx, imp, f2); err != nil {
		t.Fatalf("WriteFileInTx f2: %v", err)
	}
	if got := spy.getFolderTileExistsViewByPathCalls - afterFirst; got != 0 {
		t.Errorf("second file in an already-tiled dir must skip the tile view; got %d additional calls",
			got)
	}
}

// --- E: skip DeleteInvalidFileByPath when the file was never invalid ---

// TestWriteFileInTx_E_SkipsDeleteWhenNeverInvalid proves E: when
// f.HadInvalidEntry is false (the common preload case) the
// DeleteInvalidFileByPath round-trip is skipped entirely.
func TestWriteFileInTx_E_SkipsDeleteWhenNeverInvalid(t *testing.T) {
	_, rwPool, _, ctx := createTestPoolsAndDir(t)
	imp, spy, tx := newSpyImporter(t, ctx, rwPool)
	defer tx.Rollback()

	f := thumbFile(t, path.Join("d2", "clean.jpg"), false, false /*hadInvalid*/)
	if err := WriteFileInTx(ctx, imp, f); err != nil {
		t.Fatalf("WriteFileInTx: %v", err)
	}
	if spy.deleteInvalidFileByPathCalls != 0 {
		t.Errorf("never-invalid file (HadInvalidEntry=false) must not issue DeleteInvalidFileByPath; got %d calls",
			spy.deleteInvalidFileByPathCalls)
	}
}

// TestWriteFileInTx_E_DeletesWhenPreviouslyInvalid proves E: when
// f.HadInvalidEntry is true the stale invalid_files row IS cleared.
func TestWriteFileInTx_E_DeletesWhenPreviouslyInvalid(t *testing.T) {
	_, rwPool, _, ctx := createTestPoolsAndDir(t)
	imp, spy, tx := newSpyImporter(t, ctx, rwPool)
	defer tx.Rollback()

	f := thumbFile(t, path.Join("d3", "wasinvalid.jpg"), false, true /*hadInvalid*/)
	if err := WriteFileInTx(ctx, imp, f); err != nil {
		t.Fatalf("WriteFileInTx: %v", err)
	}
	if spy.deleteInvalidFileByPathCalls != 1 {
		t.Errorf("previously-invalid file (HadInvalidEntry=true) must issue DeleteInvalidFileByPath exactly once; got %d calls",
			spy.deleteInvalidFileByPathCalls)
	}
}

// TestWriteFileInTx_E_ClearsStaleInvalidRow is the correctness-critical
// integration assertion for E: a previously-invalid file, after a successful
// WriteFileInTx + commit, must have its invalid_files row removed — otherwise
// the next run would skip a now-valid file. This guards the silent-correctness
// trap where the flag is set only in the wrong branch (the unchanged-skip
// branch that never reaches WriteFileInTx).
func TestWriteFileInTx_E_ClearsStaleInvalidRow(t *testing.T) {
	roPool, rwPool, _, ctx := createTestPoolsAndDir(t)
	const p = "d4/recover.jpg"

	// Seed an invalid_files row for the path (simulating a prior failed run).
	{
		cpc, err := rwPool.Get()
		if err != nil {
			t.Fatalf("rwPool.Get: %v", err)
		}
		if err := cpc.Queries.UpsertInvalidFile(ctx, gallerydb.UpsertInvalidFileParams{
			Path:   p,
			Mtime:  1600000000,
			Size:   512,
			Reason: sql.NullString{String: "prior corruption", Valid: true},
		}); err != nil {
			rwPool.Put(cpc)
			t.Fatalf("seed UpsertInvalidFile: %v", err)
		}
		rwPool.Put(cpc)
	}

	// Confirm the row exists.
	cpcRo, err := roPool.Get()
	if err != nil {
		t.Fatalf("roPool.Get: %v", err)
	}
	if _, err := cpcRo.Queries.GetInvalidFileByPath(ctx, p); err != nil {
		roPool.Put(cpcRo)
		t.Fatalf("precondition: invalid_files row should exist: %v", err)
	}
	roPool.Put(cpcRo)

	// Write the file with HadInvalidEntry=true (as checkIfFileModifiedCore would
	// set it after detecting the row with a changed mtime), then commit.
	imp, _, tx := newSpyImporter(t, ctx, rwPool)
	f := thumbFile(t, p, false, true /*hadInvalid*/)
	if err := WriteFileInTx(ctx, imp, f); err != nil {
		tx.Rollback()
		t.Fatalf("WriteFileInTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// The stale invalid_files row must be gone.
	cpcRo2, err := roPool.Get()
	if err != nil {
		t.Fatalf("roPool.Get (post): %v", err)
	}
	defer roPool.Put(cpcRo2)
	_, err = cpcRo2.Queries.GetInvalidFileByPath(ctx, p)
	if err != sql.ErrNoRows {
		t.Errorf("invalid_files row should be cleared after successful WriteFileInTx; got err=%v (want sql.ErrNoRows)", err)
	}
}

// TestCheckIfFileModified_SetsHadInvalidEntryForChangedInvalidFile is the guard
// for the E correctness trap: checkIfFileModifiedCore must set f.HadInvalidEntry
// = true whenever an invalid_files row exists for the path, INCLUDING the case
// where the file has since changed (mtime differs) and therefore proceeds to
// reprocessing. The silent bug would set the flag only in the unchanged-skip
// branch (which returns early and never reaches WriteFileInTx), leaving a
// previously-invalid-now-changed file with HadInvalidEntry=false → WriteFileInTx
// skips the DELETE → the stale invalid_files row persists → the next run skips
// a now-valid file. This test fails if the flag is set in the wrong branch.
func TestCheckIfFileModified_SetsHadInvalidEntryForChangedInvalidFile(t *testing.T) {
	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
	name := createTestImage(t, imagesDir, "recov.jpg")

	// Read the real on-disk mtime so we can seed a DIFFERENT mtime in invalid_files.
	info, err := os.Stat(filepath.Join(imagesDir, name))
	if err != nil {
		t.Fatalf("stat test image: %v", err)
	}
	realMtime := info.ModTime().Unix()
	seededMtime := realMtime - 86400 // deliberately different → not unchanged

	// Seed an invalid_files row with the stale mtime.
	{
		cpc, err := rwPool.Get()
		if err != nil {
			t.Fatalf("rwPool.Get: %v", err)
		}
		if err := cpc.Queries.UpsertInvalidFile(ctx, gallerydb.UpsertInvalidFileParams{
			Path:   name,
			Mtime:  seededMtime,
			Size:   999,
			Reason: sql.NullString{String: "prior corruption", Valid: true},
		}); err != nil {
			rwPool.Put(cpc)
			t.Fatalf("seed UpsertInvalidFile: %v", err)
		}
		rwPool.Put(cpc)
	}

	cpcRo, err := roPool.Get()
	if err != nil {
		t.Fatalf("roPool.Get: %v", err)
	}
	defer roPool.Put(cpcRo)

	f := &File{ImagesDir: imagesDir, Path: name}
	unchanged, err := checkIfFileModifiedCore(ctx,
		cpcRo.Queries.GetFileByPath,
		cpcRo.Queries.GetInvalidFileByPath,
		f)
	if err != nil {
		t.Fatalf("checkIfFileModifiedCore: %v", err)
	}

	// The file changed (seeded mtime != real mtime), so it must proceed to
	// reprocessing (unchanged == false), AND the flag must be set so WriteFileInTx
	// clears the stale row.
	if unchanged {
		t.Error("file with a stale invalid_files row (changed mtime) should proceed to reprocessing, not be skipped")
	}
	if !f.HadInvalidEntry {
		t.Error("HadInvalidEntry must be true when an invalid_files row exists for the path " +
			"(regardless of mtime match); otherwise WriteFileInTx skips the DELETE and the stale row persists")
	}
}
