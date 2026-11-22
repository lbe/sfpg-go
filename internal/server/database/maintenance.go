package database

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

const (
	// walCheckpointBytes is the WAL size that forces a checkpoint.
	walCheckpointBytes = 256 << 20
	// walCheckpointAge is how long the WAL may go without a checkpoint.
	walCheckpointAge = 5 * time.Minute
	// defaultOptimizeInterval is how often PRAGMA optimize runs by default.
	defaultOptimizeInterval = time.Hour
)

// maintain is the batcher's OnAfterCommit. It runs after each commit
// (postFlush) and every maintenanceInterval, with no transaction open:
// a WAL checkpoint when one is due, then PRAGMA optimize when one is due.
func (w *Writer) maintain(ctx context.Context, lastCheckpoint, _ time.Time, _ int64, postFlush bool) {
	pool := w.rw
	w.checkpointWAL(ctx, pool, lastCheckpoint, postFlush)
	w.optimize(ctx, pool)
}

// checkpointWAL runs a TRUNCATE checkpoint when the WAL is over
// walCheckpointBytes or none has run for walCheckpointAge. It skips while the
// rebuild's read cursor is open, which would make the checkpoint wait, and
// after a flush that wrote nothing, so an empty flush does not truncate a
// large WAL left from before.
func (w *Writer) checkpointWAL(ctx context.Context, pool *dbconnpool.DbSQLConnPool, lastCheckpoint time.Time, postFlush bool) {
	if w.index.scanHeld.Load() || (postFlush && !w.wroteDML.Load()) {
		return
	}
	info, err := os.Stat(w.walPath)
	switch {
	case err != nil:
		if !os.IsNotExist(err) {
			slog.Warn("failed to stat WAL file", "path", w.walPath, "err", err)
		}
	case info.Size() > walCheckpointBytes:
		slog.Info("WAL file exceeds threshold, forcing checkpoint")
		checkpoint(ctx, pool)
		return
	}
	if !lastCheckpoint.IsZero() && time.Since(lastCheckpoint) >= walCheckpointAge {
		slog.Info("WAL checkpoint: 5 minutes elapsed")
		checkpoint(ctx, pool)
	}
}

func checkpoint(ctx context.Context, pool *dbconnpool.DbSQLConnPool) {
	cpc, err := pool.Get()
	if err != nil {
		slog.Error("failed to get connection for WAL checkpoint", "err", err)
		return
	}
	defer pool.Put(cpc)
	var busy, walFrames, checkpointed int
	if err := cpc.Conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &walFrames, &checkpointed); err != nil {
		slog.Error("WAL checkpoint failed", "err", err)
		return
	}
	slog.Debug("WAL checkpoint completed", "busy", busy, "wal_frames", walFrames, "checkpointed", checkpointed)
}

// optimize runs PRAGMA optimize when the optimize interval has passed since
// the last run.
func (w *Writer) optimize(ctx context.Context, pool *dbconnpool.DbSQLConnPool) {
	interval := time.Duration(w.optimizeInterval.Load())
	if interval <= 0 {
		interval = defaultOptimizeInterval
	}
	if time.Since(w.lastOptimize) < interval {
		return
	}
	slog.Info("PRAGMA optimize: interval elapsed", "interval", interval)
	cpc, err := pool.Get()
	if err != nil {
		slog.Warn("failed to get connection for PRAGMA optimize", "err", err)
		return
	}
	defer pool.Put(cpc)
	if dbconnpool.RunPragmaOptimize(ctx, cpc.Conn, dbconnpool.PragmaOptimizeDefault) == nil {
		w.lastOptimize = time.Now()
	}
}
