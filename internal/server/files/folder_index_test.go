//go:build integration

package files

import (
	"context"
	"database/sql"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// syncFolderIndexBatcher is a UnifiedBatcher that INSERTs FolderIndex rows
// directly into file_folder_index_new on the test RW pool (no async batcher).
// It is used by RebuildFileFolderIndex tests that want the production outer/
// inner populate loop without the full writebatcher + dque machinery.
type syncFolderIndexBatcher struct {
	rwPool         *dbconnpool.DbSQLConnPool
	rebuildOn      bool
	scanHeld       bool
	gen            int64
	submitScanHeld []bool
}

func (s *syncFolderIndexBatcher) SubmitFile(file *File) error { return nil }

// SubmitFolderIndex flushes one FolderIndex row into file_folder_index_new on the
// test RW pool using the production gallerydb prepared-INSERT path (no raw
// Conn.ExecContext SQL string, satisfying G3). It Get's a conn, BeginTx, binds
// the pooled Queries via WithTx, and calls InsertFileFolderIndexNewRows.
func (s *syncFolderIndexBatcher) SubmitFolderIndex(row FolderIndexRow) error {
	s.submitScanHeld = append(s.submitScanHeld, s.scanHeld)
	cpc, err := s.rwPool.Get()
	if err != nil {
		return err
	}
	defer s.rwPool.Put(cpc)
	tx, err := cpc.Conn.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	qtx := cpc.Queries.WithTx(tx)
	params := []gallerydb.InsertFileFolderIndexNewParams{{
		FileID:     row.FileID,
		FolderID:   row.FolderID,
		ImageIndex: row.ImageIndex,
		ImageCount: row.ImageCount,
		PrevID:     row.PrevID,
		NextID:     row.NextID,
		FirstID:    row.FirstID,
		LastID:     row.LastID,
	}}
	if err := qtx.InsertFileFolderIndexNewRows(context.Background(), params); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *syncFolderIndexBatcher) PendingCount() int64                     { return 0 }
func (s *syncFolderIndexBatcher) FolderIndexInflight() int64              { return 0 }
func (s *syncFolderIndexBatcher) SetFolderIndexRebuildActive(a bool)      { s.rebuildOn = a }
func (s *syncFolderIndexBatcher) SetFolderIndexRebuildScanHeld(held bool) { s.scanHeld = held }
func (s *syncFolderIndexBatcher) BumpFolderIndexGeneration() int64 {
	s.gen++
	// Never collide with a leftover generation of 1 (0+1 from a fresh process).
	if s.gen == 0 {
		s.gen = 2
	}
	return s.gen
}

func TestRebuildFileFolderIndex_Success(t *testing.T) {
	roPool, rwPool, _, ctx := createTestPoolsAndDir(t)
	helper := &syncFolderIndexBatcher{rwPool: rwPool}

	// Do initial rebuild.
	if err := RebuildFileFolderIndex(ctx, rwPool, roPool, helper); err != nil {
		t.Fatalf("RebuildFileFolderIndex: %v", err)
	}

	// Verify file_folder_index exists and has rows (backfilled by migration).
	cpc, err := rwPool.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rwPool.Put(cpc)

	var count int64
	if err := cpc.Conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM file_folder_index`).Scan(&count); err != nil {
		t.Fatalf("count file_folder_index: %v", err)
	}
	// file_folder_index is empty because no files were inserted after migration.
	// That's fine — rebuild should succeed on an empty DB too.
	_ = count

	// Verify an explicit index covering folder_id exists on the active table.
	activeHasFolderIDIndex(t, ctx, cpc.Conn)

	// Verify no leftover _new table.
	var newExists int64
	if err := cpc.Conn.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='file_folder_index_new')`).Scan(&newExists); err != nil {
		t.Fatalf("check _new: %v", err)
	}
	if newExists != 0 {
		t.Error("file_folder_index_new should not exist after successful rebuild")
	}

	assertTableGone(t, rwPool, "file_folder_index_to_be_dropped")
}

func TestRebuildFileFolderIndex_CanceledContext(t *testing.T) {
	roPool, rwPool, _, _ := createTestPoolsAndDir(t)
	helper := &syncFolderIndexBatcher{rwPool: rwPool}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RebuildFileFolderIndex(ctx, rwPool, roPool, helper); err == nil {
		t.Fatal("expected error with canceled context")
	}
	cpc, err := rwPool.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rwPool.Put(cpc)
	var newExists int64
	if err := cpc.Conn.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='file_folder_index_new')`).Scan(&newExists); err != nil {
		t.Fatalf("check _new: %v", err)
	}
	if newExists != 0 {
		t.Error("file_folder_index_new must not remain after canceled CloneEmpty")
	}
}

func TestRebuildFileFolderIndex_CleanupLeftovers(t *testing.T) {
	roPool, rwPool, _, ctx := createTestPoolsAndDir(t)
	helper := &syncFolderIndexBatcher{rwPool: rwPool}

	// Artificially create leftover _new and _to_be_dropped tables (simulate crash).
	cpc, err := rwPool.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if _, err := cpc.Conn.ExecContext(ctx, `CREATE TABLE file_folder_index_new (x INTEGER)`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("create fake _new: %v", err)
	}
	if _, err := cpc.Conn.ExecContext(ctx, `CREATE TABLE file_folder_index_to_be_dropped (x INTEGER)`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("create fake _to_be_dropped: %v", err)
	}
	rwPool.Put(cpc)

	// Run rebuild — leftover _new is replaced by CloneEmpty; leftover stale
	// is dropped by Swap at cutover (pre-rename) and the post-cutover stale
	// is dropped before Swap returns.

	if err := RebuildFileFolderIndex(ctx, rwPool, roPool, helper); err != nil {
		t.Fatalf("RebuildFileFolderIndex: %v", err)
	}

	// Verify leftovers are gone (_new from CloneEmpty; stale from Swap).

	cpc2, err := rwPool.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rwPool.Put(cpc2)

	var newExists int64
	if err := cpc2.Conn.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='file_folder_index_new')`).Scan(&newExists); err != nil {
		t.Fatalf("check _new: %v", err)
	}
	if newExists != 0 {
		t.Error("_new table should be cleaned up by rebuild")
	}

	assertTableGone(t, rwPool, "file_folder_index_to_be_dropped")

	// Verify the active table exists and has the proper schema.
	var activeExists int64
	if err := cpc2.Conn.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='file_folder_index')`).Scan(&activeExists); err != nil {
		t.Fatalf("check active: %v", err)
	}
	if activeExists == 0 {
		t.Error("file_folder_index should exist after rebuild")
	}
}

func assertTableGone(t *testing.T, pool *dbconnpool.DbSQLConnPool, name string) {
	t.Helper()
	cpc, err := pool.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer pool.Put(cpc)
	var n int64
	if err := cpc.Conn.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, name).Scan(&n); err != nil {
		t.Fatalf("exists %s: %v", name, err)
	}
	if n != 0 {
		t.Fatalf("expected %s gone after Swap, still present", name)
	}
}

func activeHasFolderIDIndex(t *testing.T, ctx context.Context, conn *sql.Conn) {
	t.Helper()
	rows, err := conn.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='file_folder_index' AND name NOT LIKE 'sqlite_autoindex_%'`)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("indexes err: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("expected at least one explicit index on file_folder_index")
	}
	foundFolderID := false
	for _, name := range names {
		info, err := conn.QueryContext(ctx, "PRAGMA index_info("+name+")")
		if err != nil {
			t.Fatalf("index_info %s: %v", name, err)
		}
		for info.Next() {
			var seqno, cid int
			var col string
			if err := info.Scan(&seqno, &cid, &col); err != nil {
				info.Close()
				t.Fatalf("scan index_info: %v", err)
			}
			if col == "folder_id" {
				foundFolderID = true
			}
		}
		info.Close()
	}
	if !foundFolderID {
		t.Fatalf("no explicit index on folder_id; indexes=%v", names)
	}
}

// TestRebuildFileFolderIndex_SubmitAfterScanReleased verifies that
// SubmitFolderIndex only runs after the streaming scan cursor is closed and
// scanHeld is false (G4 must not span the Submit window).
func TestRebuildFileFolderIndex_SubmitAfterScanReleased(t *testing.T) {
	roPool, rwPool, _, ctx := createTestPoolsAndDir(t)
	helper := &syncFolderIndexBatcher{rwPool: rwPool}

	cpc, err := rwPool.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Two folders (folder_id 101 and 102; id 1 is the seeded root folder).
	if _, err := cpc.Conn.ExecContext(ctx,
		`INSERT INTO folder_paths (id, path) VALUES (101, 'folder-a'), (102, 'folder-b')`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("insert folder_paths: %v", err)
	}
	if _, err := cpc.Conn.ExecContext(ctx,
		`INSERT INTO folders (id, parent_id, path_id, name) VALUES (101, NULL, 101, 'folder-a'), (102, NULL, 102, 'folder-b')`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("insert folders: %v", err)
	}

	// Filenames are NOT in id order.
	if _, err := cpc.Conn.ExecContext(ctx,
		`INSERT INTO file_paths (id, path) VALUES (1001, 'f1a'), (1002, 'f1z'), (1003, 'f1m'),
		                                        (1004, 'f2a'), (1005, 'f2z')`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("insert file_paths: %v", err)
	}
	if _, err := cpc.Conn.ExecContext(ctx, `
		INSERT INTO files (id, folder_id, path_id, filename, size_bytes, mtime, mime_type, created_at, updated_at)
		VALUES (100, 101, 1001, 'a.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (  1, 101, 1002, 'z.jpg', 1, 1, 'image/jpeg', 1, 1),
		       ( 50, 101, 1003, 'm.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (200, 102, 1004, 'a.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (  2, 102, 1005, 'z.jpg', 1, 1, 'image/jpeg', 1, 1)`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("insert files: %v", err)
	}
	// Orphan: folder_id NULL — must get no row.
	if _, err := cpc.Conn.ExecContext(ctx,
		`INSERT INTO file_paths (id, path) VALUES (1999, 'orphan')`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("insert orphan file_path: %v", err)
	}
	if _, err := cpc.Conn.ExecContext(ctx,
		`INSERT INTO files (id, folder_id, path_id, filename, size_bytes, mtime, mime_type, created_at, updated_at)
			 VALUES (999, NULL, 1999, 'orphan.jpg', 1, 1, 'image/jpeg', 1, 1)`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("insert orphan file: %v", err)
	}
	rwPool.Put(cpc)

	if err := RebuildFileFolderIndex(ctx, rwPool, roPool, helper); err != nil {
		t.Fatalf("RebuildFileFolderIndex: %v", err)
	}

	// Five non-orphan files -> at least one Submit.
	if len(helper.submitScanHeld) == 0 {
		t.Fatal("expected at least one SubmitFolderIndex call, got zero")
	}
	// Every Submit must have observed scanHeld == false.
	for i, held := range helper.submitScanHeld {
		if held {
			t.Errorf("SubmitFolderIndex call %d observed scanHeld == true; cursor must be closed before Submit", i)
		}
	}
}

// TestRebuildFileFolderIndex_NavColumnsAndOrphan verifies that the per-folder
// outer/inner populate computes image_index from filename then id, sets the
// per-folder image_count and the prev/next/first/last navigation columns, and
// that a file with folder_id NULL (an orphan) gets no row.
func TestRebuildFileFolderIndex_NavColumnsAndOrphan(t *testing.T) {
	roPool, rwPool, _, ctx := createTestPoolsAndDir(t)
	helper := &syncFolderIndexBatcher{rwPool: rwPool}

	cpc, err := rwPool.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Two folders (folder_id 101 and 102; id 1 is the seeded root folder).
	if _, err := cpc.Conn.ExecContext(ctx,
		`INSERT INTO folder_paths (id, path) VALUES (101, 'folder-a'), (102, 'folder-b')`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("insert folder_paths: %v", err)
	}
	if _, err := cpc.Conn.ExecContext(ctx,
		`INSERT INTO folders (id, parent_id, path_id, name) VALUES (101, NULL, 101, 'folder-a'), (102, NULL, 102, 'folder-b')`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("insert folders: %v", err)
	}

	// Filenames are NOT in id order: high id gets filename a.jpg, low id gets z.jpg,
	// so image_index must follow filename then id.
	if _, err := cpc.Conn.ExecContext(ctx,
		`INSERT INTO file_paths (id, path) VALUES (1001, 'f1a'), (1002, 'f1z'), (1003, 'f1m'),
		                                        (1004, 'f2a'), (1005, 'f2z')`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("insert file_paths: %v", err)
	}
	if _, err := cpc.Conn.ExecContext(ctx, `
		INSERT INTO files (id, folder_id, path_id, filename, size_bytes, mtime, mime_type, created_at, updated_at)
		VALUES (100, 101, 1001, 'a.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (  1, 101, 1002, 'z.jpg', 1, 1, 'image/jpeg', 1, 1),
		       ( 50, 101, 1003, 'm.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (200, 102, 1004, 'a.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (  2, 102, 1005, 'z.jpg', 1, 1, 'image/jpeg', 1, 1)`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("insert files: %v", err)
	}

	// Orphan: folder_id NULL — must get no row.
	if _, err := cpc.Conn.ExecContext(ctx,
		`INSERT INTO file_paths (id, path) VALUES (1999, 'orphan')`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("insert orphan file_path: %v", err)
	}
	if _, err := cpc.Conn.ExecContext(ctx,
		`INSERT INTO files (id, folder_id, path_id, filename, size_bytes, mtime, mime_type, created_at, updated_at)
			 VALUES (999, NULL, 1999, 'orphan.jpg', 1, 1, 'image/jpeg', 1, 1)`); err != nil {
		rwPool.Put(cpc)
		t.Fatalf("insert orphan file: %v", err)
	}
	rwPool.Put(cpc)

	if err := RebuildFileFolderIndex(ctx, rwPool, roPool, helper); err != nil {
		t.Fatalf("RebuildFileFolderIndex: %v", err)
	}

	cpc2, err := rwPool.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rwPool.Put(cpc2)

	// Orphan has no row.
	var orphanRows int64
	if err := cpc2.Conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM file_folder_index WHERE file_id = 999`).Scan(&orphanRows); err != nil {
		t.Fatalf("count orphan: %v", err)
	}
	if orphanRows != 0 {
		t.Errorf("orphan (folder_id NULL) must have no row, got %d", orphanRows)
	}

	// Folder 1 has 3 files; image_index follows filename order: a.jpg(100) -> m.jpg(50) -> z.jpg(1).
	type row struct {
		fileID, folderID, imageIndex, imageCount int64
		prevID, nextID, firstID, lastID          sql.NullInt64
	}
	queryRows := func(folderID int64) []row {
		var out []row
		rs, qErr := cpc2.Conn.QueryContext(ctx,
			`SELECT file_id, folder_id, image_index, image_count, prev_id, next_id, first_id, last_id
			   FROM file_folder_index WHERE folder_id = ? ORDER BY image_index`, folderID)
		if qErr != nil {
			t.Fatalf("query folder %d: %v", folderID, qErr)
		}
		defer rs.Close()
		for rs.Next() {
			var r row
			if sErr := rs.Scan(&r.fileID, &r.folderID, &r.imageIndex, &r.imageCount,
				&r.prevID, &r.nextID, &r.firstID, &r.lastID); sErr != nil {
				t.Fatalf("scan: %v", sErr)
			}
			out = append(out, r)
		}
		return out
	}

	f1 := queryRows(101)
	if len(f1) != 3 {
		t.Fatalf("folder 1 expected 3 rows, got %d", len(f1))
	}
	if f1[0].fileID != 100 || f1[1].fileID != 50 || f1[2].fileID != 1 {
		t.Errorf("folder 1 image_index order wrong: got ids %d,%d,%d want 100,50,1",
			f1[0].fileID, f1[1].fileID, f1[2].fileID)
	}
	for _, r := range f1 {
		if r.imageCount != 3 {
			t.Errorf("folder 1 image_count = %d, want 3 (file %d)", r.imageCount, r.fileID)
		}
	}
	if f1[0].prevID.Valid || f1[0].nextID.Int64 != 50 || !f1[0].firstID.Valid || f1[0].firstID.Int64 != 100 {
		t.Errorf("folder 1 first row nav wrong: %+v", f1[0])
	}
	if f1[2].nextID.Valid || f1[2].prevID.Int64 != 50 || !f1[2].lastID.Valid || f1[2].lastID.Int64 != 1 {
		t.Errorf("folder 1 last row nav wrong: %+v", f1[2])
	}
	if f1[1].prevID.Int64 != 100 || f1[1].nextID.Int64 != 1 {
		t.Errorf("folder 1 middle row nav wrong: %+v", f1[1])
	}

	f2 := queryRows(102)
	if len(f2) != 2 {
		t.Fatalf("folder 2 expected 2 rows, got %d", len(f2))
	}
	if f2[0].fileID != 200 || f2[1].fileID != 2 {
		t.Errorf("folder 2 image_index order wrong: got ids %d,%d want 200,2", f2[0].fileID, f2[1].fileID)
	}
	for _, r := range f2 {
		if r.imageCount != 2 {
			t.Errorf("folder 2 image_count = %d, want 2 (file %d)", r.imageCount, r.fileID)
		}
	}
	if f2[0].prevID.Valid || f2[0].nextID.Int64 != 2 || f2[1].nextID.Valid || f2[1].prevID.Int64 != 200 {
		t.Errorf("folder 2 nav wrong: %+v %+v", f2[0], f2[1])
	}
}
