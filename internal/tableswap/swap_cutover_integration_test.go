package tableswap

import (
	"context"
	"testing"
)

// TestSwapCutoverRenamesActiveToStaleAndDestToActive verifies Swap after CloneEmpty:
//   - Swap runs its two renames in one transaction.
//   - After the cutover (with post-cutover DROP skipped), t is the former dest
//     (empty), t_new is gone, and t_to_be_dropped holds the former active table's data.
//   - When the dest table (t_new) is missing, Swap returns an error.
//
// The staleDropSkip seam is turned on so t_to_be_dropped is not dropped after
// cutover, letting the test observe it deterministically. The seam is restored
// to staleDropSync via t.Cleanup.
func TestSwapCutoverRenamesActiveToStaleAndDestToActive(t *testing.T) {
	staleDrop = staleDropSkip
	t.Cleanup(func() { staleDrop = staleDropSync })

	ctx := context.Background()

	// --- Scenario 1: Happy-path cutover after CloneEmpty. ---
	{
		db := openTestDB(t)

		// Active table t with a distinguishing row so we can identify the
		// former-active content after the cutover.
		if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, key TEXT, val TEXT)`); err != nil {
			t.Fatalf("create table t: %v", err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO t (id, key, val) VALUES (1, 'k1', 'hello')`); err != nil {
			t.Fatalf("insert into t: %v", err)
		}

		// Establish the dest table (empty) via CloneEmpty.
		if err := CloneEmpty(ctx, db, "t"); err != nil {
			t.Fatalf("CloneEmpty returned error: %v", err)
		}
		if !tableExists(t, db, "table", "t_new") {
			t.Fatal("precondition: t_new must exist after CloneEmpty")
		}

		// Cutover: Swap runs the renames in one transaction and Puts cpc.
		cpc, put := leaseSwapConn(t, db)
		if err := Swap(ctx, cpc, put, "t"); err != nil {
			t.Fatalf("Swap returned error: %v", err)
		}

		// t_new must be gone after the cutover.
		if tableExists(t, db, "table", "t_new") {
			t.Error("expected t_new to no longer exist after Swap")
		}

		// t_to_be_dropped must exist and must hold the former active table's
		// data (distinguishing row). Because staleDropSkip is on, the stale
		// table is not dropped after cutover and remains observable.
		if !tableExists(t, db, "table", "t_to_be_dropped") {
			t.Fatal("expected t_to_be_dropped to exist after Swap")
		}
		var staleCount int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM t_to_be_dropped").Scan(&staleCount); err != nil {
			t.Fatalf("count rows in t_to_be_dropped: %v", err)
		}
		if staleCount != 1 {
			t.Errorf("expected t_to_be_dropped to hold former active (1 row), got %d", staleCount)
		}
		var staleVal string
		if err := db.QueryRowContext(ctx, "SELECT val FROM t_to_be_dropped WHERE id=1").Scan(&staleVal); err != nil {
			t.Fatalf("select distinguishing row from t_to_be_dropped: %v", err)
		}
		if staleVal != "hello" {
			t.Errorf("expected former active val 'hello' in t_to_be_dropped, got %q", staleVal)
		}

		// t (the former dest) must be empty.
		var tCount int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM t").Scan(&tCount); err != nil {
			t.Fatalf("count rows in t: %v", err)
		}
		if tCount != 0 {
			t.Errorf("expected t (former dest) to be empty, got %d", tCount)
		}
	}

	// --- Scenario 2: Missing dest (t_new) must return an error. ---
	{
		db := openTestDB(t)
		if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, key TEXT)`); err != nil {
			t.Fatalf("create table t: %v", err)
		}
		// Deliberately do NOT run CloneEmpty: no t_new exists.
		cpc, put := leaseSwapConn(t, db)
		swapErr := Swap(ctx, cpc, put, "t")
		if swapErr == nil {
			t.Error("expected Swap to return an error when dest t_new is missing, got nil")
		}
	}
}
