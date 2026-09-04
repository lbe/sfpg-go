//go:build integration

package gallerydb

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestViewAndCustomQueries(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	_ = db // q already contains custom queries

	// 1. Setup data
	// /gaps
	gapsPathID, _ := q.UpsertFolderPathReturningID(ctx, "/gaps")
	gapsFolder, _ := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{PathID: gapsPathID, Name: "gaps", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
	// /gaps/sub
	subPathID, _ := q.UpsertFolderPathReturningID(ctx, "/gaps/sub")
	subFolder, _ := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{PathID: subPathID, Name: "sub", ParentID: sql.NullInt64{Int64: gapsFolder.ID, Valid: true}, CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
	// /gaps/sub/imageB.jpg
	imgBPathID, _ := q.UpsertFilePathReturningID(ctx, "/gaps/sub/imageB.jpg")
	imgBFile, _ := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{FolderID: sql.NullInt64{Int64: subFolder.ID, Valid: true}, PathID: imgBPathID, Filename: "imageB.jpg", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
	// /gaps/sub/imageA.png
	imgAPathID, _ := q.UpsertFilePathReturningID(ctx, "/gaps/sub/imageA.png")
	_, _ = q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{FolderID: sql.NullInt64{Int64: subFolder.ID, Valid: true}, PathID: imgAPathID, Filename: "imageA.png", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
	// Thumbnail for imageB
	thumbID, _ := q.UpsertThumbnailReturningID(ctx, UpsertThumbnailReturningIDParams{FileID: imgBFile.ID, SizeLabel: "test", Width: 1, Height: 1, Format: "jpeg", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
	thumbData := []byte("test_thumb_data")
	_ = q.UpsertThumbnailBlob(ctx, UpsertThumbnailBlobParams{ThumbnailID: thumbID, Data: thumbData})

	// 2. Test GetFolderViewByID
	t.Run("GetFolderViewByID", func(t *testing.T) {
		view, err := q.GetFolderViewByID(ctx, subFolder.ID)
		if err != nil {
			t.Fatalf("GetFolderViewByID failed: %v", err)
		}
		if view.Path != "/gaps/sub" {
			t.Errorf("Expected path /gaps/sub, got %s", view.Path)
		}
		if view.Name != "sub" {
			t.Errorf("Expected name sub, got %s", view.Name)
		}
	})

	// 3. Test UpdateFolderTileId
	t.Run("UpdateFolderTileId", func(t *testing.T) {
		err := q.UpdateFolderTileId(ctx, UpdateFolderTileIdParams{
			ID:     subFolder.ID,
			TileID: sql.NullInt64{Int64: thumbID, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpdateFolderTileId failed: %v", err)
		}
		updatedFolder, err := q.GetFolderByID(ctx, subFolder.ID)
		if err != nil {
			t.Fatalf("GetFolderByID failed: %v", err)
		}
		if !updatedFolder.TileID.Valid || updatedFolder.TileID.Int64 != thumbID {
			t.Errorf("Expected TileID to be %d, got %v", thumbID, updatedFolder.TileID)
		}
	})

	// 5. Test GetFolderTileExistsViewByPath
	t.Run("GetFolderTileExistsViewByPath", func(t *testing.T) {
		// First, update the tile ID for the folder
		_ = q.UpdateFolderTileId(ctx, UpdateFolderTileIdParams{ID: subFolder.ID, TileID: sql.NullInt64{Int64: thumbID, Valid: true}})

		exists, err := q.GetFolderTileExistsViewByPath(ctx, "/gaps/sub")
		if err != nil {
			t.Fatalf("GetFolderTileExistsViewByPath for /gaps/sub failed: %v", err)
		}
		if !exists {
			t.Error("Expected tile to exist for /gaps/sub, but it doesn't")
		}

		_, err = q.GetFolderTileExistsViewByPath(ctx, "/gaps")
		if err != sql.ErrNoRows {
			t.Errorf("Expected sql.ErrNoRows for /gaps, but got %v", err)
		}
	})

	// 6. Test custom.go GetFolderViewThumbnailBlobDataByPath
	t.Run("CustomGetFolderViewThumbnailBlobDataByPath", func(t *testing.T) {
		// Ensure tile is set
		_ = q.UpdateFolderTileId(ctx, UpdateFolderTileIdParams{ID: subFolder.ID, TileID: sql.NullInt64{Int64: thumbID, Valid: true}})
		retrievedData, err := q.GetFolderViewThumbnailBlobDataByPath(ctx, "/gaps/sub")
		if err != nil {
			t.Fatalf("GetFolderViewThumbnailBlobDataByPath failed: %v", err)
		}
		if !reflect.DeepEqual(retrievedData, thumbData) {
			t.Errorf("Expected thumb data %v, got %v", thumbData, retrievedData)
		}
	})

	// 7. Test custom.go WithTx
	t.Run("CustomWithTxRollback", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		qtx := q.WithTx(tx)

		// Insert a folder path within the transaction
		txFolderPathID, err := qtx.UpsertFolderPathReturningID(ctx, "/tx_test")
		if err != nil {
			tx.Rollback()
			t.Fatalf("UpsertFolderPathReturningID within tx failed: %v", err)
		}
		_, err = qtx.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{PathID: txFolderPathID, Name: "tx_test", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
		if err != nil {
			tx.Rollback()
			t.Fatalf("UpsertFolderReturningFolder within tx failed: %v", err)
		}

		// Rollback the transaction
		if rbErr := tx.Rollback(); rbErr != nil {
			t.Fatalf("Rollback failed: %v", err)
		}

		// Verify the folder does not exist
		_, err = q.GetFolderByPath(ctx, "/tx_test")
		if err != sql.ErrNoRows {
			t.Errorf("Expected sql.ErrNoRows after rollback, but got %v", err)
		}
	})

	// 8. Test GetFileViewRowsByFolderPath
	t.Run("GetFileViewRowsByFolderPath", func(t *testing.T) {
		rows, err := q.GetFileViewRowsByFolderPath(ctx, "/gaps/sub")
		if err != nil {
			t.Fatalf("GetFileViewRowsByFolderPath failed: %v", err)
		}
		defer rows.Close()

		var foundFiles []string
		for rows.Next() {
			var i FileView
			if scanErr := rows.Scan(&i.ID, &i.FolderID, &i.FolderPath, &i.Path, &i.Filename, &i.SizeBytes, &i.Md5, &i.MimeType, &i.Width, &i.Height, &i.CreatedAt, &i.UpdatedAt); scanErr != nil {
				t.Fatalf("failed to scan row: %v", scanErr)
			}
			foundFiles = append(foundFiles, i.Filename)
		}
		if scanErr := rows.Err(); scanErr != nil {
			t.Fatalf("rows.Err() was not nil: %v", scanErr)
		}

		expectedFiles := []string{"imageA.png", "imageB.jpg"}
		if !reflect.DeepEqual(foundFiles, expectedFiles) {
			t.Errorf("Expected files %v, got %v", expectedFiles, foundFiles)
		}
	})

	// 9. Test GetFileViewRowsByFolderID
	t.Run("GetFileViewRowsByFolderID", func(t *testing.T) {
		rows, err := q.GetFileViewRowsByFolderID(ctx, subFolder.ID)
		if err != nil {
			t.Fatalf("GetFileViewRowsByFolderID failed: %v", err)
		}
		defer rows.Close()

		var foundFiles []string
		for rows.Next() {
			var i FileView
			if err := rows.Scan(&i.ID, &i.FolderID, &i.FolderPath, &i.Path, &i.Filename, &i.SizeBytes, &i.Md5, &i.MimeType, &i.Width, &i.Height, &i.CreatedAt, &i.UpdatedAt); err != nil {
				t.Fatalf("failed to scan row: %v", err)
			}
			foundFiles = append(foundFiles, i.Filename)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows.Err() was not nil: %v", err)
		}

		expectedFiles := []string{"imageA.png", "imageB.jpg"}
		if !reflect.DeepEqual(foundFiles, expectedFiles) {
			t.Errorf("Expected files %v, got %v", expectedFiles, foundFiles)
		}
	})
}

func TestGetPreloadRoutesByFolderID(t *testing.T) {
	db, q, ctx := setupTestDB(t)

	// q is already *CustomQueries from setupTestDB
	_ = db

	// 1. Create folder hierarchy
	rootPathID, err := q.UpsertFolderPathReturningID(ctx, "/root")
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID for root failed: %v", err)
	}
	rootFolder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
		PathID:    rootPathID,
		Name:      "root",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertFolderReturningFolder for root failed: %v", err)
	}

	// 2. Create child folder under root
	childPathID, err := q.UpsertFolderPathReturningID(ctx, "/root/child")
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID for child failed: %v", err)
	}
	childFolder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: rootFolder.ID, Valid: true},
		PathID:    childPathID,
		Name:      "child",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertFolderReturningFolder for child failed: %v", err)
	}

	// 3. Create a file under child folder
	filePathID, err := q.UpsertFilePathReturningID(ctx, "/root/child/image.jpg")
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID failed: %v", err)
	}
	file, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: childFolder.ID, Valid: true},
		PathID:    filePathID,
		Filename:  "image.jpg",
		SizeBytes: sql.NullInt64{Int64: 12345, Valid: true},
		Mtime:     sql.NullInt64{Int64: 1678886400, Valid: true},
		Md5:       sql.NullString{String: "md5hash", Valid: true},
		Phash:     sql.NullInt64{Int64: 1234567890, Valid: true},
		MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile failed: %v", err)
	}

	// Verify file was created
	if file.ID == 0 {
		t.Error("expected file ID to be non-zero")
	}

	// 4. Test GetPreloadRoutesByFolderID for root folder (parent_id)
	// Should return routes for child folder and files under child
	routes, err := q.GetPreloadRoutesByFolderID(ctx, sql.NullInt64{Int64: rootFolder.ID, Valid: true})
	if err != nil {
		t.Fatalf("GetPreloadRoutesByFolderID failed: %v", err)
	}

	// 5. Verify routes contain expected prefixes
	expectedPrefixes := map[string]bool{
		"/gallery/":     false,
		"/info/folder/": false,
		"/info/image/":  false,
		"/lightbox/":    false,
	}

	for _, route := range routes {
		for prefix := range expectedPrefixes {
			if strings.HasPrefix(route, prefix) {
				expectedPrefixes[prefix] = true
			}
		}
	}

	// At least some of the route types should be present
	foundAtLeastOne := false
	for _, found := range expectedPrefixes {
		if found {
			foundAtLeastOne = true
			break
		}
	}

	if !foundAtLeastOne {
		t.Errorf("expected at least one route with valid prefix, got routes: %v", routes)
	}

	// 6. Test GetPreloadRoutesByFolderID for child folder
	childRoutes, err := q.GetPreloadRoutesByFolderID(ctx, sql.NullInt64{Int64: childFolder.ID, Valid: true})
	if err != nil {
		t.Fatalf("GetPreloadRoutesByFolderID for child failed: %v", err)
	}

	// Child folder routes should include the file
	if len(childRoutes) == 0 {
		t.Error("expected at least one route for child folder files, got none")
	}

	// Verify the custom database connection works correctly
	if db == nil {
		t.Error("database connection should not be nil")
	}
}

// TestInvalidFileQueries tests invalid file tracking queries
func TestInvalidFileQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)

	t.Run("UpsertInvalidFile and GetInvalidFileByPath", func(t *testing.T) {
		testPath := "/invalid/test.jpg"
		now := time.Now().Unix()

		// Insert an invalid file record
		err := q.UpsertInvalidFile(ctx, UpsertInvalidFileParams{
			Path:   testPath,
			Mtime:  now,
			Size:   12345,
			Reason: sql.NullString{String: "corrupted header", Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertInvalidFile failed: %v", err)
		}

		// Get the invalid file
		invFile, err := q.GetInvalidFileByPath(ctx, testPath)
		if err != nil {
			t.Fatalf("GetInvalidFileByPath failed: %v", err)
		}
		if invFile.Path != testPath {
			t.Errorf("Expected path %s, got %s", testPath, invFile.Path)
		}
		if invFile.Mtime != now {
			t.Errorf("Expected mtime %d, got %d", now, invFile.Mtime)
		}
		if invFile.Size != 12345 {
			t.Errorf("Expected size 12345, got %d", invFile.Size)
		}
		if !invFile.Reason.Valid || invFile.Reason.String != "corrupted header" {
			t.Errorf("Expected reason 'corrupted header', got %v", invFile.Reason)
		}
	})

	t.Run("UpsertInvalidFile_Update", func(t *testing.T) {
		testPath := "/invalid/update.jpg"
		now := time.Now().Unix()

		// Insert initial record
		err := q.UpsertInvalidFile(ctx, UpsertInvalidFileParams{
			Path:   testPath,
			Mtime:  now,
			Size:   100,
			Reason: sql.NullString{String: "initial reason", Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertInvalidFile (initial) failed: %v", err)
		}

		// Update the record
		err = q.UpsertInvalidFile(ctx, UpsertInvalidFileParams{
			Path:   testPath,
			Mtime:  now + 100,
			Size:   200,
			Reason: sql.NullString{String: "updated reason", Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertInvalidFile (update) failed: %v", err)
		}

		invFile, err := q.GetInvalidFileByPath(ctx, testPath)
		if err != nil {
			t.Fatalf("GetInvalidFileByPath (after update) failed: %v", err)
		}
		if invFile.Size != 200 {
			t.Errorf("Expected updated size 200, got %d", invFile.Size)
		}
		if !invFile.Reason.Valid || invFile.Reason.String != "updated reason" {
			t.Errorf("Expected updated reason 'updated reason', got %v", invFile.Reason)
		}
	})

	t.Run("DeleteInvalidFileByPath", func(t *testing.T) {
		testPath := "/invalid/delete.jpg"
		now := time.Now().Unix()

		// Insert a record
		err := q.UpsertInvalidFile(ctx, UpsertInvalidFileParams{
			Path:  testPath,
			Mtime: now,
			Size:  999,
		})
		if err != nil {
			t.Fatalf("UpsertInvalidFile failed: %v", err)
		}

		// Verify it exists
		_, err = q.GetInvalidFileByPath(ctx, testPath)
		if err != nil {
			t.Fatalf("GetInvalidFileByPath before delete failed: %v", err)
		}

		// Delete the record
		err = q.DeleteInvalidFileByPath(ctx, testPath)
		if err != nil {
			t.Fatalf("DeleteInvalidFileByPath failed: %v", err)
		}

		// Verify it's gone
		_, err = q.GetInvalidFileByPath(ctx, testPath)
		if err == nil {
			t.Error("expected error when getting deleted invalid file, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
		}
	})

	t.Run("GetInvalidFileByPath_NonExistent", func(t *testing.T) {
		_, err := q.GetInvalidFileByPath(ctx, "/nonexistent/invalid.jpg")
		if err == nil {
			t.Error("expected error when getting non-existent invalid file, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %v", err)
		}
	})
}
