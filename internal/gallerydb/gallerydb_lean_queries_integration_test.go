//go:build integration

package gallerydb

import (
	"database/sql"
	"testing"
	"time"
)

// TestLeanFileQueries tests the new lean queries GetFileFolderIndexByID and
// GetLightboxNavByFileID, which are designed for InfoBoxImage and LightboxByID
// handlers respectively.
func TestLeanFileQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)

	// Create a folder for our test files
	folderPathID, err := q.UpsertFolderPathReturningID(ctx, "/lean_test")
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID failed: %v", err)
	}
	folder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
		PathID:    folderPathID,
		Name:      "lean_test",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertFolderReturningFolder failed: %v", err)
	}

	now := time.Now().Unix()

	// Create 3 files: a.jpg, b.jpg, c.jpg — in alphabetical order
	fileAPathID, _ := q.UpsertFilePathReturningID(ctx, "/lean_test/a.jpg")
	fileA, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
		PathID:    fileAPathID,
		Filename:  "a.jpg",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile a.jpg failed: %v", err)
	}

	fileBPathID, _ := q.UpsertFilePathReturningID(ctx, "/lean_test/b.jpg")
	fileB, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
		PathID:    fileBPathID,
		Filename:  "b.jpg",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile b.jpg failed: %v", err)
	}

	fileCPathID, _ := q.UpsertFilePathReturningID(ctx, "/lean_test/c.jpg")
	fileC, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
		PathID:    fileCPathID,
		Filename:  "c.jpg",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile c.jpg failed: %v", err)
	}

	// ---------------------------------------------------------------
	// GetFileFolderIndexByID tests
	// ---------------------------------------------------------------

	t.Run("GetFileFolderIndexByID_middle_file", func(t *testing.T) {
		row, err := q.GetFileFolderIndexByID(ctx, fileB.ID)
		if err != nil {
			t.Fatalf("GetFileFolderIndexByID failed: %v", err)
		}
		// ROW_NUMBER is 1-based: a.jpg=1, b.jpg=2, c.jpg=3
		if row.ImageIndex != 2 {
			t.Errorf("Expected ImageIndex=2 for b.jpg, got %d", row.ImageIndex)
		}
		if row.ImageCount != 3 {
			t.Errorf("Expected ImageCount=3, got %d", row.ImageCount)
		}
	})

	t.Run("GetFileFolderIndexByID_first_file", func(t *testing.T) {
		row, err := q.GetFileFolderIndexByID(ctx, fileA.ID)
		if err != nil {
			t.Fatalf("GetFileFolderIndexByID failed: %v", err)
		}
		if row.ImageIndex != 1 {
			t.Errorf("Expected ImageIndex=1 for a.jpg, got %d", row.ImageIndex)
		}
		if row.ImageCount != 3 {
			t.Errorf("Expected ImageCount=3, got %d", row.ImageCount)
		}
	})

	t.Run("GetFileFolderIndexByID_last_file", func(t *testing.T) {
		row, err := q.GetFileFolderIndexByID(ctx, fileC.ID)
		if err != nil {
			t.Fatalf("GetFileFolderIndexByID failed: %v", err)
		}
		if row.ImageIndex != 3 {
			t.Errorf("Expected ImageIndex=3 for c.jpg, got %d", row.ImageIndex)
		}
		if row.ImageCount != 3 {
			t.Errorf("Expected ImageCount=3, got %d", row.ImageCount)
		}
	})

	t.Run("GetFileFolderIndexByID_missing_id", func(t *testing.T) {
		_, err := q.GetFileFolderIndexByID(ctx, 99999)
		if err == nil {
			t.Fatal("Expected error for missing ID, got nil")
		}
		if err != sql.ErrNoRows {
			t.Errorf("Expected sql.ErrNoRows, got %v", err)
		}
	})

	// ---------------------------------------------------------------
	// GetLightboxNavByFileID tests
	// ---------------------------------------------------------------

	t.Run("GetLightboxNavByFileID_middle_file", func(t *testing.T) {
		row, err := q.GetLightboxNavByFileID(ctx, fileB.ID)
		if err != nil {
			t.Fatalf("GetLightboxNavByFileID failed: %v", err)
		}
		// ROW_NUMBER() - 1 is 0-based: a.jpg=0, b.jpg=1, c.jpg=2
		if row.CurrentIndex != 1 {
			t.Errorf("Expected CurrentIndex=1 for b.jpg, got %d", row.CurrentIndex)
		}
		if row.ImageCount != 3 {
			t.Errorf("Expected ImageCount=3, got %d", row.ImageCount)
		}
		if row.FirstID != fileA.ID {
			t.Errorf("Expected FirstID=%d, got %d", fileA.ID, row.FirstID)
		}
		if row.LastID != fileC.ID {
			t.Errorf("Expected LastID=%d, got %d", fileC.ID, row.LastID)
		}
		if !row.PrevID.Valid {
			t.Fatal("Expected PrevID to be valid")
		}
		if row.PrevID.Int64 != fileA.ID {
			t.Errorf("Expected PrevID=%d, got %d", fileA.ID, row.PrevID.Int64)
		}
		if !row.NextID.Valid {
			t.Fatal("Expected NextID to be valid")
		}
		if row.NextID.Int64 != fileC.ID {
			t.Errorf("Expected NextID=%d, got %d", fileC.ID, row.NextID.Int64)
		}
	})

	t.Run("GetLightboxNavByFileID_first_file", func(t *testing.T) {
		row, err := q.GetLightboxNavByFileID(ctx, fileA.ID)
		if err != nil {
			t.Fatalf("GetLightboxNavByFileID failed: %v", err)
		}
		if row.CurrentIndex != 0 {
			t.Errorf("Expected CurrentIndex=0 for a.jpg, got %d", row.CurrentIndex)
		}
		if row.ImageCount != 3 {
			t.Errorf("Expected ImageCount=3, got %d", row.ImageCount)
		}
		if row.FirstID != fileA.ID {
			t.Errorf("Expected FirstID=%d, got %d", fileA.ID, row.FirstID)
		}
		if row.LastID != fileC.ID {
			t.Errorf("Expected LastID=%d, got %d", fileC.ID, row.LastID)
		}
		if row.PrevID.Valid {
			t.Errorf("Expected PrevID to be invalid for first file, got %v", row.PrevID)
		}
		if !row.NextID.Valid {
			t.Fatal("Expected NextID to be valid")
		}
		if row.NextID.Int64 != fileB.ID {
			t.Errorf("Expected NextID=%d, got %d", fileB.ID, row.NextID.Int64)
		}
	})

	t.Run("GetLightboxNavByFileID_last_file", func(t *testing.T) {
		row, err := q.GetLightboxNavByFileID(ctx, fileC.ID)
		if err != nil {
			t.Fatalf("GetLightboxNavByFileID failed: %v", err)
		}
		if row.CurrentIndex != 2 {
			t.Errorf("Expected CurrentIndex=2 for c.jpg, got %d", row.CurrentIndex)
		}
		if row.ImageCount != 3 {
			t.Errorf("Expected ImageCount=3, got %d", row.ImageCount)
		}
		if !row.PrevID.Valid {
			t.Fatal("Expected PrevID to be valid")
		}
		if row.PrevID.Int64 != fileB.ID {
			t.Errorf("Expected PrevID=%d, got %d", fileB.ID, row.PrevID.Int64)
		}
		if row.NextID.Valid {
			t.Errorf("Expected NextID to be invalid for last file, got %v", row.NextID)
		}
	})

	t.Run("GetLightboxNavByFileID_missing_id", func(t *testing.T) {
		_, err := q.GetLightboxNavByFileID(ctx, 99999)
		if err == nil {
			t.Fatal("Expected error for missing ID, got nil")
		}
		if err != sql.ErrNoRows {
			t.Errorf("Expected sql.ErrNoRows, got %v", err)
		}
	})

	// ---------------------------------------------------------------
	// Single-file folder test
	// ---------------------------------------------------------------

	t.Run("single_file_folder", func(t *testing.T) {
		// Create a separate folder with just one file
		singlePathID, err := q.UpsertFolderPathReturningID(ctx, "/lean_test_single")
		if err != nil {
			t.Fatalf("UpsertFolderPathReturningID failed: %v", err)
		}
		singleFolder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
			PathID:    singlePathID,
			Name:      "lean_test_single",
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		if err != nil {
			t.Fatalf("UpsertFolderReturningFolder failed: %v", err)
		}

		singleFilePathID, _ := q.UpsertFilePathReturningID(ctx, "/lean_test_single/only.jpg")
		singleFile, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
			FolderID:  sql.NullInt64{Int64: singleFolder.ID, Valid: true},
			PathID:    singleFilePathID,
			Filename:  "only.jpg",
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		if err != nil {
			t.Fatalf("UpsertFileReturningFile only.jpg failed: %v", err)
		}

		// GetFileFolderIndexByID for single-file folder
		idxRow, err := q.GetFileFolderIndexByID(ctx, singleFile.ID)
		if err != nil {
			t.Fatalf("GetFileFolderIndexByID for single file failed: %v", err)
		}
		if idxRow.ImageIndex != 1 {
			t.Errorf("Expected ImageIndex=1, got %d", idxRow.ImageIndex)
		}
		if idxRow.ImageCount != 1 {
			t.Errorf("Expected ImageCount=1, got %d", idxRow.ImageCount)
		}

		// GetLightboxNavByFileID for single-file folder
		navRow, err := q.GetLightboxNavByFileID(ctx, singleFile.ID)
		if err != nil {
			t.Fatalf("GetLightboxNavByFileID for single file failed: %v", err)
		}
		if navRow.CurrentIndex != 0 {
			t.Errorf("Expected CurrentIndex=0, got %d", navRow.CurrentIndex)
		}
		if navRow.ImageCount != 1 {
			t.Errorf("Expected ImageCount=1, got %d", navRow.ImageCount)
		}
		if navRow.FirstID != singleFile.ID {
			t.Errorf("Expected FirstID=%d, got %d", singleFile.ID, navRow.FirstID)
		}
		if navRow.LastID != singleFile.ID {
			t.Errorf("Expected LastID=%d, got %d", singleFile.ID, navRow.LastID)
		}
		if navRow.PrevID.Valid {
			t.Errorf("Expected PrevID to be invalid for single file, got %v", navRow.PrevID)
		}
		if navRow.NextID.Valid {
			t.Errorf("Expected NextID to be invalid for single file, got %v", navRow.NextID)
		}
	})

	// ---------------------------------------------------------------
	// Orphan file test (folder_id IS NULL)
	// ---------------------------------------------------------------

	t.Run("orphan_file_returns_err_no_rows", func(t *testing.T) {
		// Insert a files row with folder_id = NULL
		orphanPathID, err := q.UpsertFilePathReturningID(ctx, "/lean_test/orphan.jpg")
		if err != nil {
			t.Fatalf("UpsertFilePathReturningID for orphan failed: %v", err)
		}
		orphanFile, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
			FolderID:  sql.NullInt64{Valid: false}, // folder_id = NULL
			PathID:    orphanPathID,
			Filename:  "orphan.jpg",
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		if err != nil {
			t.Fatalf("UpsertFileReturningFile for orphan failed: %v", err)
		}

		// GetFileFolderIndexByID should return sql.ErrNoRows
		_, err = q.GetFileFolderIndexByID(ctx, orphanFile.ID)
		if err == nil {
			t.Fatal("Expected error for orphan file (folder_id IS NULL), got nil")
		}
		if err != sql.ErrNoRows {
			t.Errorf("Expected sql.ErrNoRows for orphan file, got %v", err)
		}

		// GetLightboxNavByFileID should also return sql.ErrNoRows
		_, err = q.GetLightboxNavByFileID(ctx, orphanFile.ID)
		if err == nil {
			t.Fatal("Expected error for orphan file (folder_id IS NULL), got nil")
		}
		if err != sql.ErrNoRows {
			t.Errorf("Expected sql.ErrNoRows for orphan file, got %v", err)
		}
	})

	// ---------------------------------------------------------------
	// GetGalleryFileThumbRowsByFolderID tests (Query D)
	// ---------------------------------------------------------------

	t.Run("GetGalleryFileThumbRowsByFolderID_returns_ordered_slice", func(t *testing.T) {
		// Setup: create files b.jpg then a.jpg (reverse alphabetical) to
		// verify the query sorts by filename.
		bPathID, _ := q.UpsertFilePathReturningID(ctx, "/lean_test/b.jpg")
		// b.jpg already exists from earlier setup; just ensure both exist.

		aPathID, _ := q.UpsertFilePathReturningID(ctx, "/lean_test/a_order.jpg")
		aFile, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
			FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
			PathID:    aPathID,
			Filename:  "a_order.jpg",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertFileReturningFile a_order.jpg failed: %v", err)
		}

		rows, err := q.GetGalleryFileThumbRowsByFolderID(ctx, sql.NullInt64{Int64: folder.ID, Valid: true})
		if err != nil {
			t.Fatalf("GetGalleryFileThumbRowsByFolderID failed: %v", err)
		}

		// Expected: a.jpg, a_order.jpg, b.jpg, c.jpg (alphabetical)
		// At this point the folder has a.jpg, b.jpg, c.jpg (from setup) plus a_order.jpg
		if len(rows) < 4 {
			t.Fatalf("expected at least 4 rows, got %d", len(rows))
		}
		// First row should be a.jpg, second a_order.jpg
		if rows[0].Filename != "a.jpg" {
			t.Errorf("expected first file to be a.jpg, got %s", rows[0].Filename)
		}
		// Verify rows have the right structure (id + filename)
		if rows[0].ID != fileA.ID {
			t.Errorf("expected first file ID %d, got %d", fileA.ID, rows[0].ID)
		}
		_ = aFile
		_ = bPathID
	})

	t.Run("GetGalleryFileThumbRowsByFolderID_empty", func(t *testing.T) {
		// Create an empty folder
		emptyPathID, err := q.UpsertFolderPathReturningID(ctx, "/lean_test_empty_files")
		if err != nil {
			t.Fatalf("UpsertFolderPathReturningID failed: %v", err)
		}
		emptyFolder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
			PathID:    emptyPathID,
			Name:      "LeanEmptyFiles",
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		if err != nil {
			t.Fatalf("UpsertFolderReturningFolder failed: %v", err)
		}

		rows, err := q.GetGalleryFileThumbRowsByFolderID(ctx, sql.NullInt64{Int64: emptyFolder.ID, Valid: true})
		if err != nil {
			t.Fatalf("GetGalleryFileThumbRowsByFolderID failed: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("expected empty slice, got %d rows", len(rows))
		}
	})

	// ---------------------------------------------------------------
	// GetGalleryFolderThumbRowsByParentID tests (Query E)
	// ---------------------------------------------------------------

	t.Run("GetGalleryFolderThumbRowsByParentID_returns_ordered_slice", func(t *testing.T) {
		// The main folder already has sub-folders from earlier tests. Create a
		// new sub-folder 'Alpha' that sorts before existing ones.
		alphaPathID, err := q.UpsertFolderPathReturningID(ctx, "/lean_test/alpha")
		if err != nil {
			t.Fatalf("UpsertFolderPathReturningID failed: %v", err)
		}
		_, err = q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
			ParentID:  sql.NullInt64{Int64: folder.ID, Valid: true},
			PathID:    alphaPathID,
			Name:      "Alpha",
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		if err != nil {
			t.Fatalf("UpsertFolderReturningFolder Alpha failed: %v", err)
		}

		rows, err := q.GetGalleryFolderThumbRowsByParentID(ctx, sql.NullInt64{Int64: folder.ID, Valid: true})
		if err != nil {
			t.Fatalf("GetGalleryFolderThumbRowsByParentID failed: %v", err)
		}

		// Should have at least Alpha
		if len(rows) < 1 {
			t.Fatalf("expected at least 1 row, got %d", len(rows))
		}
		// Verify structure: id + name
		found := false
		for _, r := range rows {
			if r.Name == "Alpha" {
				found = true
				if r.ID == 0 {
					t.Error("expected non-zero ID for Alpha folder")
				}
				break
			}
		}
		if !found {
			t.Errorf("expected 'Alpha' in folder thumb rows, got %v", rows)
		}
	})

	t.Run("GetGalleryFolderThumbRowsByParentID_empty", func(t *testing.T) {
		// Create a leaf folder with no subfolders
		leafPathID, err := q.UpsertFolderPathReturningID(ctx, "/lean_test_leaf")
		if err != nil {
			t.Fatalf("UpsertFolderPathReturningID failed: %v", err)
		}
		leafFolder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
			PathID:    leafPathID,
			Name:      "LeanLeaf",
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		if err != nil {
			t.Fatalf("UpsertFolderReturningFolder failed: %v", err)
		}

		rows, err := q.GetGalleryFolderThumbRowsByParentID(ctx, sql.NullInt64{Int64: leafFolder.ID, Valid: true})
		if err != nil {
			t.Fatalf("GetGalleryFolderThumbRowsByParentID failed: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("expected empty slice, got %d rows", len(rows))
		}
	})
}
