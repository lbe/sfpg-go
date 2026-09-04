//go:build integration

package gallerydb

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/lbe/sfpg-go/migrations"
)

// openMemoryDB returns an un-migrated :memory: SQLite database.
func openMemoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open :memory: database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// openMigratedMemoryDB returns a :memory: SQLite database with main migrations applied.
func openMigratedMemoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMemoryDB(t)
	applyMainMigrations(t, db)
	return db
}

func applyMainMigrations(t *testing.T, db *sql.DB) {
	t.Helper()
	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		t.Fatalf("failed to create sqlite driver instance: %v", err)
	}
	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		t.Fatalf("failed to create iofs source driver: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
	if err != nil {
		t.Fatalf("failed to create migrate instance: %v", err)
	}
	if migErr := m.Up(); migErr != nil && migErr != migrate.ErrNoChange {
		t.Fatalf("failed to apply migrations: %v", migErr)
	}
}

// openMigratedAttachedMemoryDB returns a :memory: SQLite database with main
// migrations applied and a temp-file thumbs.db attached as "thumbs".
func openMigratedAttachedMemoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMemoryDB(t)
	applyMainMigrations(t, db)

	thumbsDBPath := filepath.Join(t.TempDir(), "thumbs.db")
	thumbsMigrator, err := migrations.NewThumbsMigrator(thumbsDBPath)
	if err != nil {
		t.Fatalf("failed to create thumbs migrator: %v", err)
	}
	if thumbsErr := thumbsMigrator.Up(); thumbsErr != nil && thumbsErr != migrate.ErrNoChange {
		t.Fatalf("failed to apply thumbs migrations: %v", thumbsErr)
	}
	thumbsMigrator.Close()

	if _, err := db.ExecContext(context.Background(),
		"ATTACH DATABASE 'file:"+filepath.ToSlash(thumbsDBPath)+"' AS thumbs"); err != nil {
		t.Fatalf("failed to attach thumbs: %v", err)
	}
	return db
}

// setupAttachedTestDB returns a migrated DB with thumbs.db attached and a
// prepared *CustomQueries. It is like setupTestDB but keeps the raw *sql.DB
// reference so callers can also run raw SQL for seed data.
func setupAttachedTestDB(t *testing.T) (*sql.DB, *CustomQueries, context.Context) {
	t.Helper()

	tempDir := t.TempDir()
	mainDBPath := filepath.Join(tempDir, "test.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")

	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(mainDBPath))
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		t.Fatalf("failed to create sqlite driver instance: %v", err)
	}
	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		t.Fatalf("failed to create iofs source driver: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
	if err != nil {
		t.Fatalf("failed to create migrate instance: %v", err)
	}
	if migErr := m.Up(); migErr != nil && migErr != migrate.ErrNoChange {
		t.Fatalf("failed to apply migrations: %v", migErr)
	}

	thumbsMigrator, err := migrations.NewThumbsMigrator(thumbsDBPath)
	if err != nil {
		t.Fatalf("failed to create thumbs migrator: %v", err)
	}
	if thumbsErr := thumbsMigrator.Up(); thumbsErr != nil && thumbsErr != migrate.ErrNoChange {
		t.Fatalf("failed to apply thumbs migrations: %v", thumbsErr)
	}
	thumbsMigrator.Close()

	if _, err := db.ExecContext(context.Background(),
		"ATTACH DATABASE 'file:"+filepath.ToSlash(thumbsDBPath)+"' AS thumbs"); err != nil {
		t.Fatalf("failed to attach thumbs: %v", err)
	}

	ctx := context.Background()
	q, err := PrepareCustomQueries(ctx, db)
	if err != nil {
		t.Fatalf("failed to prepare queries: %v", err)
	}

	return db, q, ctx
}

func TestPrepare_HappyPath(t *testing.T) {
	db := openMigratedMemoryDB(t)

	q, err := Prepare(context.Background(), db)
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if q.clearHttpCacheStmt == nil {
		t.Error("expected clearHttpCacheStmt to be non-nil")
	}
	if q.getConfigsStmt == nil {
		t.Error("expected getConfigsStmt to be non-nil")
	}
	if q.upsertXMPRawStmt == nil {
		t.Error("expected upsertXMPRawStmt to be non-nil")
	}
}

func TestQueriesClose_HappyPath(t *testing.T) {
	db := openMigratedMemoryDB(t)

	q, err := Prepare(context.Background(), db)
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	if err := q.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

func TestPrepareCustomQueries_HappyPath(t *testing.T) {
	_, q, ctx := setupAttachedTestDB(t)

	if q.getBatchLoadTargetsStmt == nil {
		t.Error("expected getBatchLoadTargetsStmt to be non-nil")
	}
	if q.upsertThumbnailBlobStmt == nil {
		t.Error("expected upsertThumbnailBlobStmt to be non-nil")
	}

	// Ensure the prepared queries are usable.
	_, err := q.GetConfigs(ctx)
	if err != nil {
		t.Errorf("GetConfigs failed: %v", err)
	}
}

func TestPrepareCustomQueries_CustomStatementFails(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"GetBatchLoadTargets", getBatchLoadTargets},
		{"GetFileViewRowsByFolderID", getFileViewRowsByFolderID},
		{"GetFileViewRowsByFolderPath", getFileViewRowsByFolderPath},
		{"GetFolderViewThumbnailBlobDataByPath", getFolderViewThumbnailBlobDataByPath},
		{"GetPreloadRoutesByFolderID", getPreloadRoutesByFolderID},
		{"GetThumbnailBlobDataByID", getThumbnailBlobDataByID},
		{"UpsertThumbnailBlob", upsertThumbnailBlob},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := openMigratedAttachedMemoryDB(t)

			orig := prepareContextFn
			prepareContextFn = func(ctx context.Context, d DBTX, query string) (*sql.Stmt, error) {
				if query == tc.query {
					return nil, errors.New("prepare denied")
				}
				return orig(ctx, d, query)
			}
			t.Cleanup(func() { prepareContextFn = orig })

			_, err := PrepareCustomQueries(context.Background(), db)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			want := "error preparing query " + tc.name
			if !strings.Contains(err.Error(), want) {
				t.Errorf("expected error to contain %q, got %q", want, err.Error())
			}
		})
	}
}

func TestCustomQueriesClose_HappyPath(t *testing.T) {
	_, q, ctx := setupAttachedTestDB(t)
	_ = ctx

	if err := q.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

func TestCustomQueriesClose_CustomStatementCloseFails(t *testing.T) {
	tests := []struct {
		name  string
		stmt  *sql.Stmt
		field string
	}{
		{"getBatchLoadTargetsStmt", nil, "getBatchLoadTargetsStmt"},
		{"getFileViewRowsByFolderIDStmt", nil, "getFileViewRowsByFolderIDStmt"},
		{"getFileViewRowsByFolderPathStmt", nil, "getFileViewRowsByFolderPathStmt"},
		{"getFolderViewThumbnailBlobDataByPathStmt", nil, "getFolderViewThumbnailBlobDataByPathStmt"},
		{"getPreloadRoutesByFolderIDStmt", nil, "getPreloadRoutesByFolderIDStmt"},
		{"getThumbnailBlobDataByIDStmt", nil, "getThumbnailBlobDataByIDStmt"},
		{"upsertThumbnailBlobStmt", nil, "upsertThumbnailBlobStmt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, q, _ := setupAttachedTestDB(t)

			var target *sql.Stmt
			switch tc.name {
			case "getBatchLoadTargetsStmt":
				target = q.getBatchLoadTargetsStmt
			case "getFileViewRowsByFolderIDStmt":
				target = q.getFileViewRowsByFolderIDStmt
			case "getFileViewRowsByFolderPathStmt":
				target = q.getFileViewRowsByFolderPathStmt
			case "getFolderViewThumbnailBlobDataByPathStmt":
				target = q.getFolderViewThumbnailBlobDataByPathStmt
			case "getPreloadRoutesByFolderIDStmt":
				target = q.getPreloadRoutesByFolderIDStmt
			case "getThumbnailBlobDataByIDStmt":
				target = q.getThumbnailBlobDataByIDStmt
			case "upsertThumbnailBlobStmt":
				target = q.upsertThumbnailBlobStmt
			default:
				t.Fatalf("unknown statement field %q", tc.name)
			}
			_ = tc.stmt
			_ = tc.field

			orig := stmtCloseFn
			stmtCloseFn = func(s *sql.Stmt) error {
				if s == target {
					return errors.New("close denied")
				}
				return orig(s)
			}
			t.Cleanup(func() { stmtCloseFn = orig })

			err := q.Close()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			want := "error closing " + tc.name
			if !strings.Contains(err.Error(), want) {
				t.Errorf("expected error to contain %q, got %q", want, err.Error())
			}
		})
	}
}

func TestExec_FallbackRawSQL(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	_, err := q.exec(context.Background(), nil, "INSERT INTO missing_table VALUES (1)")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQuery_FallbackRawSQL(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	_, err := q.query(context.Background(), nil, "SELECT * FROM missing_table")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQueryRow_FallbackRawSQL(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	row := q.queryRow(context.Background(), nil, "SELECT 'x' FROM missing_table")
	var b bool
	if err := row.Scan(&b); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestExec_TransactionBranch(t *testing.T) {
	db := openMigratedMemoryDB(t)
	q, err := Prepare(context.Background(), db)
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	qtx := q.WithTx(tx)
	if err := qtx.UpsertConfigValueOnly(context.Background(), UpsertConfigValueOnlyParams{
		Key:       "tx_exec_key",
		Value:     "tx_exec_value",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("UpsertConfigValueOnly within tx failed: %v", err)
	}
}

func TestQuery_TransactionBranch(t *testing.T) {
	db := openMigratedMemoryDB(t)
	q, err := Prepare(context.Background(), db)
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	qtx := q.WithTx(tx)
	_, err = qtx.GetConfigs(context.Background())
	if err != nil {
		t.Fatalf("GetConfigs within tx failed: %v", err)
	}
}

func TestGetConfigs_QueryError(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	_, err := q.GetConfigs(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetGalleryFileThumbRowsByFolderID_QueryError(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	_, err := q.GetGalleryFileThumbRowsByFolderID(context.Background(), sql.NullInt64{Int64: 1, Valid: true})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetGalleryFolderThumbRowsByParentID_QueryError(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	_, err := q.GetGalleryFolderThumbRowsByParentID(context.Background(), sql.NullInt64{Int64: 1, Valid: true})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetPreloadRoutesByFolderID_QueryError(t *testing.T) {
	db := openMemoryDB(t)
	q := NewCustomQueries(db)

	_, err := q.GetPreloadRoutesByFolderID(context.Background(), sql.NullInt64{Int64: 1, Valid: true})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetHttpCacheOldestCreated_QueryError(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	_, err := q.GetHttpCacheOldestCreated(context.Background(), 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetIPTCKeywords_QueryError(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	_, err := q.GetIPTCKeywords(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetXMPPropertiesByFile_QueryError(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	_, err := q.GetXMPPropertiesByFile(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestConfigKeyExists_QueryError(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	_, err := q.ConfigKeyExists(context.Background(), "key")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestInsertConfigIfNotExists_ExecError(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	err := q.InsertConfigIfNotExists(context.Background(), InsertConfigIfNotExistsParams{
		Key:       "key",
		Value:     "value",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetModuleState_QueryError(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	_, err := q.GetModuleState(context.Background(), "discovery")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSetModuleState_ExecError(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	err := q.SetModuleState(context.Background(), SetModuleStateParams{
		Name:     "discovery",
		IsActive: 1,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestGetBatchLoadTargets_TargetCountFormula verifies the batch load target
// count formula: len == 3*folderCount + 2*fileCount.
func TestGetBatchLoadTargets_TargetCountFormula(t *testing.T) {
	_, q, ctx := setupAttachedTestDB(t)
	const folderCount, fileCount = 2, 3
	folderIDs, fileIDs := seedBatchLoadTargetRowsN(t, q, ctx, folderCount, fileCount)

	targets, err := q.GetBatchLoadTargets(ctx)
	if err != nil {
		t.Fatalf("GetBatchLoadTargets: %v", err)
	}

	want := 3*folderCount + 2*fileCount // 12
	if len(targets) != want {
		t.Fatalf("len(targets)=%d want %d (3*folders + 2*files)", len(targets), want)
	}

	// Per-folder variant rows (use first folder ID)
	fid := folderIDs[0]
	assertTarget(t, targets, "/gallery/"+strconv.FormatInt(fid, 10), "full")
	assertTarget(t, targets, "/gallery/"+strconv.FormatInt(fid, 10), "gallery-content")
	assertTarget(t, targets, "/info/folder/"+strconv.FormatInt(fid, 10), "box_info")

	// Per-file variant rows (use first file ID)
	fileID := fileIDs[0]
	assertTarget(t, targets, "/info/image/"+strconv.FormatInt(fileID, 10), "box_info")
	assertTarget(t, targets, "/lightbox/"+strconv.FormatInt(fileID, 10), "lightbox-ui")
}

func TestGetBatchLoadTargets_RowsCloseError(t *testing.T) {
	_, q, ctx := setupAttachedTestDB(t)
	seedBatchLoadTargetRows(t, q, ctx)

	orig := rowsCloseFn
	rowsCloseFn = func(r *sql.Rows) error { return errors.New("rows close denied") }
	t.Cleanup(func() { rowsCloseFn = orig })

	_, err := q.GetBatchLoadTargets(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetBatchLoadTargets_RowsErrError(t *testing.T) {
	_, q, ctx := setupAttachedTestDB(t)
	seedBatchLoadTargetRows(t, q, ctx)

	orig := rowsErrFn
	rowsErrFn = func(r *sql.Rows) error { return errors.New("rows err denied") }
	t.Cleanup(func() { rowsErrFn = orig })

	_, err := q.GetBatchLoadTargets(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// assertTarget fails if no BatchLoadTarget matches the given path and variant.
func assertTarget(t *testing.T, targets []BatchLoadTarget, path, variant string) {
	t.Helper()
	for _, tt := range targets {
		if tt.Path == path && tt.Variant == variant {
			return
		}
	}
	t.Fatalf("target (path=%q, variant=%q) not found", path, variant)
}

// seedBatchLoadTargetRowsN inserts folderCount folders and fileCount files.
// Returns the folder and file IDs in insertion order.
func seedBatchLoadTargetRowsN(t *testing.T, q *CustomQueries, ctx context.Context, folderCount, fileCount int) (folderIDs, fileIDs []int64) {
	t.Helper()
	now := time.Now().Unix()
	for n := 0; n < folderCount; n++ {
		folderPath := "/batchseed" + strconv.Itoa(n)
		pathID, err := q.UpsertFolderPathReturningID(ctx, folderPath)
		if err != nil {
			t.Fatalf("UpsertFolderPathReturningID(%q): %v", folderPath, err)
		}
		folder, err := q.UpsertFolderReturningFolder(ctx, UpsertFolderReturningFolderParams{
			PathID: pathID, Name: "batchseed" + strconv.Itoa(n), CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertFolderReturningFolder(%q): %v", folderPath, err)
		}
		folderIDs = append(folderIDs, folder.ID)
	}
	for n := 0; n < fileCount; n++ {
		filePath := "/batchseed0/photo" + strconv.Itoa(n) + ".jpg"
		filePathID, err := q.UpsertFilePathReturningID(ctx, filePath)
		if err != nil {
			t.Fatalf("UpsertFilePathReturningID(%q): %v", filePath, err)
		}
		file, err := q.UpsertFileReturningFile(ctx, UpsertFileReturningFileParams{
			FolderID:  sql.NullInt64{Int64: folderIDs[0], Valid: true},
			PathID:    filePathID,
			Filename:  "photo" + strconv.Itoa(n) + ".jpg",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertFileReturningFile(%q): %v", filePath, err)
		}
		fileIDs = append(fileIDs, file.ID)
	}
	return folderIDs, fileIDs
}

// seedBatchLoadTargetRows inserts one folder and one file so GetBatchLoadTargets returns rows.
func seedBatchLoadTargetRows(t *testing.T, q *CustomQueries, ctx context.Context) {
	t.Helper()
	seedBatchLoadTargetRowsN(t, q, ctx, 1, 1)
}
