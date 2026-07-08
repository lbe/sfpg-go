package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/migrations"
)

// TestSetDirectories verifies that Setup creates the DB directory.
func TestSetDirectories(t *testing.T) {
	tempDir := t.TempDir()

	ctx := context.Background()
	cfg := config.DefaultConfig()
	dbPath, dbRwPool, dbRoPool, err := Setup(ctx, tempDir, cfg)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer func() {
		if dbRoPool != nil {
			_ = dbRoPool.Close()
		}
		if dbRwPool != nil {
			_ = dbRwPool.Close()
		}
	}()

	dbDir := filepath.Join(tempDir, "DB")
	if _, err := os.Stat(dbDir); os.IsNotExist(err) {
		t.Errorf("DB directory should exist after Setup, but does not: %v", err)
	}

	expectedDBPath := filepath.Join(dbDir, "sfpg.db")
	if dbPath.Main != expectedDBPath {
		t.Errorf("expected dbPath.Main to be %q, got %q", expectedDBPath, dbPath.Main)
	}
}

// TestMigrateDB verifies that migrateDB correctly applies database migrations.
func TestMigrateDB(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "sfpg.db")

	// First run: should create and migrate the database
	t.Run("initial migration", func(t *testing.T) {
		if err := migrateDB(dbPath); err != nil {
			t.Fatalf("migrateDB failed: %v", err)
		}

		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			t.Fatal("database file was not created")
		}

		db, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			t.Fatalf("failed to open database after migration: %v", err)
		}
		defer db.Close()

		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='config'").Scan(&count)
		if err != nil {
			t.Fatalf("failed to query tables: %v", err)
		}
		if count == 0 {
			t.Error("config table was not created by migration")
		}
	})

	// Second run: should handle existing database gracefully
	t.Run("rerun migration", func(t *testing.T) {
		if err := migrateDB(dbPath); err != nil {
			t.Fatalf("migrateDB failed on rerun: %v", err)
		}

		db, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			t.Fatalf("failed to open database after re-migration: %v", err)
		}
		defer db.Close()

		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='config'").Scan(&count)
		if err != nil {
			t.Fatalf("failed to query tables after re-migration: %v", err)
		}
		if count != 1 {
			t.Error("config table not found after re-migration")
		}
	})
}

// TestConfigureDatabaseDSN verifies that the SQLite DSN strings include expected pragmas.
func TestConfigureDatabaseDSN(t *testing.T) {
	ro, rw := configureDatabaseDSN("./test.db")
	// RO DSN should be simple (just mode=ro) - WAL is persistent
	if !strings.Contains(ro, "mode=ro") {
		t.Fatalf("ro DSN missing mode=ro: %s", ro)
	}
	// RW DSN should have transaction lock and mode
	if !strings.Contains(rw, "_txlock=immediate") {
		t.Fatalf("rw DSN missing immediate txlock: %s", rw)
	}
	if !strings.Contains(rw, "mode=rwc") {
		t.Fatalf("rw DSN missing mode=rwc: %s", rw)
	}
}

func TestCreateDatabasePools(t *testing.T) {
	ctx := context.Background()
	// Use a temporary file database instead of :memory: so migrations can be applied
	// and shared between the RO and RW pools
	tempDir := t.TempDir()
	tempDB := filepath.Join(tempDir, "test.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")

	// Run migrations first (use simple DSN, no pragmas needed for migration)
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(tempDB))
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

	if upErr := m.Up(); upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		db.Close()
		t.Fatalf("failed to apply migrations: %v", upErr)
	}
	db.Close()

	// Run thumbs migration (required for CustomQueries that reference thumbs.thumbnail_blobs)
	if migErr := migrateBlobsDB(thumbsDBPath); migErr != nil {
		t.Fatalf("migrateBlobsDB failed: %v", migErr)
	}

	// Now create the pools with the migrated database
	// Use simple DSNs without WAL - the test just needs to verify pool creation
	ro := "file:" + filepath.ToSlash(tempDB) + "?mode=ro"
	rw := "file:" + filepath.ToSlash(tempDB) + "?_txlock=immediate&mode=rwc"

	dbRwPool, dbRoPool, err := createDatabasePools(ctx, ro, rw, thumbsDBPath, nil)
	if err != nil {
		t.Fatalf("createDatabasePools failed: %v", err)
	}
	defer func() {
		_ = dbRoPool.Close()
		_ = dbRwPool.Close()
	}()

	if dbRwPool == nil || dbRoPool == nil {
		t.Fatalf("pools not created")
	}

	c, err := dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get rw conn: %v", err)
	}
	dbRwPool.Put(c)

	c2, err := dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get ro conn: %v", err)
	}
	dbRoPool.Put(c2)
}

func TestEnsureRootFolderExists(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "sfpg.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")

	if err := migrateDB(dbPath); err != nil {
		t.Fatalf("migrateDB failed: %v", err)
	}
	if err := migrateBlobsDB(thumbsDBPath); err != nil {
		t.Fatalf("migrateBlobsDB failed: %v", err)
	}

	ro, rw := configureDatabaseDSN(dbPath)
	dbRwPool, dbRoPool, err := createDatabasePools(ctx, ro, rw, thumbsDBPath, nil)
	if err != nil {
		t.Fatalf("createDatabasePools failed: %v", err)
	}
	defer func() {
		_ = dbRoPool.Close()
		_ = dbRwPool.Close()
	}()

	cpcRw, err := dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get cpcRw: %v", err)
	}
	defer dbRwPool.Put(cpcRw)

	if ensureErr := ensureRootFolderExists(ctx, cpcRw, tempDir); ensureErr != nil {
		t.Fatalf("ensureRootFolderExists failed: %v", ensureErr)
	}

	id, err := cpcRw.Queries.GetFolderIDByPath(ctx, "")
	if err != nil {
		t.Fatalf("GetFolderIDByPath failed: %v", err)
	}
	if id == 0 {
		t.Fatalf("root folder id invalid: %d", id)
	}
}

func TestSetup_MkdirAllDBDirFails(t *testing.T) {
	orig := osMkdirAll
	osMkdirAll = func(path string, perm os.FileMode) error {
		return errors.New("mkdir denied")
	}
	t.Cleanup(func() { osMkdirAll = orig })

	ctx := context.Background()
	_, _, _, err := Setup(ctx, t.TempDir(), config.DefaultConfig())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to create DB directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetup_MkdirAllThumbsDirFails(t *testing.T) {
	orig := osMkdirAll
	callCount := 0
	osMkdirAll = func(path string, perm os.FileMode) error {
		callCount++
		if callCount == 2 {
			return errors.New("mkdir thumbs denied")
		}
		return orig(path, perm)
	}
	t.Cleanup(func() { osMkdirAll = orig })

	ctx := context.Background()
	_, _, _, err := Setup(ctx, t.TempDir(), config.DefaultConfig())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to create thumbs DB directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetup_GetRwConnFails(t *testing.T) {
	orig := dbRwPoolGet
	dbRwPoolGet = func(p *dbconnpool.DbSQLConnPool) (*dbconnpool.CpConn, error) {
		return nil, errors.New("get denied")
	}
	t.Cleanup(func() { dbRwPoolGet = orig })

	ctx := context.Background()
	_, _, _, err := Setup(ctx, t.TempDir(), config.DefaultConfig())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to get RW conn for root check") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetup_EnsureRootFolderExistsFails(t *testing.T) {
	orig := ensureRootFolderExistsFn
	ensureRootFolderExistsFn = func(ctx context.Context, cpcRw *dbconnpool.CpConn, rootDir string) error {
		return errors.New("root check denied")
	}
	t.Cleanup(func() { ensureRootFolderExistsFn = orig })

	ctx := context.Background()
	_, _, _, err := Setup(ctx, t.TempDir(), config.DefaultConfig())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "root folder check failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetup_MigrateDBFails(t *testing.T) {
	orig := migrateDBFn
	migrateDBFn = func(dbPath string) error {
		return errors.New("migration failed")
	}
	t.Cleanup(func() { migrateDBFn = orig })

	ctx := context.Background()
	_, _, _, err := Setup(ctx, t.TempDir(), config.DefaultConfig())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "migration failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetup_MigrateBlobsDBFails(t *testing.T) {
	orig := migrateBlobsDBFn
	migrateBlobsDBFn = func(dbPath string) error {
		return errors.New("thumbs migration failed")
	}
	t.Cleanup(func() { migrateBlobsDBFn = orig })

	ctx := context.Background()
	_, _, _, err := Setup(ctx, t.TempDir(), config.DefaultConfig())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "thumbs migration failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetup_CreateDatabasePoolsFails(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 1
	cfg.DBMinIdleConnections = 10

	ctx := context.Background()
	_, _, _, err := Setup(ctx, t.TempDir(), cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pool creation failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMigrateDB_OpenFileFails(t *testing.T) {
	orig := osOpenFile
	osOpenFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return nil, errors.New("open denied")
	}
	t.Cleanup(func() { osOpenFile = orig })

	err := migrateDB(filepath.Join(t.TempDir(), "sfpg.db"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to open database file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMigrateDB_UpFails(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "sfpg.db")

	if err := migrateDB(dbPath); err != nil {
		t.Fatalf("initial migration failed: %v", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, execErr := db.Exec("UPDATE schema_migrations SET version = 999, dirty = 1"); execErr != nil {
		t.Fatalf("dirty migration: %v", execErr)
	}

	err = migrateDB(dbPath)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "up migration failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMigrateBlobsDB_OpenFileFails(t *testing.T) {
	orig := osOpenFile
	osOpenFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return nil, errors.New("open denied")
	}
	t.Cleanup(func() { osOpenFile = orig })

	err := migrateBlobsDB(filepath.Join(t.TempDir(), "thumbs.db"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to open thumbs database file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMigrateBlobsDB_UpFails(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "thumbs.db")

	if err := migrateBlobsDB(dbPath); err != nil {
		t.Fatalf("initial thumbs migration failed: %v", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, execErr := db.Exec("UPDATE schema_migrations SET version = 999, dirty = 1"); execErr != nil {
		t.Fatalf("dirty thumbs migration: %v", execErr)
	}

	err = migrateBlobsDB(dbPath)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "thumbs up migration failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateDatabasePools_RoPoolFails(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	tempDB := filepath.Join(tempDir, "test.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")

	// Run main migrations
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(tempDB))
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

	if upErr := m.Up(); upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		db.Close()
		t.Fatalf("failed to apply migrations: %v", upErr)
	}
	db.Close()

	// Run thumbs migration
	if migErr := migrateBlobsDB(thumbsDBPath); migErr != nil {
		t.Fatalf("migrateBlobsDB failed: %v", migErr)
	}

	ro := "file:" + filepath.ToSlash(tempDB) + "?mode=ro"
	rw := "file:" + filepath.ToSlash(tempDB) + "?_txlock=immediate&mode=rwc"

	orig := newDbSQLConnPool
	callCount := 0
	newDbSQLConnPool = func(ctx context.Context, dataSourceName string, cfg dbconnpool.Config) (*dbconnpool.DbSQLConnPool, error) {
		callCount++
		if callCount == 1 {
			return orig(ctx, dataSourceName, cfg)
		}
		return nil, errors.New("ro pool denied")
	}
	t.Cleanup(func() { newDbSQLConnPool = orig })

	_, _, err = createDatabasePools(ctx, ro, rw, thumbsDBPath, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to create RO pool") {
		t.Fatalf("unexpected error: %v", err)
	}
}
