package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/modulestate"
	"github.com/lbe/sfpg-go/internal/writebatcher"
	"github.com/lbe/sfpg-go/web"
)

// buildWriteBatcher creates a WriteBatcher configured with the shared callbacks.
func (app *App) buildWriteBatcher(maxBatchSize int, flushInterval time.Duration) (*writebatcher.WriteBatcher[BatchedWrite], error) {
	var batcherConn *dbconnpool.CpConn

	return writebatcher.New(app.ctx, writebatcher.Config[BatchedWrite]{
		MaxBatchSize:        maxBatchSize,
		MaxBatchBytes:       int64(8 * 1024 * 1024),
		FlushInterval:       flushInterval,
		ChannelSize:         4096,
		DQueDirPath:         app.dqueDirPath,
		DQueItemsPerSegment: 250,
		SizeFunc: func(bw BatchedWrite) int64 {
			return bw.Size()
		},
		BeginTx: func(ctx context.Context) (*sql.Tx, error) {
			cpc, getErr := app.dbRwPool.Get()
			if getErr != nil {
				return nil, getErr
			}
			batcherConn = cpc
			app.batcherQueries = cpc.Queries
			return cpc.Conn.BeginTx(ctx, nil)
		},
		Flush: app.flushBatchedWrites,
		OnSuccess: func(batch []BatchedWrite) {
			if batcherConn != nil {
				app.dbRwPool.Put(batcherConn)
				batcherConn = nil
			}
			app.batcherQueries = nil
			app.maybeEvictCacheEntries(batch)
			cleanupBatchedWriteResources(batch)
		},
		OnError: func(err error, batch []BatchedWrite) {
			if batcherConn != nil {
				app.dbRwPool.Put(batcherConn)
				batcherConn = nil
			}
			app.batcherQueries = nil
			var filesCount, cacheEntriesCount int
			for _, bw := range batch {
				switch {
				case bw.File != nil:
					filesCount++
				case bw.CacheEntry != nil:
					cacheEntriesCount++
				}
			}
			slog.Error("failed to flush unified batch",
				"err", err,
				"files", filesCount,
				"cache_entries", cacheEntriesCount)
			cleanupBatchedWriteResources(batch)
		},
		OnAfterCommit:       app.walCheckpointAfterCommit,
		MaintenanceInterval: 5 * time.Minute,
	})
}

// setDB initializes and configures the database using the database package.
func (app *App) setDB() {
	var err error
	app.dbPaths, app.dbRwPool, app.dbRoPool, err = database.Setup(app.ctx, app.rootDir, app.config)
	if err != nil {
		slog.Error("failed to setup database", "err", err)
		panic("main")
	}

	// Derive dque overflow directory path from the main database path
	app.dqueDirPath = filepath.Join(filepath.Dir(app.dbPaths.Main), filepath.Base(app.dbPaths.Main)+"-dque")

	// Log initial pool configuration for diagnosability
	configuredMax := 100 // default when app.config is nil
	configuredMinIdle := 10
	if app.config != nil {
		configuredMax = app.config.DBMaxPoolSize
		configuredMinIdle = app.config.DBMinIdleConnections
	}
	slog.Info("database pools initialized")
	app.logDBPoolConfiguredVsEffective("setDB", configuredMax, configuredMinIdle)

	// Initialize unified WriteBatcher for all high-volume writes.
	// Use high-throughput settings during initial preloading.
	app.writeBatcher, err = app.buildWriteBatcher(10000, 50*time.Millisecond)
	if err != nil {
		slog.Error("failed to create unified WriteBatcher", "err", err)
		panic("failed to create unified WriteBatcher")
	}
	slog.Info("unified WriteBatcher initialized",
		"max_batch_size", 10000,
		"max_batch_bytes", 8*1024*1024,
		"flush_interval_ms", 50,
		"channel_size", 4096,
		"dque_dir", app.dqueDirPath,
		"dque_enabled", app.dqueDirPath != "")

	// Initialize CacheStore using the RW pool
	app.cacheStore = cachelite.NewSQLiteCacheStore(app.dbRwPool)

	// Initialize atomic cache size counter
	if size, err := app.cacheStore.SizeBytes(app.ctx); err == nil {
		app.cacheSizeBytes.Store(size)
		slog.Debug("Initialized cache size counter", "bytes", size)
	} else {
		slog.Warn("Failed to initialize cache size counter", "err", err)
	}

	// Initialize ConfigService after database pools are created
	app.configService = config.NewService(app.dbRwPool, app.dbRoPool)

	// Initialize AuthService
	app.authService = auth.NewService(&loginStoreAdapter{app: app})

	// Initialize ModuleStateService (discovery active/inactive for batch load guard)
	app.moduleStateService = modulestate.NewService(app.dbRwPool)

	// Keep rebuild logic
	if app.authHandlers != nil {
		app.ensureSessionAndRestart()
		if err := app.buildHandlers(web.FS); err != nil {
			slog.Error("failed to rebuild handlers after setDB", "err", err)
			panic(fmt.Sprintf("rebuild handlers after setDB: %v", err))
		}
	}
}

// walCheckpointAfterCommit is called by writebatcher after each successful commit
// and by the maintenance timer (every 5 minutes).
// It checks WAL file size and checkpoints if > 2GB or if 5 minutes have elapsed.
// It also runs PRAGMA optimize every 1 hour.
// This runs in the writebatcher's worker goroutine, ensuring no active transactions.
func (app *App) walCheckpointAfterCommit(ctx context.Context, lastWalCheckpointTime time.Time, lastOptimizeTime time.Time, totalCommitted int64) {
	const walSizeThreshold = 256 * 1024 * 1024 // 256MB

	// Check WAL file size
	walPath := app.dbPaths.Main + "-wal"
	info, err := os.Stat(walPath)
	if err != nil {
		// If file doesn't exist or can't be accessed, skip size check
		if !os.IsNotExist(err) {
			slog.Warn("failed to stat WAL file", "path", walPath, "err", err)
		}
	} else if info.Size() > walSizeThreshold {
		slog.Info("WAL file exceeds threshold, forcing checkpoint",
			"path", walPath,
			"size_bytes", info.Size(),
			"size_mb", float64(info.Size())/1024/1024)
		if err := app.performWALCheckpoint(ctx); err != nil {
			slog.Error("WAL checkpoint failed", "err", err)
		}
		return
	}

	// Time-based WAL checkpoint: every 5 minutes since last checkpoint
	// lastWalCheckpointTime is zero when called from flush (skip time check)
	// lastWalCheckpointTime is set when called from maintenance timer (check 5 min elapsed)
	if !lastWalCheckpointTime.IsZero() && time.Since(lastWalCheckpointTime) >= 5*time.Minute {
		slog.Info("WAL checkpoint: 5 minutes elapsed since last checkpoint",
			"last_checkpoint", lastWalCheckpointTime.Format(time.RFC3339))
		if err := app.performWALCheckpoint(ctx); err != nil {
			slog.Error("WAL checkpoint failed", "err", err)
		}
	}

	// PRAGMA optimize: every 1 hour since last optimize
	// lastOptimizeTime is zero when called from flush (skip check)
	// lastOptimizeTime is set when called from maintenance timer (check 1 hour elapsed)
	if !lastOptimizeTime.IsZero() && time.Since(lastOptimizeTime) >= 1*time.Hour {
		slog.Info("PRAGMA optimize: 1 hour elapsed since last optimize",
			"last_optimize", lastOptimizeTime.Format(time.RFC3339))
		cpc, err := app.dbRwPool.Get()
		if err != nil {
			slog.Error("failed to get connection for PRAGMA optimize", "err", err)
			return
		}
		cpc.PragmaOptimize(ctx)
		app.dbRwPool.Put(cpc)
	}
}

// performWALCheckpoint executes a WAL checkpoint using TRUNCATE mode.
// TRUNCATE actually frees the WAL space (vs PASSIVE or RESTART).
// Must be called with no active transactions.
func (app *App) performWALCheckpoint(ctx context.Context) error {
	cpc, err := app.dbRwPool.Get()
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer app.dbRwPool.Put(cpc)

	// TRUNCATE actually frees the WAL space
	result, err := cpc.Conn.QueryContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	if err != nil {
		return fmt.Errorf("wal checkpoint failed: %w", err)
	}
	defer result.Close()

	// Parse result: wal_frames|wal_frames_checkpointed|wal_frames_in_log
	if result.Next() {
		var walFrames, checkpointed, inLog int
		if err := result.Scan(&walFrames, &checkpointed, &inLog); err != nil {
			slog.Warn("failed to parse wal_checkpoint result", "err", err)
		} else {
			slog.Debug("WAL checkpoint completed",
				"wal_frames", walFrames,
				"checkpointed", checkpointed,
				"in_log", inLog)
		}
	}

	return nil
}

// reconfigurePoolsFromConfig recreates database pools with settings from the
// given config and reinitializes dependent services. Returns an error if the
// pool recreation itself fails (non-nil errors from dependent reinit are logged).
func (app *App) reconfigurePoolsFromConfig() error {
	app.configMu.RLock()
	if app.config == nil {
		app.configMu.RUnlock()
		return nil // Nothing to reconfigure
	}

	// Log old pool configuration for diagnostics
	oldMaxConns := app.dbRwPool.Config.MaxConnections
	oldMinIdle := app.dbRwPool.Config.MinIdleConnections
	newMaxConns := app.config.DBMaxPoolSize
	newMinIdle := app.config.DBMinIdleConnections

	app.configMu.RUnlock()

	// If settings didn't change, no reconfiguration needed
	if oldMaxConns == int64(newMaxConns) && oldMinIdle == int64(newMinIdle) {
		return nil
	}

	// Log the reconfiguration
	slog.Info("reconfiguring database pools from loaded config",
		"old_max_connections", oldMaxConns,
		"new_max_connections", newMaxConns,
		"old_min_idle_connections", oldMinIdle,
		"new_min_idle_connections", newMinIdle)

	// Recreate pools with new configuration
	newRwPool, newRoPool, err := database.RecreatePoolsWithConfig(
		app.ctx,
		app.dbPaths,
		app.config,
		app.dbRwPool,
		app.dbRoPool,
	)
	if err != nil {
		slog.Error("failed to recreate pools with new config", "err", err)
		return fmt.Errorf("reconfigure pools: %w", err)
	}

	// Update pool references
	app.dbRwPool = newRwPool
	app.dbRoPool = newRoPool

	// Reinitialize CacheStore with new pool
	app.cacheStore = cachelite.NewSQLiteCacheStore(app.dbRwPool)

	// Reinitialize ConfigService with new pool references
	app.configService = config.NewService(app.dbRwPool, app.dbRoPool)

	// Reinitialize ModuleStateService with new pool (used by walkImageDir for discovery state)
	app.moduleStateService = modulestate.NewService(app.dbRwPool)

	// Reinitialize WriteBatcher with new pool references
	var rerr error
	// Close old batcher BEFORE creating a new one to release the dque flock.
	// If the new batcher uses the same dque directory, it would fail to open
	// the dque while the old one still holds the file lock.
	if app.writeBatcher != nil {
		app.writeBatcher.Close()
	}

	app.writeBatcher, rerr = app.buildWriteBatcher(1000, 200*time.Millisecond)
	if rerr != nil {
		slog.Error("failed to recreate WriteBatcher after pool reconfiguration", "err", rerr)
	}

	// Update cache middleware with new pool references
	if app.cacheMW != nil {
		app.cacheMW.UpdatePool(app.dbRwPool)
	}

	// Reinitialize AuthService with potential new state
	app.authService = auth.NewService(&loginStoreAdapter{app: app})

	// Rebuild handlers with new pool references
	if app.authHandlers != nil {
		if err := app.buildHandlers(web.FS); err != nil {
			slog.Error("failed to rebuild handlers after pool reconfiguration", "err", err)
			return fmt.Errorf("rebuild handlers after pool reconfiguration: %w", err)
		}
	}

	// Log configured vs effective pool values for diagnosability
	slog.Info("database pools reconfigured successfully",
		"max_connections", newMaxConns,
		"min_idle_connections", newMinIdle)
	app.logDBPoolConfiguredVsEffective("reconfigurePoolsFromConfig", newMaxConns, newMinIdle)

	return nil
}
