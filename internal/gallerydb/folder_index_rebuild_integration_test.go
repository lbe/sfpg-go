//go:build integration

package gallerydb

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
)

// TestCountFilesForFolderIndexRebuild_MatchesStreamExcludesOrphan verifies the
// COUNT query returns the same count as the stream and excludes orphans.
func TestCountFilesForFolderIndexRebuild_MatchesStreamExcludesOrphan(t *testing.T) {
	db, prepared, ctx := setupTestDB(t)

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer conn.Close()

	// Same fixture as TestQueryFilesForFolderIndexRebuild_StreamsInFolderFilenameOrder.
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO folder_paths (id, path) VALUES (101, 'folder-a'), (102, 'folder-b')`); err != nil {
		t.Fatalf("insert folder_paths: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO folders (id, parent_id, path_id, name) VALUES (101, NULL, 101, 'folder-a'), (102, NULL, 102, 'folder-b')`); err != nil {
		t.Fatalf("insert folders: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO file_paths (id, path) VALUES (1001, 'f1a'), (1002, 'f1z'), (1003, 'f1m'),
		                                        (1004, 'f2a'), (1005, 'f2z')`); err != nil {
		t.Fatalf("insert file_paths: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO files (id, folder_id, path_id, filename, size_bytes, mtime, mime_type, created_at, updated_at)
		VALUES (100, 101, 1001, 'a.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (  1, 101, 1002, 'z.jpg', 1, 1, 'image/jpeg', 1, 1),
		       ( 50, 101, 1003, 'm.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (200, 102, 1004, 'a.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (  2, 102, 1005, 'z.jpg', 1, 1, 'image/jpeg', 1, 1)`); err != nil {
		t.Fatalf("insert files: %v", err)
	}
	// Orphan: folder_id NULL — must not be counted.
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO file_paths (id, path) VALUES (1999, 'orphan')`); err != nil {
		t.Fatalf("insert orphan file_path: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO files (id, folder_id, path_id, filename, size_bytes, mtime, mime_type, created_at, updated_at)
			 VALUES (999, NULL, 1999, 'orphan.jpg', 1, 1, 'image/jpeg', 1, 1)`); err != nil {
		t.Fatalf("insert orphan file: %v", err)
	}

	// COUNT must match the stream.
	count, err := prepared.CountFilesForFolderIndexRebuild(ctx)
	if err != nil {
		t.Fatalf("CountFilesForFolderIndexRebuild: %v", err)
	}

	// Stream to verify.
	rows, err := prepared.QueryFilesForFolderIndexRebuild(ctx)
	if err != nil {
		t.Fatalf("QueryFilesForFolderIndexRebuild: %v", err)
	}
	var streamCount int64
	for rows.Next() {
		streamCount++
		var id, folderID int64
		if err := rows.Scan(&id, &folderID); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if count != streamCount {
		t.Errorf("count %d != stream %d", count, streamCount)
	}
	if count != 5 {
		t.Errorf("expected 5 folder-bearing files, got %d", count)
	}
}

// TestCountFilesForFolderIndexRebuildSQL_IsParts2 verifies the COUNT SQL is
// embedded as parts[2] and the SELECT/INSERT indices are unchanged.
func TestCountFilesForFolderIndexRebuildSQL_IsParts2(t *testing.T) {
	content, err := os.ReadFile("../../sqlc/queries/embed.go")
	if err != nil {
		t.Fatalf("read embed.go: %v", err)
	}
	text := string(content)

	if !strings.Contains(text, "CountFilesForFolderIndexRebuildSQL") {
		t.Fatal("CountFilesForFolderIndexRebuildSQL function missing in embed.go")
	}
	if !strings.Contains(text, "parts[2]") {
		t.Fatal("CountFilesForFolderIndexRebuildSQL must use parts[2]")
	}

	// SELECT/INSERT indices must be unchanged.
	if !strings.Contains(text, "strings.Split(fileFolderIndexRebuildSQL, \"-- statement-break\")[0]") {
		t.Error("QueryFilesForFolderIndexRebuildSQL must remain parts[0]")
	}
	if !strings.Contains(text, "parts[1]") {
		t.Error("InsertFileFolderIndexNewSQL must remain parts[1]")
	}
}

// TestInsertFileFolderIndexNewRows_RequiresTx verifies G2/G3: InsertFileFolderIndexNewRows
// must be called on a WithTx-bound CustomQueries. Called without WithTx it returns an
// error and must not INSERT.
func TestInsertFileFolderIndexNewRows_RequiresTx(t *testing.T) {
	_, prepared, ctx := setupTestDB(t)

	// NewCustomQueries without WithTx must reject the call (no tx binding).
	unprepared := NewCustomQueries(prepared.db)
	if err := unprepared.InsertFileFolderIndexNewRows(ctx, []InsertFileFolderIndexNewParams{
		{FileID: 1, FolderID: 1, ImageIndex: 1, ImageCount: 1, FirstID: 1, LastID: 1},
	}); err == nil {
		t.Fatal("expected error calling InsertFileFolderIndexNewRows without WithTx, got nil")
	}
}

// TestInsertFileFolderIndexNewRows_OnePrepareManyExec verifies G6: the flush executes
// exactly ONE prepared INSERT on the transaction, then Exec's it once per row (here 3).
// It overrides prepareContextFn to count prepares and the new stmtExecContextFn to count
// Execs, so a per-row Prepare or raw tx.ExecContext regression fails the test.
func TestInsertFileFolderIndexNewRows_OnePrepareManyExec(t *testing.T) {
	db, prepared, ctx := setupTestDB(t)

	// Create an empty file_folder_index_new mirroring the active table's schema so
	// the INSERT target exists (CloneEmpty lives in tableswap, which imports dbconnpool
	// -> gallerydb, so we cannot call it here without an import cycle).
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE file_folder_index_new (
			file_id INTEGER NOT NULL,
			folder_id INTEGER NOT NULL,
			image_index INTEGER NOT NULL,
			image_count INTEGER NOT NULL,
			prev_id INTEGER,
			next_id INTEGER,
			first_id INTEGER NOT NULL,
			last_id INTEGER NOT NULL,
			PRIMARY KEY (file_id, folder_id))`); err != nil {
		t.Fatalf("create dest: %v", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	prepareCount := 0
	execCount := 0
	origPrepare := prepareContextFn
	origExec := stmtExecContextFn
	prepareContextFn = func(c context.Context, d DBTX, query string) (*sql.Stmt, error) {
		prepareCount++
		return origPrepare(c, d, query)
	}
	stmtExecContextFn = func(c context.Context, s *sql.Stmt, args ...interface{}) (sql.Result, error) {
		execCount++
		return origExec(c, s, args...)
	}
	t.Cleanup(func() {
		prepareContextFn = origPrepare
		stmtExecContextFn = origExec
	})

	qtx := prepared.WithTx(tx)
	rows := []InsertFileFolderIndexNewParams{
		{FileID: 10, FolderID: 2, ImageIndex: 1, ImageCount: 3, NextID: sql.NullInt64{Int64: 11, Valid: true}, FirstID: 10, LastID: 12},
		{FileID: 11, FolderID: 2, ImageIndex: 2, ImageCount: 3, PrevID: sql.NullInt64{Int64: 10, Valid: true}, NextID: sql.NullInt64{Int64: 12, Valid: true}, FirstID: 10, LastID: 12},
		{FileID: 12, FolderID: 2, ImageIndex: 3, ImageCount: 3, PrevID: sql.NullInt64{Int64: 11, Valid: true}, FirstID: 10, LastID: 12},
	}
	if err := qtx.InsertFileFolderIndexNewRows(ctx, rows); err != nil {
		t.Fatalf("InsertFileFolderIndexNewRows: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if prepareCount != 1 {
		t.Errorf("expected exactly 1 Prepare (G6), got %d", prepareCount)
	}
	if execCount != 3 {
		t.Errorf("expected exactly 3 Exec (G6, one per row), got %d", execCount)
	}

	// All three rows present in the dest.
	verifyConn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn verify: %v", err)
	}
	defer verifyConn.Close()
	var n int
	if err := verifyConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM file_folder_index_new").Scan(&n); err != nil {
		t.Fatalf("count dest: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 rows in file_folder_index_new, got %d", n)
	}
}

// TestQueryFilesForFolderIndexRebuild_StreamsInFolderFilenameOrder verifies G1 streaming
// SELECT: rows are returned in folder_id, filename, id order, and orphan files (folder_id
// IS NULL) are skipped.
func TestQueryFilesForFolderIndexRebuild_StreamsInFolderFilenameOrder(t *testing.T) {
	db, prepared, ctx := setupTestDB(t)

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer conn.Close()

	// Two folders.
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO folder_paths (id, path) VALUES (101, 'folder-a'), (102, 'folder-b')`); err != nil {
		t.Fatalf("insert folder_paths: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO folders (id, parent_id, path_id, name) VALUES (101, NULL, 101, 'folder-a'), (102, NULL, 102, 'folder-b')`); err != nil {
		t.Fatalf("insert folders: %v", err)
	}

	// Filenames are NOT in id order within each folder.
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO file_paths (id, path) VALUES (1001, 'f1a'), (1002, 'f1z'), (1003, 'f1m'),
		                                        (1004, 'f2a'), (1005, 'f2z')`); err != nil {
		t.Fatalf("insert file_paths: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO files (id, folder_id, path_id, filename, size_bytes, mtime, mime_type, created_at, updated_at)
		VALUES (100, 101, 1001, 'a.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (  1, 101, 1002, 'z.jpg', 1, 1, 'image/jpeg', 1, 1),
		       ( 50, 101, 1003, 'm.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (200, 102, 1004, 'a.jpg', 1, 1, 'image/jpeg', 1, 1),
		       (  2, 102, 1005, 'z.jpg', 1, 1, 'image/jpeg', 1, 1)`); err != nil {
		t.Fatalf("insert files: %v", err)
	}
	// Orphan: folder_id NULL — must be skipped by the stream.
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO file_paths (id, path) VALUES (1999, 'orphan')`); err != nil {
		t.Fatalf("insert orphan file_path: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO files (id, folder_id, path_id, filename, size_bytes, mtime, mime_type, created_at, updated_at)
			 VALUES (999, NULL, 1999, 'orphan.jpg', 1, 1, 'image/jpeg', 1, 1)`); err != nil {
		t.Fatalf("insert orphan file: %v", err)
	}

	rows, err := prepared.QueryFilesForFolderIndexRebuild(ctx)
	if err != nil {
		t.Fatalf("QueryFilesForFolderIndexRebuild: %v", err)
	}
	defer rows.Close()

	type fr struct {
		id       int64
		folderID int64
	}
	var got []fr
	for rows.Next() {
		var id, folderID int64
		if err := rows.Scan(&id, &folderID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, fr{id: id, folderID: folderID})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if len(got) != 5 {
		t.Fatalf("expected 5 streamed rows (orphan skipped), got %d: %+v", len(got), got)
	}
	// Order must be folder 101 then folder 102, each by filename (filenames are
	// NOT in id order: a.jpg(100), m.jpg(50), z.jpg(1) for folder 101; a.jpg(200),
	// z.jpg(2) for folder 102). The SELECT only returns id, folder_id, so we assert
	// the resulting id sequence matches the filename-ordered expectation.
	want := []fr{
		{id: 100, folderID: 101},
		{id: 50, folderID: 101},
		{id: 1, folderID: 101},
		{id: 200, folderID: 102},
		{id: 2, folderID: 102},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}
