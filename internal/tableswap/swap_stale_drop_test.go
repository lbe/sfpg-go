package tableswap

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

// TestSwapPutOnCutoverError verifies that when Swap fails at cutover (no
// t_new), it returns an error AND Puts the leased connection before returning.
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
