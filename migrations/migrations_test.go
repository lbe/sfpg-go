package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
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

	if err == sql.ErrNoRows {
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
