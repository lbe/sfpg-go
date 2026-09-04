package tableswap

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"testing"
)

// openTestDBForIndexes opens an in-memory SQLite database for CreateIndexes
// integration tests, mirroring the openTestDB helper used in the CloneEmpty
// tests.
func openTestDBForIndexes(t *testing.T) *sql.DB {
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

// countIndexesByTable returns the count of sqlite_master index entries for the
// given table. When excludeAutoindex is true, sqlite_autoindex_* names are
// excluded (only explicit CREATE INDEX entries are counted).
func countIndexesByTable(t *testing.T, db *sql.DB, tblName string, excludeAutoindex bool) int {
	t.Helper()
	query := "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name=?"
	args := []any{tblName}
	if excludeAutoindex {
		query += " AND name NOT LIKE 'sqlite_autoindex_%'"
	}
	var count int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count indexes for %s: %v", tblName, err)
	}
	return count
}

// indexNamesByTable returns the non-autoindex index names for a table in
// sqlite_master, ordered by name for determinism.
func indexNamesByTable(t *testing.T, db *sql.DB, tblName string) []string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='index' AND tbl_name=? AND name NOT LIKE 'sqlite_autoindex_%' ORDER BY name", tblName)
	if err != nil {
		t.Fatalf("query index names for %s: %v", tblName, err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan index name: %v", err)
		}
		names = append(names, n)
	}
	return names
}

// indexedColumnSets returns, for each explicit (non-autoindex) index on table,
// the ordered comma-joined column names from PRAGMA index_info. The result is
// sorted by the column set string so callers can compare multisets directly.
func indexedColumnSets(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	names := indexNamesByTable(t, db, table)
	var sets []string
	for _, name := range names {
		cols := indexColumns(t, db, name)
		sets = append(sets, strings.Join(cols, ","))
	}
	sort.Strings(sets)
	return sets
}

// indexColumns returns the ordered column names for an index via
// PRAGMA index_info.
func indexColumns(t *testing.T, db *sql.DB, indexName string) []string {
	t.Helper()
	rows, err := db.Query("PRAGMA index_info(" + indexName + ")")
	if err != nil {
		t.Fatalf("pragma index_info(%s): %v", indexName, err)
	}
	defer rows.Close()
	type infoRow struct {
		seq  int
		cid  int
		name string
	}
	var collected []infoRow
	for rows.Next() {
		var r infoRow
		if err := rows.Scan(&r.seq, &r.cid, &r.name); err != nil {
			t.Fatalf("scan pragma index_info row: %v", err)
		}
		collected = append(collected, r)
	}
	sort.Slice(collected, func(i, j int) bool {
		return collected[i].seq < collected[j].seq
	})
	var cols []string
	for _, r := range collected {
		cols = append(cols, r.name)
	}
	return cols
}

// indexIsUnique reports whether the index with the given name on the given
// table is unique. PRAGMA index_list returns columns in the order
// [seq name unique origin partial]; the third column (unique) is 1 for unique
// indexes and 0 otherwise.
func indexIsUnique(t *testing.T, db *sql.DB, table, indexName string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA index_list(" + table + ")")
	if err != nil {
		t.Fatalf("pragma index_list(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		// Bind in the exact column order returned by PRAGMA index_list so that
		// uniqueFlag receives the 'unique' flag (column 3), not the 'partial'
		// flag (column 5). The previous code bound them out of order, causing
		// the unique scan to always read the partial flag (always 0).
		var seq int
		var name string
		var uniqueFlag int
		var origin, partial sql.NullString
		if err := rows.Scan(&seq, &name, &uniqueFlag, &origin, &partial); err != nil {
			t.Fatalf("scan pragma index_list row: %v", err)
		}
		if name == indexName {
			return uniqueFlag != 0
		}
	}
	return false
}

// findIndexByNameSet finds the index name on table whose indexed column set
// matches the given ordered columns. Returns "" if not found.
func findIndexByNameSet(t *testing.T, db *sql.DB, table string, cols []string) string {
	t.Helper()
	names := indexNamesByTable(t, db, table)
	for _, name := range names {
		got := indexColumns(t, db, name)
		if strings.Join(got, ",") == strings.Join(cols, ",") {
			return name
		}
	}
	return ""
}

// TestCreateIndexes_RestoresTempStore verifies that CreateIndexes preserves
// the connection's PRAGMA temp_store value after returning (T4 gate). Two
// subtests cover the default (0) and explicit-MEMORY (2) values.
func TestCreateIndexes_RestoresTempStore(t *testing.T) {
	ctx := context.Background()

	t.Run("default-0-roundtrip", func(t *testing.T) {
		db := openTestDBForIndexes(t)
		if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, key TEXT)`); err != nil {
			t.Fatalf("create table t: %v", err)
		}
		if _, err := db.ExecContext(ctx, `CREATE INDEX idx_t_key ON t (key)`); err != nil {
			t.Fatalf("create index: %v", err)
		}
		if err := CloneEmpty(ctx, db, "t"); err != nil {
			t.Fatalf("CloneEmpty: %v", err)
		}

		var saved int
		if err := db.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&saved); err != nil {
			t.Fatalf("pragma temp_store: %v", err)
		}

		if err := CreateIndexes(ctx, db, "t"); err != nil {
			t.Fatalf("CreateIndexes: %v", err)
		}

		var got int
		if err := db.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&got); err != nil {
			t.Fatalf("pragma temp_store after: %v", err)
		}
		if got != saved {
			t.Errorf("temp_store changed: saved=%d, got=%d", saved, got)
		}
	})

	t.Run("explicit-2-roundtrip", func(t *testing.T) {
		db := openTestDBForIndexes(t)
		if _, err := db.ExecContext(ctx, "PRAGMA temp_store=2"); err != nil {
			t.Fatalf("set temp_store=2: %v", err)
		}
		var saved int
		if err := db.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&saved); err != nil {
			t.Fatalf("pragma temp_store: %v", err)
		}
		if saved != 2 {
			t.Fatalf("expected temp_store=2, got %d", saved)
		}

		if _, err := db.ExecContext(ctx, `CREATE TABLE t2 (id INTEGER PRIMARY KEY, key TEXT)`); err != nil {
			t.Fatalf("create table t2: %v", err)
		}
		if _, err := db.ExecContext(ctx, `CREATE INDEX idx_t2_key ON t2 (key)`); err != nil {
			t.Fatalf("create index: %v", err)
		}
		if err := CloneEmpty(ctx, db, "t2"); err != nil {
			t.Fatalf("CloneEmpty: %v", err)
		}

		if err := CreateIndexes(ctx, db, "t2"); err != nil {
			t.Fatalf("CreateIndexes: %v", err)
		}

		var got int
		if err := db.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&got); err != nil {
			t.Fatalf("pragma temp_store after: %v", err)
		}
		if got != 2 {
			t.Errorf("temp_store changed: saved=%d, got=%d", saved, got)
		}
	})
}

// assertUniquenessPreserved verifies that for every explicit index column set
// present on src, the matching index on dest has the same unique flag. This
// catches a CreateIndexes that silently drops the UNIQUE keyword from an
// explicit CREATE UNIQUE INDEX — the previous test only asserted uniqueness of
// a single non-unique index against itself, producing a tautology
// (false != false -> always false -> never failing).
func assertUniquenessPreserved(t *testing.T, db *sql.DB, src, dest string) {
	t.Helper()
	srcSets := indexedColumnSets(t, db, src)
	for _, cs := range srcSets {
		srcCols := strings.Split(cs, ",")
		srcName := findIndexByNameSet(t, db, src, srcCols)
		destName := findIndexByNameSet(t, db, dest, srcCols)
		if destName == "" {
			t.Errorf("no index with columns %q on %s; uniqueness not comparable", cs, dest)
			continue
		}
		srcUnique := indexIsUnique(t, db, src, srcName)
		destUnique := indexIsUnique(t, db, dest, destName)
		if srcUnique != destUnique {
			t.Errorf("uniqueness mismatch for columns %q: %s=%v, %s=%v",
				cs, src, srcUnique, dest, destUnique)
		}
	}
}

// TestCreateIndexesCopiesExplicitIndexesOnActiveTable verifies that after
// CloneEmpty creates the destination table t_new, CreateIndexes copies every
// explicit (non-sqlite_autoindex) index defined on the active table t onto
// t_new — preserving the indexed columns and uniqueness — and does not copy
// indexes belonging to other tables. It also exercises the cross-table
// OCCUPANCY requirement: a name live on a sibling table must not be reused.
func TestCreateIndexesCopiesExplicitIndexesOnActiveTable(t *testing.T) {
	ctx := context.Background()
	db := openTestDBForIndexes(t)

	// Active table t with:
	//   - a UNIQUE constraint on key (yields a skipped sqlite_autoindex_t_1)
	//   - two explicit non-unique indexes
	//   - one explicit UNIQUE index (idx_t_val) so uniqueness preservation is
	//     actually tested with a unique != unique case, not a false tautology.
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE t (
			id  INTEGER PRIMARY KEY,
			key TEXT NOT NULL,
			val TEXT
		)`); err != nil {
		t.Fatalf("create table t: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX idx_t_key ON t (key)`); err != nil {
		t.Fatalf("create index idx_t_key: %v", err)
	}
	// Second explicit index — a composite one to verify column preservation.
	if _, err := db.ExecContext(ctx, `CREATE INDEX idx_t_val_key ON t (val, key)`); err != nil {
		t.Fatalf("create index idx_t_val_key: %v", err)
	}
	// Explicit UNIQUE index so assertUniquenessPreserved sees a real
	// unique-flagged index (unique=1) on both t and t_new.
	if _, err := db.ExecContext(ctx, `CREATE UNIQUE INDEX idx_t_val ON t (val)`); err != nil {
		t.Fatalf("create unique index idx_t_val: %v", err)
	}

	// A third table whose index name collides with what allocateIndexName
	// would first try when copying idx_t_key: the base name idx_t_key is taken
	// by t's own index, so allocateIndexName's first candidate is idx_t_key_1.
	// Because SQLite index names are DB-global, allocating idx_t_key_1 onto
	// t_new would collide with third_table's index. This forces CreateIndexes
	// to consult the OCCUPANCY of ALL tables (every index name in
	// sqlite_master, not just t's) when allocating. A CreateIndexes that only
	// scanned t's index names would allocate idx_t_key_1, hit a SQLite
	// duplicate-name error, and the test would fail.
	if _, err := db.ExecContext(ctx, `CREATE TABLE third_table (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table third_table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX idx_t_key_1 ON third_table (id)`); err != nil {
		t.Fatalf("create index idx_t_key_1 on third_table: %v", err)
	}

	// Snapshot the full set of index names across ALL tables before
	// CreateIndexes runs. This is the OCCUPANCY set: every index name in
	// sqlite_master (all tables) plus names already allocated in this run.
	// No allocated name on t_new may collide with this set.
	preIndexes := indexNamesByTable(t, db, "t")
	preIndexes = append(preIndexes, indexNamesByTable(t, db, "third_table")...)

	// CloneEmpty: creates t_new with t's schema.
	if err := CloneEmpty(ctx, db, "t"); err != nil {
		t.Fatalf("CloneEmpty returned error: %v", err)
	}

	// Precondition: t has at least two explicit indexes (acceptance criterion).
	tExplicitCount := countIndexesByTable(t, db, "t", true)
	if tExplicitCount < 2 {
		t.Fatalf("precondition failed: expected t to have at least 2 explicit indexes, got %d", tExplicitCount)
	}

	// CreateIndexes: should copy explicit indexes from t onto t_new.
	if err := CreateIndexes(ctx, db, "t"); err != nil {
		t.Fatalf("CreateIndexes returned error: %v", err)
	}

	// Assert: t retains at least two explicit indexes.
	afterExplicit := countIndexesByTable(t, db, "t", true)
	if afterExplicit < 2 {
		t.Errorf("expected t to retain at least 2 explicit indexes, got %d", afterExplicit)
	}

	// Assert: t_new has the same number of explicit indexes as t (every
	// COPY-set index was allocated and CREATE'd on t_new).
	tNewExplicit := countIndexesByTable(t, db, "t_new", true)
	if tNewExplicit != afterExplicit {
		t.Errorf("expected t_new to have %d explicit indexes (same as t), got %d", afterExplicit, tNewExplicit)
	}

	// Assert: no sqlite_autoindex_* entries were created on t_new — only the
	// explicit indexes from t were copied.
	tNewAll := countIndexesByTable(t, db, "t_new", false)
	if tNewAll != afterExplicit {
		t.Errorf("expected t_new to have %d total index entries (no autoindex), got %d", afterExplicit, tNewAll)
	}

	// Assert: third_table's index was NOT copied to t_new.
	thirdCount := countIndexesByTable(t, db, "third_table", true)
	if thirdCount != 1 {
		t.Errorf("expected third_table to retain its 1 explicit index, got %d", thirdCount)
	}

	// Assert: every index on t_new has the same columns as a corresponding
	// index on t (multiset of column sequences must match).
	tColSets := indexedColumnSets(t, db, "t")
	tNewColSets := indexedColumnSets(t, db, "t_new")
	if len(tColSets) != len(tNewColSets) {
		t.Fatalf("column-set count mismatch: t=%d, t_new=%d", len(tColSets), len(tNewColSets))
	}
	tSet := make(map[string]int)
	for _, cs := range tColSets {
		tSet[cs]++
	}
	for _, cs := range tNewColSets {
		if tSet[cs] == 0 {
			t.Errorf("t_new has index on columns %q which does not exist on t", cs)
		}
		tSet[cs]--
	}

	// Assert: uniqueness is preserved for EVERY COPY-set explicit index on t_new
	// versus t. This now exercises a real unique vs. unique comparison via the
	// explicit CREATE UNIQUE INDEX (idx_t_val), so a CreateIndexes that drops
	// UNIQUE would fail here.
	assertUniquenessPreserved(t, db, "t", "t_new")

	// Assert: allocated index names on t_new do not collide with ANY name
	// already present in sqlite_master before CreateIndexes ran (the OCCUPANCY
	// set across all tables). Since idx_t_key_1 is live on third_table, the
	// allocation for idx_t_key must have avoided that name (e.g. idx_t_key_2).
	preSet := make(map[string]struct{}, len(preIndexes))
	for _, n := range preIndexes {
		preSet[n] = struct{}{}
	}
	tNewNames := indexNamesByTable(t, db, "t_new")
	for _, n := range tNewNames {
		if _, collides := preSet[n]; collides {
			t.Errorf("t_new index %q collides with an existing index name in the OCCUPANCY set (all tables)", n)
		}
	}

	// Assert: the unique index (idx_t_val columns [val]) on t_new is actually
	// flagged unique, proving indexIsUnique reads the correct PRAGMA column.
	uniqueIdxName := findIndexByNameSet(t, db, "t_new", []string{"val"})
	if uniqueIdxName == "" {
		t.Fatal("expected a unique index on val in t_new")
	}
	if !indexIsUnique(t, db, "t_new", uniqueIdxName) {
		t.Errorf("expected t_new index %q (columns: val) to be unique, got non-unique", uniqueIdxName)
	}
}
