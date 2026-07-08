//go:build integration

package gallerydb

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
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

func TestPrepare_StatementFails(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"ClearHttpCache", clearHttpCache},
		{"ClearLoginAttempts", clearLoginAttempts},
		{"ConfigKeyExists", configKeyExists},
		{"CountHttpCacheEntries", countHttpCacheEntries},
		{"DeleteHttpCacheByID", deleteHttpCacheByID},
		{"DeleteHttpCacheByKey", deleteHttpCacheByKey},
		{"DeleteHttpCacheExpired", deleteHttpCacheExpired},
		{"DeleteIPTC", deleteIPTC},
		{"DeleteIPTCKeyword", deleteIPTCKeyword},
		{"DeleteInvalidFileByPath", deleteInvalidFileByPath},
		{"DeleteXMPProperty", deleteXMPProperty},
		{"DeleteXMPRaw", deleteXMPRaw},
		{"GetConfigValueByKey", getConfigValueByKey},
		{"GetConfigs", getConfigs},
		{"GetExifByFile", getExifByFile},
		{"GetFileByPath", getFileByPath},
		{"GetFileViewByID", getFileViewByID},
		{"GetFileViewsByFolderIDOrderByFileName", getFileViewsByFolderIDOrderByFileName},
		{"GetFolderByID", getFolderByID},
		{"GetFolderByPath", getFolderByPath},
		{"GetFolderIDByPath", getFolderIDByPath},
		{"GetFolderTileExistsViewByPath", getFolderTileExistsViewByPath},
		{"GetFolderViewByID", getFolderViewByID},
		{"GetFoldersViewsByParentIDOrderByName", getFoldersViewsByParentIDOrderByName},
		{"GetGalleryStatistics", getGalleryStatistics},
		{"GetHttpCacheByKey", getHttpCacheByKey},
		{"GetHttpCacheOldestCreated", getHttpCacheOldestCreated},
		{"GetHttpCacheSizeBytes", getHttpCacheSizeBytes},
		{"GetIPTCByFile", getIPTCByFile},
		{"GetIPTCKeywords", getIPTCKeywords},
		{"GetInvalidFileByPath", getInvalidFileByPath},
		{"GetLoginAttempt", getLoginAttempt},
		{"GetModuleState", getModuleState},
		{"GetThumbnailExistsViewByID", getThumbnailExistsViewByID},
		{"GetThumbnailsByFileID", getThumbnailsByFileID},
		{"GetXMPPropertiesByFile", getXMPPropertiesByFile},
		{"GetXMPRaw", getXMPRaw},
		{"HttpCacheExistsByKey", httpCacheExistsByKey},
		{"InsertConfigIfNotExists", insertConfigIfNotExists},
		{"InsertIPTCKeyword", insertIPTCKeyword},
		{"SetModuleState", setModuleState},
		{"UnlockAccount", unlockAccount},
		{"UpdateFolderTileId", updateFolderTileId},
		{"UpsertConfigValueOnly", upsertConfigValueOnly},
		{"UpsertExif", upsertExif},
		{"UpsertFilePathReturningID", upsertFilePathReturningID},
		{"UpsertFileReturningFile", upsertFileReturningFile},
		{"UpsertFolderPathReturningID", upsertFolderPathReturningID},
		{"UpsertFolderReturningFolder", upsertFolderReturningFolder},
		{"UpsertHttpCache", upsertHttpCache},
		{"UpsertIPTC", upsertIPTC},
		{"UpsertInvalidFile", upsertInvalidFile},
		{"UpsertLoginAttempt", upsertLoginAttempt},
		{"UpsertThumbnailReturningID", upsertThumbnailReturningID},
		{"UpsertXMPProperty", upsertXMPProperty},
		{"UpsertXMPRaw", upsertXMPRaw},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := openMigratedMemoryDB(t)

			orig := prepareContextFn
			prepareContextFn = func(ctx context.Context, d DBTX, query string) (*sql.Stmt, error) {
				if query == tc.query {
					return nil, errors.New("prepare denied")
				}
				return orig(ctx, d, query)
			}
			t.Cleanup(func() { prepareContextFn = orig })

			_, err := Prepare(context.Background(), db)
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

func TestQueriesClose_StatementCloseFails(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"clearHttpCacheStmt"},
		{"clearLoginAttemptsStmt"},
		{"configKeyExistsStmt"},
		{"countHttpCacheEntriesStmt"},
		{"deleteHttpCacheByIDStmt"},
		{"deleteHttpCacheByKeyStmt"},
		{"deleteHttpCacheExpiredStmt"},
		{"deleteIPTCStmt"},
		{"deleteIPTCKeywordStmt"},
		{"deleteInvalidFileByPathStmt"},
		{"deleteXMPPropertyStmt"},
		{"deleteXMPRawStmt"},
		{"getConfigValueByKeyStmt"},
		{"getConfigsStmt"},
		{"getExifByFileStmt"},
		{"getFileByPathStmt"},
		{"getFileViewByIDStmt"},
		{"getFileViewsByFolderIDOrderByFileNameStmt"},
		{"getFolderByIDStmt"},
		{"getFolderByPathStmt"},
		{"getFolderIDByPathStmt"},
		{"getFolderTileExistsViewByPathStmt"},
		{"getFolderViewByIDStmt"},
		{"getFoldersViewsByParentIDOrderByNameStmt"},
		{"getGalleryStatisticsStmt"},
		{"getHttpCacheByKeyStmt"},
		{"getHttpCacheOldestCreatedStmt"},
		{"getHttpCacheSizeBytesStmt"},
		{"getIPTCByFileStmt"},
		{"getIPTCKeywordsStmt"},
		{"getInvalidFileByPathStmt"},
		{"getLoginAttemptStmt"},
		{"getModuleStateStmt"},
		{"getThumbnailExistsViewByIDStmt"},
		{"getThumbnailsByFileIDStmt"},
		{"getXMPPropertiesByFileStmt"},
		{"getXMPRawStmt"},
		{"httpCacheExistsByKeyStmt"},
		{"insertConfigIfNotExistsStmt"},
		{"insertIPTCKeywordStmt"},
		{"setModuleStateStmt"},
		{"unlockAccountStmt"},
		{"updateFolderTileIdStmt"},
		{"upsertConfigValueOnlyStmt"},
		{"upsertExifStmt"},
		{"upsertFilePathReturningIDStmt"},
		{"upsertFileReturningFileStmt"},
		{"upsertFolderPathReturningIDStmt"},
		{"upsertFolderReturningFolderStmt"},
		{"upsertHttpCacheStmt"},
		{"upsertIPTCStmt"},
		{"upsertInvalidFileStmt"},
		{"upsertLoginAttemptStmt"},
		{"upsertThumbnailReturningIDStmt"},
		{"upsertXMPPropertyStmt"},
		{"upsertXMPRawStmt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := openMigratedMemoryDB(t)

			q, err := Prepare(context.Background(), db)
			if err != nil {
				t.Fatalf("Prepare failed: %v", err)
			}

			// Determine the concrete *sql.Stmt pointer to fail on.
			var target *sql.Stmt
			switch tc.name {
			case "clearHttpCacheStmt":
				target = q.clearHttpCacheStmt
			case "clearLoginAttemptsStmt":
				target = q.clearLoginAttemptsStmt
			case "configKeyExistsStmt":
				target = q.configKeyExistsStmt
			case "countHttpCacheEntriesStmt":
				target = q.countHttpCacheEntriesStmt
			case "deleteHttpCacheByIDStmt":
				target = q.deleteHttpCacheByIDStmt
			case "deleteHttpCacheByKeyStmt":
				target = q.deleteHttpCacheByKeyStmt
			case "deleteHttpCacheExpiredStmt":
				target = q.deleteHttpCacheExpiredStmt
			case "deleteIPTCStmt":
				target = q.deleteIPTCStmt
			case "deleteIPTCKeywordStmt":
				target = q.deleteIPTCKeywordStmt
			case "deleteInvalidFileByPathStmt":
				target = q.deleteInvalidFileByPathStmt
			case "deleteXMPPropertyStmt":
				target = q.deleteXMPPropertyStmt
			case "deleteXMPRawStmt":
				target = q.deleteXMPRawStmt
			case "getConfigValueByKeyStmt":
				target = q.getConfigValueByKeyStmt
			case "getConfigsStmt":
				target = q.getConfigsStmt
			case "getExifByFileStmt":
				target = q.getExifByFileStmt
			case "getFileByPathStmt":
				target = q.getFileByPathStmt
			case "getFileViewByIDStmt":
				target = q.getFileViewByIDStmt
			case "getFileViewsByFolderIDOrderByFileNameStmt":
				target = q.getFileViewsByFolderIDOrderByFileNameStmt
			case "getFolderByIDStmt":
				target = q.getFolderByIDStmt
			case "getFolderByPathStmt":
				target = q.getFolderByPathStmt
			case "getFolderIDByPathStmt":
				target = q.getFolderIDByPathStmt
			case "getFolderTileExistsViewByPathStmt":
				target = q.getFolderTileExistsViewByPathStmt
			case "getFolderViewByIDStmt":
				target = q.getFolderViewByIDStmt
			case "getFoldersViewsByParentIDOrderByNameStmt":
				target = q.getFoldersViewsByParentIDOrderByNameStmt
			case "getGalleryStatisticsStmt":
				target = q.getGalleryStatisticsStmt
			case "getHttpCacheByKeyStmt":
				target = q.getHttpCacheByKeyStmt
			case "getHttpCacheOldestCreatedStmt":
				target = q.getHttpCacheOldestCreatedStmt
			case "getHttpCacheSizeBytesStmt":
				target = q.getHttpCacheSizeBytesStmt
			case "getIPTCByFileStmt":
				target = q.getIPTCByFileStmt
			case "getIPTCKeywordsStmt":
				target = q.getIPTCKeywordsStmt
			case "getInvalidFileByPathStmt":
				target = q.getInvalidFileByPathStmt
			case "getLoginAttemptStmt":
				target = q.getLoginAttemptStmt
			case "getModuleStateStmt":
				target = q.getModuleStateStmt
			case "getThumbnailExistsViewByIDStmt":
				target = q.getThumbnailExistsViewByIDStmt
			case "getThumbnailsByFileIDStmt":
				target = q.getThumbnailsByFileIDStmt
			case "getXMPPropertiesByFileStmt":
				target = q.getXMPPropertiesByFileStmt
			case "getXMPRawStmt":
				target = q.getXMPRawStmt
			case "httpCacheExistsByKeyStmt":
				target = q.httpCacheExistsByKeyStmt
			case "insertConfigIfNotExistsStmt":
				target = q.insertConfigIfNotExistsStmt
			case "insertIPTCKeywordStmt":
				target = q.insertIPTCKeywordStmt
			case "setModuleStateStmt":
				target = q.setModuleStateStmt
			case "unlockAccountStmt":
				target = q.unlockAccountStmt
			case "updateFolderTileIdStmt":
				target = q.updateFolderTileIdStmt
			case "upsertConfigValueOnlyStmt":
				target = q.upsertConfigValueOnlyStmt
			case "upsertExifStmt":
				target = q.upsertExifStmt
			case "upsertFilePathReturningIDStmt":
				target = q.upsertFilePathReturningIDStmt
			case "upsertFileReturningFileStmt":
				target = q.upsertFileReturningFileStmt
			case "upsertFolderPathReturningIDStmt":
				target = q.upsertFolderPathReturningIDStmt
			case "upsertFolderReturningFolderStmt":
				target = q.upsertFolderReturningFolderStmt
			case "upsertHttpCacheStmt":
				target = q.upsertHttpCacheStmt
			case "upsertIPTCStmt":
				target = q.upsertIPTCStmt
			case "upsertInvalidFileStmt":
				target = q.upsertInvalidFileStmt
			case "upsertLoginAttemptStmt":
				target = q.upsertLoginAttemptStmt
			case "upsertThumbnailReturningIDStmt":
				target = q.upsertThumbnailReturningIDStmt
			case "upsertXMPPropertyStmt":
				target = q.upsertXMPPropertyStmt
			case "upsertXMPRawStmt":
				target = q.upsertXMPRawStmt
			default:
				t.Fatalf("unknown statement field %q", tc.name)
			}

			orig := stmtCloseFn
			stmtCloseFn = func(s *sql.Stmt) error {
				if s == target {
					return errors.New("close denied")
				}
				return orig(s)
			}
			t.Cleanup(func() { stmtCloseFn = orig })

			err = q.Close()
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

func TestCustomQueriesClose_EmbeddedQueriesCloseFails(t *testing.T) {
	_, q, _ := setupAttachedTestDB(t)

	target := q.Queries.clearHttpCacheStmt

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
	if !strings.Contains(err.Error(), "error closing embedded Queries") {
		t.Errorf("expected embedded close error, got %q", err.Error())
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

func TestGetFileViewsByFolderIDOrderByFileName_QueryError(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	_, err := q.GetFileViewsByFolderIDOrderByFileName(context.Background(), sql.NullInt64{Int64: 1, Valid: true})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetFoldersViewsByParentIDOrderByName_QueryError(t *testing.T) {
	db := openMemoryDB(t)
	q := New(db)

	_, err := q.GetFoldersViewsByParentIDOrderByName(context.Background(), sql.NullInt64{Int64: 1, Valid: true})
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
