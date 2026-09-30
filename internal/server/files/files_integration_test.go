//go:build integration

package files

import (
	"context"
	"database/sql"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/server/pathutil"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

func TestProcessDiscoveryWorkerFile_Integration(t *testing.T) {
	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)

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

	destInfo, err := os.Stat(destImgPath)
	if err != nil {
		t.Fatalf("stat destination: %v", err)
	}
	f := &File{
		ImagesDir: imagesDir,
		Path:      destImgName,
		File: gallerydb.File{
			Mtime:     sql.NullInt64{Int64: destInfo.ModTime().Unix(), Valid: true},
			SizeBytes: sql.NullInt64{Int64: destInfo.Size(), Valid: true},
		},
	}

	if err := processDiscoveryWorkerFile(f); err != nil {
		t.Fatalf("processDiscoveryWorkerFile: %v", err)
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

	tx, err := cpcRw.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	qtx := cpcRw.Queries.WithTx(tx)
	imp := &gallerylib.Importer{Q: qtx}
	if err := WriteFileInTx(ctx, imp, f); err != nil {
		_ = tx.Rollback()
		t.Fatalf("WriteFileInTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
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

// TestNewPoolFunc_RunPoolWorkerSuccess verifies pool worker successfully processes a file.
func TestNewPoolFunc_RunPoolWorkerSuccess(t *testing.T) {
	roPool, rwPool, imagesDir, _ := createTestPoolsAndDir(t)
	importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}

	rel := createTestImage(t, imagesDir, "worker-test.jpg")
	full := filepath.ToSlash(filepath.Join(imagesDir, rel))

	q := queue.NewQueue[DiscoveryPathWork](1)
	if err := q.Enqueue(testDiscoveryPath(full)); err != nil {
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

// TestWriteFileInTx_Integration verifies WriteFileInTx writes file data within a transaction.
func TestWriteFileInTx_Integration(t *testing.T) {
	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)

	// Create test image and process it
	path := createTestImage(t, imagesDir, "test_write_tx.jpg")
	processor := NewFileProcessor(roPool, rwPool, func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}, imagesDir, nil)
	t.Cleanup(func() { _ = processor.Close() })

	file, err := processor.ProcessDiscoveryFile(ctx, fileWithWalkMetadata(t, imagesDir, path))
	if err != nil {
		t.Fatalf("ProcessDiscoveryFile: %v", err)
	}
	if file.Thumbnail == nil {
		t.Fatal("ProcessDiscoveryFile did not generate thumbnail")
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
	file, err := processor.ProcessDiscoveryFile(ctx, fileWithWalkMetadata(t, imagesDir, path))
	if err != nil {
		t.Fatalf("ProcessDiscoveryFile: %v", err)
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

// discoveryContentsSpy wraps a FileProcessor and counts worker paths that ran
// processFileContents (thumbnail buffer populated).
type discoveryContentsSpy struct {
	inner              FileProcessor
	contentsExtractedN atomic.Int64
}

func (s *discoveryContentsSpy) ProcessDiscoveryFile(ctx context.Context, file *File) (*File, error) {
	out, err := s.inner.ProcessDiscoveryFile(ctx, file)
	if out != nil && out.Thumbnail != nil && out.Thumbnail.Len() > 0 {
		s.contentsExtractedN.Add(1)
	}
	return out, err
}

func (s *discoveryContentsSpy) RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string, folderID int64) error {
	return s.inner.RecordInvalidFile(ctx, path, mtime, size, reason, folderID)
}

func (s *discoveryContentsSpy) SubmitFileForWrite(file *File) error {
	return s.inner.SubmitFileForWrite(file)
}

func (s *discoveryContentsSpy) PendingWriteCount() int64 {
	return s.inner.PendingWriteCount()
}

func (s *discoveryContentsSpy) Close() error {
	return s.inner.Close()
}

// TestDiscoveryWalk_DirEntCatalog_skipsUnchangedWorkerSQL runs production walk
// wiring with catalog plus discovery workers; unchanged seeded files must not
// enqueue and must not run processFileContents in workers.
func TestDiscoveryWalk_DirEntCatalog_skipsUnchangedWorkerSQL(t *testing.T) {
	const mtimeUnix = int64(1_700_000_450)
	const sizeBytes = int64(96)

	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
	if err := os.MkdirAll(filepath.Join(imagesDir, "album"), 0o755); err != nil {
		t.Fatalf("mkdir album: %v", err)
	}
	unchangedPaths := []string{"album/catalog-skip-1.jpg", "album/catalog-skip-2.jpg", "album/catalog-skip-3.jpg"}
	for _, rel := range unchangedPaths {
		writeWalkTestImage(t, imagesDir, rel, int(sizeBytes), mtimeUnix)
		seedGalleryFileRow(t, ctx, rwPool, rel, mtimeUnix, sizeBytes)
	}

	q := queue.NewQueue[DiscoveryPathWork](32)
	stats := &ProcessingStats{}
	deps := walkDepsWithCatalog(t, imagesDir, roPool, q)
	deps.Stats = stats
	normalized := filepath.ToSlash(imagesDir)

	importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}
	inner := NewFileProcessor(roPool, rwPool, importerFactory, imagesDir, &mockUnifiedBatcher{})
	spy := &discoveryContentsSpy{inner: inner}
	t.Cleanup(func() { _ = spy.Close() })

	pool := workerpool.NewPool(context.Background(), 2, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(2)

	workerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poolFunc := NewPoolFuncWithProcessor(spy, q, normalized, pathutil.RemoveImagesDirPrefix, stats, nil)

	var workerWg sync.WaitGroup
	for id := 1; id <= 2; id++ {
		workerWg.Add(1)
		go func(workerID int) {
			defer workerWg.Done()
			_ = poolFunc(workerCtx, pool, roPool, rwPool, q.Len, workerID)
		}(id)
	}

	WalkImageDir(deps)

	if got := q.Len(); got != 0 {
		t.Fatalf("expected empty discovery queue after catalog walk, got len=%d items=%v", got, q.Slice())
	}

	deadline := time.Now().Add(3 * time.Second)
	for stats.InFlight.Load() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out with InFlight=%d", stats.InFlight.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	workerWg.Wait()

	wantFiles := uint64(len(unchangedPaths))
	if got := stats.TotalFound.Load(); got != wantFiles {
		t.Errorf("TotalFound: got %d, want %d", got, wantFiles)
	}
	if got := stats.AlreadyExisting.Load(); got != wantFiles {
		t.Errorf("AlreadyExisting: got %d, want %d", got, wantFiles)
	}
	assertDiscoveryStatsConservation(t, stats)
	if got := stats.NewlyInserted.Load(); got != 0 {
		t.Errorf("NewlyInserted: got %d, want 0", got)
	}
	if got := spy.contentsExtractedN.Load(); got != 0 {
		t.Errorf("processFileContents invocations: got %d, want 0", got)
	}
	if got := q.Len(); got != 0 {
		t.Errorf("queue len after worker drain: got %d, want 0", got)
	}
}

func TestDiscoveryCatalog_mergeFileAndInvalidRows_integration(t *testing.T) {
	roPool, rwPool, _, ctx := createTestPoolsAndDir(t)
	cpcRw, err := rwPool.Get()
	if err != nil {
		t.Fatalf("get rw: %v", err)
	}
	defer rwPool.Put(cpcRw)

	const galleryPath = "album/photo.jpg"
	folderID := ensureGalleryFolderChain(t, ctx, cpcRw.Queries, galleryPath)
	now := time.Now().Unix()
	pathID, err := cpcRw.Queries.UpsertFilePathReturningID(ctx, galleryPath)
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID: %v", err)
	}
	if _, err := cpcRw.Queries.UpsertFileReturningFile(ctx, gallerydb.UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folderID, Valid: true},
		PathID:    pathID,
		Filename:  "photo.jpg",
		SizeBytes: sql.NullInt64{Int64: 100, Valid: true},
		Md5:       sql.NullString{String: "md5", Valid: true},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertFileReturningFile: %v", err)
	}
	if err := cpcRw.Queries.UpsertInvalidFile(ctx, gallerydb.UpsertInvalidFileParams{
		Path: galleryPath, Mtime: now, Size: 200, FolderID: folderID,
		Reason: sql.NullString{String: "decode", Valid: true},
	}); err != nil {
		t.Fatalf("UpsertInvalidFile: %v", err)
	}

	cpcRo, err := roPool.Get()
	if err != nil {
		t.Fatalf("get ro: %v", err)
	}
	defer roPool.Put(cpcRo)
	folderIDParam := sql.NullInt64{Int64: folderID, Valid: true}
	fileRows, err := cpcRo.Queries.ListDiscoveryFilesByFolderID(ctx, folderIDParam)
	if err != nil {
		t.Fatalf("ListDiscoveryFilesByFolderID: %v", err)
	}
	invalidRows, err := cpcRo.Queries.ListDiscoveryInvalidByFolderID(ctx, folderID)
	if err != nil {
		t.Fatalf("ListDiscoveryInvalidByFolderID: %v", err)
	}
	m := buildDirEntMapFromRows(fileRows, invalidRows)
	key := parallelwalkdir.HashPathBytes([]byte("photo.jpg"))
	st, ok := m[key]
	if !ok {
		t.Fatal("missing merged catalog entry")
	}
	if !st.FileIDValid || !st.FileMD5Valid || !st.InvalidPathValid || st.InvalidSize != 200 {
		t.Fatalf("merged state: %+v", st)
	}
}

func writeMinimalJPEG(path string) error {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, nil)
}

func seedInvalidFileRow(t *testing.T, ctx context.Context, rwPool *dbconnpool.DbSQLConnPool, galleryPath string, mtimeUnix, sizeBytes int64) {
	t.Helper()
	cpcRw, err := rwPool.Get()
	if err != nil {
		t.Fatalf("get RW conn: %v", err)
	}
	defer rwPool.Put(cpcRw)
	folderID := ensureGalleryFolderChain(t, ctx, cpcRw.Queries, galleryPath)
	if err := cpcRw.Queries.UpsertInvalidFile(ctx, gallerydb.UpsertInvalidFileParams{
		Path: galleryPath, Mtime: mtimeUnix, Size: sizeBytes, FolderID: folderID,
		Reason: sql.NullString{String: "decode", Valid: true},
	}); err != nil {
		t.Fatalf("UpsertInvalidFile: %v", err)
	}
}

// TestDiscoveryWalk_DirEntCatalog_skipsUnchangedInvalid_stats counts walk-time SkippedInvalid.
func TestDiscoveryWalk_DirEntCatalog_skipsUnchangedInvalid_stats(t *testing.T) {
	const mtimeUnix = int64(1_700_000_500)
	const sizeBytes = int64(88)
	rel := "album/invalid-skip.jpg"

	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
	if err := os.MkdirAll(filepath.Join(imagesDir, "album"), 0o755); err != nil {
		t.Fatalf("mkdir album: %v", err)
	}
	writeWalkTestImage(t, imagesDir, rel, int(sizeBytes), mtimeUnix)
	seedInvalidFileRow(t, ctx, rwPool, rel, mtimeUnix, sizeBytes)

	q := queue.NewQueue[DiscoveryPathWork](4)
	stats := &ProcessingStats{}
	deps := walkDepsWithCatalog(t, imagesDir, roPool, q)
	deps.Stats = stats
	WalkImageDir(deps)

	if got := q.Len(); got != 0 {
		t.Fatalf("expected empty queue, got len=%d", got)
	}
	if stats.TotalFound.Load() != 1 {
		t.Errorf("TotalFound: got %d, want 1", stats.TotalFound.Load())
	}
	if stats.SkippedInvalid.Load() != 1 {
		t.Errorf("SkippedInvalid: got %d, want 1", stats.SkippedInvalid.Load())
	}
	assertDiscoveryStatsConservation(t, stats)
}

// TestDiscoveryWalk_mixedFixture_conservationIdentity exercises Process, AlreadyExisting, and SkippedInvalid paths.
func TestDiscoveryWalk_mixedFixture_conservationIdentity(t *testing.T) {
	const mtimeUnix = int64(1_700_000_600)
	const sizeBytes = int64(120)

	roPool, rwPool, imagesDir, ctx := createTestPoolsAndDir(t)
	if err := os.MkdirAll(filepath.Join(imagesDir, "mix"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeWalkTestImage(t, imagesDir, "mix/existing.jpg", int(sizeBytes), mtimeUnix)
	seedGalleryFileRow(t, ctx, rwPool, "mix/existing.jpg", mtimeUnix, sizeBytes)

	writeWalkTestImage(t, imagesDir, "mix/invalid.jpg", int(sizeBytes), mtimeUnix)
	seedInvalidFileRow(t, ctx, rwPool, "mix/invalid.jpg", mtimeUnix, sizeBytes)

	dest := writeWalkTestImage(t, imagesDir, "mix/new.jpg", int(sizeBytes), mtimeUnix)
	if err := writeMinimalJPEG(dest); err != nil {
		t.Fatalf("write jpeg: %v", err)
	}

	q := queue.NewQueue[DiscoveryPathWork](8)
	stats := &ProcessingStats{}
	deps := walkDepsWithCatalog(t, imagesDir, roPool, q)
	deps.Stats = stats
	WalkImageDir(deps)

	if stats.TotalFound.Load() != 3 {
		t.Errorf("TotalFound: got %d, want 3", stats.TotalFound.Load())
	}
	if stats.AlreadyExisting.Load() != 1 {
		t.Errorf("AlreadyExisting: got %d, want 1", stats.AlreadyExisting.Load())
	}
	if stats.SkippedInvalid.Load() != 1 {
		t.Errorf("SkippedInvalid: got %d, want 1", stats.SkippedInvalid.Load())
	}
	if got := q.Len(); got != 1 {
		t.Fatalf("expected 1 enqueued new file, got %d", got)
	}

	importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}
	inner := NewFileProcessor(roPool, rwPool, importerFactory, imagesDir, &mockUnifiedBatcher{})
	t.Cleanup(func() { _ = inner.Close() })
	normalized := filepath.ToSlash(imagesDir)
	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)
	workerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	poolFunc := NewPoolFuncWithProcessor(inner, q, normalized, pathutil.RemoveImagesDirPrefix, stats, nil)
	var workerWg sync.WaitGroup
	workerWg.Add(1)
	go func() {
		defer workerWg.Done()
		_ = poolFunc(workerCtx, pool, roPool, rwPool, q.Len, 1)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for q.Len() > 0 || stats.InFlight.Load() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out draining new file: q=%d inFlight=%d", q.Len(), stats.InFlight.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	workerWg.Wait()

	if stats.NewlyInserted.Load() != 1 {
		t.Errorf("NewlyInserted: got %d, want 1", stats.NewlyInserted.Load())
	}
	assertDiscoveryStatsConservation(t, stats)
}

// TestDiscoveryWalk_processingFailure_workerSkippedInvalid counts worker SkippedInvalid after recordInvalidFileFromPath.
func TestDiscoveryWalk_processingFailure_workerSkippedInvalid(t *testing.T) {
	roPool, rwPool, imagesDir, _ := createTestPoolsAndDir(t)
	const mtimeUnix = int64(1_700_000_700)
	rel := "poison-walk.jpg"
	data := []byte{0xFF, 0xD8, 0xFF, 0x00}
	full := writeWalkTestImage(t, imagesDir, rel, len(data), mtimeUnix)
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("write poison: %v", err)
	}

	q := queue.NewQueue[DiscoveryPathWork](4)
	stats := &ProcessingStats{}
	deps := walkDepsWithCatalog(t, imagesDir, roPool, q)
	deps.Stats = stats

	importerFactory := func(conn *sql.Conn, q *gallerydb.CustomQueries) Importer {
		return &gallerylib.Importer{Conn: conn, Q: q}
	}
	inner := NewFileProcessor(roPool, rwPool, importerFactory, imagesDir, &mockUnifiedBatcher{})
	t.Cleanup(func() { _ = inner.Close() })

	normalized := filepath.ToSlash(imagesDir)
	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)
	workerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	poolFunc := NewPoolFuncWithProcessor(inner, q, normalized, pathutil.RemoveImagesDirPrefix, stats, nil)

	var workerWg sync.WaitGroup
	workerWg.Add(1)
	go func() {
		defer workerWg.Done()
		_ = poolFunc(workerCtx, pool, roPool, rwPool, q.Len, 1)
	}()

	WalkImageDir(deps)

	deadline := time.Now().Add(3 * time.Second)
	for stats.InFlight.Load() > 0 || q.Len() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out InFlight=%d q.Len=%d", stats.InFlight.Load(), q.Len())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	workerWg.Wait()

	if stats.TotalFound.Load() != 1 {
		t.Errorf("TotalFound: got %d, want 1", stats.TotalFound.Load())
	}
	if stats.SkippedInvalid.Load() != 1 {
		t.Errorf("SkippedInvalid: got %d, want 1", stats.SkippedInvalid.Load())
	}
	if stats.NewlyInserted.Load() != 0 {
		t.Errorf("NewlyInserted: got %d, want 0", stats.NewlyInserted.Load())
	}
	assertDiscoveryStatsConservation(t, stats)
}
