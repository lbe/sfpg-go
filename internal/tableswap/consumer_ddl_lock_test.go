package tableswap

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// Copied from internal/cachelite/cache.go (rotateCreateActiveTableSQL).
const httpCacheCreateSQL = `
CREATE TABLE IF NOT EXISTS http_cache (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  key                 TEXT NOT NULL UNIQUE,
  method              TEXT NOT NULL,
  path                TEXT NOT NULL,
  query_string        TEXT,
  status              INTEGER NOT NULL,
  content_type        TEXT,
  cache_control       TEXT,
  etag                TEXT,
  last_modified       TEXT,
  vary                TEXT,
  body                BLOB NOT NULL,
  content_length      INTEGER,
  created_at          INTEGER NOT NULL,
  expires_at          INTEGER
)`

// Copied from internal/cachelite/cache.go (httpCacheIndexCreateStatements).
var httpCacheIndexSQL = []string{
	"CREATE INDEX IF NOT EXISTS idx_http_cache_key ON http_cache(key)",
	"CREATE INDEX IF NOT EXISTS idx_http_cache_path ON http_cache(path)",
	"CREATE INDEX IF NOT EXISTS idx_http_cache_created ON http_cache(created_at)",
	"CREATE INDEX IF NOT EXISTS idx_http_cache_expires ON http_cache(expires_at)",
	"CREATE INDEX IF NOT EXISTS idx_http_cache_content_length ON http_cache(content_length)",
}

// Copied from migrations/migrations/020_file_folder_index.up.sql.
const fileFolderIndexCreateSQL = `
CREATE TABLE file_folder_index (
    file_id     INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
    folder_id   INTEGER NOT NULL,
    image_index INTEGER NOT NULL,
    image_count INTEGER NOT NULL,
    prev_id     INTEGER,
    next_id     INTEGER,
    first_id    INTEGER NOT NULL,
    last_id     INTEGER NOT NULL
)`

const fileFolderIndexIndexSQL = `CREATE INDEX idx_file_folder_index_folder_id ON file_folder_index(folder_id)`

func TestCloneEmptyCreateIndexesLocksHTTPCacheDDL(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	mustExecLock(t, db, ctx, httpCacheCreateSQL)
	for _, stmt := range httpCacheIndexSQL {
		mustExecLock(t, db, ctx, stmt)
	}
	assertConsumerCloneAndIndexes(t, ctx, db, "http_cache")
}

func TestCloneEmptyCreateIndexesLocksFileFolderIndexDDL(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	mustExecLock(t, db, ctx, `CREATE TABLE files (id INTEGER PRIMARY KEY)`)
	mustExecLock(t, db, ctx, fileFolderIndexCreateSQL)
	mustExecLock(t, db, ctx, fileFolderIndexIndexSQL)
	assertConsumerCloneAndIndexes(t, ctx, db, "file_folder_index")
}

func assertConsumerCloneAndIndexes(t *testing.T, ctx context.Context, db *sql.DB, active string) {
	t.Helper()
	dest := destName(active)
	wantCols := colNames(t, db, active)
	wantSets := indexedColumnSets(t, db, active)

	if err := CloneEmpty(ctx, db, active); err != nil {
		t.Fatalf("CloneEmpty(%s): %v", active, err)
	}
	gotCols := colNames(t, db, dest)
	if !equalStringSlice(wantCols, gotCols) {
		t.Fatalf("CloneEmpty(%s): dest columns %v, want %v (rewrite must not rename columns)", active, gotCols, wantCols)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + dest).Scan(&n); err != nil {
		t.Fatalf("count dest: %v", err)
	}
	if n != 0 {
		t.Fatalf("CloneEmpty(%s): dest row count %d, want 0", active, n)
	}
	for _, c := range gotCols {
		if c == dest {
			t.Fatalf("CloneEmpty(%s): column %q equals dest table name — identifier rewrite leaked into a column", active, c)
		}
	}

	if err := CreateIndexes(ctx, db, active); err != nil {
		t.Fatalf("CreateIndexes(%s): %v", active, err)
	}
	gotSets := indexedColumnSets(t, db, dest)
	if !equalStringSlice(wantSets, gotSets) {
		t.Fatalf("CreateIndexes(%s): dest indexed columns %v, want %v", active, gotSets, wantSets)
	}
	for _, name := range indexNamesByTable(t, db, dest) {
		if strings.Contains(name, dest) {
			t.Fatalf("CreateIndexes(%s): dest index %q contains dest table name %q (blind table rewrite)", active, name, dest)
		}
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustExecLock(t *testing.T, db *sql.DB, ctx context.Context, query string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query); err != nil {
		t.Fatalf("exec: %v\nSQL: %s", err, query)
	}
}
