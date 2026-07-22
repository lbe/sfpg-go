package dbconnpool

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

const (
	// PragmaOptimizeFreshConnection runs PRAGMA optimize=0x10002 once per
	// process on a fresh pooled connection: ANALYZE tables that may benefit
	// (0x00002) and consider all tables, not only recently used (0x10000).
	// Deferred until after listen and quiet so pool init stays fast.
	PragmaOptimizeFreshConnection = 0x10002

	// PragmaOptimizeDefault runs PRAGMA optimize with no special flags,
	// letting SQLite decide which optimizations to apply based on its
	// internal statistics.
	PragmaOptimizeDefault = 0
)

// RunPragmaOptimize executes PRAGMA optimize on the given connection with
// the specified mask. When mask is 0, the simple form "PRAGMA optimize" is
// used. Otherwise the mask is formatted as a hex value.
func RunPragmaOptimize(ctx context.Context, conn *sql.Conn, mask int) error {
	var query string
	if mask == 0 {
		query = `PRAGMA optimize`
	} else {
		query = fmt.Sprintf(`PRAGMA optimize=%#x`, mask)
	}
	start := time.Now()
	_, err := conn.ExecContext(ctx, query)
	if err != nil {
		slog.Warn("PRAGMA optimize failed",
			"mask", mask,
			"duration", time.Since(start),
			"err", err,
		)
		return err
	}
	slog.Info("PRAGMA optimize succeeded",
		"mask", mask,
		"duration", time.Since(start),
	)
	return nil
}
