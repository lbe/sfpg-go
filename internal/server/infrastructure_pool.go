package server

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"

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

// seedTestDatabase is set from helpers_test.go init() to copy test database files into
// the test rootDir before Setup. Nil in production builds.
var seedTestDatabase func(rootDir string) error

// =====================================================================
// DB pool lifecycle
// =====================================================================

// SetupDB creates database pools and cache store. Write batcher startup and cache
// size calibration are deferred until after config load and pool resize.
func (s *InfrastructureService) SetupDB(ctx context.Context, cfg *config.Config) {
	if seedTestDatabase != nil {
		if err := seedTestDatabase(s.rootDir); err != nil {
			slog.Error("failed to seed test database", "err", err)
			panic("main")
		}
	}
	var err error
	s.dbPaths, s.dbRwPool, s.dbRoPool, err = s.dbInitializer.Setup(ctx, s.rootDir, cfg)
	if err != nil {
		slog.Error("failed to setup database", "err", err)
		panic("main")
	}

	s.discoveryDQueDirPath = filepath.Join(
		filepath.Dir(s.dbPaths.Main),
		"discovery-dque",
	)

	defaults := config.DefaultConfig()
	configuredMax := defaults.DBMaxPoolSize
	configuredMinIdle := defaults.DBMinIdleConnections
	configuredInterval := defaults.DBPoolMonitorInterval
	if cfg != nil {
		configuredMax = cfg.DBMaxPoolSize
		configuredMinIdle = cfg.DBMinIdleConnections
		configuredInterval = cfg.DBPoolMonitorInterval
	}
	slog.Info("database pools initialized")
	s.logDBPoolConfiguredVsEffective("SetupDB", configuredMax, configuredMinIdle, configuredInterval)

	s.cacheStore = cachelite.NewSQLiteCacheStore(s.dbRwPool)
}

// StartWriteBatcher creates the database writer after pools are ready.
func (s *InfrastructureService) StartWriteBatcher(ctx context.Context, deferDQueDrain bool, dqueMaxDiskBytes int64) {
	if s.writer != nil {
		return
	}
	w, err := database.NewWriter(ctx, database.WriterConfig{
		Paths:          s.dbPaths,
		RW:             s.dbRwPool,
		RO:             s.dbRoPool,
		MaxDiskBytes:   dqueMaxDiskBytes,
		DeferDQueDrain: deferDQueDrain,
		// OnFolderCreated is set after the writer starts.
		OnFolderCreated: func() {
			if s.OnFolderCreated != nil {
				s.OnFolderCreated()
			}
		},
		OnCacheStored: s.cacheStored,
		OnCacheFailed: s.cacheFlushFailed,
	})
	if err != nil {
		slog.Error("failed to create unified WriteBatcher", "err", err)
		panic("failed to create unified WriteBatcher")
	}
	s.writer = w
}

// StartDQueDrain begins draining persisted dque overflow after the server is listening.
func (s *InfrastructureService) StartDQueDrain() {
	if s.writer == nil {
		return
	}
	s.writer.StartDQueDrain()
}

// ReconfigurePools recreates database pools from loaded config. It runs
// only during startup, before StartWriteBatcher: the writer holds the pools
// it was started with, and a config change at runtime restarts the process.
func (s *InfrastructureService) ReconfigurePools(ctx context.Context, config *config.Config) error {
	if s.writer != nil {
		return errors.New("reconfigure pools: the writer has started; pools are reconfigured only before it")
	}
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

	if s.cacheMW != nil {
		s.cacheMW.UpdatePool(s.dbRwPool)
	}

	slog.Info("database pools reconfigured successfully")
	s.logDBPoolConfiguredVsEffective("ReconfigurePools", config.DBMaxPoolSize, config.DBMinIdleConnections, config.DBPoolMonitorInterval)
	return nil
}
