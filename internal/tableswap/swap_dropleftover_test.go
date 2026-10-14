package tableswap

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// TestSwapDropsPriorStaleBeforeCutover verifies that when a prior
// t_to_be_dropped table already exists, Swap drops it inside the same
// transaction BEFORE the renames, then renames t → t_to_be_dropped and
// t_new → t atomically. The prior leftover (with a distinguishing marker row)
// must be gone, and the new t_to_be_dropped must hold the former active table's
// data. staleDropSkip is turned on via the package seam so the post-cutover
// DROP does not run; the seam is restored via t.Cleanup.
//
// This test sets up a stale leftover and turns on staleDropSkip only to prevent
// the post-cutover DROP from racing assertions, not to bypass the pre-rename
// DROP — that DROP must occur inside Swap's transaction regardless of staleDropSkip.
func TestSwapDropsPriorStaleBeforeCutover(t *testing.T) {
	// Restore staleDropSync afterward so other tests see the production default.
	staleDrop = staleDropSkip
	t.Cleanup(func() { staleDrop = staleDropSync })

	ctx := context.Background()
	db := openTestDB(t)

	// Active table t with a distinguishing row.
	if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, key TEXT, val TEXT)`); err != nil {
		t.Fatalf("create table t: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO t (id, key, val) VALUES (1, 'k1', 'hello')`); err != nil {
		t.Fatalf("insert into t: %v", err)
	}

	// Dest table t_new (empty), as CloneEmpty would create.
	if _, err := db.ExecContext(ctx, `CREATE TABLE t_new (id INTEGER PRIMARY KEY, key TEXT, val TEXT)`); err != nil {
		t.Fatalf("create table t_new: %v", err)
	}

	// Prior stale leftover with a distinguishing marker row so we can confirm
	// it is gone after Swap (not merely empty).
	if _, err := db.ExecContext(ctx, `CREATE TABLE t_to_be_dropped (marker TEXT)`); err != nil {
		t.Fatalf("create prior stale t_to_be_dropped: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO t_to_be_dropped (marker) VALUES ('OLD_LEFTOVER')`); err != nil {
		t.Fatalf("insert marker into prior stale t_to_be_dropped: %v", err)
	}

	// Cutover — must succeed even though a prior t_to_be_dropped exists.
	cpc, put := leaseSwapConn(t, db)
	swapErr := Swap(ctx, cpc, put, "t")
	if swapErr != nil {
		t.Fatalf("Swap returned error: %v", swapErr)
	}

	// The prior leftover t_to_be_dropped must be gone entirely. After a correct
	// Swap, the single t_to_be_dropped is the NEW stale (former active table),
	// whose schema has no 'marker' column. If the prior leftover's schema
	// survived, its 'marker' column would still be present, so assert it is
	// absent. Querying sqlite_master (not SELECT marker FROM t_to_be_dropped)
	// avoids a 'no such column' error against the new stale's schema.
	if tableExists(t, db, "table", "t_to_be_dropped") {
		var createSQL string
		if err := db.QueryRowContext(ctx,
			"SELECT sql FROM sqlite_master WHERE type='table' AND name='t_to_be_dropped'").Scan(&createSQL); err != nil {
			if !isNoRows(err) {
				t.Fatalf("select create sql for t_to_be_dropped: %v", err)
			}
		} else if strings.Contains(createSQL, "marker") {
			t.Fatal("t_to_be_dropped still carries the prior leftover's 'marker' schema")
		}
	}

	// After the cutover:
	//   t_new must be gone (renamed to t).
	if tableExists(t, db, "table", "t_new") {
		t.Error("expected t_new to no longer exist after Swap")
	}

	// t must be the former dest (empty — no rows).
	var tCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM t").Scan(&tCount); err != nil {
		t.Fatalf("count rows in t: %v", err)
	}
	if tCount != 0 {
		t.Errorf("expected t (former dest) to be empty, got %d rows", tCount)
	}

	// t_to_be_dropped must hold the former active table's data (the
	// distinguishing 'hello' row), proving it is the NEW stale, not the prior
	// leftover.
	var staleCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM t_to_be_dropped").Scan(&staleCount); err != nil {
		t.Fatalf("count rows in t_to_be_dropped: %v", err)
	}
	if staleCount != 1 {
		t.Fatalf("expected t_to_be_dropped to hold former active (1 row), got %d", staleCount)
	}
	var staleVal string
	if err := db.QueryRowContext(ctx, "SELECT val FROM t_to_be_dropped WHERE id=1").Scan(&staleVal); err != nil {
		t.Fatalf("select distinguishing row from t_to_be_dropped: %v", err)
	}
	if staleVal != "hello" {
		t.Errorf("expected t_to_be_dropped to hold former active val 'hello', got %q", staleVal)
	}
}

// isNoRows reports whether err is sql.ErrNoRows.
func isNoRows(err error) bool {
	return err != nil && errors.Is(err, sql.ErrNoRows)
}
