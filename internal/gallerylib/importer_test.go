package gallerylib_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	time "time"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"

	"errors"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/lbe/sfpg-go/migrations"
)

// setupTestDB creates a test database with migrations applied.
// Uses production-equivalent DSN configuration from app.configureDatabaseDSN().
func setupTestDB(t *testing.T) (*sql.DB, *gallerydb.CustomQueries, context.Context) {
	tempDir := t.TempDir()
	dbfile := filepath.Join(tempDir, "test_gallery.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")

	// Match production DSN from app.configureDatabaseDSN()
	mmapSize := strconv.Itoa(39 * 1024 * 1024 * 1024)
	params := []string{
		"_cache_size=10240",
		"_pragma=cache(shared)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=temp_store(memory)",
		"_pragma=foreign_keys(true)",
		"_pragma=mmap_size(" + mmapSize + ")",
		"_txlock=deferred",
	}
	dsn := filepath.ToSlash(dbfile) + "?" + strings.Join(params, "&")

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		t.Fatalf("sqlite.WithInstance: %v", err)
	}
	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		t.Fatalf("failed to create iofs source driver: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}
	if migErr := m.Up(); migErr != nil && !errors.Is(migErr, migrate.ErrNoChange) {
		t.Fatal(err)
	}
	m2, err := migrations.NewThumbsMigrator(thumbsDBPath)
	if err != nil {
		t.Fatalf("NewThumbsMigrator: %v", err)
	}
	if thumbsErr := m2.Up(); thumbsErr != nil && !errors.Is(thumbsErr, migrate.ErrNoChange) {
		m2.Close()
		t.Fatalf("thumbs migrate: %v", thumbsErr)
	}
	m2.Close()
	if _, attachErr := db.ExecContext(context.Background(),
		fmt.Sprintf("ATTACH DATABASE 'file:%s' AS thumbs", filepath.ToSlash(thumbsDBPath))); attachErr != nil {
		t.Fatalf("ATTACH thumbs: %v", attachErr)
	}
	ctx := context.Background()
	q, err := gallerydb.PrepareCustomQueries(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	return db, q, ctx
}

func createTestThumbnail(t *testing.T, q *gallerydb.CustomQueries, ctx context.Context, folderName, fileName string) int64 {
	t.Helper()
	now := time.Now().Unix()

	folderPath := "/thumbs/" + folderName
	folderPathID, err := q.UpsertFolderPathReturningID(ctx, folderPath)
	if err != nil {
		t.Fatalf("UpsertFolderPathReturningID failed: %v", err)
	}
	folder, err := q.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{},
		PathID:    folderPathID,
		Name:      folderName,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertFolderReturningFolder failed: %v", err)
	}

	filePath := folderPath + "/" + fileName
	filePathID, err := q.UpsertFilePathReturningID(ctx, filePath)
	if err != nil {
		t.Fatalf("UpsertFilePathReturningID failed: %v", err)
	}
	file, err := q.UpsertFileReturningFile(ctx, gallerydb.UpsertFileReturningFileParams{
		FolderID:  sql.NullInt64{Int64: folder.ID, Valid: true},
		PathID:    filePathID,
		Filename:  fileName,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertFileReturningFile failed: %v", err)
	}

	thumbID, err := q.UpsertThumbnailReturningID(ctx, gallerydb.UpsertThumbnailReturningIDParams{
		FileID:    file.ID,
		SizeLabel: "test",
		Width:     1,
		Height:    1,
		Format:    "jpeg",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertThumbnailReturningID failed: %v", err)
	}

	return thumbID
}

// createRootFolderMockQueries wraps a real *gallerydb.CustomQueries and lets
// tests override individual methods used by CreateRootFolderEntry.
type createRootFolderMockQueries struct {
	*gallerydb.CustomQueries
	firstGetErr            error
	upsertPathErr          error
	upsertFolderErr        error
	upsertFolderZeroID     bool
	fallbackGetErr         error
	fallbackGetID          int64
	getFolderIDByPathCalls int
}

func (m *createRootFolderMockQueries) GetFolderIDByPath(ctx context.Context, path string) (int64, error) {
	m.getFolderIDByPathCalls++
	if m.getFolderIDByPathCalls == 1 && m.firstGetErr != nil {
		return 0, m.firstGetErr
	}
	if m.getFolderIDByPathCalls > 1 {
		if m.fallbackGetErr != nil {
			return 0, m.fallbackGetErr
		}
		if m.fallbackGetID != 0 {
			return m.fallbackGetID, nil
		}
	}
	return m.CustomQueries.GetFolderIDByPath(ctx, path)
}

func (m *createRootFolderMockQueries) UpsertFolderPathReturningID(ctx context.Context, path string) (int64, error) {
	if m.upsertPathErr != nil {
		return 0, m.upsertPathErr
	}
	return m.CustomQueries.UpsertFolderPathReturningID(ctx, path)
}

func (m *createRootFolderMockQueries) UpsertFolderReturningFolder(ctx context.Context, arg gallerydb.UpsertFolderReturningFolderParams) (gallerydb.Folder, error) {
	if m.upsertFolderErr != nil {
		return gallerydb.Folder{}, m.upsertFolderErr
	}
	if m.upsertFolderZeroID {
		return gallerydb.Folder{ID: 0}, nil
	}
	return m.CustomQueries.UpsertFolderReturningFolder(ctx, arg)
}

func TestCreateRootFolderEntry_GetFolderIDByPathNonNoRowsError(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	mock := &createRootFolderMockQueries{
		CustomQueries: q,
		firstGetErr:   errors.New("lookup failed"),
	}
	imp := &gallerylib.Importer{Q: mock}

	_, err := imp.CreateRootFolderEntry(ctx, time.Now().Unix())
	if err == nil {
		t.Fatal("expected error when GetFolderIDByPath fails with non-ErrNoRows error")
	}
	if !strings.Contains(err.Error(), "error checking for existing root folder") {
		t.Fatalf("expected error to wrap 'error checking for existing root folder', got: %v", err)
	}
}

func TestCreateRootFolderEntry_UpsertFolderPathReturningIDFails(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	mock := &createRootFolderMockQueries{
		CustomQueries: q,
		firstGetErr:   sql.ErrNoRows,
		upsertPathErr: errors.New("upsert path failed"),
	}
	imp := &gallerylib.Importer{Q: mock}

	_, err := imp.CreateRootFolderEntry(ctx, time.Now().Unix())
	if err == nil {
		t.Fatal("expected error when UpsertFolderPathReturningID fails")
	}
	if !strings.Contains(err.Error(), "error upserting root folder path") {
		t.Fatalf("expected error to wrap 'error upserting root folder path', got: %v", err)
	}
}

func TestCreateRootFolderEntry_UpsertFolderReturningFolderFails(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	mock := &createRootFolderMockQueries{
		CustomQueries:   q,
		firstGetErr:     sql.ErrNoRows,
		upsertFolderErr: errors.New("upsert folder failed"),
	}
	imp := &gallerylib.Importer{Q: mock}

	_, err := imp.CreateRootFolderEntry(ctx, time.Now().Unix())
	if err == nil {
		t.Fatal("expected error when UpsertFolderReturningFolder fails")
	}
	if !strings.Contains(err.Error(), "error upserting root folder") {
		t.Fatalf("expected error to wrap 'error upserting root folder', got: %v", err)
	}
}

func TestCreateRootFolderEntry_DefensiveFallbackSucceeds(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	mock := &createRootFolderMockQueries{
		CustomQueries:      q,
		firstGetErr:        sql.ErrNoRows,
		upsertFolderZeroID: true,
		fallbackGetID:      42,
	}
	imp := &gallerylib.Importer{Q: mock}

	id, err := imp.CreateRootFolderEntry(ctx, time.Now().Unix())
	if err != nil {
		t.Fatalf("expected no error when defensive fallback succeeds, got: %v", err)
	}
	if id != 42 {
		t.Fatalf("expected fallback ID 42, got %d", id)
	}
	if mock.getFolderIDByPathCalls != 2 {
		t.Fatalf("expected 2 GetFolderIDByPath calls, got %d", mock.getFolderIDByPathCalls)
	}
}

func TestCreateRootFolderEntry_DefensiveFallbackFails(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	mock := &createRootFolderMockQueries{
		CustomQueries:      q,
		firstGetErr:        sql.ErrNoRows,
		upsertFolderZeroID: true,
		fallbackGetErr:     errors.New("fallback failed"),
	}
	imp := &gallerylib.Importer{Q: mock}

	_, err := imp.CreateRootFolderEntry(ctx, time.Now().Unix())
	if err == nil {
		t.Fatal("expected error when defensive fallback fails")
	}
	if !strings.Contains(err.Error(), "failed to obtain root folder id after upsert") {
		t.Fatalf("expected error to wrap 'failed to obtain root folder id after upsert', got: %v", err)
	}
}

func TestIsDirTiled_UnmarkedDir(t *testing.T) {
	imp := &gallerylib.Importer{}
	if imp.IsDirTiled("/any/dir") {
		t.Fatal("expected unmarked directory to report not tiled")
	}
}

func TestIsDirTiled_MarkedDir(t *testing.T) {
	imp := &gallerylib.Importer{}
	imp.MarkDirTiled("/photos")
	if !imp.IsDirTiled("/photos") {
		t.Fatal("expected marked directory to report tiled")
	}
}

func TestIsDirTiled_MarkedDir_OtherDir(t *testing.T) {
	imp := &gallerylib.Importer{}
	imp.MarkDirTiled("/photos")
	if imp.IsDirTiled("/other") {
		t.Fatal("expected unrelated directory to report not tiled")
	}
}

func TestMarkDirTiled_LazilyInitializes(t *testing.T) {
	imp := &gallerylib.Importer{}
	imp.MarkDirTiled("/photos")
	if !imp.IsDirTiled("/photos") {
		t.Fatal("expected MarkDirTiled to lazily initialize tiledDirs and mark the directory")
	}
}

func TestMarkDirTiled_Idempotent(t *testing.T) {
	imp := &gallerylib.Importer{}
	imp.MarkDirTiled("/photos")
	if !imp.IsDirTiled("/photos") {
		t.Fatal("expected directory to be tiled after first mark")
	}
	imp.MarkDirTiled("/photos")
	if !imp.IsDirTiled("/photos") {
		t.Fatal("expected directory to remain tiled after second mark")
	}
}

func TestUpsertPathChain(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	imp := &gallerylib.Importer{Q: q}
	path := filepath.Join(string(os.PathSeparator), "photos", "2025", "vacation", "beach.jpg")

	var f gallerydb.File

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if rbErr := tx.Rollback(); rbErr != nil {
				t.Logf("tx.Rollback: %v", rbErr)
			}
		}()
		imp.Q = q.WithTx(tx)

		// first call should insert
		mtime := time.Now().Unix()
		size := int64(1024)
		md5 := "md5hash"
		phash := int64(12345)
		width := int64(1920)
		height := int64(1080)
		mime := "image/jpeg"
		f, err = imp.UpsertPathChain(ctx, path, mtime, size, md5, phash, width, height, mime)
		if err != nil {
			t.Fatal(err)
		}
		if !f.MimeType.Valid || f.MimeType.String != mime {
			t.Errorf("expected MimeType %s, got %v", mime, f.MimeType)
		}
		if f.Filename != "beach.jpg" {
			t.Fatalf("expected filename beach.jpg, got %s", f.Filename)
		}
		if f.Mtime.Int64 != mtime {
			t.Fatalf("expected mtime %d, got %d", mtime, f.Mtime.Int64)
		}
		if f.SizeBytes.Int64 != size {
			t.Fatalf("expected size %d, got %d", size, f.SizeBytes.Int64)
		}
		if f.Md5.String != md5 {
			t.Fatalf("expected md5 %s, got %s", md5, f.Md5.String)
		}
		if f.Phash.Int64 != phash {
			t.Fatalf("expected phash %d, got %d", phash, f.Phash.Int64)
		}
		if f.MimeType.String != mime {
			t.Fatalf("expected mime %s, got %s", mime, f.MimeType.String)
		}
		if cmtErr := tx.Commit(); cmtErr != nil {
			t.Fatalf("tx.Commit: %v", cmtErr)
		}
	}()

	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if rbErr := tx.Rollback(); rbErr != nil {
				t.Logf("tx.Rollback: %v", rbErr)
			}
		}()
		imp.Q = q.WithTx(tx)

		// call again should not duplicate
		mtime := time.Now().Unix()
		size := int64(1024)
		md5 := "md5hash"
		phash := int64(12345)
		width := int64(1920)
		height := int64(1080)
		mime := "image/jpeg"
		f2, err := imp.UpsertPathChain(ctx, path, mtime, size, md5, phash, width, height, mime)
		if err != nil {
			t.Fatal(err)
		}
		if f2.ID != f.ID {
			t.Fatalf("expected same file id %d, got %d", f.ID, f2.ID)
		}
		if cmtErr := tx.Commit(); cmtErr != nil {
			t.Fatalf("tx.Commit: %v", cmtErr)
		}
	}()
}

func TestUpsertPathChain_OnFolderCreated(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	var folderCreatedCalls int
	imp := &gallerylib.Importer{
		Q: q,
		OnFolderCreated: func() {
			folderCreatedCalls++
		},
	}

	path := filepath.Join(string(os.PathSeparator), "photos", "2025", "beach.jpg")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil {
			t.Logf("tx.Rollback: %v", rbErr)
		}
	}()
	imp.Q = q.WithTx(tx)

	mtime := time.Now().Unix()
	_, err = imp.UpsertPathChain(ctx, path, mtime, 1024, "md5", 12345, 1920, 1080, "image/jpeg")
	if err != nil {
		t.Fatalf("UpsertPathChain: %v", err)
	}

	// The path "photos/2025/beach.jpg" should create folders "photos" and "2025".
	if folderCreatedCalls != 2 {
		t.Errorf("OnFolderCreated called %d times, want 2 (photos + 2025)", folderCreatedCalls)
	}
}

func TestRootFile(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	imp := &gallerylib.Importer{Q: q}
	path := filepath.Join(string(os.PathSeparator), "lonely.jpg")

	mtime := time.Now().Unix()
	size := int64(1024)
	md5 := "md5hash"
	phash := int64(12345)
	width := int64(800)
	height := int64(600)
	mime := "image/jpeg"
	f, err := imp.UpsertPathChain(ctx, path, mtime, size, md5, phash, width, height, mime)
	if err != nil {
		t.Fatal(err)
	}
	if f.Filename != "lonely.jpg" {
		t.Fatalf("expected lonely.jpg got %s", f.Filename)
	}
	if !f.FolderID.Valid || f.FolderID.Int64 != 1 {
		t.Fatalf("expected FolderID to be Valid=true with Int64=1, got %+v", f.FolderID)
	}
}

func TestCreateRootFolderEntry(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	imp := &gallerylib.Importer{Q: q}

	testMtime := time.Now().Unix()

	// 1. First call should succeed
	rootFolderID, err := imp.CreateRootFolderEntry(ctx, testMtime)
	if err != nil {
		t.Fatalf("First call to CreateRootFolderEntry failed: %v", err)
	}
	if rootFolderID == 0 {
		t.Fatal("Expected a non-zero folder ID")
	}

	// Verify it's in the DB
	retrieved, err := q.GetFolderByID(ctx, rootFolderID)
	if err != nil {
		t.Fatalf("Failed to retrieve created root folder: %v", err)
	}
	if retrieved.Name != "Gallery" {
		t.Errorf("Retrieved folder has wrong name")
	}

	// 2. Second call should succeed and return the same ID (idempotent)
	secondID, err := imp.CreateRootFolderEntry(ctx, testMtime)
	if err != nil {
		t.Fatalf("Second call to CreateRootFolderEntry failed: %v", err)
	}
	if secondID != rootFolderID {
		t.Fatalf("Expected same ID on second call: first=%d second=%d", rootFolderID, secondID)
	}
}

func TestCreateRootFolderEntry_ContextError(t *testing.T) {
	db, q, _ := setupTestDB(t)
	defer db.Close()

	imp := &gallerylib.Importer{Q: q}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := imp.CreateRootFolderEntry(ctx, time.Now().Unix()); err == nil {
		t.Fatal("expected error with canceled context")
	}
}

func TestCreateRootFolderEntry_ReadOnlyDB(t *testing.T) {
	tempDir := t.TempDir()
	dbfile := filepath.Join(tempDir, "test_gallery_ro.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")

	baseDB, err := sql.Open("sqlite3", dbfile)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := sqlite.WithInstance(baseDB, &sqlite.Config{})
	if err != nil {
		t.Fatalf("sqlite.WithInstance: %v", err)
	}
	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		baseDB.Close()
		t.Fatalf("failed to create iofs source driver: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
	if err != nil {
		baseDB.Close()
		t.Fatal(err)
	}
	if migErr := m.Up(); migErr != nil && !errors.Is(migErr, migrate.ErrNoChange) {
		baseDB.Close()
		t.Fatal(migErr)
	}
	if err = baseDB.Close(); err != nil {
		t.Fatal(err)
	}
	m2, err := migrations.NewThumbsMigrator(thumbsDBPath)
	if err != nil {
		t.Fatalf("NewThumbsMigrator: %v", err)
	}
	if thumbsErr := m2.Up(); thumbsErr != nil && !errors.Is(thumbsErr, migrate.ErrNoChange) {
		m2.Close()
		t.Fatalf("thumbs migrate: %v", thumbsErr)
	}
	m2.Close()

	// Read-only DSN - no pragmas needed for read-only mode
	readOnlyDSN := "file:" + filepath.ToSlash(dbfile) + "?mode=ro"
	roDB, err := sql.Open("sqlite3", readOnlyDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer roDB.Close()
	if _, attachErr := roDB.ExecContext(context.Background(),
		fmt.Sprintf("ATTACH DATABASE 'file:%s' AS thumbs", filepath.ToSlash(thumbsDBPath))); attachErr != nil {
		t.Fatalf("ATTACH thumbs: %v", attachErr)
	}
	ctx := context.Background()
	q, err := gallerydb.PrepareCustomQueries(ctx, roDB)
	if err != nil {
		t.Fatal(err)
	}
	imp := &gallerylib.Importer{Q: q}

	if _, err := imp.CreateRootFolderEntry(ctx, time.Now().Unix()); err == nil {
		t.Fatal("expected error with read-only database")
	}
}

func TestUpdateFolderTileChain_SingleFolder(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	imp := &gallerylib.Importer{Q: q}

	// Create a folder
	folderPathID, err := q.UpsertFolderPathReturningID(ctx, "/test_folder")
	if err != nil {
		t.Fatal(err)
	}
	folder, err := q.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{},
		PathID:    folderPathID,
		Name:      "test_folder",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	thumbnailID := createTestThumbnail(t, q, ctx, "single", "tile.jpg")

	// Call UpdateFolderTileChain
	err = imp.UpdateFolderTileChain(ctx, folder.ID, thumbnailID)
	if err != nil {
		t.Fatalf("UpdateFolderTileChain failed: %v", err)
	}

	// Verify tile_id is updated
	updatedFolder, err := q.GetFolderByID(ctx, folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updatedFolder.TileID.Valid || updatedFolder.TileID.Int64 != thumbnailID {
		t.Errorf("Expected folder tile_id to be %d, got %v", thumbnailID, updatedFolder.TileID)
	}
}

func TestUpdateFolderTileChain_HierarchyUntilExistingTile(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	imp := &gallerylib.Importer{Q: q}

	// Create hierarchy: root -> parent -> child
	rootPathID, err := q.UpsertFolderPathReturningID(ctx, "/root")
	if err != nil {
		t.Fatal(err)
	}
	rootFolder, err := q.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{},
		PathID:    rootPathID,
		Name:      "root",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set an initial tile_id for the root folder using a real thumbnail
	initialRootTileID := createTestThumbnail(t, q, ctx, "root", "root_tile.jpg")
	err = q.UpdateFolderTileId(ctx, gallerydb.UpdateFolderTileIdParams{
		TileID: sql.NullInt64{Int64: initialRootTileID, Valid: true},
		ID:     rootFolder.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	parentPathID, err := q.UpsertFolderPathReturningID(ctx, "/root/parent")
	if err != nil {
		t.Fatal(err)
	}
	parentFolder, err := q.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: rootFolder.ID, Valid: true},
		PathID:    parentPathID,
		Name:      "parent",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	childPathID, err := q.UpsertFolderPathReturningID(ctx, "/root/parent/child")
	if err != nil {
		t.Fatal(err)
	}
	childFolder, err := q.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: parentFolder.ID, Valid: true},
		PathID:    childPathID,
		Name:      "child",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Call UpdateFolderTileChain for the child folder
	newThumbnailID := createTestThumbnail(t, q, ctx, "child", "child_tile.jpg")
	err = imp.UpdateFolderTileChain(ctx, childFolder.ID, newThumbnailID)
	if err != nil {
		t.Fatalf("UpdateFolderTileChain failed: %v", err)
	}

	// Verify tile_id for child and parent are updated
	updatedChild, err := q.GetFolderByID(ctx, childFolder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updatedChild.TileID.Valid || updatedChild.TileID.Int64 != newThumbnailID {
		t.Errorf("Expected child folder tile_id to be %d, got %v", newThumbnailID, updatedChild.TileID)
	}

	updatedParent, err := q.GetFolderByID(ctx, parentFolder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updatedParent.TileID.Valid || updatedParent.TileID.Int64 != newThumbnailID {
		t.Errorf("Expected parent folder tile_id to be %d, got %v", newThumbnailID, updatedParent.TileID)
	}

	// Verify root folder's tile_id remains unchanged
	updatedRoot, err := q.GetFolderByID(ctx, rootFolder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updatedRoot.TileID.Valid || updatedRoot.TileID.Int64 != initialRootTileID {
		t.Errorf("Expected root folder tile_id to be %d, got %v", initialRootTileID, updatedRoot.TileID)
	}
}

func TestUpdateFolderTileChain_HierarchyToRoot(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	imp := &gallerylib.Importer{Q: q}

	// Create hierarchy: root -> parent -> child (no initial tile_ids)
	rootPathID, err := q.UpsertFolderPathReturningID(ctx, "/root_no_tile")
	if err != nil {
		t.Fatal(err)
	}
	rootFolder, err := q.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{},
		PathID:    rootPathID,
		Name:      "root_no_tile",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	parentPathID, err := q.UpsertFolderPathReturningID(ctx, "/root_no_tile/parent")
	if err != nil {
		t.Fatal(err)
	}
	parentFolder, err := q.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: rootFolder.ID, Valid: true},
		PathID:    parentPathID,
		Name:      "parent",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	childPathID, err := q.UpsertFolderPathReturningID(ctx, "/root_no_tile/parent/child")
	if err != nil {
		t.Fatal(err)
	}
	childFolder, err := q.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: parentFolder.ID, Valid: true},
		PathID:    childPathID,
		Name:      "child",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Call UpdateFolderTileChain for the child folder
	newThumbnailID := createTestThumbnail(t, q, ctx, "child_root", "child_tile.jpg")
	err = imp.UpdateFolderTileChain(ctx, childFolder.ID, newThumbnailID)
	if err != nil {
		t.Fatalf("UpdateFolderTileChain failed: %v", err)
	}

	// Verify tile_id for child, parent, and root are updated
	updatedChild, err := q.GetFolderByID(ctx, childFolder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updatedChild.TileID.Valid || updatedChild.TileID.Int64 != newThumbnailID {
		t.Errorf("Expected child folder tile_id to be %d, got %v", newThumbnailID, updatedChild.TileID)
	}

	updatedParent, err := q.GetFolderByID(ctx, parentFolder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updatedParent.TileID.Valid || updatedParent.TileID.Int64 != newThumbnailID {
		t.Errorf("Expected parent folder tile_id to be %d, got %v", newThumbnailID, updatedParent.TileID)
	}

	updatedRoot, err := q.GetFolderByID(ctx, rootFolder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updatedRoot.TileID.Valid || updatedRoot.TileID.Int64 != newThumbnailID {
		t.Errorf("Expected root folder tile_id to be %d, got %v", newThumbnailID, updatedRoot.TileID)
	}
}

func TestUpdateFolderTileChain_RootFolder(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	imp := &gallerylib.Importer{Q: q}

	// Create a root folder
	rootPathID, err := q.UpsertFolderPathReturningID(ctx, "/single_root")
	if err != nil {
		t.Fatal(err)
	}
	rootFolder, err := q.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{},
		PathID:    rootPathID,
		Name:      "single_root",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Call UpdateFolderTileChain for the root folder
	newThumbnailID := createTestThumbnail(t, q, ctx, "root_only", "root_tile.jpg")
	err = imp.UpdateFolderTileChain(ctx, rootFolder.ID, newThumbnailID)
	if err != nil {
		t.Fatalf("UpdateFolderTileChain failed: %v", err)
	}

	// Verify its tile_id is updated
	updatedRoot, err := q.GetFolderByID(ctx, rootFolder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updatedRoot.TileID.Valid || updatedRoot.TileID.Int64 != newThumbnailID {
		t.Errorf("Expected root folder tile_id to be %d, got %v", newThumbnailID, updatedRoot.TileID)
	}
}

func TestUpdateFolderTileChain_MissingFolderNoError(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	imp := &gallerylib.Importer{Q: q}

	if err := imp.UpdateFolderTileChain(ctx, 99999, 1); err != nil {
		t.Fatalf("expected nil error for missing folder, got %v", err)
	}
}

func TestUpdateFolderTileChain_ExistingTileStops(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	imp := &gallerylib.Importer{Q: q}

	folderPathID, err := q.UpsertFolderPathReturningID(ctx, "/tile_stop")
	if err != nil {
		t.Fatal(err)
	}
	folder, err := q.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{},
		PathID:    folderPathID,
		Name:      "tile_stop",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	initialTileID := createTestThumbnail(t, q, ctx, "stop", "tile.jpg")
	if err = q.UpdateFolderTileId(ctx, gallerydb.UpdateFolderTileIdParams{
		TileID: sql.NullInt64{Int64: initialTileID, Valid: true},
		ID:     folder.ID,
	}); err != nil {
		t.Fatal(err)
	}

	if err = imp.UpdateFolderTileChain(ctx, folder.ID, initialTileID+1); err != nil {
		t.Fatalf("UpdateFolderTileChain failed: %v", err)
	}

	updated, err := q.GetFolderByID(ctx, folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.TileID.Valid || updated.TileID.Int64 != initialTileID {
		t.Fatalf("expected tile to remain %d, got %v", initialTileID, updated.TileID)
	}
}

func TestUpdateFolderTileChain_GetFolderError(t *testing.T) {
	db, q, ctx := setupTestDB(t)

	imp := &gallerylib.Importer{Q: q}

	folderPathID, err := q.UpsertFolderPathReturningID(ctx, "/error_folder")
	if err != nil {
		t.Fatal(err)
	}
	folder, err := q.UpsertFolderReturningFolder(ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{},
		PathID:    folderPathID,
		Name:      "error_folder",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := imp.UpdateFolderTileChain(ctx, folder.ID, 1); err == nil {
		t.Fatal("expected error after db close")
	}
}

// TestCreateRootFolderEntry_StoreMtime verifies that root folder mtime is extracted and persisted.
func TestCreateRootFolderEntry_StoreMtime(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	// Create a temp directory to use as root
	rootDir := t.TempDir()

	// Get the directory's actual mtime from filesystem
	stat, err := os.Stat(rootDir)
	if err != nil {
		t.Fatalf("failed to stat temp dir: %v", err)
	}
	expectedMtime := stat.ModTime().Unix()

	imp := &gallerylib.Importer{Q: q}

	// Call CreateRootFolderEntry with mtime
	rootID, err := imp.CreateRootFolderEntry(ctx, expectedMtime)
	if err != nil {
		t.Fatalf("CreateRootFolderEntry failed: %v", err)
	}

	if rootID == 0 {
		t.Fatal("expected non-zero root folder ID")
	}

	// Retrieve the root folder and verify mtime was stored
	rootFolderView, err := q.GetFolderViewByID(ctx, rootID)
	if err != nil {
		t.Fatalf("failed to get root folder view: %v", err)
	}

	if !rootFolderView.Mtime.Valid {
		t.Error("expected root folder Mtime to be valid, got NULL")
	} else if rootFolderView.Mtime.Int64 != expectedMtime {
		t.Errorf("expected root folder mtime %d, got %d", expectedMtime, rootFolderView.Mtime.Int64)
	}
}

// TestUpsertPathChain_FolderMtime verifies that intermediate folder mtimes are extracted and stored.
func TestUpsertPathChain_FolderMtime(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	imp := &gallerylib.Importer{Q: q}

	// Create root folder first
	rootMtime := time.Now().Unix()
	rootID, err := imp.CreateRootFolderEntry(ctx, rootMtime)
	if err != nil {
		t.Fatalf("CreateRootFolderEntry failed: %v", err)
	}

	// Create a subdirectory structure using UpsertPathChain with a real filesystem path
	rootDir := t.TempDir()
	subDir := filepath.Join(rootDir, "subfolder")
	err = os.Mkdir(subDir, 0o755)
	if err != nil {
		t.Fatalf("failed to create subdirectory: %v", err)
	}

	// Create a file in the subdirectory
	filePath := filepath.Join(subDir, "testfile.jpg")
	err = os.WriteFile(filePath, []byte("test content"), 0o644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Get file stat for mtime
	fileStat, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}
	fileMtime := fileStat.ModTime().Unix()

	// Upsert the path chain (should create intermediate folder with mtime)
	normalizedPath := filepath.ToSlash(filepath.Clean(filePath))
	file, err := imp.UpsertPathChain(ctx, normalizedPath, fileMtime, 100, "abc123", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("UpsertPathChain failed: %v", err)
	}

	if file.ID == 0 {
		t.Fatal("expected non-zero file ID")
	}

	// Get the intermediate folder and verify mtime was stored
	folders, err := q.GetFoldersViewsByParentIDOrderByName(ctx, sql.NullInt64{Int64: rootID, Valid: true})
	if err != nil {
		t.Fatalf("failed to get folders: %v", err)
	}

	if len(folders) != 1 {
		t.Fatalf("expected 1 intermediate folder, got %d", len(folders))
	}

	subFolder := folders[0]
	if !subFolder.Mtime.Valid {
		t.Error("expected intermediate folder Mtime to be valid, got NULL")
	} else if subFolder.Mtime.Int64 == 0 {
		t.Error("expected intermediate folder mtime to be non-zero")
	}
}
