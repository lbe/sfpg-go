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

// FS embeds main database SQL migration files.
//
//go:embed migrations/*.sql
var FS embed.FS

// ThumbsFS embeds thumbnail database SQL migration files.
//
//go:embed thumbs/*.sql
var ThumbsFS embed.FS

var (
	// iofsNewFn is a testable hook for iofs.New.
	iofsNewFn = iofs.New

	// migrateNewWithSourceInstanceFn is a testable hook for migrate.NewWithSourceInstance.
	migrateNewWithSourceInstanceFn = migrate.NewWithSourceInstance
)

// NewThumbsMigrator creates a migrator for the thumbnail blob database (thumbs.db).
func NewThumbsMigrator(dbPath string) (*migrate.Migrate, error) {
	d, err := iofsNewFn(ThumbsFS, "thumbs")
	if err != nil {
		return nil, fmt.Errorf("create thumbs migrations source: %w", err)
	}

	var dsn string
	if dbPath == ":memory:" {
		// Opaque URL form parses on Go 1.26.0+; sqlite://:memory: fails url.Parse there.
		dsn = "sqlite::memory:"
	} else {
		dsn = "sqlite://" + filepath.ToSlash(dbPath)
	}

	m, err := migrateNewWithSourceInstanceFn("iofs", d, dsn)
	if err != nil {
		return nil, fmt.Errorf("initialize thumbs migrator: %w", err)
	}
	return m, nil
}
