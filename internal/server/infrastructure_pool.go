package server

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
)

// databaseInitializer abstracts database.Setup and database.RecreatePoolsWithConfig.
type databaseInitializer interface {
	Setup(ctx context.Context, rootDir string, cfg *config.Config) (database.DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error)
	RecreatePoolsWithConfig(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error)
}

// defaultDatabaseInitializer is the production implementation of databaseInitializer.
type defaultDatabaseInitializer struct{}

func (defaultDatabaseInitializer) Setup(ctx context.Context, rootDir string, cfg *config.Config) (database.DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	return database.Setup(ctx, rootDir, cfg)
}

func (defaultDatabaseInitializer) RecreatePoolsWithConfig(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	return database.RecreatePoolsWithConfig(ctx, dbPaths, cfg, oldRw, oldRo)
}

// dbPoolForCheckpoint is the subset of *dbconnpool.DbSQLConnPool used by WAL checkpoint logic.
type dbPoolForCheckpoint interface {
	Get() (*dbconnpool.CpConn, error)
	Put(*dbconnpool.CpConn)
}

// =====================================================================
// DB pool lifecycle
// =====================================================================

// SetupDB creates database pools and cache store. Write batcher startup and cache
// size calibration are deferred until after config load and pool resize.
func (s *InfrastructureService) SetupDB(ctx context.Context, cfg *config.Config) {
	var err error
	s.dbPaths, s.dbRwPool, s.dbRoPool, err = s.dbInitializer.Setup(ctx, s.rootDir, cfg)
	if err != nil {
		slog.Error("failed to setup database", "err", err)
		panic("main")
	}

	s.dqueDirPath = filepath.Join(
		filepath.Dir(s.dbPaths.Main),
		filepath.Base(s.dbPaths.Main)+"-dque",
	)
	s.discoveryDQueDirPath = filepath.Join(
		filepath.Dir(s.dbPaths.Main),
		"discovery-dque",
	)

	configuredMax := 100
	configuredMinIdle := 10
	configuredInterval := config.DefaultConfig().DBPoolMonitorInterval
	if cfg != nil {
		configuredMax = cfg.DBMaxPoolSize
		configuredMinIdle = cfg.DBMinIdleConnections
		configuredInterval = cfg.DBPoolMonitorInterval
	}
	slog.Info("database pools initialized")
	s.logDBPoolConfiguredVsEffective("SetupDB", configuredMax, configuredMinIdle, configuredInterval)

	s.cacheStore = cachelite.NewSQLiteCacheStore(s.dbRwPool)
}

// StartWriteBatcher creates the unified write batcher after pools are ready.
func (s *InfrastructureService) StartWriteBatcher(ctx context.Context, deferDQueDrain bool, dqueMaxDiskBytes int64) {
	if s.writeBatcher != nil {
		return
	}
	var err error
	if s.testSeams.BuildWriteBatcher != nil {
		s.writeBatcher, err = s.testSeams.BuildWriteBatcher(ctx, 10000, 50*time.Millisecond)
	} else {
		s.writeBatcher, err = s.buildWriteBatcher(ctx, 10000, 50*time.Millisecond, deferDQueDrain, dqueMaxDiskBytes)
	}
	if err != nil {
		slog.Error("failed to create unified WriteBatcher", "err", err)
		panic("failed to create unified WriteBatcher")
	}
	slog.Info("unified WriteBatcher initialized",
		"max_batch_size", 10000, "max_batch_bytes", 8*1024*1024,
		"flush_interval_ms", 50, "channel_size", 4096,
		"dque_dir", s.dqueDirPath, "dque_enabled", s.dqueDirPath != "",
		"dque_drain_deferred", deferDQueDrain)

	// Initialize the periodic optimize clock so the first run waits a full interval.
	if _, ok := s.lastPragmaOptimizeRun.Load().(time.Time); !ok {
		s.lastPragmaOptimizeRun.Store(time.Now())
	}
}

// StartDQueDrain begins draining persisted dque overflow after the server is listening.
func (s *InfrastructureService) StartDQueDrain() {
	if s.writeBatcher == nil {
		return
	}
	s.writeBatcher.StartDQueDrain()
}

// ReconfigurePools recreates database pools from loaded config.
func (s *InfrastructureService) ReconfigurePools(ctx context.Context, config *config.Config) error {
	if config == nil {
		return nil
	}
	oldMaxConns := s.dbRwPool.Config.MaxConnections
	oldMinIdle := s.dbRwPool.Config.MinIdleConnections
	oldMonitorInterval := s.dbRwPool.Config.MonitorInterval
	newMaxConns, newMinIdle, newMonitorInterval := database.EffectivePoolLimits(config)

	if oldMaxConns == newMaxConns && oldMinIdle == newMinIdle && oldMonitorInterval == newMonitorInterval {
		return nil
	}
	slog.Info("reconfiguring database pools from loaded config",
		"old_max", oldMaxConns, "new_max", newMaxConns,
		"old_min_idle", oldMinIdle, "new_min_idle", newMinIdle)

	var newRw, newRo *dbconnpool.DbSQLConnPool
	var rErr error
	if s.testSeams.RecreatePoolsWithConfig != nil {
		newRw, newRo, rErr = s.testSeams.RecreatePoolsWithConfig(ctx, s.dbPaths, config, s.dbRwPool, s.dbRoPool)
	} else {
		newRw, newRo, rErr = s.dbInitializer.RecreatePoolsWithConfig(ctx, s.dbPaths, config, s.dbRwPool, s.dbRoPool)
	}
	if rErr != nil {
		return rErr
	}
	s.dbRwPool = newRw
	s.dbRoPool = newRo
	s.cacheStore = cachelite.NewSQLiteCacheStore(s.dbRwPool)

	if s.writeBatcher != nil {
		s.writeBatcher.Close()
		var bwErr error
		if s.testSeams.BuildWriteBatcher != nil {
			s.writeBatcher, bwErr = s.testSeams.BuildWriteBatcher(ctx, 1000, 200*time.Millisecond)
		} else {
			s.writeBatcher, bwErr = s.buildWriteBatcher(ctx, 1000, 200*time.Millisecond, false, config.DQueMaxDiskBytes)
		}
		if bwErr != nil {
			slog.Error("failed to recreate write batcher", "err", bwErr)
		}
	}
	if s.cacheMW != nil {
		s.cacheMW.UpdatePool(s.dbRwPool)
	}

	slog.Info("database pools reconfigured successfully")
	s.logDBPoolConfiguredVsEffective("ReconfigurePools", config.DBMaxPoolSize, config.DBMinIdleConnections, config.DBPoolMonitorInterval)
	return nil
}

// =====================================================================
// WAL checkpoint + periodic PRAGMA optimize
// =====================================================================

// walCheckpointAfterCommit checks WAL file size and time-based checkpoint.
// PRAGMA optimize has been moved to maybeRunPeriodicOptimize.
func (s *InfrastructureService) walCheckpointAfterCommit(ctx context.Context, lastWalCheckpointTime, lastOptimizeTime time.Time, totalCommitted int64, postFlush bool) {
	// G4: the RO rebuild scan cursor pins the WAL write lock; a TRUNCATE checkpoint
	// while it is open busy-waits up to busy_timeout and caps flush throughput.
	// Skip only while the cursor is held, not for the whole rebuild-active window.
	if s.folderIndexRebuildScanHeld.Load() {
		return
	}
	// D4: post-flush with no DML must not TRUNCATE a leftover multi-GB WAL.
	if postFlush && !s.lastFlushWroteDML.Load() {
		return
	}
	const walSizeThreshold = 256 * 1024 * 1024
	walPath := s.dbPaths.Main + "-wal"
	info, err := os.Stat(walPath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("failed to stat WAL file", "path", walPath, "err", err)
		}
	} else if info.Size() > walSizeThreshold {
		slog.Info("WAL file exceeds threshold, forcing checkpoint")
		if s.testSeams.PerformWALCheckpoint != nil {
			s.testSeams.PerformWALCheckpoint(ctx)
		} else {
			s.performWALCheckpoint(ctx, s.dbRwPool)
		}
		return
	}
	if !lastWalCheckpointTime.IsZero() && time.Since(lastWalCheckpointTime) >= 5*time.Minute {
		slog.Info("WAL checkpoint: 5 minutes elapsed")
		if s.testSeams.PerformWALCheckpoint != nil {
			s.testSeams.PerformWALCheckpoint(ctx)
		} else {
			s.performWALCheckpoint(ctx, s.dbRwPool)
		}
	}
}

// maybeRunPeriodicOptimize runs PRAGMA optimize if the interval has elapsed.
// The clock is initialized in StartWriteBatcher so the first run waits a full interval.
func (s *InfrastructureService) maybeRunPeriodicOptimize(ctx context.Context) {
	interval := time.Duration(s.dbOptimizeInterval.Load())
	if interval <= 0 {
		interval = time.Hour
	}
	lastRun, ok := s.lastPragmaOptimizeRun.Load().(time.Time)
	if !ok || lastRun.IsZero() {
		// Clock not started yet — skip.
		return
	}
	if time.Since(lastRun) < interval {
		return
	}
	slog.Info("PRAGMA optimize: interval elapsed", "interval", interval)
	var optimized bool
	switch {
	case s.testSeams.PragmaOptimize != nil:
		s.testSeams.PragmaOptimize(ctx, s.dbRwPool)
		optimized = true
	case s.dbRwPool == nil:
		slog.Warn("failed to get connection for PRAGMA optimize", "err", errors.New("database pool is nil"))
	default:
		optimized = s.pragmaOptimize(ctx, s.dbRwPool)
	}
	if optimized {
		s.lastPragmaOptimizeRun.Store(time.Now())
	}
}

// postCommitMaintenance is called by the write batcher after each successful commit.
// It runs WAL checkpoint logic and periodic PRAGMA optimize.
func (s *InfrastructureService) postCommitMaintenance(ctx context.Context, lastWalCheckpointTime, lastOptimizeTime time.Time, totalCommitted int64, postFlush bool) {
	s.walCheckpointAfterCommit(ctx, lastWalCheckpointTime, lastOptimizeTime, totalCommitted, postFlush)
	s.maybeRunPeriodicOptimize(ctx)
}

func (s *InfrastructureService) pragmaOptimize(ctx context.Context, pool dbPoolForCheckpoint) bool {
	cpcRw, poolErr := pool.Get()
	if poolErr != nil {
		slog.Warn("failed to get connection for PRAGMA optimize", "err", poolErr)
		return false
	}
	defer pool.Put(cpcRw)
	return dbconnpool.RunPragmaOptimize(ctx, cpcRw.Conn, dbconnpool.PragmaOptimizeDefault) == nil
}

func (s *InfrastructureService) performWALCheckpoint(ctx context.Context, pool dbPoolForCheckpoint) {
	cpcRw, err := pool.Get()
	if err != nil {
		slog.Error("failed to get connection for WAL checkpoint", "err", err)
		return
	}
	defer pool.Put(cpcRw)
	var result *sql.Rows
	var qErr error
	if s.testSeams.WALCheckpointQuery != nil {
		result, qErr = s.testSeams.WALCheckpointQuery(ctx, cpcRw.Conn)
	} else {
		result, qErr = cpcRw.Conn.QueryContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	}
	if qErr != nil {
		slog.Error("WAL checkpoint failed", "err", qErr)
		return
	}
	defer result.Close()
	if result.Next() {
		var walFrames, checkpointed, inLog int
		if scanErr := result.Scan(&walFrames, &checkpointed, &inLog); scanErr != nil {
			slog.Warn("failed to parse wal_checkpoint result", "err", scanErr)
		} else {
			slog.Debug("WAL checkpoint completed",
				"wal_frames", walFrames, "checkpointed", checkpointed, "in_log", inLog)
		}
	}
}
