package migrations

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestMigrationsEmbed(t *testing.T) {
	files, err := FS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("failed to read embedded migrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no embedded migration files found")
	}
}

func TestMigration_AddETagConfig(t *testing.T) {
	// Setup test database
	dbfile := filepath.Join(t.TempDir(), "test_migration.db")
	db, err := sql.Open("sqlite3", dbfile)
	if err != nil {
		t.Fatalf("Open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Apply all migrations
	migrator, err := NewMigrator(dbfile)
	if err != nil {
		t.Fatalf("Create migrator: %v", err)
	}
	defer migrator.Close()

	if migErr := migrator.Up(); migErr != nil {
		t.Fatalf("Apply migrations: %v", err)
	}

	// Query config table for etag_version
	var key, value, valueType, category, description string
	var requiresRestart int
	err = db.QueryRowContext(ctx,
		`SELECT key, value, type, category, requires_restart, description
         FROM config WHERE key = 'etag_version'`,
	).Scan(&key, &value, &valueType, &category, &requiresRestart, &description)

	if errors.Is(err, sql.ErrNoRows) {
		t.Fatal("etag_version config entry not found after migration")
	}
	if err != nil {
		t.Fatalf("Query etag_version: %v", err)
	}

	// Verify metadata
	if valueType != "string" {
		t.Errorf("type = %q, want %q", valueType, "string")
	}
	if category != "server" {
		t.Errorf("category = %q, want %q", category, "server")
	}
	if requiresRestart != 0 {
		t.Errorf("requires_restart = %d, want 0", requiresRestart)
	}
	if description == "" {
		t.Error("description is empty")
	}

	// Verify value format (YYYYMMDD-NN)
	if len(value) != 11 {
		t.Errorf("value length = %d, want 11 (format: YYYYMMDD-NN)", len(value))
	}
	// Check format with regex
	if match, _ := regexp.MatchString(`^\d{8}-\d{2}$`, value); !match {
		t.Errorf("value = %q does not match expected format YYYYMMDD-NN", value)
	}
	// Verify it ends with -01 (initial version)
	if value[8:] != "-01" {
		t.Errorf("value suffix = %q, want %q", value[8:], "-01")
	}
	t.Logf("etag_version value: %s", value)
}

func TestThumbsMigration(t *testing.T) {
	dbfile := filepath.Join(t.TempDir(), "test_thumbs.db")
	migrator, err := NewThumbsMigrator(dbfile)
	if err != nil {
		t.Fatalf("NewThumbsMigrator: %v", err)
	}
	defer migrator.Close()

	if upErr := migrator.Up(); upErr != nil && upErr.Error() != "no change" {
		t.Fatalf("thumbs Up: %v", err)
	}

	db, err := sql.Open("sqlite3", dbfile)
	if err != nil {
		t.Fatalf("open thumbs db: %v", err)
	}
	defer db.Close()

	var name string
	err = db.QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name='thumbnail_blobs'`).Scan(&name)
	if err != nil {
		t.Fatalf("thumbnail_blobs table not found: %v", err)
	}
	if name != "thumbnail_blobs" {
		t.Errorf("expected thumbnail_blobs, got %s", name)
	}
}

func TestNewMigrator_Errors(t *testing.T) {
	cases := []struct {
		name    string
		setup   func() (cleanup func())
		wantErr string
	}{
		{
			name: "iofs new fails",
			setup: func() func() {
				orig := iofsNewFn
				iofsNewFn = func(fsys fs.FS, path string) (source.Driver, error) {
					return nil, errors.New("iofs new failed")
				}
				return func() { iofsNewFn = orig }
			},
			wantErr: "create migrations source",
		},
		{
			name: "new with source instance fails",
			setup: func() func() {
				orig := migrateNewWithSourceInstanceFn
				migrateNewWithSourceInstanceFn = func(sourceName string, instance source.Driver, databaseURL string) (*migrate.Migrate, error) {
					return nil, errors.New("migrate new failed")
				}
				return func() { migrateNewWithSourceInstanceFn = orig }
			},
			wantErr: "initialize migrator",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := tc.setup()
			defer cleanup()

			_, err := NewMigrator(filepath.Join(t.TempDir(), "test.db"))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want containing %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestNewMigrator_MemoryDSN(t *testing.T) {
	m, err := NewMigrator(":memory:")
	if err != nil {
		t.Fatalf("NewMigrator(:memory:) error = %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil migrator")
	}
	m.Close()
}

func TestNewThumbsMigrator_Errors(t *testing.T) {
	cases := []struct {
		name    string
		setup   func() (cleanup func())
		wantErr string
	}{
		{
			name: "iofs new fails",
			setup: func() func() {
				orig := iofsNewFn
				iofsNewFn = func(fsys fs.FS, path string) (source.Driver, error) {
					return nil, errors.New("iofs new failed")
				}
				return func() { iofsNewFn = orig }
			},
			wantErr: "create thumbs migrations source",
		},
		{
			name: "new with source instance fails",
			setup: func() func() {
				orig := migrateNewWithSourceInstanceFn
				migrateNewWithSourceInstanceFn = func(sourceName string, instance source.Driver, databaseURL string) (*migrate.Migrate, error) {
					return nil, errors.New("migrate new failed")
				}
				return func() { migrateNewWithSourceInstanceFn = orig }
			},
			wantErr: "initialize thumbs migrator",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := tc.setup()
			defer cleanup()

			_, err := NewThumbsMigrator(filepath.Join(t.TempDir(), "test.db"))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want containing %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestNewThumbsMigrator_MemoryDSN(t *testing.T) {
	m, err := NewThumbsMigrator(":memory:")
	if err != nil {
		t.Fatalf("NewThumbsMigrator(:memory:) error = %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil migrator")
	}
	m.Close()
}
