//go:build integration

package gallerydb

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs" // Added
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/lbe/sfpg-go/migrations" // Added
)

// setupTestDB creates an in-memory SQLite database for testing, applies migrations,
// and returns a connection, a queries object, and a context.
func setupTestDB(t *testing.T) (*sql.DB, *Queries, context.Context) {
	t.Helper()

	// Use in-memory database for faster tests
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		db.Close()
		t.Fatalf("failed to create sqlite driver instance: %v", err)
	}

	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		db.Close()
		t.Fatalf("failed to create iofs source driver: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create migrate instance: %v", err)
	}
	if migErr := m.Up(); migErr != nil && migErr != migrate.ErrNoChange {
		// Force clean state in case of dirty database
		if _, isDirty := migErr.(migrate.ErrDirty); isDirty {
			m.Force(1)
		} else {
			db.Close()
			t.Fatalf("failed to apply migrations: %v", migErr)
		}
	}

	ctx := context.Background()
	q, err := Prepare(ctx, db)
	if err != nil {
		db.Close()
		t.Fatalf("failed to prepare queries: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
	})

	return db, q, ctx
}

func TestFolderAndFileQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)

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

	// 2. Verify folder retrieval
	retrievedRoot, err := q.GetFolderByID(ctx, rootFolder.ID)
	if err != nil {
		t.Fatalf("GetFolderByID for root failed: %v", err)
	}
	if retrievedRoot.Name != "root" {
		t.Errorf("Expected root folder name 'root', got %s", retrievedRoot.Name)
	}

	retrievedChild, err := q.GetFolderByPath(ctx, "/root/child")
	if err != nil {
		t.Fatalf("GetFolderByPath for child failed: %v", err)
	}
	if retrievedChild.ID != childFolder.ID {
		t.Errorf("GetFolderByPath returned wrong folder ID")
	}
	if !retrievedChild.ParentID.Valid || retrievedChild.ParentID.Int64 != rootFolder.ID {
		t.Errorf("Expected child's parent ID to be %d, got %v", rootFolder.ID, retrievedChild.ParentID)
	}

	// 3. Create a file
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

	// 4. Verify file retrieval
	// Note: GetFileByID was removed, using GetFileByPath instead
	retrievedFile, err := q.GetFileByPath(ctx, "/root/child/image.jpg")
	if err != nil {
		t.Fatalf("GetFileByPath failed: %v", err)
	}
	if retrievedFile.Filename != "image.jpg" {
		t.Errorf("Expected filename 'image.jpg', got %s", retrievedFile.Filename)
	}
	if !retrievedFile.FolderID.Valid || retrievedFile.FolderID.Int64 != childFolder.ID {
		t.Errorf("Expected file's folder ID to be %d, got %v", childFolder.ID, retrievedFile.FolderID)
	}
	if !retrievedFile.SizeBytes.Valid || retrievedFile.SizeBytes.Int64 != 12345 {
		t.Errorf("Expected size_bytes to be 12345, got %v", retrievedFile.SizeBytes)
	}
	if !retrievedFile.Mtime.Valid || retrievedFile.Mtime.Int64 != 1678886400 {
		t.Errorf("Expected mtime to be 1678886400, got %v", retrievedFile.Mtime)
	}
	if !retrievedFile.Md5.Valid || retrievedFile.Md5.String != "md5hash" {
		t.Errorf("Expected md5 to be 'md5hash', got %v", retrievedFile.Md5)
	}
	if !retrievedFile.Phash.Valid || retrievedFile.Phash.Int64 != 1234567890 {
		t.Errorf("Expected phash to be 1234567890, got %v", retrievedFile.Phash)
	}
	if !retrievedFile.MimeType.Valid || retrievedFile.MimeType.String != "image/jpeg" {
		t.Errorf("Expected mime_type to be 'image/jpeg', got %v", retrievedFile.MimeType)
	}

	// 5. Verify view using GetFileViewByID
	// Note: GetFileViewByPath was removed from gallerydb
	fileView, err := q.GetFileViewByID(ctx, file.ID)
	if err != nil {
		t.Fatalf("GetFileViewByID failed: %v", err)
	}
	if fileView.Filename != "image.jpg" {
		t.Errorf("Expected view filename 'image.jpg', got %s", fileView.Filename)
	}
	if fileView.FolderPath.String != "/root/child" {
		t.Errorf("Expected view folder path '/root/child', got %s", fileView.FolderPath.String)
	}
}

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

func TestViewAndCustomQueries(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	cq, err := PrepareCustomQueries(ctx, db)
	if err != nil {
		t.Fatalf("PrepareCustomQueries failed: %v", err)
	}

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

	// 3. Test GetFileViewsByFolderIDOrderByFileName
	t.Run("GetFileViewsByFolderIDOrderByFileName", func(t *testing.T) {
		views, err := q.GetFileViewsByFolderIDOrderByFileName(ctx, sql.NullInt64{Int64: subFolder.ID, Valid: true})
		if err != nil {
			t.Fatalf("GetFileViewsByFolderIDOrderByFileName failed: %v", err)
		}
		if len(views) != 2 {
			t.Fatalf("Expected 2 file views, got %d", len(views))
		}
		if views[0].Filename != "imageA.png" {
			t.Errorf("Expected first file to be imageA.png, got %s", views[0].Filename)
		}
		if views[1].Filename != "imageB.jpg" {
			t.Errorf("Expected second file to be imageB.jpg, got %s", views[1].Filename)
		}
	})

	// 4. Test UpdateFolderTileId
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
		retrievedData, err := cq.GetFolderViewThumbnailBlobDataByPath(ctx, "/gaps/sub")
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
		qtx := cq.WithTx(tx)

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
		rows, err := cq.GetFileViewRowsByFolderPath(ctx, "/gaps/sub")
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
		rows, err := cq.GetFileViewRowsByFolderID(ctx, subFolder.ID)
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
		if err != sql.ErrNoRows {
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
		if err != sql.ErrNoRows {
			t.Errorf("Expected sql.ErrNoRows after delete, got %v", err)
		}
	})
}

func TestFolderPathsAndFolderViews(t *testing.T) {
	_, q, ctx := setupTestDB(t)

	// Insert several folder paths
	_, err := q.UpsertFolderPathReturningID(ctx, "/a")
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID /a failed: %v", err)
	}
	_, err = q.UpsertFolderPathReturningID(ctx, "/a/b")
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID /a/b failed: %v", err)
	}
	_, err = q.UpsertFolderPathReturningID(ctx, "/c")
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID /c failed: %v", err)
	}

	// Test GetFoldersViewsByParentIDOrderByName
	// Create a parent folder and two children with names to check ordering
	parentPathID, _ := q.UpsertFolderPathReturningID(ctx, "/parent")
	parentFolder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{PathID: parentPathID, Name: "parent", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
	if err != nil {
		t.Fatalf("Insert parent folder failed: %v", err)
	}

	// childB then childA to verify ordering by name yields A then B
	childBPathID, _ := q.UpsertFolderPathReturningID(ctx, "/parent/B")
	_, err = q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{ParentID: sql.NullInt64{Int64: parentFolder.ID, Valid: true}, PathID: childBPathID, Name: "B", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
	if err != nil {
		t.Fatalf("Insert child B failed: %v", err)
	}
	childAPathID, _ := q.UpsertFolderPathReturningID(ctx, "/parent/A")
	_, err = q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{ParentID: sql.NullInt64{Int64: parentFolder.ID, Valid: true}, PathID: childAPathID, Name: "A", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
	if err != nil {
		t.Fatalf("Insert child A failed: %v", err)
	}

	views, err := q.GetFoldersViewsByParentIDOrderByName(ctx, sql.NullInt64{Int64: parentFolder.ID, Valid: true})
	if err != nil {
		t.Fatalf("GetFoldersViewsByParentIDOrderByName failed: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("expected 2 child folder views, got %d", len(views))
	}
	if views[0].Name != "A" || views[1].Name != "B" {
		t.Errorf("expected ordered children [A B], got [%s %s]", views[0].Name, views[1].Name)
	}
}

func TestGetFileByPathAndGetFolderIDByPath(t *testing.T) {
	_, q, ctx := setupTestDB(t)

	// Create folder and file path
	folderPathID, err := q.UpsertFolderPathReturningID(ctx, "/filetest")
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID failed: %v", err)
	}
	folder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{PathID: folderPathID, Name: "filetest", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()})
	if err != nil {
		t.Fatalf("UpsertFolderReturningFolder failed: %v", err)
	}

	// Insert file path and file
	fpID, err := q.UpsertFilePathReturningID(ctx, "/filetest/img.jpg")
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID failed: %v", err)
	}
	f, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
		PathID:    fpID,
		Filename:  "img.jpg",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile failed: %v", err)
	}

	// Test GetFileByPath
	got, err := q.GetFileByPath(ctx, "/filetest/img.jpg")
	if err != nil {
		t.Fatalf("GetFileByPath failed: %v", err)
	}
	if got.ID != f.ID {
		t.Errorf("GetFileByPath returned wrong ID: expected %d, got %d", f.ID, got.ID)
	}

	// Test GetFolderIDByPath
	fid, err := q.GetFolderIDByPath(ctx, "/filetest")
	if err != nil {
		t.Fatalf("GetFolderIDByPath failed: %v", err)
	}
	if fid != folder.ID {
		t.Errorf("GetFolderIDByPath returned wrong ID: expected %d, got %d", folder.ID, fid)
	}
}

func TestLoginAttemptQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	username := "testuser"
	now := time.Now().Unix()

	t.Run("GetLoginAttempt_NonExistent", func(t *testing.T) {
		_, err := q.GetLoginAttempt(ctx, username)
		if err == nil {
			t.Error("expected error when getting non-existent login attempt, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %v", err)
		}
	})

	t.Run("UpsertLoginAttempt_Insert", func(t *testing.T) {
		failedAttempts := int64(1)
		err := q.UpsertLoginAttempt(ctx, UpsertLoginAttemptParams{
			Username:       username,
			FailedAttempts: failedAttempts,
			LastAttemptAt:  now,
			LockedUntil:    sql.NullInt64{Valid: false},
		})
		if err != nil {
			t.Fatalf("UpsertLoginAttempt (insert) failed: %v", err)
		}

		attempt, err := q.GetLoginAttempt(ctx, username)
		if err != nil {
			t.Fatalf("GetLoginAttempt failed: %v", err)
		}
		if attempt.Username != username {
			t.Errorf("expected username %q, got %q", username, attempt.Username)
		}
		if attempt.FailedAttempts != failedAttempts {
			t.Errorf("expected failed_attempts %d, got %d", failedAttempts, attempt.FailedAttempts)
		}
		if attempt.LockedUntil.Valid {
			t.Error("expected locked_until to be NULL, but it was set")
		}
	})

	t.Run("UpsertLoginAttempt_UpdateIncrement", func(t *testing.T) {
		newFailedAttempts := int64(2)
		err := q.UpsertLoginAttempt(ctx, UpsertLoginAttemptParams{
			Username:       username,
			FailedAttempts: newFailedAttempts,
			LastAttemptAt:  now + 1,
			LockedUntil:    sql.NullInt64{Valid: false},
		})
		if err != nil {
			t.Fatalf("UpsertLoginAttempt (update) failed: %v", err)
		}

		attempt, err := q.GetLoginAttempt(ctx, username)
		if err != nil {
			t.Fatalf("GetLoginAttempt failed: %v", err)
		}
		if attempt.FailedAttempts != newFailedAttempts {
			t.Errorf("expected failed_attempts %d, got %d", newFailedAttempts, attempt.FailedAttempts)
		}
	})

	t.Run("UpsertLoginAttempt_SetLockout", func(t *testing.T) {
		failedAttempts := int64(3)
		lockedUntil := now + 3600 // 1 hour from now
		err := q.UpsertLoginAttempt(ctx, UpsertLoginAttemptParams{
			Username:       username,
			FailedAttempts: failedAttempts,
			LastAttemptAt:  now + 2,
			LockedUntil:    sql.NullInt64{Int64: lockedUntil, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertLoginAttempt (set lockout) failed: %v", err)
		}

		attempt, err := q.GetLoginAttempt(ctx, username)
		if err != nil {
			t.Fatalf("GetLoginAttempt failed: %v", err)
		}
		if attempt.FailedAttempts != failedAttempts {
			t.Errorf("expected failed_attempts %d, got %d", failedAttempts, attempt.FailedAttempts)
		}
		if !attempt.LockedUntil.Valid {
			t.Error("expected locked_until to be set, but it was NULL")
		}
		if attempt.LockedUntil.Int64 != lockedUntil {
			t.Errorf("expected locked_until %d, got %d", lockedUntil, attempt.LockedUntil.Int64)
		}
	})

	t.Run("ClearLoginAttempts", func(t *testing.T) {
		err := q.ClearLoginAttempts(ctx, username)
		if err != nil {
			t.Fatalf("ClearLoginAttempts failed: %v", err)
		}

		_, err = q.GetLoginAttempt(ctx, username)
		if err == nil {
			t.Error("expected error when getting cleared login attempt, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %v", err)
		}
	})

	t.Run("UnlockAccount", func(t *testing.T) {
		// First create a locked account
		lockedUntil := now + 3600
		err := q.UpsertLoginAttempt(ctx, UpsertLoginAttemptParams{
			Username:       username,
			FailedAttempts: 3,
			LastAttemptAt:  now,
			LockedUntil:    sql.NullInt64{Int64: lockedUntil, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertLoginAttempt failed: %v", err)
		}

		// Unlock the account
		err = q.UnlockAccount(ctx, username)
		if err != nil {
			t.Fatalf("UnlockAccount failed: %v", err)
		}

		// Verify account is unlocked
		attempt, err := q.GetLoginAttempt(ctx, username)
		if err != nil {
			t.Fatalf("GetLoginAttempt failed: %v", err)
		}
		if attempt.FailedAttempts != 0 {
			t.Errorf("expected failed_attempts 0 after unlock, got %d", attempt.FailedAttempts)
		}
		if attempt.LockedUntil.Valid {
			t.Error("expected locked_until to be NULL after unlock, but it was set")
		}
	})

	t.Run("CleanupExpiredLockouts", func(t *testing.T) {
		// Create multiple accounts with expired and non-expired lockouts
		expiredUsername := "expireduser"
		activeUsername := "activeuser"
		pastTime := now - 7200   // 2 hours ago
		futureTime := now + 3600 // 1 hour from now

		// Expired lockout
		err := q.UpsertLoginAttempt(ctx, UpsertLoginAttemptParams{
			Username:       expiredUsername,
			FailedAttempts: 3,
			LastAttemptAt:  pastTime,
			LockedUntil:    sql.NullInt64{Int64: pastTime, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertLoginAttempt (expired) failed: %v", err)
		}

		// Active lockout
		err = q.UpsertLoginAttempt(ctx, UpsertLoginAttemptParams{
			Username:       activeUsername,
			FailedAttempts: 3,
			LastAttemptAt:  now,
			LockedUntil:    sql.NullInt64{Int64: futureTime, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertLoginAttempt (active) failed: %v", err)
		}
	})
}

func TestGetPreloadRoutesByFolderID(t *testing.T) {
	db, q, ctx := setupTestDB(t)

	// Prepare custom queries
	cq := &CustomQueries{
		Queries: q,
	}

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
	rows, err := cq.GetPreloadRoutesByFolderID(ctx, sql.NullInt64{Int64: rootFolder.ID, Valid: true})
	if err != nil {
		t.Fatalf("GetPreloadRoutesByFolderID failed: %v", err)
	}
	defer rows.Close()

	var routes []string
	for rows.Next() {
		var route string
		if scanErr := rows.Scan(&route); scanErr != nil {
			t.Fatalf("rows.Scan failed: %v", scanErr)
		}
		routes = append(routes, route)
	}

	if scanErr := rows.Err(); scanErr != nil {
		t.Fatalf("rows.Err failed: %v", scanErr)
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
	rows2, err := cq.GetPreloadRoutesByFolderID(ctx, sql.NullInt64{Int64: childFolder.ID, Valid: true})
	if err != nil {
		t.Fatalf("GetPreloadRoutesByFolderID for child failed: %v", err)
	}
	defer rows2.Close()

	var childRoutes []string
	for rows2.Next() {
		var route string
		if err := rows2.Scan(&route); err != nil {
			t.Fatalf("rows2.Scan failed: %v", err)
		}
		childRoutes = append(childRoutes, route)
	}

	if err := rows2.Err(); err != nil {
		t.Fatalf("rows2.Err failed: %v", err)
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

// TestConfigQueries tests configuration-related queries
func TestConfigQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	now := time.Now().Unix()

	t.Run("UpsertConfigValueOnly and GetConfigValueByKey", func(t *testing.T) {
		// Insert a config value
		err := q.UpsertConfigValueOnly(ctx, UpsertConfigValueOnlyParams{
			Key:       "test_key",
			Value:     "test_value",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertConfigValueOnly failed: %v", err)
		}

		// Get the config value
		value, err := q.GetConfigValueByKey(ctx, "test_key")
		if err != nil {
			t.Fatalf("GetConfigValueByKey failed: %v", err)
		}
		if value != "test_value" {
			t.Errorf("Expected value 'test_value', got %s", value)
		}

		// Update the config value
		err = q.UpsertConfigValueOnly(ctx, UpsertConfigValueOnlyParams{
			Key:       "test_key",
			Value:     "updated_value",
			CreatedAt: now,
			UpdatedAt: now + 1,
		})
		if err != nil {
			t.Fatalf("UpsertConfigValueOnly (update) failed: %v", err)
		}

		value, err = q.GetConfigValueByKey(ctx, "test_key")
		if err != nil {
			t.Fatalf("GetConfigValueByKey (after update) failed: %v", err)
		}
		if value != "updated_value" {
			t.Errorf("Expected updated value 'updated_value', got %s", value)
		}
	})

	t.Run("GetConfigValueByKey_NonExistent", func(t *testing.T) {
		_, err := q.GetConfigValueByKey(ctx, "nonexistent_key")
		if err == nil {
			t.Error("expected error when getting non-existent config key, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %v", err)
		}
	})

	t.Run("GetConfigs", func(t *testing.T) {
		// Insert another config value
		err := q.UpsertConfigValueOnly(ctx, UpsertConfigValueOnlyParams{
			Key:       "another_key",
			Value:     "another_value",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertConfigValueOnly failed: %v", err)
		}

		// Get all configs
		configs, err := q.GetConfigs(ctx)
		if err != nil {
			t.Fatalf("GetConfigs failed: %v", err)
		}
		// Should have at least the configs we inserted
		if len(configs) < 2 {
			t.Errorf("Expected at least 2 configs, got %d", len(configs))
		}

		// Find our test configs
		foundTestKey := false
		foundAnotherKey := false
		for _, cfg := range configs {
			if cfg.Key == "test_key" {
				foundTestKey = true
				if cfg.Value != "updated_value" {
					t.Errorf("Expected test_key value 'updated_value', got %s", cfg.Value)
				}
			}
			if cfg.Key == "another_key" {
				foundAnotherKey = true
				if cfg.Value != "another_value" {
					t.Errorf("Expected another_key value 'another_value', got %s", cfg.Value)
				}
			}
		}
		if !foundTestKey {
			t.Error("Did not find test_key in configs")
		}
		if !foundAnotherKey {
			t.Error("Did not find another_key in configs")
		}
	})
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

// TestHttpCacheQueries tests HTTP cache queries
func TestHttpCacheQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	now := time.Now().Unix()

	t.Run("UpsertHttpCache and GetHttpCacheByKey", func(t *testing.T) {
		// Insert a cache entry
		params := UpsertHttpCacheParams{
			Key:             "test_key_1",
			Method:          "GET",
			Path:            "/api/test",
			Encoding:        "gzip",
			Status:          200,
			ContentType:     sql.NullString{String: "application/json", Valid: true},
			ContentEncoding: sql.NullString{String: "gzip", Valid: true},
			CacheControl:    sql.NullString{String: "max-age=3600", Valid: true},
			Etag:            sql.NullString{String: `\"12345\"`, Valid: true},
			LastModified:    sql.NullString{String: "Mon, 01 Jan 2024 00:00:00 GMT", Valid: true},
			Body:            []byte(`{"test": "data"}`),
			ContentLength:   sql.NullInt64{Int64: 17, Valid: true},
			CreatedAt:       now,
			ExpiresAt:       sql.NullInt64{Int64: now + 3600, Valid: true},
		}
		err := q.UpsertHttpCache(ctx, params)
		if err != nil {
			t.Fatalf("UpsertHttpCache failed: %v", err)
		}

		// Get the cache entry
		entry, err := q.GetHttpCacheByKey(ctx, "test_key_1")
		if err != nil {
			t.Fatalf("GetHttpCacheByKey failed: %v", err)
		}
		if entry.Key != "test_key_1" {
			t.Errorf("Expected key 'test_key_1', got %s", entry.Key)
		}
		if entry.Method != "GET" {
			t.Errorf("Expected method 'GET', got %s", entry.Method)
		}
		if entry.Status != 200 {
			t.Errorf("Expected status 200, got %d", entry.Status)
		}
		if string(entry.Body) != `{"test": "data"}` {
			t.Errorf("Expected body '{\"test\": \"data\"}', got %s", string(entry.Body))
		}
	})

	t.Run("GetHttpCacheByKey_NonExistent", func(t *testing.T) {
		_, err := q.GetHttpCacheByKey(ctx, "nonexistent_key")
		if err == nil {
			t.Error("expected error when getting non-existent cache entry, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %v", err)
		}
	})

	t.Run("HttpCacheExistsByKey", func(t *testing.T) {
		// Check existing key
		exists, err := q.HttpCacheExistsByKey(ctx, "test_key_1")
		if err != nil {
			t.Fatalf("HttpCacheExistsByKey (existing) failed: %v", err)
		}
		if exists != 1 {
			t.Errorf("Expected exists=1 for existing key, got %d", exists)
		}

		// Check non-existing key
		exists, err = q.HttpCacheExistsByKey(ctx, "nonexistent_key")
		if err != nil {
			t.Fatalf("HttpCacheExistsByKey (non-existing) failed: %v", err)
		}
		if exists != 0 {
			t.Errorf("Expected exists=0 for non-existing key, got %d", exists)
		}
	})

	t.Run("CountHttpCacheEntries", func(t *testing.T) {
		// Add another entry
		err := q.UpsertHttpCache(ctx, UpsertHttpCacheParams{
			Key:       "test_key_2",
			Method:    "GET",
			Path:      "/api/test2",
			Encoding:  "gzip",
			Status:    200,
			CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertHttpCache (second entry) failed: %v", err)
		}

		// Count entries
		count, err := q.CountHttpCacheEntries(ctx)
		if err != nil {
			t.Fatalf("CountHttpCacheEntries failed: %v", err)
		}
		if count < 2 {
			t.Errorf("Expected at least 2 cache entries, got %d", count)
		}
	})

	t.Run("GetHttpCacheSizeBytes", func(t *testing.T) {
		size, err := q.GetHttpCacheSizeBytes(ctx)
		if err != nil {
			t.Fatalf("GetHttpCacheSizeBytes failed: %v", err)
		}
		// Size should be at least the sum of our test entries
		// We can't check exact size due to type being interface{}, but it should be non-nil
		if size == nil {
			t.Error("Expected size to be non-nil")
		}
	})

	t.Run("GetHttpCacheOldestCreated", func(t *testing.T) {
		// Get oldest entries
		entries, err := q.GetHttpCacheOldestCreated(ctx, 2)
		if err != nil {
			t.Fatalf("GetHttpCacheOldestCreated failed: %v", err)
		}
		if len(entries) == 0 {
			t.Error("Expected at least 1 oldest entry, got none")
		}
		if len(entries) > 2 {
			t.Errorf("Expected at most 2 entries, got %d", len(entries))
		}
	})

	t.Run("DeleteHttpCacheByID", func(t *testing.T) {
		// Get an entry to find its ID
		entry, err := q.GetHttpCacheByKey(ctx, "test_key_1")
		if err != nil {
			t.Fatalf("GetHttpCacheByKey for delete test failed: %v", err)
		}

		// Delete by ID
		err = q.DeleteHttpCacheByID(ctx, entry.ID)
		if err != nil {
			t.Fatalf("DeleteHttpCacheByID failed: %v", err)
		}

		// Verify it's gone
		_, err = q.GetHttpCacheByKey(ctx, "test_key_1")
		if err == nil {
			t.Error("expected error after DeleteHttpCacheByID, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
		}
	})

	t.Run("DeleteHttpCacheByKey", func(t *testing.T) {
		// Delete by key
		err := q.DeleteHttpCacheByKey(ctx, "test_key_2")
		if err != nil {
			t.Fatalf("DeleteHttpCacheByKey failed: %v", err)
		}

		// Verify it's gone
		_, err = q.GetHttpCacheByKey(ctx, "test_key_2")
		if err == nil {
			t.Error("expected error after DeleteHttpCacheByKey, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
		}
	})

	t.Run("DeleteHttpCacheExpired", func(t *testing.T) {
		// Insert an expired entry
		pastTime := now - 3600
		err := q.UpsertHttpCache(ctx, UpsertHttpCacheParams{
			Key:       "expired_key",
			Method:    "GET",
			Path:      "/api/expired",
			Encoding:  "gzip",
			Status:    200,
			CreatedAt: pastTime - 100,
			ExpiresAt: sql.NullInt64{Int64: pastTime, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertHttpCache (expired) failed: %v", err)
		}

		// Insert a non-expired entry
		err = q.UpsertHttpCache(ctx, UpsertHttpCacheParams{
			Key:       "nonexpired_key",
			Method:    "GET",
			Path:      "/api/valid",
			Encoding:  "gzip",
			Status:    200,
			CreatedAt: now,
			ExpiresAt: sql.NullInt64{Int64: now + 3600, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertHttpCache (non-expired) failed: %v", err)
		}

		// Delete expired entries
		err = q.DeleteHttpCacheExpired(ctx)
		if err != nil {
			t.Fatalf("DeleteHttpCacheExpired failed: %v", err)
		}

		// Verify expired entry is gone
		_, err = q.GetHttpCacheByKey(ctx, "expired_key")
		if err == nil {
			t.Error("expected error for expired entry after DeleteHttpCacheExpired, but got nil")
		}

		// Verify non-expired entry still exists
		_, err = q.GetHttpCacheByKey(ctx, "nonexpired_key")
		if err != nil {
			t.Errorf("Expected non-expired entry to exist, got error: %v", err)
		}
	})

	t.Run("ClearHttpCache", func(t *testing.T) {
		// Ensure we have an entry
		err := q.UpsertHttpCache(ctx, UpsertHttpCacheParams{
			Key:       "to_be_cleared",
			Method:    "GET",
			Path:      "/api/clearme",
			Encoding:  "gzip",
			Status:    200,
			CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertHttpCache (for clear test) failed: %v", err)
		}

		// Clear all cache
		err = q.ClearHttpCache(ctx)
		if err != nil {
			t.Fatalf("ClearHttpCache failed: %v", err)
		}

		// Verify all entries are gone
		count, err := q.CountHttpCacheEntries(ctx)
		if err != nil {
			t.Fatalf("CountHttpCacheEntries (after clear) failed: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 entries after ClearHttpCache, got %d", count)
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
		} else if err != sql.ErrNoRows {
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
		// Setup migrations
		driver, err := sqlite.WithInstance(db, &sqlite.Config{})
		if err != nil {
			t.Fatalf("failed to create sqlite driver: %v", err)
		}
		d, err := iofs.New(migrations.FS, "migrations")
		if err != nil {
			t.Fatalf("failed to create iofs source: %v", err)
		}
		m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
		if err != nil {
			t.Fatalf("failed to create migrate instance: %v", err)
		}
		if migErr := m.Up(); migErr != nil && migErr != migrate.ErrNoChange {
			t.Fatalf("failed to apply migrations: %v", migErr)
		}

		ctx := context.Background()
		q, err := Prepare(ctx, db)
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

func TestGetGalleryStatistics(t *testing.T) {
	_, q, ctx := setupTestDB(t)

	// Create folder hierarchy
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

	// Create files with different sizes
	now := time.Now()
	filePathID1, err := q.UpsertFilePathReturningID(ctx, "/root/child/image1.jpg")
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID failed: %v", err)
	}
	_, err = q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: childFolder.ID, Valid: true},
		PathID:    filePathID1,
		Filename:  "image1.jpg",
		SizeBytes: sql.NullInt64{Int64: 1000000, Valid: true}, // 1MB
		Mtime:     sql.NullInt64{Int64: now.Unix(), Valid: true},
		Md5:       sql.NullString{String: "md5hash1", Valid: true},
		Phash:     sql.NullInt64{Int64: 1234567890, Valid: true},
		MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
		CreatedAt: now.Add(-2 * time.Hour).Unix(), // created 2 hours ago
		UpdatedAt: now.Add(-1 * time.Hour).Unix(), // updated 1 hour ago
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile for image1 failed: %v", err)
	}

	filePathID2, err := q.UpsertFilePathReturningID(ctx, "/root/child/image2.jpg")
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID failed: %v", err)
	}
	_, err = q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: childFolder.ID, Valid: true},
		PathID:    filePathID2,
		Filename:  "image2.jpg",
		SizeBytes: sql.NullInt64{Int64: 2500000, Valid: true}, // 2.5MB
		Mtime:     sql.NullInt64{Int64: now.Unix(), Valid: true},
		Md5:       sql.NullString{String: "md5hash2", Valid: true},
		Phash:     sql.NullInt64{Int64: 1234567890, Valid: true},
		MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
		CreatedAt: now.Add(-3 * time.Hour).Unix(), // created 3 hours ago
		UpdatedAt: now.Unix(),                     // updated now
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile for image2 failed: %v", err)
	}

	// Get statistics
	stats, err := q.GetGalleryStatistics(ctx)
	if err != nil {
		t.Fatalf("GetGalleryStatistics failed: %v", err)
	}

	// Verify counts
	if stats.CtFolders != 2 {
		t.Errorf("Expected 2 folders, got %d", stats.CtFolders)
	}
	if stats.CtFiles != 2 {
		t.Errorf("Expected 2 files, got %d", stats.CtFiles)
	}

	// Verify total size (1MB + 2.5MB = 3.5MB = 3500000 bytes)
	expectedSize := 3500000.0
	if !stats.SzFiles.Valid {
		t.Errorf("Expected SzFiles to be valid, got NULL")
	} else if stats.SzFiles.Float64 != expectedSize {
		t.Errorf("Expected total size %f, got %f", expectedSize, stats.SzFiles.Float64)
	}

	// Verify timestamps
	if stats.MinCreatedAt == nil {
		t.Errorf("Expected MinCreatedAt to have a value, got NULL")
	}
	if stats.MaxUpdatedAt == nil {
		t.Errorf("Expected MaxUpdatedAt to have a value, got NULL")
	}
}
