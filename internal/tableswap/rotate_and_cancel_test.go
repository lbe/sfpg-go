package tableswap

import (
	"context"
	"database/sql"
	"slices"
	"testing"
)

// TestIndexNamesAfterRotate covers the full rotation lifecycle for index
// naming: after the first CloneEmpty+CreateIndexes+Swap, the live table keeps
// idx_t_x_1 (the active index is preserved under its rotated name), and after
// the stale table is dropped a second CloneEmpty+CreateIndexes reuses idx_t_x
// instead of allocating idx_t_x_1_1. This exercises the identifier-bounded
// name allocation (allocateIndexName/stripIndexSuffix) across a live rotation
// rather than the unit-level name math alone.
func TestIndexNamesAfterRotate(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	mustExec(t, db, ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)")
	mustExec(t, db, ctx, "CREATE INDEX idx_t_x ON t(v)")

	if err := CloneEmpty(ctx, db, "t"); err != nil {
		t.Fatalf("CloneEmpty 1: %v", err)
	}
	if err := CreateIndexes(ctx, db, "t"); err != nil {
		t.Fatalf("CreateIndexes 1: %v", err)
	}

	// First rotation: dest must get idx_t_x_1, never idx_t_x or idx_t_new_x.
	first := indexNamesByTable(t, db, "t_new")
	if !equalStringSet(first, []string{"idx_t_x_1"}) {
		t.Fatalf("first CreateIndexes: want {idx_t_x_1}, got %v", first)
	}

	// Skip the post-cutover DROP so the stale table is observable and the
	// second rotation is deterministic; restore the default via t.Cleanup.
	staleDrop = staleDropSkip
	t.Cleanup(func() { staleDrop = staleDropSync })

	cpc, put := leaseSwapConn(t, db)
	if err := Swap(ctx, cpc, put, "t"); err != nil {
		t.Fatalf("Swap: %v", err)
	}

	// After cutover, the live table (former dest) carries idx_t_x_1; the stale
	// table (former active) still carries idx_t_x.
	live := indexNamesByTable(t, db, "t")
	if !equalStringSet(live, []string{"idx_t_x_1"}) {
		t.Fatalf("post-swap live t: want {idx_t_x_1}, got %v", live)
	}
	stale := indexNamesByTable(t, db, "t_to_be_dropped")
	if !equalStringSet(stale, []string{"idx_t_x"}) {
		t.Fatalf("post-swap stale: want {idx_t_x}, got %v", stale)
	}

	// Drop the stale table so idx_t_x becomes free again.
	mustExec(t, db, ctx, "DROP TABLE IF EXISTS t_to_be_dropped")

	// Second rotation on the now-live table: idx_t_x must be reused, NOT
	// allocated as idx_t_x_1_1.
	if err := CloneEmpty(ctx, db, "t"); err != nil {
		t.Fatalf("CloneEmpty 2: %v", err)
	}
	if err := CreateIndexes(ctx, db, "t"); err != nil {
		t.Fatalf("CreateIndexes 2: %v", err)
	}
	second := indexNamesByTable(t, db, "t_new")
	if !equalStringSet(second, []string{"idx_t_x"}) {
		t.Fatalf("second CreateIndexes: want {idx_t_x}, got %v", second)
	}
}

// TestCanceledContextReturnsError verifies that CloneEmpty, CreateIndexes, and
// Swap each return an error wrapping context.Canceled when invoked with an
// already-canceled context, and that no operation leaves committed partial
// state. Postconditions are asserted per operation, not collapsed into a
// single Swap-only guard.
func TestCanceledContextReturnsError(t *testing.T) {
	ctxSetup := context.Background()
	db := openTestDB(t)

	mustExec(t, db, ctxSetup, "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)")
	mustExec(t, db, ctxSetup, "CREATE INDEX idx_t_v ON t(v)")
	mustExec(t, db, ctxSetup, "INSERT INTO t (id, v) VALUES (1, 'keep')")

	cases := []struct {
		name string
		// prep runs before the canceled op (using a live context) to set up
		// the precondition each operation requires.
		prep func()
		// run is the operation invoked with the canceled context.
		run func(ctx context.Context) error
		// post asserts the per-operation postcondition after the canceled op.
		post func(t *testing.T, db *sql.DB)
	}{
		{
			name: "CloneEmpty",
			prep: func() {},
			run: func(ctx context.Context) error {
				// No t_new exists yet: a committed clone would create it.
				return CloneEmpty(ctx, db, "t")
			},
			post: func(t *testing.T, db *sql.DB) {
				if tableExists(t, db, "table", "t_new") {
					t.Errorf("CloneEmpty committed under canceled ctx: t_new exists")
				}
			},
		},
		{
			name: "CreateIndexes",
			prep: func() {
				// Destination exists so CreateIndexes reaches index
				// allocation; a committed run would copy idx_t_v onto t_new.
				mustExec(t, db, ctxSetup, "CREATE TABLE IF NOT EXISTS t_new (id INTEGER PRIMARY KEY, v TEXT)")
			},
			run: func(ctx context.Context) error {
				return CreateIndexes(ctx, db, "t")
			},
			post: func(t *testing.T, db *sql.DB) {
				// The index must NOT have been copied onto t_new.
				idx := indexNamesByTable(t, db, "t_new")
				if contains(idx, "idx_t_v") {
					t.Errorf("CreateIndexes committed under canceled ctx: idx_t_v copied to t_new")
				}
			},
		},
		{
			name: "Swap",
			prep: func() {
				// Destination exists so Swap reaches the rename; a committed
				// swap would promote t_new to t and rename t to the stale.
				mustExec(t, db, ctxSetup, "CREATE TABLE IF NOT EXISTS t_new (id INTEGER PRIMARY KEY, v TEXT)")
			},
			run: func(ctx context.Context) error {
				cpc, put := leaseSwapConn(t, db)
				return Swap(ctx, cpc, put, "t")
			},
			post: func(t *testing.T, db *sql.DB) {
				// t must not have been renamed away: the marker row stays on t.
				var cnt int
				if err := db.QueryRowContext(ctxSetup, "SELECT COUNT(*) FROM t WHERE id=1 AND v='keep'").Scan(&cnt); err != nil {
					t.Fatalf("query t marker: %v", err)
				}
				if cnt != 1 {
					t.Errorf("Swap promoted t under canceled ctx: marker row gone from t (cnt=%d)", cnt)
				}
				// No stale rename occurred.
				if tableExists(t, db, "table", "t_to_be_dropped") {
					t.Errorf("Swap committed under canceled ctx: t_to_be_dropped exists")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.prep()

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			err := tc.run(ctx)
			if err == nil {
				t.Fatalf("%s: expected error with canceled ctx, got nil", tc.name)
			}

			tc.post(t, db)
		})
	}
}

func mustExec(t *testing.T, db *sql.DB, ctx context.Context, query string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}

func equalStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]int, len(a))
	for _, s := range a {
		set[s]++
	}
	for _, s := range b {
		set[s]--
		if set[s] < 0 {
			return false
		}
	}
	return true
}
