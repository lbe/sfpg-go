//go:build integration

package gallerydb

import (
	"database/sql"
	"testing"
	"time"
)

func TestFolderInfoCounts(t *testing.T) {
	_, q, ctx := setupTestDB(t)

	// Create a parent folder
	parentPathID, err := q.UpsertFolderPathReturningID(ctx, "/folder_info_test")
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID failed: %v", err)
	}
	parent, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
		PathID:    parentPathID,
		Name:      "folder_info_test",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertFolderReturningFolder failed: %v", err)
	}

	// Create 2 child subfolders
	for _, name := range []string{"child_a", "child_b"} {
		childPathID, err := q.UpsertFolderPathReturningID(ctx, "/folder_info_test/"+name)
		if err != nil {
			t.Fatalf("UpsertFolderPathReturningID for %s failed: %v", name, err)
		}
		_, err = q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
			ParentID:  sql.NullInt64{Int64: parent.ID, Valid: true},
			PathID:    childPathID,
			Name:      name,
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		if err != nil {
			t.Fatalf("UpsertFolderReturningFolder for %s failed: %v", name, err)
		}
	}

	// Create files: 2 images (a.jpg, b.png), 1 non-image (notes.txt)
	now := time.Now().Unix()
	testFiles := []struct {
		path     string
		filename string
	}{
		{"/folder_info_test/a.jpg", "a.jpg"},
		{"/folder_info_test/b.png", "b.png"},
		{"/folder_info_test/notes.txt", "notes.txt"},
	}
	for _, tf := range testFiles {
		fpID, err := q.UpsertFilePathReturningID(ctx, tf.path)
		if err != nil {
			t.Fatalf("UpsertFilePathReturningID for %s failed: %v", tf.filename, err)
		}
		_, err = q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
			FolderID:  sql.NullInt64{Int64: parent.ID, Valid: true},
			PathID:    fpID,
			Filename:  tf.filename,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertFileReturningFile for %s failed: %v", tf.filename, err)
		}
	}

	// Test: Folder + 2 children + a.jpg, b.png, notes.txt → Dir=2, Image=2, File=1
	t.Run("mixed_counts", func(t *testing.T) {
		row, err := q.GetFolderInfoCountsByID(ctx, parent.ID)
		if err != nil {
			t.Fatalf("GetFolderInfoCountsByID failed: %v", err)
		}
		if row.DirCount != 2 {
			t.Errorf("expected DirCount=2, got %d", row.DirCount)
		}
		if row.ImageCount != 2 {
			t.Errorf("expected ImageCount=2, got %d", row.ImageCount)
		}
		if row.FileCount != 1 {
			t.Errorf("expected FileCount=1, got %d", row.FileCount)
		}
	})

	// Test: Photo.JPG → image (case insensitive)
	t.Run("case_insensitive_image", func(t *testing.T) {
		// Create a second folder for this test
		f2PathID, err := q.UpsertFolderPathReturningID(ctx, "/folder_info_case_test")
		if err != nil {
			t.Fatalf("UpsertFolderPathReturningID failed: %v", err)
		}
		f2, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
			PathID:    f2PathID,
			Name:      "folder_info_case_test",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertFolderReturningFolder failed: %v", err)
		}

		// Add Photo.JPG (uppercase extension)
		fpID, err := q.UpsertFilePathReturningID(ctx, "/folder_info_case_test/Photo.JPG")
		if err != nil {
			t.Fatalf("UpsertFilePathReturningID failed: %v", err)
		}
		_, err = q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
			FolderID:  sql.NullInt64{Int64: f2.ID, Valid: true},
			PathID:    fpID,
			Filename:  "Photo.JPG",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertFileReturningFile failed: %v", err)
		}

		row, err := q.GetFolderInfoCountsByID(ctx, f2.ID)
		if err != nil {
			t.Fatalf("GetFolderInfoCountsByID failed: %v", err)
		}
		if row.DirCount != 0 {
			t.Errorf("expected DirCount=0, got %d", row.DirCount)
		}
		if row.ImageCount != 1 {
			t.Errorf("expected ImageCount=1 for Photo.JPG, got %d", row.ImageCount)
		}
		if row.FileCount != 0 {
			t.Errorf("expected FileCount=0, got %d", row.FileCount)
		}
	})

	// Test: Empty folder → zeros
	t.Run("empty_folder", func(t *testing.T) {
		emptyPathID, err := q.UpsertFolderPathReturningID(ctx, "/folder_info_empty_test")
		if err != nil {
			t.Fatalf("UpsertFolderPathReturningID failed: %v", err)
		}
		emptyFolder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
			PathID:    emptyPathID,
			Name:      "empty_folder",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertFolderReturningFolder failed: %v", err)
		}

		row, err := q.GetFolderInfoCountsByID(ctx, emptyFolder.ID)
		if err != nil {
			t.Fatalf("GetFolderInfoCountsByID failed: %v", err)
		}
		if row.DirCount != 0 {
			t.Errorf("expected DirCount=0 for empty folder, got %d", row.DirCount)
		}
		if row.ImageCount != 0 {
			t.Errorf("expected ImageCount=0 for empty folder, got %d", row.ImageCount)
		}
		if row.FileCount != 0 {
			t.Errorf("expected FileCount=0 for empty folder, got %d", row.FileCount)
		}
	})

	// Test: Missing id → sql.ErrNoRows
	t.Run("missing_folder", func(t *testing.T) {
		_, err := q.GetFolderInfoCountsByID(ctx, 99999)
		if err == nil {
			t.Fatal("expected error for missing folder, got nil")
		}
		if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %v", err)
		}
	})

	// Test: foo.jpg.bak → FileCount (not image) — suffix/filepath.Ext semantics
	t.Run("bak_suffix_not_image", func(t *testing.T) {
		bakPathID, err := q.UpsertFolderPathReturningID(ctx, "/folder_info_bak_test")
		if err != nil {
			t.Fatalf("UpsertFolderPathReturningID failed: %v", err)
		}
		bakFolder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
			PathID:    bakPathID,
			Name:      "bak_test",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertFolderReturningFolder failed: %v", err)
		}

		fpID, err := q.UpsertFilePathReturningID(ctx, "/folder_info_bak_test/foo.jpg.bak")
		if err != nil {
			t.Fatalf("UpsertFilePathReturningID failed: %v", err)
		}
		_, err = q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
			FolderID:  sql.NullInt64{Int64: bakFolder.ID, Valid: true},
			PathID:    fpID,
			Filename:  "foo.jpg.bak",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertFileReturningFile failed: %v", err)
		}

		row, err := q.GetFolderInfoCountsByID(ctx, bakFolder.ID)
		if err != nil {
			t.Fatalf("GetFolderInfoCountsByID failed: %v", err)
		}
		if row.DirCount != 0 {
			t.Errorf("expected DirCount=0, got %d", row.DirCount)
		}
		if row.ImageCount != 0 {
			t.Errorf("expected ImageCount=0 for .bak file, got %d", row.ImageCount)
		}
		if row.FileCount != 1 {
			t.Errorf("expected FileCount=1 for foo.jpg.bak, got %d", row.FileCount)
		}
	})
}
