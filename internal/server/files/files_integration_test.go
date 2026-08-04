//go:build integration

package files

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

func TestGenerateThumbnailAndUpdateDbIfNeeded_Integration(t *testing.T) {
	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
	importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}

	destImgName := "test-image.jpg"
	destImgPath := filepath.Join(imagesDir, destImgName)

	// Create a simple 1x1 red JPEG image
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	file, err := os.Create(destImgPath)
	if err != nil {
		t.Fatalf("create dummy image: %v", err)
	}
	if err := jpeg.Encode(file, img, nil); err != nil {
		file.Close()
		t.Fatalf("encode JPEG: %v", err)
	}
	file.Close()

	cpcRw, err := rwPool.Get()
	if err != nil {
		t.Fatalf("get rw: %v", err)
	}
	defer rwPool.Put(cpcRw)

	cpcRo, err := roPool.Get()
	if err != nil {
		t.Fatalf("get ro: %v", err)
	}
	defer roPool.Put(cpcRo)

	f := File{
		ImagesDir: imagesDir,
		Path:      destImgName,
	}
	if err := ProcessFile(ctx, cpcRo, &f); err != nil {
		// We expect a non-image error here, but we continue to test the thumbnail generation part
		if len(err.Error()) < 10 || err.Error()[:10] != "non-image " {
			t.Logf("processFile failed with unexpected error: %v", err)
		}
	}

	if err := GenerateThumbnailAndUpdateDbIfNeeded(ctx, cpcRw, cpcRo, &f, importerFactory); err != nil {
		t.Fatalf("GenerateThumbnailAndUpdateDbIfNeeded: %v", err)
	}

	dbFile, err := cpcRo.Queries.GetFileByPath(ctx, destImgName)
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}

	thumbExists, err := cpcRo.Queries.GetThumbnailExistsViewByID(ctx, dbFile.ID)
	if err != nil {
		t.Fatalf("GetThumbnailExistsViewByID: %v", err)
	}
	if !thumbExists {
		t.Error("expected thumbnail to exist")
	}

	folder, err := cpcRo.Queries.GetFolderByID(ctx, dbFile.FolderID.Int64)
	if err != nil {
		t.Fatalf("GetFolderByID: %v", err)
	}
	if !folder.TileID.Valid || folder.TileID.Int64 == 0 {
		t.Error("expected folder tile to be updated")
	}
}

func TestProcessFile_Integration(t *testing.T) {
	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
	importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}

	sourceImgPath := filepath.Join("..", "..", "..", "testdata", "Metadata_test_file_-_includes_data_in_IIM,_XMP,_and_Exif.jpg")
	destImgName := "test-image.jpg"
	destImgPath := filepath.Join(imagesDir, destImgName)

	input, err := os.ReadFile(sourceImgPath)
	if err != nil {
		t.Skipf("read source image: %v", err)
	}

	if err := os.WriteFile(destImgPath, input, 0o644); err != nil {
		t.Fatalf("write destination: %v", err)
	}

	cpcRo, err := roPool.Get()
	if err != nil {
		t.Fatalf("get ro: %v", err)
	}
	defer roPool.Put(cpcRo)

	cpcRw, err := rwPool.Get()
	if err != nil {
		t.Fatalf("get rw: %v", err)
	}
	defer rwPool.Put(cpcRw)

	f := &File{
		ImagesDir: imagesDir,
		Path:      destImgName,
	}

	if err := ProcessFile(ctx, cpcRo, f); err != nil {
		t.Fatalf("ProcessFile: %v", err)
	}

	t.Logf("CameraMake from test: %+v", f.Exif.CameraMake)
	expectedMime := "image/jpeg"
	if f.File.MimeType.String != expectedMime {
		t.Errorf("MimeType = %v, want %v", f.File.MimeType.String, expectedMime)
	}

	expectedMake := "samsung"
	if !f.Exif.CameraMake.Valid || f.Exif.CameraMake.String != expectedMake {
		t.Errorf("CameraMake = %q, want %q", f.Exif.CameraMake.String, expectedMake)
	}

	if !f.File.Md5.Valid || f.File.Md5.String == "" {
		t.Error("expected Md5 to be populated")
	}
	if !f.File.Phash.Valid || f.File.Phash.Int64 == 0 {
		t.Error("expected Phash to be populated")
	}
	if f.Thumbnail == nil || f.Thumbnail.Len() == 0 {
		t.Error("expected ThumbnailData to be populated")
	}

	if err := GenerateThumbnailAndUpdateDbIfNeeded(ctx, cpcRw, cpcRo, f, importerFactory); err != nil {
		t.Fatalf("GenerateThumbnailAndUpdateDbIfNeeded: %v", err)
	}

	dbFile, err := cpcRo.Queries.GetFileByPath(ctx, destImgName)
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	exif, err := cpcRo.Queries.GetExifByFile(ctx, dbFile.ID)
	if err != nil {
		t.Fatalf("GetExifByFile: %v", err)
	}
	if exif.FileID != dbFile.ID {
		t.Errorf("exif.FileID = %d, want %d", exif.FileID, dbFile.ID)
	}
	if !exif.CameraMake.Valid || exif.CameraMake.String != expectedMake {
		t.Errorf("persisted CameraMake = %q, want %q", exif.CameraMake.String, expectedMake)
	}
}

func TestUpsertThumbnail_FullFlow_SuccessAndRollback(t *testing.T) {
	_, rwPool, _, ctx := createTestPoolsAndDir(t)

	cpcRw, err := rwPool.Get()
	if err != nil {
		t.Fatalf("get RW: %v", err)
	}
	defer rwPool.Put(cpcRw)

	// Create a file path and file so thumbnails can reference a real file_id
	fpID, err := cpcRw.Queries.UpsertFilePathReturningID(ctx, "/test/path/img.jpg")
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID: %v", err)
	}
	file, err := cpcRw.Queries.UpsertFileReturningFile(ctx, gallerydb.UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Valid: false},
		PathID:    fpID,
		Filename:  "img.jpg",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile: %v", err)
	}

	// Success path: call upsertThumbnail and assert the blob is present
	thumb := []byte("thumb-bytes")
	thumbnailID, err := UpsertThumbnail(ctx, cpcRw, file.ID, thumb)
	if err != nil {
		t.Fatalf("UpsertThumbnail: %v", err)
	}
	// Verify blob exists
	data, err := cpcRw.Queries.GetThumbnailBlobDataByID(ctx, thumbnailID)
	if err != nil {
		t.Fatalf("GetThumbnailBlobDataByID: %v", err)
	}
	if string(data) != string(thumb) {
		t.Fatalf("thumbnail blob mismatch: got %v", data)
	}

	// Rollback path: create a new file so we can verify rollback cleaned up its rows,
	// then temporarily replace upsertThumbnailTxOnly to force an error
	fpID2, err := cpcRw.Queries.UpsertFilePathReturningID(ctx, "/test/path/img2.jpg")
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID: %v", err)
	}
	file2, err := cpcRw.Queries.UpsertFileReturningFile(ctx, gallerydb.UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Valid: false},
		PathID:    fpID2,
		Filename:  "img2.jpg",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile: %v", err)
	}

	orig := UpsertThumbnailTxOnly
	UpsertThumbnailTxOnly = func(qtx ThumbnailTx, ctx context.Context, fileID int64, thumb []byte) (int64, error) {
		// Insert a thumbnail row inside the tx, then return an error to force rollback
		id, err2 := qtx.UpsertThumbnailReturningID(ctx, gallerydb.UpsertThumbnailReturningIDParams{
			FileID:    fileID,
			SizeLabel: "m",
			Width:     0,
			Height:    0,
			Format:    "jpg",
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		})
		if err2 != nil {
			return 0, err2
		}
		// don't insert blob, return error to cause outer rollback
		return id, sql.ErrConnDone
	}
	defer func() { UpsertThumbnailTxOnly = orig }()

	_, err = UpsertThumbnail(ctx, cpcRw, file2.ID, []byte("other"))
	if err == nil {
		t.Fatal("expected error from UpsertThumbnail (forced), got nil")
	}
	// Ensure any thumbnail inserted in the inner tx was rolled back: try to get thumbnails for fileID2
	_, err = cpcRw.Queries.GetThumbnailsByFileID(ctx, file2.ID)
	if err == nil {
		t.Fatal("expected no thumbnails after rollback, but some exist")
	}
}

func TestInvalidFileSkipping_Integration(t *testing.T) {
	processor, _, _, imagesDir := createTestProcessor(t, nil)
	ctx := context.Background()

	// Create a non-image file
	path := "invalid.txt"
	fullPath := filepath.Join(imagesDir, path)
	if err := os.WriteFile(fullPath, []byte("not an image"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// First attempt fails (non-image)
	_, err = processor.ProcessFile(ctx, path)
	if err == nil {
		t.Fatal("expected ProcessFile to fail for non-image")
	}
	// Record as invalid (simulating what the worker does)
	if err := processor.RecordInvalidFile(ctx, path, info.ModTime().Unix(), info.Size(), "non-image"); err != nil {
		t.Fatalf("RecordInvalidFile: %v", err)
	}

	// Second attempt should skip (unchanged, no error)
	file, err := processor.ProcessFile(ctx, path)
	if err != nil {
		t.Fatalf("second ProcessFile: %v", err)
	}
	if !file.Ok || file.Exists {
		t.Errorf("expected skip (Ok=true, Exists=false), got Ok=%v Exists=%v", file.Ok, file.Exists)
	}
}

func TestInvalidFileReprocessing_Integration(t *testing.T) {
	processor, roPool, _, imagesDir := createTestProcessor(t, nil)
	ctx := context.Background()

	// Create non-image, process (fails), record invalid
	path := "reprocess.txt"
	fullPath := filepath.Join(imagesDir, path)
	if err := os.WriteFile(fullPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := processor.ProcessFile(ctx, path)
	if err == nil {
		t.Fatal("expected ProcessFile to fail")
	}
	info, _ := os.Stat(fullPath)
	_ = processor.RecordInvalidFile(ctx, path, info.ModTime().Unix(), info.Size(), "non-image")

	// Replace with valid image (different mtime/size)
	srcPath := filepath.Join("..", "..", "..", "testdata", "Metadata_test_file_-_includes_data_in_IIM,_XMP,_and_Exif.jpg")
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Skipf("read source image: %v", err)
	}
	if err := os.WriteFile(fullPath, src, 0o644); err != nil {
		t.Fatalf("overwrite: %v", err)
	}

	// Should reprocess and succeed (no longer in invalid_files after success)
	file, err := processor.ProcessFile(ctx, path)
	if err != nil {
		t.Fatalf("ProcessFile after replace: %v", err)
	}
	if file.Ok && !file.Exists {
		t.Error("expected file to be processed (not skipped as invalid)")
	}
	// GenerateThumbnail should clear invalid_files and succeed
	if err := processor.GenerateThumbnail(ctx, file); err != nil {
		t.Fatalf("GenerateThumbnail: %v", err)
	}
	// Verify invalid_files entry was cleared
	cpcRo, err := roPool.Get()
	if err != nil {
		t.Fatalf("roPool.Get: %v", err)
	}
	defer roPool.Put(cpcRo)
	_, err = cpcRo.Queries.GetInvalidFileByPath(ctx, path)
	if err != sql.ErrNoRows {
		t.Errorf("expected invalid_files entry cleared after success, got err %v", err)
	}
}

// TestGenerateThumbnail_CallsImporterMethods verifies importer methods are called correctly.
func TestGenerateThumbnail_CallsImporterMethods(t *testing.T) {
	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
	cpcRw, err := rwPool.Get()
	if err != nil {
		t.Fatalf("get rw: %v", err)
	}
	defer rwPool.Put(cpcRw)
	cpcRo, err := roPool.Get()
	if err != nil {
		t.Fatalf("get ro: %v", err)
	}
	defer roPool.Put(cpcRo)
	var wrapped *wrappedImporter
	importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
		if wrapped == nil {
			gi := &gallerylib.Importer{Conn: conn, Q: q}
			wrapped = &wrappedImporter{inner: gi}
			return wrapped
		}
		if wrapped.inner == nil {
			wrapped.inner = &gallerylib.Importer{Conn: conn, Q: q}
		} else {
			wrapped.inner.Conn = conn
			wrapped.inner.Q = q
		}
		return wrapped
	}
	f := &File{
		ImagesDir: imagesDir,
		Path:      "test-dir/testfile.jpg",
		Thumbnail: bytes.NewBuffer([]byte{1, 2, 3}),
		File:      gallerydb.File{Mtime: sql.NullInt64{Int64: time.Now().Unix(), Valid: true}, SizeBytes: sql.NullInt64{Int64: 123, Valid: true}},
	}
	if genErr := GenerateThumbnailAndUpdateDbIfNeeded(ctx, cpcRw, cpcRo, f, importerFactory); genErr != nil {
		t.Fatalf("GenerateThumbnailAndUpdateDbIfNeeded: %v", genErr)
	}
	if wrapped == nil {
		t.Fatal("wrapped importer not created")
	}
	if wrapped.upsertCalls == 0 {
		t.Error("expected UpsertPathChain called at least once")
	}
	if wrapped.updateCalls == 0 {
		t.Error("expected UpdateFolderTileChain called at least once")
	}
}

// TestGenerateThumbnail_ClearsInvalidFileOnSuccess verifies invalid_files entry is cleared on success.
func TestGenerateThumbnail_ClearsInvalidFileOnSuccess(t *testing.T) {
	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
	cpcRw, err := rwPool.Get()
	if err != nil {
		t.Fatalf("get rw: %v", err)
	}
	defer rwPool.Put(cpcRw)
	cpcRo, err := roPool.Get()
	if err != nil {
		t.Fatalf("get ro: %v", err)
	}
	defer roPool.Put(cpcRo)
	path := "test-dir/cleared.jpg"
	if upsertErr := cpcRw.Queries.UpsertInvalidFile(ctx, gallerydb.UpsertInvalidFileParams{
		Path: path, Mtime: 1, Size: 2, Reason: sql.NullString{String: "non-image", Valid: true},
	}); upsertErr != nil {
		t.Fatalf("UpsertInvalidFile: %v", upsertErr)
	}
	importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}
	f := &File{
		ImagesDir: imagesDir,
		Path:      path,
		Thumbnail: bytes.NewBuffer([]byte{1, 2, 3}),
		File:      gallerydb.File{Mtime: sql.NullInt64{Int64: time.Now().Unix(), Valid: true}, SizeBytes: sql.NullInt64{Int64: 3, Valid: true}},
	}
	if genErr := GenerateThumbnailAndUpdateDbIfNeeded(ctx, cpcRw, cpcRo, f, importerFactory); genErr != nil {
		t.Fatalf("GenerateThumbnailAndUpdateDbIfNeeded: %v", err)
	}
	_, err = cpcRo.Queries.GetInvalidFileByPath(ctx, path)
	if err != sql.ErrNoRows {
		t.Errorf("expected invalid_files entry to be cleared, got err %v", err)
	}
}

// TestNewPoolFunc_RunPoolWorkerSuccess verifies pool worker successfully processes a file.
func TestNewPoolFunc_RunPoolWorkerSuccess(t *testing.T) {
	roPool, rwPool, imagesDir, _ := createTestPoolsAndDir(t)
	importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}

	rel := createTestImage(t, imagesDir, "worker-test.jpg")
	full := filepath.ToSlash(filepath.Join(imagesDir, rel))

	q := queue.NewQueue[string](1)
	if err := q.Enqueue(full); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	processor := NewFileProcessor(roPool, rwPool, importerFactory, imagesDir, &mockUnifiedBatcher{})
	defer processor.Close()

	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poolFunc := NewPoolFuncWithProcessor(processor, q, filepath.ToSlash(imagesDir), testRemovePrefix, nil, nil)
	done := make(chan error, 1)
	baseline := pool.Stats.CompletedTasks.Load()

	go func() {
		done <- poolFunc(ctx, pool, roPool, rwPool, q.Len, 1)
	}()

	waitForCompleted(t, pool, baseline+1)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("runPoolWorker returned error: %v", err)
	}

	if pool.Stats.SuccessfulTasks.Load() == 0 {
		t.Fatalf("expected successful task count to be > 0")
	}
}

// TestFileProcessor_ProcessFile_Integration verifies FileProcessor.ProcessFile works with real database.
func TestFileProcessor_ProcessFile_Integration(t *testing.T) {
	processor, _, _, imagesDir := createTestProcessor(t, nil)

	file, err := processor.ProcessFile(context.Background(), createTestImage(t, imagesDir, "test.jpg"))
	if err != nil {
		t.Fatalf("ProcessFile() error = %v", err)
	}
	if file == nil {
		t.Fatal("ProcessFile() returned nil file on success")
	}
}

// TestFileProcessor_CheckIfModified_Integration verifies FileProcessor.CheckIfModified with real database.
func TestFileProcessor_CheckIfModified_Integration(t *testing.T) {
	processor, _, _, imagesDir := createTestProcessor(t, nil)
	createTestImage(t, imagesDir, "test.jpg")

	unchanged, err := processor.CheckIfModified(context.Background(), "test.jpg")
	if err != nil {
		t.Fatalf("CheckIfModified() error = %v", err)
	}
	if unchanged {
		t.Errorf("CheckIfModified() unchanged = %v, want false", unchanged)
	}
}

// TestFileProcessor_GenerateThumbnail_Integration verifies FileProcessor.GenerateThumbnail with real database.
func TestFileProcessor_GenerateThumbnail_Integration(t *testing.T) {
	processor, _, _, imagesDir := createTestProcessor(t, nil)
	path := createTestImage(t, imagesDir, "test.jpg")

	f, err := processor.ProcessFile(context.Background(), path)
	if err != nil {
		t.Fatalf("pre-process file: %v", err)
	}

	if err := processor.GenerateThumbnail(context.Background(), f); err != nil {
		t.Fatalf("GenerateThumbnail() error = %v", err)
	}
}

// TestWriteFileInTx_Integration verifies WriteFileInTx writes file data within a transaction.
func TestWriteFileInTx_Integration(t *testing.T) {
	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)

	// Create test image and process it
	path := createTestImage(t, imagesDir, "test_write_tx.jpg")
	processor := NewFileProcessor(roPool, rwPool, func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}, imagesDir, nil)
	t.Cleanup(func() { _ = processor.Close() })

	file, err := processor.ProcessFile(ctx, path)
	if err != nil {
		t.Fatalf("ProcessFile: %v", err)
	}
	if file.Thumbnail == nil {
		t.Fatal("ProcessFile did not generate thumbnail")
	}

	// Begin transaction and call WriteFileInTx
	connRW, err := rwPool.Get()
	if err != nil {
		t.Fatalf("Get RW conn: %v", err)
	}
	defer rwPool.Put(connRW)
	tx, err := connRW.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	qtx := connRW.Queries.WithTx(tx)
	imp := &gallerylib.Importer{Q: qtx}

	if writeErr := WriteFileInTx(ctx, imp, file); writeErr != nil {
		_ = tx.Rollback()
		t.Fatalf("WriteFileInTx: %v", err)
	}

	if commitErr := tx.Commit(); commitErr != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Verify file exists in DB
	cpcRo, err := roPool.Get()
	if err != nil {
		t.Fatalf("Get RO conn: %v", err)
	}
	defer roPool.Put(cpcRo)

	dbFile, err := cpcRo.Queries.GetFileByPath(ctx, path)
	if err != nil {
		t.Errorf("GetFileByPath: %v", err)
	}
	if dbFile.ID == 0 {
		t.Error("file ID is 0")
	}

	// Verify thumbnail exists
	thumbExists, err := cpcRo.Queries.GetThumbnailExistsViewByID(ctx, dbFile.ID)
	if err != nil {
		t.Errorf("GetThumbnailExistsViewByID: %v", err)
	}
	if !thumbExists {
		t.Error("thumbnail does not exist after WriteFileInTx")
	}

	// Verify EXIF persisted (if any was present)
	if file.Exif.CameraMake.Valid {
		exif, err := cpcRo.Queries.GetExifByFile(ctx, dbFile.ID)
		if err != nil {
			t.Errorf("GetExifByFile: %v", err)
		}
		if exif.FileID != dbFile.ID {
			t.Error("EXIF not associated with correct file")
		}
	}

	// Verify thumbnail buffer was returned to pool (f.Thumbnail should be nil)
	if file.Thumbnail != nil {
		t.Error("thumbnail buffer was not returned to pool")
	}
}

// TestSubmitFileForWrite_Integration verifies file is submitted to batcher correctly.
func TestSubmitFileForWrite_Integration(t *testing.T) {
	var submitted *File
	mockUB := &mockUnifiedBatcher{
		SubmitFileFunc: func(file *File) error {
			submitted = file
			return nil
		},
	}
	processor, _, _, imagesDir := createTestProcessor(t, mockUB)
	ctx := context.Background()

	// Create test image and process it
	path := createTestImage(t, imagesDir, "test_submit_async.jpg")
	file, err := processor.ProcessFile(ctx, path)
	if err != nil {
		t.Fatalf("ProcessFile: %v", err)
	}

	// Submit for async write
	if submitErr := processor.SubmitFileForWrite(file); submitErr != nil {
		t.Fatalf("SubmitFileForWrite: %v", submitErr)
	}

	if submitted == nil {
		t.Error("expected file to be submitted to batcher")
	} else if submitted.Path != file.Path {
		t.Errorf("expected submitted file path %s, got %s", file.Path, submitted.Path)
	}
}

func TestWriteFileInTx_PersistsXMP(t *testing.T) {
	_, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)

	// Path must be relative (same as other WriteFileInTx integration tests).
	path := createTestImage(t, imagesDir, "xmp_persist.jpg")

	cpcRw, err := rwPool.Get()
	if err != nil {
		t.Fatalf("rwPool.Get: %v", err)
	}
	defer rwPool.Put(cpcRw)

	tx, err := cpcRw.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	const rawXML = `<rdf:RDF xmlns:exif="http://ns.adobe.com/exif/1.0/">
<exif:GPSLatitude>26,34.951N</exif:GPSLatitude>
<exif:GPSLongitude>80,12.014W</exif:GPSLongitude>
</rdf:RDF>`

	f := &File{
		Path:   path,
		Exists: false,
		File: gallerydb.File{
			Mtime:     sql.NullInt64{Int64: 1700000000, Valid: true},
			SizeBytes: sql.NullInt64{Int64: 1024, Valid: true},
			MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
			Md5:       sql.NullString{String: "md5xmp", Valid: true},
			Phash:     sql.NullInt64{Int64: 123, Valid: true},
			Width:     sql.NullInt64{Int64: 100, Valid: true},
			Height:    sql.NullInt64{Int64: 100, Valid: true},
		},
		XmpRaw: gallerydb.UpsertXMPRawParams{
			RawXml: sql.NullString{String: rawXML, Valid: true},
		},
		XmpProps: []gallerydb.UpsertXMPPropertyParams{
			{Namespace: "exif", Property: "GPSLatitude", Value: sql.NullString{String: "26,34.951N", Valid: true}},
			{Namespace: "exif", Property: "GPSLongitude", Value: sql.NullString{String: "80,12.014W", Valid: true}},
		},
	}

	qtx := cpcRw.Queries.WithTx(tx)
	imp := &gallerylib.Importer{Q: qtx}
	if err := WriteFileInTx(ctx, imp, f); err != nil {
		t.Fatalf("WriteFileInTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	fileID := f.File.ID
	if fileID == 0 {
		t.Fatal("file ID not set")
	}

	raw, err := cpcRw.Queries.GetXMPRaw(ctx, fileID)
	if err != nil {
		t.Fatalf("GetXMPRaw: %v", err)
	}
	if !raw.RawXml.Valid || raw.RawXml.String != rawXML {
		t.Errorf("raw XML mismatch: got %q", raw.RawXml.String)
	}

	props, err := cpcRw.Queries.GetXMPPropertiesByFile(ctx, fileID)
	if err != nil {
		t.Fatalf("GetXMPPropertiesByFile: %v", err)
	}
	if len(props) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(props))
	}
	foundLat, foundLon := false, false
	for _, p := range props {
		if p.Namespace == "exif" && p.Property == "GPSLatitude" && p.Value.String == "26,34.951N" {
			foundLat = true
		}
		if p.Namespace == "exif" && p.Property == "GPSLongitude" && p.Value.String == "80,12.014W" {
			foundLon = true
		}
	}
	if !foundLat || !foundLon {
		t.Errorf("missing GPS properties: lat=%v lon=%v", foundLat, foundLon)
	}
}
