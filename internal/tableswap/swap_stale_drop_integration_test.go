package tableswap

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

// TestSwap_HoldSeamReturnsBeforeDrop verifies the hold-seam contract:
//  1. Swap must return within a short timeout (100ms). If the DROP were
//     synchronous on the Swap goroutine, Swap would block on the held worker
//     and miss the timeout.
//  2. Immediately after Swap returns, t_to_be_dropped (the former active)
//     must STILL exist (DROP has not run yet) — proving Swap does not wait
//     for the DROP while the hold is engaged.
//  3. After the hold is released, the worker runs DROP in its own transaction
//     and t_to_be_dropped disappears.
//
// The hold seam spawns dropStale but blocks the worker before it acquires a
// DB connection or runs DROP. With SetMaxOpenConns(1), holding before BeginTx
// is mandatory: if the worker held a connection while blocked, the post-Swap
// queries below would hang.
//
// The leased CpConn (cpc) is obtained in the test goroutine BEFORE go Swap so
// that, while the hold is engaged, hold-period observations can be made via
// cpc.Conn (the single connection) without touching db. After the hold is
// released the worker owns the connection; post-release observations go
// through db only.
//
// The seam is restored to its default (staleDropSync) via t.Cleanup, and
// t.Cleanup(dropHold.release) guarantees the hold cannot leak and hang
// openTestDB's cleanup if a Fatalf fires during the hold.
func TestSwap_HoldSeamReturnsBeforeDrop(t *testing.T) {
	// Engage hold mode and a fresh hold gate; restore default afterward.
	staleDrop = staleDropHold
	dropHold = newDropHold()
	t.Cleanup(func() { staleDrop = staleDropSync })
	t.Cleanup(dropHold.release)

	ctx := context.Background()
	db := openTestDB(t)

	// Active table t with a distinguishing row so we can identify the
	// former-active content.
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

	// Lease the swap connection in the test goroutine BEFORE go Swap so cpc is
	// available for hold-period observations via cpc.Conn. Do NOT lease inside
	// the Swap goroutine.
	cpc, put := leaseSwapConn(t, db)

	// Wrap put so the test can observe when the hold-seam drop goroutine Puts the
	// connection. Pass putWrapped to Swap, not put.
	var putCalled atomic.Bool
	putWrapped := func(c *dbconnpool.CpConn) {
		putCalled.Store(true)
		put(c)
	}

	// Swap must return within 100ms even though the hold-seam DROP worker is held.
	done := make(chan error, 1)
	go func() { done <- Swap(ctx, cpc, putWrapped, "t") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Swap returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Swap did not return within 100ms; DROP may be synchronous inside Swap (held worker)")
	}

	// Immediately after Swap returns, the leased connection has NOT been Put
	// yet (the hold-seam drop goroutine owns the Put). Hold still engaged.
	if putCalled.Load() {
		t.Fatal("Swap Put the connection before the hold-seam drop finished (hold still engaged)")
	}

	// Immediately after Swap returns, the stale table must still exist: the
	// held worker has not run DROP yet. Observe via cpc.Conn (the lone
	// connection, currently idle) — do NOT touch db while the hold is engaged.
	var heldExists int
	if err := cpc.Conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='t_to_be_dropped'").Scan(&heldExists); err != nil {
		t.Fatalf("sqlite_master query via cpc.Conn (held): %v", err)
	}
	if heldExists == 0 {
		t.Fatal("expected t_to_be_dropped to still exist immediately after Swap (DROP held), but it is already gone")
	}

	// Cutover post-conditions: t_new must be gone (renamed to t) and t (the
	// former dest) must be empty. Still observed via cpc.Conn (hold engaged).
	var tNewGone int
	if err := cpc.Conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='t_new'").Scan(&tNewGone); err != nil {
		t.Fatalf("sqlite_master query t_new via cpc.Conn (held): %v", err)
	}
	if tNewGone != 0 {
		t.Error("expected t_new to no longer exist after Swap")
	}
	var tCount int
	if err := cpc.Conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM t").Scan(&tCount); err != nil {
		t.Fatalf("count rows in t via cpc.Conn (held): %v", err)
	}
	if tCount != 0 {
		t.Errorf("expected t (former dest) to be empty, got %d rows", tCount)
	}

	// Release the hold; the worker runs DROP in its own transaction and Puts.
	// The body also calls release to satisfy the hold observations above, so
	// release MUST be idempotent (dropHoldGate uses sync.Once around close).
	// t.Cleanup(dropHold.release) is the safety net for the hold-leak case.
	dropHold.release()

	// Wait until the hold-seam drop goroutine has Put (deadline 2s). This proves
	// DROP ran to completion asynchronously. After release, do NOT touch
	// cpc.Conn — the worker owns it.
	deadline := time.Now().Add(2 * time.Second)
	for !putCalled.Load() {
		if time.Now().After(deadline) {
			t.Fatal("hold-seam drop did not Put the connection within 2s of release")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// After Put, DROP has committed: the stale table must be gone. Observe via
	// *sql.DB (never cpc.Conn).
	if tableExists(t, db, "table", "t_to_be_dropped") {
		t.Fatal("expected t_to_be_dropped to be dropped after the hold-seam drop Puts")
	}
}

// TestSwap_HoldSeamPutsAfterRelease verifies the hold-seam contract for connection
// ownership: after a successful cutover, Swap returns WITHOUT Putting the
// leased connection (the hold-seam drop goroutine owns the Put). The test releases
// the hold and waits until the drop goroutine has Put, then asserts the stale
// table is gone via *sql.DB (never cpc.Conn, which the worker owns once
// released).
func TestSwap_HoldSeamPutsAfterRelease(t *testing.T) {
	staleDrop = staleDropHold
	dropHold = newDropHold()
	t.Cleanup(func() { staleDrop = staleDropSync })
	t.Cleanup(dropHold.release)

	ctx := context.Background()
	db := openTestDB(t)

	if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, key TEXT, val TEXT)`); err != nil {
		t.Fatalf("create table t: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO t (id, key, val) VALUES (1, 'k1', 'hello')`); err != nil {
		t.Fatalf("insert into t: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE t_new (id INTEGER PRIMARY KEY, key TEXT, val TEXT)`); err != nil {
		t.Fatalf("create table t_new: %v", err)
	}

	// putCalled is shared with the drop goroutine; a plain bool would race
	// under go test -race, so it is an atomic.Bool.
	var putCalled atomic.Bool
	cpc, put := leaseSwapConn(t, db)
	putWrapped := func(c *dbconnpool.CpConn) {
		putCalled.Store(true)
		put(c)
	}

	// Swap must return within 100ms; the hold-seam DROP worker is held. Bound the
	// return with a select deadline, not a sleep.
	done := make(chan error, 1)
	go func() { done <- Swap(ctx, cpc, putWrapped, "t") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Swap returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Swap did not return within 100ms; DROP may be synchronous inside Swap (held worker)")
	}

	// Swap returned WITHOUT Put (the drop goroutine owns it). Assert
	// immediately, no sleep.
	if putCalled.Load() {
		t.Fatal("Swap Put the connection before the hold-seam drop finished (hold still engaged)")
	}

	// Release the hold; the worker runs DROP and Puts.
	dropHold.release()

	// Wait until the drop goroutine has Put (deadline 2s).
	deadline := time.Now().Add(2 * time.Second)
	for !putCalled.Load() {
		if time.Now().After(deadline) {
			t.Fatal("hold-seam drop did not Put the connection within 2s of release")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// After Put, the stale table must be gone — observed via *sql.DB, never
	// cpc.Conn (the worker owned and released it).
	if tableExists(t, db, "table", "t_to_be_dropped") {
		t.Fatal("expected t_to_be_dropped to be dropped after the hold-seam drop Puts")
	}
}

// TestSwapPutOnCutoverError verifies that when Swap fails at cutover (no
// t_new), it returns an error AND Puts the leased connection before returning.
// No hold seam is engaged, so Swap Puts on the cutover error path.
func TestSwapPutOnCutoverError(t *testing.T) {
	staleDrop = staleDropSync
	t.Cleanup(func() { staleDrop = staleDropSync })

	ctx := context.Background()
	db := openTestDB(t)

	if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, key TEXT)`); err != nil {
		t.Fatalf("create table t: %v", err)
	}
	// Deliberately no t_new: Swap must error at cutover.

	var putCalled atomic.Bool
	cpc, put := leaseSwapConn(t, db)
	putWrapped := func(c *dbconnpool.CpConn) {
		putCalled.Store(true)
		put(c)
	}

	swapErr := Swap(ctx, cpc, putWrapped, "t")
	if swapErr == nil {
		t.Fatal("expected Swap to return an error when dest t_new is missing, got nil")
	}
	if !putCalled.Load() {
		t.Fatal("expected Swap to Put the connection on cutover error before returning")
	}
}

// TestSwap_DefaultDropCompletesBeforeReturn is the production contract: with
// the default seam (staleDropSync), Swap does not return until DROP TABLE of
// t_to_be_dropped has finished and the leased connection has been Put.
func TestSwap_DefaultDropCompletesBeforeReturn(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, key TEXT, val TEXT)`); err != nil {
		t.Fatalf("create table t: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO t (id, key, val) VALUES (1, 'k1', 'hello')`); err != nil {
		t.Fatalf("insert into t: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE t_new (id INTEGER PRIMARY KEY, key TEXT, val TEXT)`); err != nil {
		t.Fatalf("create table t_new: %v", err)
	}

	var putCalled atomic.Bool
	cpc, put := leaseSwapConn(t, db)
	putWrapped := func(c *dbconnpool.CpConn) {
		putCalled.Store(true)
		put(c)
	}

	if err := Swap(ctx, cpc, putWrapped, "t"); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if !putCalled.Load() {
		t.Fatal("Swap returned before DROP TABLE Put the connection")
	}
	if tableExists(t, db, "table", "t_to_be_dropped") {
		t.Fatal("t_to_be_dropped still exists after Swap returned")
	}
}
