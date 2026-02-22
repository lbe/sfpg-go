package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	"go.local/sfpg/internal/server/config"
	"go.local/sfpg/migrations"
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
	if dbPath != expectedDBPath {
		t.Errorf("expected dbPath to be %q, got %q", expectedDBPath, dbPath)
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
	tempDB := filepath.Join(t.TempDir(), "test.db")

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

	if upErr := m.Up(); upErr != nil && upErr != migrate.ErrNoChange {
		db.Close()
		t.Fatalf("failed to apply migrations: %v", upErr)
	}
	db.Close()

	// Now create the pools with the migrated database
	// Use simple DSNs without WAL - the test just needs to verify pool creation
	ro := "file:" + filepath.ToSlash(tempDB) + "?mode=ro"
	rw := "file:" + filepath.ToSlash(tempDB) + "?_txlock=immediate&mode=rwc"

	dbRwPool, dbRoPool, err := createDatabasePools(ctx, ro, rw, nil)
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

	if err := migrateDB(dbPath); err != nil {
		t.Fatalf("migrateDB failed: %v", err)
	}

	ro, rw := configureDatabaseDSN(dbPath)
	dbRwPool, dbRoPool, err := createDatabasePools(ctx, ro, rw, nil)
	if err != nil {
		t.Fatalf("createDatabasePools failed: %v", err)
	}
	defer func() {
		_ = dbRoPool.Close()
		_ = dbRwPool.Close()
	}()

	cpc, err := dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get cpc: %v", err)
	}
	defer dbRwPool.Put(cpc)

	if ensureErr := ensureRootFolderExists(ctx, cpc, tempDir); ensureErr != nil {
		t.Fatalf("ensureRootFolderExists failed: %v", ensureErr)
	}

	id, err := cpc.Queries.GetFolderIDByPath(ctx, "")
	if err != nil {
		t.Fatalf("GetFolderIDByPath failed: %v", err)
	}
	if id == 0 {
		t.Fatalf("root folder id invalid: %d", id)
	}
}

func TestSchedulePeriodicOptimization_Smoke(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Plain :memory: is sufficient - this test only verifies scheduling works
	ro := ":memory:"
	rw := ":memory:"

	dbRwPool, dbRoPool, err := createDatabasePools(ctx, ro, rw, nil)
	if err != nil {
		t.Fatalf("createDatabasePools failed: %v", err)
	}
	defer func() {
		_ = dbRoPool.Close()
		_ = dbRwPool.Close()
	}()

	var wg sync.WaitGroup
	ScheduleOptimization(ctx, dbRwPool, &wg)
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait() // Wait for goroutine to exit
}
