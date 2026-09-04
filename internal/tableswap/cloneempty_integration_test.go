package tableswap

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
)

// openTestDB opens an in-memory SQLite database suitable for CloneEmpty tests.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	// Set busy_timeout so a leftover DROP that is serialized behind the single
	// connection waits rather than failing SQLITE_BUSY immediately.
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("set busy_timeout: %v", err)
	}
	return db
}

// colNames returns the ordered column names of a table by querying PRAGMA table_info.
func colNames(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull sql.NullString
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan pragma row: %v", err)
		}
		names = append(names, name)
	}
	return names
}

// tableExists checks sqlite_master for a row of the given type and name.
func tableExists(t *testing.T, db *sql.DB, sqlType, name string) bool {
	t.Helper()
	var found int
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type=? AND name=?", sqlType, name).Scan(&found)
	if err != nil {
		t.Fatalf("query sqlite_master for %s %q: %v", sqlType, name, err)
	}
	return found > 0
}

// TestCloneEmptyCopiesEmptyTable verifies that CloneEmpty creates an empty
// destination table with the same columns, PK, and UNIQUE constraints as the
// active table, without cloning triggers or views, and that the leftover
// dest marker is removed.
func TestCloneEmptyCopiesEmptyTable(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// Active table t: columns, PK, UNIQUE, a row, a trigger, and a view naming t.
	_, err := db.ExecContext(ctx, `
		CREATE TABLE t (
			id  INTEGER PRIMARY KEY,
			key TEXT NOT NULL,
			val TEXT,
			UNIQUE (key)
		)`)
	if err != nil {
		t.Fatalf("create table t: %v", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO t (id, key, val) VALUES (1, 'k1', 'hello')`)
	if err != nil {
		t.Fatalf("insert into t: %v", err)
	}

	_, err = db.ExecContext(ctx, `CREATE TRIGGER t_trigger AFTER INSERT ON t BEGIN SELECT 1; END`)
	if err != nil {
		t.Fatalf("create trigger on t: %v", err)
	}

	_, err = db.ExecContext(ctx, `CREATE VIEW t_view AS SELECT * FROM t`)
	if err != nil {
		t.Fatalf("create view on t: %v", err)
	}

	// Leftover dest t_new with a distinguishing marker (extra column).
	_, err = db.ExecContext(ctx, `CREATE TABLE t_new (marker TEXT)`)
	if err != nil {
		t.Fatalf("create leftover t_new: %v", err)
	}

	// Run CloneEmpty.
	cloneErr := CloneEmpty(ctx, db, "t")
	if cloneErr != nil {
		t.Errorf("CloneEmpty returned error: %v", cloneErr)
	}

	// t_new must exist as a table in sqlite_master.
	if !tableExists(t, db, "table", "t_new") {
		t.Fatal("expected t_new to exist in sqlite_master as type=table")
	}

	// t_new must have the same columns as t (not the leftover marker col).
	gotCols := colNames(t, db, "t_new")
	wantCols := []string{"id", "key", "val"}
	if len(gotCols) != len(wantCols) || !equalStringSlices(gotCols, wantCols) {
		t.Errorf("t_new columns = %v, want %v", gotCols, wantCols)
	}

	// t_new must contain 0 rows.
	var rowCount int
	err = db.QueryRow("SELECT COUNT(*) FROM t_new").Scan(&rowCount)
	if err != nil {
		t.Fatalf("count rows in t_new: %v", err)
	}
	if rowCount != 0 {
		t.Errorf("expected 0 rows in t_new, got %d", rowCount)
	}

	// t_new must have a PRIMARY KEY (on id).
	rows, err := db.Query("PRAGMA table_info(t_new)")
	if err != nil {
		t.Fatalf("pragma table_info(t_new): %v", err)
	}
	defer rows.Close()
	hasPK := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull sql.NullString
		var dfltValue sql.NullString
		var pk int
		if scanErr := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); scanErr != nil {
			t.Fatalf("scan pragma: %v", scanErr)
		}
		if pk > 0 {
			hasPK = true
		}
	}
	if !hasPK {
		t.Error("expected t_new to have a PRIMARY KEY")
	}

	// t_new must have a UNIQUE constraint (on key), verifiable via
	// the CREATE SQL in sqlite_master containing UNIQUE.
	var createSQL string
	err = db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='t_new'").Scan(&createSQL)
	if err != nil {
		t.Fatalf("query sqlite_master sql for t_new: %v", err)
	}
	if !strings.Contains(strings.ToUpper(createSQL), "UNIQUE") {
		t.Errorf("expected t_new CREATE SQL to contain UNIQUE, got: %s", createSQL)
	}

	// t_new must NOT have a trigger associated with it.
	var triggerCount int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND tbl_name='t_new'").Scan(&triggerCount)
	if err != nil {
		t.Fatalf("count triggers on t_new: %v", err)
	}
	if triggerCount > 0 {
		t.Errorf("expected t_new to have no triggers, found %d", triggerCount)
	}

	// t_new must NOT be referenced by any view (the fixture view t_view
	// selects from t, not t_new). A vacuous check for a view named "t_new"
	// would always pass; instead verify no view SQL mentions t_new.
	var viewRefCount int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='view' AND sql LIKE '%t_new%'").Scan(&viewRefCount)
	if err != nil {
		t.Fatalf("count views referencing t_new: %v", err)
	}
	if viewRefCount > 0 {
		t.Errorf("expected no view referencing t_new, found %d", viewRefCount)
	}

	// Package godoc must state triggers and views are unsupported.
	docPath := "db.go"
	docContent, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	if !strings.Contains(string(docContent), "triggers and views are unsupported") {
		t.Errorf("package godoc must state triggers and views are unsupported; %s content does not contain required statement", docPath)
	}
}

// TestCloneEmptyMissingActiveTableReturnsError verifies that CloneEmpty
// returns an error when the active table does not exist, and that no
// destination table is committed as a side effect.
func TestCloneEmptyMissingActiveTableReturnsError(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// No table named "nonexistent" exists in the database.
	if err := CloneEmpty(ctx, db, "nonexistent"); err == nil {
		t.Fatal("expected CloneEmpty to return an error for a nonexistent active table, got nil")
	}

	// No destination table should have been created/committed.
	if tableExists(t, db, "table", "nonexistent_new") {
		t.Fatal("expected no dest table to be committed after error from CloneEmpty on nonexistent active table")
	}
}

func equalStringSlices(a, b []string) bool {
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
