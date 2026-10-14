//nolint:govet // legacy DB integration tests reuse err in subtests
package gallerydb

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestThumbnailQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	// 1. Create a file to associate the thumbnail with
	folderPathID, _ := q.UpsertFolderPathReturningID(ctx, "/thumbs")
	folder, _ := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
		PathID:    folderPathID,
		Name:      "thumbs",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	filePathID, _ := q.UpsertFilePathReturningID(ctx, "/thumbs/thumb_test.jpg")
	file, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
		PathID:    filePathID,
		Filename:  "thumb_test.jpg",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Failed to create file for thumbnail test: %v", err)
	}
	// 2. Create a thumbnail record
	thumbID, err := q.UpsertThumbnailReturningID(ctx, UpsertThumbnailReturningIDParams{
		FileID:    file.ID,
		SizeLabel: "test",
		Width:     100,
		Height:    100,
		Format:    "jpeg",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertThumbnailReturningID failed: %v", err)
	}
	// 3. Insert the thumbnail blob data
	thumbData := []byte{0xFF, 0xD8, 0xFF, 0xE0} // Minimal JPEG SOI marker
	err = q.UpsertThumbnailBlob(ctx, UpsertThumbnailBlobParams{
		ThumbnailID: thumbID,
		Data:        thumbData,
	})
	if err != nil {
		t.Fatalf("UpsertThumbnailBlob failed: %v", err)
	}
	// 4. Verify retrieval of the blob
	retrievedData, err := q.GetThumbnailBlobDataByID(ctx, thumbID)
	if err != nil {
		t.Fatalf("GetThumbnailBlobDataByID failed: %v", err)
	}
	if string(retrievedData) != string(thumbData) {
		t.Errorf("Expected thumbnail data %v, got %v", thumbData, retrievedData)
	}
	// 5. Verify the thumbnail exists view
	exists, err := q.GetThumbnailExistsViewByID(ctx, file.ID)
	if err != nil {
		t.Fatalf("GetThumbnailExistsViewByID failed: %v", err)
	}
	if !exists {
		t.Error("Expected GetThumbnailExistsViewByID to return true, but it returned false")
	}
}
func TestMetadataQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	// Create a file to associate metadata with
	folderPathID, _ := q.UpsertFolderPathReturningID(ctx, "/meta")
	folder, _ := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{PathID: folderPathID, Name: "meta", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
	filePathID, _ := q.UpsertFilePathReturningID(ctx, "/meta/meta_test.jpg")
	file, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
		PathID:    filePathID,
		Filename:  "meta_test.jpg",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Failed to create file for metadata test: %v", err)
	}
	t.Run("EXIF", func(t *testing.T) {
		err := q.UpsertExif(ctx, UpsertExifParams{
			FileID:      file.ID,
			CameraMake:  sql.NullString{String: "TestMake", Valid: true},
			CameraModel: sql.NullString{String: "TestModel", Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertExif failed: %v", err)
		}
		exif, err := q.GetExifByFile(ctx, file.ID)
		if err != nil {
			t.Fatalf("GetExifByFile failed: %v", err)
		}
		if !exif.CameraMake.Valid || exif.CameraMake.String != "TestMake" {
			t.Errorf("Expected CameraMake 'TestMake', got %v", exif.CameraMake)
		}
	})
	t.Run("IPTC", func(t *testing.T) {
		err := q.UpsertIPTC(ctx, UpsertIPTCParams{
			FileID: file.ID,
			Title:  sql.NullString{String: "Test Title", Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertIPTC failed: %v", err)
		}
		iptc, err := q.GetIPTCByFile(ctx, file.ID)
		if err != nil {
			t.Fatalf("GetIPTCByFile failed: %v", err)
		}
		if !iptc.Title.Valid || iptc.Title.String != "Test Title" {
			t.Errorf("Expected Title 'Test Title', got %v", iptc.Title)
		}
		err = q.DeleteIPTC(ctx, file.ID)
		if err != nil {
			t.Fatalf("DeleteIPTC failed: %v", err)
		}
		_, err = q.GetIPTCByFile(ctx, file.ID)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("Expected sql.ErrNoRows after delete, got %v", err)
		}
	})
	t.Run("XMP", func(t *testing.T) {
		err := q.UpsertXMPRaw(ctx, UpsertXMPRawParams{
			FileID: file.ID,
			RawXml: sql.NullString{String: "<test></test>", Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertXMPRaw failed: %v", err)
		}
		xmp, err := q.GetXMPRaw(ctx, file.ID)
		if err != nil {
			t.Fatalf("GetXMPRaw failed: %v", err)
		}
		if !xmp.RawXml.Valid || xmp.RawXml.String != "<test></test>" {
			t.Errorf("Expected RawXml '<test></test>', got %v", xmp.RawXml)
		}
		err = q.DeleteXMPRaw(ctx, file.ID)
		if err != nil {
			t.Fatalf("DeleteXMPRaw failed: %v", err)
		}
		_, err = q.GetXMPRaw(ctx, file.ID)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("Expected sql.ErrNoRows after delete, got %v", err)
		}
	})
}

// TestIPTCKeywordQueries tests IPTC keyword queries
func TestIPTCKeywordQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	// Create a file to associate keywords with
	folderPathID, _ := q.UpsertFolderPathReturningID(ctx, "/iptc")
	folder, _ := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
		PathID:    folderPathID,
		Name:      "iptc",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	filePathID, _ := q.UpsertFilePathReturningID(ctx, "/iptc/photo.jpg")
	file, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
		PathID:    filePathID,
		Filename:  "photo.jpg",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Failed to create file for IPTC keyword test: %v", err)
	}
	t.Run("InsertIPTCKeyword and GetIPTCKeywords", func(t *testing.T) {
		// Insert keywords
		keywords := []string{"sunset", "beach", "vacation"}
		for i, kw := range keywords {
			err := q.InsertIPTCKeyword(ctx, InsertIPTCKeywordParams{
				ID:      int64(i + 1),
				FileID:  file.ID,
				Keyword: kw,
			})
			if err != nil {
				t.Fatalf("InsertIPTCKeyword failed for %s: %v", kw, err)
			}
		}
		// Get all keywords for the file
		retrieved, err := q.GetIPTCKeywords(ctx, file.ID)
		if err != nil {
			t.Fatalf("GetIPTCKeywords failed: %v", err)
		}
		if len(retrieved) != len(keywords) {
			t.Errorf("Expected %d keywords, got %d", len(keywords), len(retrieved))
		}
		// Verify keywords match
		foundKeywords := make(map[string]bool)
		for _, kw := range retrieved {
			foundKeywords[kw.Keyword] = true
		}
		for _, expected := range keywords {
			if !foundKeywords[expected] {
				t.Errorf("Expected to find keyword '%s'", expected)
			}
		}
	})
	t.Run("GetIPTCKeywords_Empty", func(t *testing.T) {
		// Create a file with no keywords
		noKwPathID, _ := q.UpsertFilePathReturningID(ctx, "/iptc/nokw.jpg")
		noKwFile, _ := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
			FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
			PathID:    noKwPathID,
			Filename:  "nokw.jpg",
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		keywords, err := q.GetIPTCKeywords(ctx, noKwFile.ID)
		if err != nil {
			t.Fatalf("GetIPTCKeywords (empty) failed: %v", err)
		}
		if len(keywords) != 0 {
			t.Errorf("Expected 0 keywords, got %d", len(keywords))
		}
	})
	t.Run("DeleteIPTCKeyword", func(t *testing.T) {
		// Insert a keyword to delete
		err := q.InsertIPTCKeyword(ctx, InsertIPTCKeywordParams{
			ID:      999,
			FileID:  file.ID,
			Keyword: "toberemoved",
		})
		if err != nil {
			t.Fatalf("InsertIPTCKeyword for delete test failed: %v", err)
		}
		// Verify it exists
		keywords, _ := q.GetIPTCKeywords(ctx, file.ID)
		foundBefore := false
		for _, kw := range keywords {
			if kw.Keyword == "toberemoved" {
				foundBefore = true
				break
			}
		}
		if !foundBefore {
			t.Error("Keyword 'toberemoved' should exist before delete")
		}
		// Delete the keyword
		err = q.DeleteIPTCKeyword(ctx, 999)
		if err != nil {
			t.Fatalf("DeleteIPTCKeyword failed: %v", err)
		}
		// Verify it's gone
		keywords, _ = q.GetIPTCKeywords(ctx, file.ID)
		foundAfter := false
		for _, kw := range keywords {
			if kw.Keyword == "toberemoved" {
				foundAfter = true
				break
			}
		}
		if foundAfter {
			t.Error("Keyword 'toberemoved' should not exist after delete")
		}
	})
}

// TestXMPPropertyQueries tests XMP property queries
func TestXMPPropertyQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	// Create a file to associate XMP properties with
	folderPathID, _ := q.UpsertFolderPathReturningID(ctx, "/xmp")
	folder, _ := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
		PathID:    folderPathID,
		Name:      "xmp",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	filePathID, _ := q.UpsertFilePathReturningID(ctx, "/xmp/photo.jpg")
	file, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
		PathID:    filePathID,
		Filename:  "photo.jpg",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Failed to create file for XMP property test: %v", err)
	}
	t.Run("UpsertXMPProperty and GetXMPPropertiesByFile", func(t *testing.T) {
		// Insert XMP properties
		properties := []UpsertXMPPropertyParams{
			{ID: 1, FileID: file.ID, Namespace: "dc", Property: "title", Value: sql.NullString{String: "My Photo", Valid: true}},
			{ID: 2, FileID: file.ID, Namespace: "dc", Property: "description", Value: sql.NullString{String: "A nice photo", Valid: true}},
			{ID: 3, FileID: file.ID, Namespace: "exif", Property: "FNumber", Value: sql.NullString{String: "2.8", Valid: true}},
		}
		for _, prop := range properties {
			err := q.UpsertXMPProperty(ctx, prop)
			if err != nil {
				t.Fatalf("UpsertXMPProperty failed: %v", err)
			}
		}
		// Get all properties for the file
		retrieved, err := q.GetXMPPropertiesByFile(ctx, file.ID)
		if err != nil {
			t.Fatalf("GetXMPPropertiesByFile failed: %v", err)
		}
		if len(retrieved) != len(properties) {
			t.Errorf("Expected %d properties, got %d", len(properties), len(retrieved))
		}
		// Find specific properties
		foundTitle := false
		foundDescription := false
		foundFNumber := false
		for _, prop := range retrieved {
			if prop.Namespace == "dc" && prop.Property == "title" {
				foundTitle = true
				if !prop.Value.Valid || prop.Value.String != "My Photo" {
					t.Errorf("Expected title 'My Photo', got %v", prop.Value)
				}
			}
			if prop.Namespace == "dc" && prop.Property == "description" {
				foundDescription = true
				if !prop.Value.Valid || prop.Value.String != "A nice photo" {
					t.Errorf("Expected description 'A nice photo', got %v", prop.Value)
				}
			}
			if prop.Namespace == "exif" && prop.Property == "FNumber" {
				foundFNumber = true
				if !prop.Value.Valid || prop.Value.String != "2.8" {
					t.Errorf("Expected FNumber '2.8', got %v", prop.Value)
				}
			}
		}
		if !foundTitle {
			t.Error("Did not find XMP title property")
		}
		if !foundDescription {
			t.Error("Did not find XMP description property")
		}
		if !foundFNumber {
			t.Error("Did not find XMP FNumber property")
		}
	})
	t.Run("GetXMPPropertiesByFile_Empty", func(t *testing.T) {
		// Create a file with no XMP properties
		noXmpPathID, _ := q.UpsertFilePathReturningID(ctx, "/xmp/noxmp.jpg")
		noXmpFile, _ := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
			FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
			PathID:    noXmpPathID,
			Filename:  "noxmp.jpg",
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		props, err := q.GetXMPPropertiesByFile(ctx, noXmpFile.ID)
		if err != nil {
			t.Fatalf("GetXMPPropertiesByFile (empty) failed: %v", err)
		}
		if len(props) != 0 {
			t.Errorf("Expected 0 properties, got %d", len(props))
		}
	})
	t.Run("UpsertXMPProperty_Update", func(t *testing.T) {
		// Update the title property
		err := q.UpsertXMPProperty(ctx, UpsertXMPPropertyParams{
			ID:        1,
			FileID:    file.ID,
			Namespace: "dc",
			Property:  "title",
			Value:     sql.NullString{String: "Updated Title", Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertXMPProperty (update) failed: %v", err)
		}
		props, err := q.GetXMPPropertiesByFile(ctx, file.ID)
		if err != nil {
			t.Fatalf("GetXMPPropertiesByFile (after update) failed: %v", err)
		}
		foundUpdated := false
		for _, prop := range props {
			if prop.Namespace == "dc" && prop.Property == "title" {
				foundUpdated = true
				if !prop.Value.Valid || prop.Value.String != "Updated Title" {
					t.Errorf("Expected updated title 'Updated Title', got %v", prop.Value)
				}
			}
		}
		if !foundUpdated {
			t.Error("Did not find updated XMP title property")
		}
	})
	t.Run("DeleteXMPProperty", func(t *testing.T) {
		// Insert a property to delete
		err := q.UpsertXMPProperty(ctx, UpsertXMPPropertyParams{
			ID:        888,
			FileID:    file.ID,
			Namespace: "test",
			Property:  "toberemoved",
			Value:     sql.NullString{String: "remove me", Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertXMPProperty for delete test failed: %v", err)
		}
		// Verify it exists
		props, _ := q.GetXMPPropertiesByFile(ctx, file.ID)
		foundBefore := false
		for _, prop := range props {
			if prop.Namespace == "test" && prop.Property == "toberemoved" {
				foundBefore = true
				break
			}
		}
		if !foundBefore {
			t.Error("Property 'toberemoved' should exist before delete")
		}
		// Delete the property
		err = q.DeleteXMPProperty(ctx, 888)
		if err != nil {
			t.Fatalf("DeleteXMPProperty failed: %v", err)
		}
		// Verify it's gone
		props, _ = q.GetXMPPropertiesByFile(ctx, file.ID)
		foundAfter := false
		for _, prop := range props {
			if prop.Namespace == "test" && prop.Property == "toberemoved" {
				foundAfter = true
				break
			}
		}
		if foundAfter {
			t.Error("Property 'toberemoved' should not exist after delete")
		}
	})
}

// TestGetThumbnailsByFileID tests getting thumbnails by file ID
func TestGetThumbnailsByFileID(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	// Create folder and file
	folderPathID, _ := q.UpsertFolderPathReturningID(ctx, "/thumbtest")
	folder, _ := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
		PathID:    folderPathID,
		Name:      "thumbtest",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	filePathID, _ := q.UpsertFilePathReturningID(ctx, "/thumbtest/image.jpg")
	file, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
		PathID:    filePathID,
		Filename:  "image.jpg",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	t.Run("GetThumbnailsByFileID_Existing", func(t *testing.T) {
		// Create a thumbnail
		thumbID, err := q.UpsertThumbnailReturningID(ctx, UpsertThumbnailReturningIDParams{
			FileID:    file.ID,
			SizeLabel: "small",
			Width:     150,
			Height:    150,
			Format:    "jpeg",
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		if err != nil {
			t.Fatalf("UpsertThumbnailReturningID failed: %v", err)
		}
		// Get thumbnail by file ID
		thumbnail, err := q.GetThumbnailsByFileID(ctx, file.ID)
		if err != nil {
			t.Fatalf("GetThumbnailsByFileID failed: %v", err)
		}
		if thumbnail.ID != thumbID {
			t.Errorf("Expected thumbnail ID %d, got %d", thumbID, thumbnail.ID)
		}
		if thumbnail.FileID != file.ID {
			t.Errorf("Expected file ID %d, got %d", file.ID, thumbnail.FileID)
		}
		if thumbnail.SizeLabel != "small" {
			t.Errorf("Expected size label 'small', got %s", thumbnail.SizeLabel)
		}
		if thumbnail.Width != 150 {
			t.Errorf("Expected width 150, got %d", thumbnail.Width)
		}
		if thumbnail.Height != 150 {
			t.Errorf("Expected height 150, got %d", thumbnail.Height)
		}
		if thumbnail.Format != "jpeg" {
			t.Errorf("Expected format 'jpeg', got %s", thumbnail.Format)
		}
	})
	t.Run("GetThumbnailsByFileID_NonExistent", func(t *testing.T) {
		// Try to get thumbnail for a file without one
		noThumbPathID, _ := q.UpsertFilePathReturningID(ctx, "/thumbtest/nothumb.jpg")
		noThumbFile, _ := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
			FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
			PathID:    noThumbPathID,
			Filename:  "nothumb.jpg",
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		_, err := q.GetThumbnailsByFileID(ctx, noThumbFile.ID)
		if err == nil {
			t.Error("expected error when getting thumbnail for file without one, but got nil")
		} else if !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("expected sql.ErrNoRows, got %v", err)
		}
	})
}

// TestNewAndClose tests the New() and Close() functions
func TestNewAndClose(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer db.Close()
	t.Run("New", func(t *testing.T) {
		q := New(db)
		if q == nil {
			t.Fatal("New() returned nil")
		}
		// Verify the query object has the db set
		if q.db != db {
			t.Error("New() did not set db correctly")
		}
	})
	t.Run("Close", func(t *testing.T) {
		isolatedDB := newIsolatedGallerySQLDB(t)
		ctx := context.Background()
		q, err := PrepareCustomQueries(ctx, isolatedDB)
		if err != nil {
			t.Fatalf("Prepare() failed: %v", err)
		}
		// Close should succeed
		err = q.Close()
		if err != nil {
			t.Errorf("Close() returned error: %v", err)
		}
	})
}
func TestCustomQueriesThumbsDB(t *testing.T) {
	// This test requires thumbs.db to be ATTACHed as "thumbs".
	// setupTestDB provides that attachment.
	_, q, ctx := setupTestDB(t)
	// Create test folder
	pathID, err := q.UpsertFolderPathReturningID(ctx, "/test")
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID: %v", err)
	}
	folder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
		PathID:    pathID,
		Name:      "test",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertFolderReturningFolder: %v", err)
	}
	// Create test file
	filePath := "/test/test.jpg"
	filePathID, err := q.UpsertFilePathReturningID(ctx, filePath)
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID: %v", err)
	}
	file, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
		PathID:    filePathID,
		Filename:  "test.jpg",
		Width:     sql.NullInt64{Int64: 800, Valid: true},
		Height:    sql.NullInt64{Int64: 600, Valid: true},
		MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile: %v", err)
	}
	// Create thumbnail
	thumbID, err := q.UpsertThumbnailReturningID(ctx, UpsertThumbnailReturningIDParams{
		FileID:    file.ID,
		SizeLabel: "test",
		Width:     150,
		Height:    150,
		Format:    "jpeg",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertThumbnailReturningID: %v", err)
	}
	// Test UpsertThumbnailBlob (uses thumbs.thumbnail_blobs)
	blobData := []byte("fake-jpeg-data")
	err = q.UpsertThumbnailBlob(ctx, UpsertThumbnailBlobParams{
		ThumbnailID: thumbID,
		Data:        blobData,
	})
	if err != nil {
		t.Fatalf("UpsertThumbnailBlob: %v", err)
	}
	// Test GetThumbnailBlobDataByID (uses thumbs.thumbnail_blobs)
	got, err := q.GetThumbnailBlobDataByID(ctx, thumbID)
	if err != nil {
		t.Fatalf("GetThumbnailBlobDataByID: %v", err)
	}
	if string(got) != string(blobData) {
		t.Errorf("blob mismatch: got %q, want %q", got, blobData)
	}
}
func TestModuleStateQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	_, err := q.GetModuleState(ctx, "discovery")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
	startedAt := time.Now().Unix()
	err = q.SetModuleState(ctx, SetModuleStateParams{
		Name:          "discovery",
		IsActive:      1,
		LastStartedAt: sql.NullInt64{Int64: startedAt, Valid: true},
	})
	if err != nil {
		t.Fatalf("SetModuleState failed: %v", err)
	}
	state, err := q.GetModuleState(ctx, "discovery")
	if err != nil {
		t.Fatalf("GetModuleState failed: %v", err)
	}
	if state.IsActive != 1 {
		t.Errorf("expected IsActive=1, got %d", state.IsActive)
	}
	if !state.LastStartedAt.Valid || state.LastStartedAt.Int64 != startedAt {
		t.Errorf("expected LastStartedAt=%d, got %v", startedAt, state.LastStartedAt)
	}
	finishedAt := startedAt + 10
	err = q.SetModuleState(ctx, SetModuleStateParams{
		Name:           "discovery",
		IsActive:       0,
		LastFinishedAt: sql.NullInt64{Int64: finishedAt, Valid: true},
	})
	if err != nil {
		t.Fatalf("SetModuleState (update) failed: %v", err)
	}
	state, err = q.GetModuleState(ctx, "discovery")
	if err != nil {
		t.Fatalf("GetModuleState (after update) failed: %v", err)
	}
	if state.IsActive != 0 {
		t.Errorf("expected IsActive=0, got %d", state.IsActive)
	}
	if !state.LastFinishedAt.Valid || state.LastFinishedAt.Int64 != finishedAt {
		t.Errorf("expected LastFinishedAt=%d, got %v", finishedAt, state.LastFinishedAt)
	}
}

// TestModuleStatePayload_PreservedAcrossSetActive verifies that a JSON payload
// stored via SetModuleStatePayload survives a subsequent SetModuleState call that
// flips the module inactive. SetModuleState must never clobber the payload column.
func TestModuleStatePayload_PreservedAcrossSetActive(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	payloadJSON := `{"file_processing":{"total_found":7,"already_existing":6,"newly_inserted":1,"skipped_invalid":2}}`
	startedAt := time.Now().Unix()
	err := q.SetModuleState(ctx, SetModuleStateParams{
		Name:          "discovery",
		IsActive:      1,
		LastStartedAt: sql.NullInt64{Int64: startedAt, Valid: true},
	})
	if err != nil {
		t.Fatalf("SetModuleState (active) failed: %v", err)
	}
	if err := q.SetModuleStatePayload(ctx, SetModuleStatePayloadParams{
		Name:    "discovery",
		Payload: sql.NullString{String: payloadJSON, Valid: true},
	}); err != nil {
		t.Fatalf("SetModuleStatePayload failed: %v", err)
	}
	state, err := q.GetModuleState(ctx, "discovery")
	if err != nil {
		t.Fatalf("GetModuleState failed: %v", err)
	}
	if !state.Payload.Valid {
		t.Fatal("expected Payload.Valid=true after SetModuleStatePayload")
	}
	if state.Payload.String != payloadJSON {
		t.Errorf("payload mismatch:\n got %q\nwant %q", state.Payload.String, payloadJSON)
	}
	finishedAt := startedAt + 10
	err = q.SetModuleState(ctx, SetModuleStateParams{
		Name:           "discovery",
		IsActive:       0,
		LastFinishedAt: sql.NullInt64{Int64: finishedAt, Valid: true},
	})
	if err != nil {
		t.Fatalf("SetModuleState (inactive) failed: %v", err)
	}
	state, err = q.GetModuleState(ctx, "discovery")
	if err != nil {
		t.Fatalf("GetModuleState (after inactive) failed: %v", err)
	}
	if state.IsActive != 0 {
		t.Errorf("expected IsActive=0, got %d", state.IsActive)
	}
	if !state.Payload.Valid {
		t.Fatal("expected payload to survive SetModuleState")
	}
	if state.Payload.String != payloadJSON {
		t.Errorf("payload not preserved across SetModuleState:\n got %q\nwant %q", state.Payload.String, payloadJSON)
	}
}

// TestIsolatedTestDatabasesObserveNoSharedState verifies that an isolated
// memory-backed test database cannot observe rows written to the shared
// harness or to another isolated database.
func TestIsolatedTestDatabasesObserveNoSharedState(t *testing.T) {
	_, sharedQ, sharedCtx := setupTestDB(t)
	const markerKey = "testdb-isolation-marker"
	if err := sharedQ.UpsertConfigValueOnly(sharedCtx, UpsertConfigValueOnlyParams{
		Key:       markerKey,
		Value:     "shared",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("UpsertConfigValueOnly (shared): %v", err)
	}

	isolatedA := newIsolatedGallerySQLDB(t)
	ctxA := context.Background()
	qA, err := PrepareCustomQueries(ctxA, isolatedA)
	if err != nil {
		t.Fatalf("PrepareCustomQueries (isolated A): %v", err)
	}
	if exists, err := qA.ConfigKeyExists(ctxA, markerKey); err != nil {
		t.Fatalf("ConfigKeyExists (isolated A): %v", err)
	} else if exists {
		t.Fatal("isolated database observed a row from the shared harness")
	}
	if err := qA.UpsertConfigValueOnly(ctxA, UpsertConfigValueOnlyParams{
		Key:       markerKey,
		Value:     "isolated-a",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("UpsertConfigValueOnly (isolated A): %v", err)
	}

	isolatedB := newIsolatedGallerySQLDB(t)
	ctxB := context.Background()
	qB, err := PrepareCustomQueries(ctxB, isolatedB)
	if err != nil {
		t.Fatalf("PrepareCustomQueries (isolated B): %v", err)
	}
	if exists, err := qB.ConfigKeyExists(ctxB, markerKey); err != nil {
		t.Fatalf("ConfigKeyExists (isolated B): %v", err)
	} else if exists {
		t.Fatal("isolated database observed a row from another isolated database")
	}

	if exists, err := sharedQ.ConfigKeyExists(sharedCtx, markerKey); err != nil {
		t.Fatalf("ConfigKeyExists (shared): %v", err)
	} else if !exists {
		t.Fatal("shared harness lost its row to an isolated database")
	}
}
