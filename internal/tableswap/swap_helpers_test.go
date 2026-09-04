package tableswap

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

// leaseSwapConn leases a single *sql.Conn from db and wraps it as a
// *dbconnpool.CpConn for use with Swap. The returned put func closes only the
// leased *sql.Conn (never cpc.Close, which panics because Queries is nil). put
// is idempotent (guarded by its own sync.Once) so it is safe to register it via
// t.Cleanup AND pass it to Swap: Swap Puts via its own sync.Once, so a double
// Put (cleanup + Swap) is harmless.
//
// With SetMaxOpenConns(1) the lease is the only outstanding checkout, so the
// registered cleanup guarantees openTestDB's db.Close() never blocks on a
// leaked Conn — even if a test Fatalfs before releasing a hold.
func leaseSwapConn(t *testing.T, db *sql.DB) (*dbconnpool.CpConn, func(*dbconnpool.CpConn)) {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("lease swap conn: %v", err)
	}
	cpc := &dbconnpool.CpConn{Conn: conn}
	var once sync.Once
	put := func(_ *dbconnpool.CpConn) {
		once.Do(func() { _ = conn.Close() })
	}
	t.Cleanup(func() { put(nil) })
	return cpc, put
}
