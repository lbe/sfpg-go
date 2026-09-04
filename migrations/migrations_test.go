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

func TestMigration_FileFolderIndex_ColumnsAndIndex(t *testing.T) {
	dbfile := filepath.Join(t.TempDir(), "test_020.db")
	migrator, err := NewMigrator(dbfile)
	if err != nil {
		t.Fatalf("Create migrator: %v", err)
	}
	defer migrator.Close()

	if upErr := migrator.Up(); upErr != nil && upErr.Error() != "no change" {
		t.Fatalf("Apply migrations: %v", upErr)
	}

	db, err := sql.Open("sqlite3", dbfile)
	if err != nil {
		t.Fatalf("Open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Verify table exists
	var name string
	err = db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='file_folder_index'`).Scan(&name)
	if err != nil {
		t.Fatalf("file_folder_index table not found: %v", err)
	}

	// Verify expected columns
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(file_folder_index)")
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var colName, colType string
		var notNull, pk int
		var defaultVal *string
		if scanErr := rows.Scan(&cid, &colName, &colType, &notNull, &defaultVal, &pk); scanErr != nil {
			t.Fatalf("scan column: %v", scanErr)
		}
		cols[colName] = true
		if colName == "file_id" && pk != 1 {
			t.Error("file_id should be PRIMARY KEY")
		}
	}
	wantCols := []string{"file_id", "folder_id", "image_index", "image_count", "prev_id", "next_id", "first_id", "last_id"}
	for _, c := range wantCols {
		if !cols[c] {
			t.Errorf("missing column: %s", c)
		}
	}

	// Verify index exists
	err = db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_file_folder_index_folder_id'`).Scan(&name)
	if err != nil {
		t.Fatalf("idx_file_folder_index_folder_id index not found: %v", err)
	}
}

func TestMigration_FileFolderIndex_Backfill(t *testing.T) {
	dbfile := filepath.Join(t.TempDir(), "test_020_backfill.db")
	migrator, err := NewMigrator(dbfile)
	if err != nil {
		t.Fatalf("Create migrator: %v", err)
	}
	defer migrator.Close()

	// Migrate to v19 (before file_folder_index exists)
	migErr := migrator.Migrate(19)
	if migErr != nil {
		t.Fatalf("Migrate to v19: %v", migErr)
	}

	db, err := sql.Open("sqlite3", dbfile)
	if err != nil {
		t.Fatalf("Open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Insert folder_paths (separate entry per folder)
	for _, p := range []struct {
		id   int
		path string
	}{
		{1, "/albums/Vacation"},
		{2, "/albums/Singles"},
	} {
		_, err = db.ExecContext(ctx,
			"INSERT INTO folder_paths (id, path) VALUES (?, ?)", p.id, p.path)
		if err != nil {
			t.Fatalf("insert folder_paths %d: %v", p.id, err)
		}
	}

	// Insert folders (two folders for multi-folder testing)
	for _, f := range []struct {
		id, pathID int
		name       string
	}{
		{1, 1, "Vacation"},
		{2, 2, "Singles"},
	} {
		_, err = db.ExecContext(ctx,
			"INSERT INTO folders (id, path_id, name) VALUES (?, ?, ?)", f.id, f.pathID, f.name)
		if err != nil {
			t.Fatalf("insert folder %d: %v", f.id, err)
		}
	}

	// Insert file_paths
	for _, p := range []struct {
		id   int
		path string
	}{
		{1, "/albums/Vacation/photo_a.jpg"},
		{2, "/albums/Vacation/photo_b.jpg"},
		{3, "/albums/Vacation/photo_c.jpg"},
		{4, "/albums/Singles/lone.jpg"},
		{5, "/albums/orphan.jpg"},
	} {
		_, err = db.ExecContext(ctx,
			"INSERT INTO file_paths (id, path) VALUES (?, ?)", p.id, p.path)
		if err != nil {
			t.Fatalf("insert file_paths %d: %v", p.id, err)
		}
	}

	// Insert files — 3 in folder 1, 1 in folder 2, 1 orphan (folder_id NULL)
	for _, f := range []struct {
		id       int
		folderID *int
		pathID   int
		filename string
	}{
		{1, new(1), 1, "photo_a.jpg"},
		{2, new(1), 2, "photo_b.jpg"},
		{3, new(1), 3, "photo_c.jpg"},
		{4, new(2), 4, "lone.jpg"},
		{5, nil, 5, "orphan.jpg"},
	} {
		_, err = db.ExecContext(ctx,
			`INSERT INTO files (id, folder_id, path_id, filename)
			 VALUES (?, ?, ?, ?)`, f.id, f.folderID, f.pathID, f.filename)
		if err != nil {
			t.Fatalf("insert file %d: %v", f.id, err)
		}
	}

	// Apply migration 020
	migErr = migrator.Migrate(20)
	if migErr != nil {
		t.Fatalf("Migrate to v20: %v", migErr)
	}

	// Verify file_folder_index rows
	rows, err := db.QueryContext(ctx,
		`SELECT file_id, folder_id, image_index, image_count, prev_id, next_id, first_id, last_id
		 FROM file_folder_index ORDER BY file_id`)
	if err != nil {
		t.Fatalf("query file_folder_index: %v", err)
	}
	defer rows.Close()

	type idxRow struct {
		fileID, folderID, imageIndex, imageCount int
		prevID, nextID, firstID, lastID          *int
	}
	var got []idxRow
	for rows.Next() {
		var r idxRow
		if err := rows.Scan(&r.fileID, &r.folderID, &r.imageIndex, &r.imageCount,
			&r.prevID, &r.nextID, &r.firstID, &r.lastID); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		got = append(got, r)
	}

	// Should have 4 rows (3 in folder 1 + 1 in folder 2; orphan excluded)
	if len(got) != 4 {
		t.Fatalf("got %d rows, want 4 (orphan excluded)", len(got))
	}

	// Folder 1 (Vacation): files 1,2,3 (3 files ordered by filename, id)
	// img 1 (photo_a): image_index=1, image_count=3, prev=NULL, next=2, first=1, last=3
	// img 2 (photo_b): image_index=2, image_count=3, prev=1,    next=3, first=1, last=3
	// img 3 (photo_c): image_index=3, image_count=3, prev=2,    next=NULL, first=1, last=3

	if got[0].imageIndex != 1 || got[0].imageCount != 3 {
		t.Errorf("file 1 index=%d count=%d, want 1/3", got[0].imageIndex, got[0].imageCount)
	}
	if got[0].prevID != nil {
		t.Errorf("file 1 prev_id = %v, want nil", got[0].prevID)
	}
	if got[0].nextID == nil || *got[0].nextID != 2 {
		t.Errorf("file 1 next_id = %v, want 2", got[0].nextID)
	}
	if got[0].firstID == nil || *got[0].firstID != 1 {
		t.Errorf("file 1 first_id = %v, want 1", got[0].firstID)
	}
	if got[0].lastID == nil || *got[0].lastID != 3 {
		t.Errorf("file 1 last_id = %v, want 3", got[0].lastID)
	}

	if got[2].imageIndex != 3 || got[2].imageCount != 3 {
		t.Errorf("file 3 index=%d count=%d, want 3/3", got[2].imageIndex, got[2].imageCount)
	}
	if got[2].nextID != nil {
		t.Errorf("file 3 next_id = %v, want nil", got[2].nextID)
	}

	// Folder 2 (Singles): file 4 — single-file folder
	if got[3].imageIndex != 1 || got[3].imageCount != 1 {
		t.Errorf("file 4 index=%d count=%d, want 1/1", got[3].imageIndex, got[3].imageCount)
	}
	if got[3].prevID != nil {
		t.Errorf("file 4 prev_id = %v, want nil (single-file folder)", got[3].prevID)
	}
	if got[3].nextID != nil {
		t.Errorf("file 4 next_id = %v, want nil (single-file folder)", got[3].nextID)
	}
	if got[3].firstID == nil || *got[3].firstID != 4 {
		t.Errorf("file 4 first_id = %v, want 4", got[3].firstID)
	}
	if got[3].lastID == nil || *got[3].lastID != 4 {
		t.Errorf("file 4 last_id = %v, want 4", got[3].lastID)
	}

	t.Logf("got %d rows: %+v", len(got), got)
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
