package tableswap

import (
	"context"
	"database/sql"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

// Compile-time checks: *sql.DB satisfies DB, and the public functions have the
// required signatures. Do not invoke the functions with a fake: BeginTx
// returning (nil, nil) is not a *sql.DB behavior. Swap takes a dedicated
// *dbconnpool.CpConn plus a put func that the caller must not reuse.
var (
	_ DB                                                                                = (*sql.DB)(nil)
	_ func(context.Context, DB, string) error                                           = CloneEmpty
	_ func(context.Context, DB, string) error                                           = CreateIndexes
	_ func(context.Context, *dbconnpool.CpConn, func(*dbconnpool.CpConn), string) error = Swap
)

func TestPublicAPISignaturesAndDBInterface(t *testing.T) {
	// Presence of the var block above is the assertion.
}
