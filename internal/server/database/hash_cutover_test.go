package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/lbe/sfpg-go/migrations"
)

// overflowQueue makes the write overflow queue directory for dbPath and
// reports a function that says whether it still exists.
func overflowQueue(t *testing.T, dbPath string) func() bool {
	t.Helper()
	dir := dbPath + "-dque"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return func() bool {
		_, err := os.Stat(dir)
		return err == nil
	}
}

// setVersion sets the migration version recorded in dbPath.
func setVersion(t *testing.T, dbPath string, version int, dirty bool) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("UPDATE schema_migrations SET version = ?, dirty = ?", version, dirty); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateDBDiscardsOldHashQueue(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sfpg.db")

	// A new database has no pHashes to protect, but its queue is removed too.
	exists := overflowQueue(t, dbPath)
	if _, err := migrateDB(dbPath); err != nil {
		t.Fatal(err)
	}
	if exists() {
		t.Error("overflow queue of a new database kept, want removed")
	}

	// At or past the pHash migration, the queue is kept.
	exists = overflowQueue(t, dbPath)
	if _, err := migrateDB(dbPath); err != nil {
		t.Fatal(err)
	}
	if !exists() {
		t.Error("overflow queue removed at the current version, want kept")
	}

	// Below it, the queue holds old pHashes and is removed.
	setVersion(t, dbPath, phashMigration-1, false)
	if _, err := migrateDB(dbPath); err != nil {
		t.Fatal(err)
	}
	if exists() {
		t.Errorf("overflow queue kept at version %d, want removed", phashMigration-1)
	}
}

func TestMigrateDBRejectsDirtyVersion(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sfpg.db")
	if _, err := migrateDB(dbPath); err != nil {
		t.Fatal(err)
	}
	setVersion(t, dbPath, phashMigration-1, true)
	exists := overflowQueue(t, dbPath)
	if _, err := migrateDB(dbPath); err == nil {
		t.Error("migrateDB succeeded on a dirty database, want an error")
	}
	if !exists() {
		t.Error("overflow queue removed for a dirty database, want kept")
	}
}

// TestMigrateDBUpgradeClearsLegacyPHash upgrades a database at version 23, the
// latest in dev: the overflow queue goes and the old pHash is cleared.
func TestMigrateDBUpgradeClearsLegacyPHash(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sfpg.db")
	src, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, "sqlite://"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Migrate(23); err != nil {
		t.Fatal(err)
	}
	if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
		t.Fatal(srcErr, dbErr)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO file_paths (id, path) VALUES (1, 'a.jpg');
		INSERT INTO files (id, path_id, filename, phash) VALUES (1, 1, 'a.jpg', 42)`); err != nil {
		t.Fatal(err)
	}
	exists := overflowQueue(t, dbPath)

	if _, err = migrateDB(dbPath); err != nil {
		t.Fatal(err)
	}
	if exists() {
		t.Error("overflow queue kept after the upgrade, want removed")
	}
	var phash sql.NullInt64
	if err = db.QueryRow("SELECT phash FROM files WHERE id = 1").Scan(&phash); err != nil {
		t.Fatal(err)
	}
	if phash.Valid {
		t.Errorf("phash = %d after the upgrade, want NULL", phash.Int64)
	}
}
