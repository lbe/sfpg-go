// Package migrations embeds SQL migration files into the Go binary.
// These migrations are used by the application to manage the database
// schema, ensuring it is always up-to-date.
package migrations

import (
	"embed"
	"fmt"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var FS embed.FS

//go:embed thumbs/*.sql
var ThumbsFS embed.FS

// NewMigrator creates a new migrator instance from the embedded migration files.
// It initializes the migration engine using the embedded FS and connects it to the provided database path.
// dbPath should be the SQLite database file path (e.g., "/tmp/test.db") or ":memory:" for in-memory.
func NewMigrator(dbPath string) (*migrate.Migrate, error) {
	d, err := iofs.New(FS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("create migrations source: %w", err)
	}

	var dsn string
	if dbPath == ":memory:" {
		// golang-migrate requires special handling for in-memory databases
		dsn = "sqlite://:memory:"
	} else {
		dsn = "sqlite://" + filepath.ToSlash(dbPath)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, dsn)
	if err != nil {
		return nil, fmt.Errorf("initialize migrator: %w", err)
	}

	return m, nil
}

// NewThumbsMigrator creates a migrator for the thumbnail blob database (thumbs.db).
func NewThumbsMigrator(dbPath string) (*migrate.Migrate, error) {
	d, err := iofs.New(ThumbsFS, "thumbs")
	if err != nil {
		return nil, fmt.Errorf("create thumbs migrations source: %w", err)
	}

	var dsn string
	if dbPath == ":memory:" {
		dsn = "sqlite://:memory:"
	} else {
		dsn = "sqlite://" + filepath.ToSlash(dbPath)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, dsn)
	if err != nil {
		return nil, fmt.Errorf("initialize thumbs migrator: %w", err)
	}
	return m, nil
}
