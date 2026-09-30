//go:build integration

package gallerydb

import (
	"context"
	"database/sql"
	"path"
	"testing"
	"time"
)

func ensureRootFolderID(t *testing.T, q *CustomQueries, ctx context.Context) int64 {
	t.Helper()
	id, err := q.GetFolderIDByPath(ctx, "")
	if err == nil {
		return id
	}
	pathID, err := q.UpsertFolderPathReturningID(ctx, "")
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID root: %v", err)
	}
	now := time.Now().Unix()
	folder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
		PathID:    pathID,
		Name:      "",
		Mtime:     sql.NullInt64{Int64: now, Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertFolderReturningFolder root: %v", err)
	}
	return folder.ID
}

func ensureFolderAtGalleryPath(t *testing.T, q *CustomQueries, ctx context.Context, galleryPath string, parentID int64) int64 {
	t.Helper()
	id, err := q.GetFolderIDByPath(ctx, galleryPath)
	if err == nil {
		return id
	}
	pathID, err := q.UpsertFolderPathReturningID(ctx, galleryPath)
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID %q: %v", galleryPath, err)
	}
	now := time.Now().Unix()
	name := path.Base(galleryPath)
	folder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: parentID, Valid: true},
		PathID:    pathID,
		Name:      name,
		Mtime:     sql.NullInt64{Int64: now, Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertFolderReturningFolder %q: %v", galleryPath, err)
	}
	return folder.ID
}

func TestListDiscoveryFilesByFolderID_empty(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	rootID := ensureRootFolderID(t, q, ctx)
	rows, err := q.ListDiscoveryFilesByFolderID(ctx, sql.NullInt64{Int64: rootID, Valid: true})
	if err != nil {
		t.Fatalf("ListDiscoveryFilesByFolderID: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows, got %d", len(rows))
	}
}

func TestListDiscoveryCatalogByFolder_queries(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	rootID := ensureRootFolderID(t, q, ctx)
	now := time.Now().Unix()

	filePath := "root-file.jpg"
	invalidPath := "root-invalid.dat"

	filePathID, err := q.UpsertFilePathReturningID(ctx, filePath)
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID file: %v", err)
	}
	file, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: rootID, Valid: true},
		PathID:    filePathID,
		Filename:  "root-file.jpg",
		Mtime:     sql.NullInt64{Int64: now, Valid: true},
		SizeBytes: sql.NullInt64{Int64: 100, Valid: true},
		Md5:       sql.NullString{String: "root-md5", Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile: %v", err)
	}
	if err := q.UpsertInvalidFile(ctx, UpsertInvalidFileParams{
		Path:     invalidPath,
		Mtime:    now,
		Size:     42,
		Reason:   sql.NullString{String: "non-image", Valid: true},
		FolderID: rootID,
	}); err != nil {
		t.Fatalf("UpsertInvalidFile: %v", err)
	}

	inv, err := q.GetInvalidFileByPath(ctx, invalidPath)
	if err != nil {
		t.Fatalf("GetInvalidFileByPath: %v", err)
	}
	if inv.FolderID != rootID {
		t.Fatalf("invalid_files.folder_id = %d, want %d", inv.FolderID, rootID)
	}

	fileRows, err := q.ListDiscoveryFilesByFolderID(ctx, sql.NullInt64{Int64: rootID, Valid: true})
	if err != nil {
		t.Fatalf("ListDiscoveryFilesByFolderID: %v", err)
	}
	if len(fileRows) != 1 {
		t.Fatalf("expected 1 file row at root, got %d", len(fileRows))
	}
	if fileRows[0].FileID != file.ID || fileRows[0].FileFilename != "root-file.jpg" {
		t.Fatalf("file row: %+v", fileRows[0])
	}

	invalidRows, err := q.ListDiscoveryInvalidByFolderID(ctx, rootID)
	if err != nil {
		t.Fatalf("ListDiscoveryInvalidByFolderID: %v", err)
	}
	if len(invalidRows) != 1 {
		t.Fatalf("expected 1 invalid row at root, got %d", len(invalidRows))
	}
	if invalidRows[0].InvalidPath != invalidPath {
		t.Fatalf("invalid path: %q", invalidRows[0].InvalidPath)
	}

	// Nested folder: only rows in that folder_id.
	parentID := ensureFolderAtGalleryPath(t, q, ctx, "parent", rootID)
	childPath := "parent/child.jpg"
	childPathID, err := q.UpsertFilePathReturningID(ctx, childPath)
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID child: %v", err)
	}
	if _, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: parentID, Valid: true},
		PathID:    childPathID,
		Filename:  "child.jpg",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertFileReturningFile child: %v", err)
	}
	if _, err := q.UpsertFilePathReturningID(ctx, "parent/deeper/nested.jpg"); err != nil {
		t.Fatalf("UpsertFilePathReturningID nested: %v", err)
	}

	parentFiles, err := q.ListDiscoveryFilesByFolderID(ctx, sql.NullInt64{Int64: parentID, Valid: true})
	if err != nil {
		t.Fatalf("ListDiscoveryFilesByFolderID parent: %v", err)
	}
	if len(parentFiles) != 1 || parentFiles[0].FileFilename != "child.jpg" {
		t.Fatalf("parent folder files: %+v", parentFiles)
	}
}

func TestListDiscoveryInvalidByFolderID_albumOnly(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	rootID := ensureRootFolderID(t, q, ctx)
	albumID := ensureFolderAtGalleryPath(t, q, ctx, "album", rootID)
	now := time.Now().Unix()

	if err := q.UpsertInvalidFile(ctx, UpsertInvalidFileParams{
		Path:     "album/bad.raw",
		Mtime:    now,
		Size:     1,
		Reason:   sql.NullString{String: "decode", Valid: true},
		FolderID: albumID,
	}); err != nil {
		t.Fatalf("UpsertInvalidFile: %v", err)
	}

	rows, err := q.ListDiscoveryInvalidByFolderID(ctx, albumID)
	if err != nil {
		t.Fatalf("ListDiscoveryInvalidByFolderID: %v", err)
	}
	if len(rows) != 1 || rows[0].InvalidPath != "album/bad.raw" {
		t.Fatalf("rows: %+v", rows)
	}

	rootInvalid, err := q.ListDiscoveryInvalidByFolderID(ctx, rootID)
	if err != nil {
		t.Fatalf("ListDiscoveryInvalidByFolderID root: %v", err)
	}
	if len(rootInvalid) != 0 {
		t.Fatalf("expected no invalid rows at root, got %d", len(rootInvalid))
	}
}

func TestUpsertInvalidFile_updates_folder_id_on_conflict(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	rootID := ensureRootFolderID(t, q, ctx)
	albumID := ensureFolderAtGalleryPath(t, q, ctx, "album", rootID)
	now := time.Now().Unix()
	path := "album/photo.jpg"

	if err := q.UpsertInvalidFile(ctx, UpsertInvalidFileParams{
		Path: path, Mtime: now, Size: 10, FolderID: albumID,
	}); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	if err := q.UpsertInvalidFile(ctx, UpsertInvalidFileParams{
		Path: path, Mtime: now + 1, Size: 20, FolderID: rootID,
	}); err != nil {
		t.Fatalf("conflict upsert: %v", err)
	}
	inv, err := q.GetInvalidFileByPath(ctx, path)
	if err != nil {
		t.Fatalf("GetInvalidFileByPath: %v", err)
	}
	if inv.FolderID != rootID {
		t.Fatalf("folder_id after ON CONFLICT = %d, want %d", inv.FolderID, rootID)
	}
}
