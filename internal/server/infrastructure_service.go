package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// InfrastructureService owns database pools, HTTP cache, write batcher,
// and file-system paths. No context is stored — ctx is received as a
// parameter where needed.
type InfrastructureService struct {
	dbPaths              database.DatabasePaths
	dbRwPool             *dbconnpool.DbSQLConnPool
	dbRoPool             *dbconnpool.DbSQLConnPool
	cacheStore           cachelite.CacheStore
	cacheSizeBytes       atomic.Int64
	cacheEntryCount      atomic.Int64
	cacheMW              *cachelite.HTTPCacheMiddleware
	writeBatcher         *writebatcher.WriteBatcher[BatchedWrite]
	batcherQueries       *gallerydb.CustomQueries
	dqueDirPath          string
	discoveryDQueDirPath string
	rootDir              string
	imagesDir            string
	normalizedImagesDir  string
	OnFolderCreated      func()
	ImporterFactory      func(conn *sql.Conn, q *gallerydb.CustomQueries) files.Importer
	dbInitializer        databaseInitializer
	cacheMWForEvict      cacheMiddlewareForEvict
	cacheRotator         cacheRotator
	testSeams            InfrastructureTestSeams
	cacheBaselineRunning atomic.Int32

	startupPragmaOptimizeStarted atomic.Bool
	pragmaOptimizeListening      atomic.Bool

	lastPragmaOptimizeRun atomic.Value // time.Time
	dbOptimizeInterval    atomic.Int64 // nanoseconds; 0 means default 1h
}

// NewInfrastructureService constructs the infrastructure service with production defaults.
func NewInfrastructureService() *InfrastructureService {
	s := &InfrastructureService{
		dbInitializer: defaultDatabaseInitializer{},
		cacheRotator:  defaultCacheRotator{},
		testSeams: InfrastructureTestSeams{
			GetCacheSizeBytes:  cachelite.GetCacheSizeBytes,
			GetCacheEntryCount: cachelite.CountCacheEntries,
			EvictLRU:           cachelite.EvictLRU,
		},
	}
	s.ImporterFactory = func(conn *sql.Conn, q *gallerydb.CustomQueries) files.Importer {
		return &gallerylib.Importer{Conn: conn, Q: q, OnFolderCreated: s.OnFolderCreated}
	}
	return s
}

// =====================================================================
// Accessors (used by SubsystemManager)
// =====================================================================

// DBRwPool returns the read-write database connection pool.
func (s *InfrastructureService) DBRwPool() *dbconnpool.DbSQLConnPool { return s.dbRwPool }

// DBRoPool returns the read-only database connection pool.
func (s *InfrastructureService) DBRoPool() *dbconnpool.DbSQLConnPool { return s.dbRoPool }

// WriteBatcher returns the unified write batcher, if initialized.
func (s *InfrastructureService) WriteBatcher() *writebatcher.WriteBatcher[BatchedWrite] {
	return s.writeBatcher
}

// CacheMW returns the HTTP cache middleware, if initialized.
func (s *InfrastructureService) CacheMW() *cachelite.HTTPCacheMiddleware { return s.cacheMW }

// =====================================================================
// Lifecycle
// =====================================================================

// Shutdown closes the write batcher. The real writeBatcher.Close error branch
// is unreachable in production because WriteBatcher.Close always returns nil;
// it is exercised via testSeams.ShutdownWriteBatcher.
func (s *InfrastructureService) Shutdown() {
	if s.testSeams.ShutdownWriteBatcher != nil {
		if err := s.testSeams.ShutdownWriteBatcher(); err != nil {
			slog.Error("error closing write batcher", "err", err)
		}
		return
	}
	if s.writeBatcher == nil {
		return
	}
	if err := s.writeBatcher.Close(); err != nil {
		slog.Error("error closing write batcher", "err", err)
	}
}

// =====================================================================
// Handler queries
// =====================================================================

// GetHandlerQueries returns handler database queries for the given connection.
func (s *InfrastructureService) GetHandlerQueries(cpc *dbconnpool.CpConn) interfaces.HandlerQueries {
	if s.testSeams.HandlerQueries != nil {
		return s.testSeams.HandlerQueries
	}
	return cpc.Queries
}

// GetMetadataQueries returns metadata queries for the given connection.
func (s *InfrastructureService) GetMetadataQueries(cpc *dbconnpool.CpConn) interfaces.MetadataQueries {
	return cpc.Queries
}

// GetConfigQueries returns configuration queries for the given connection.
func (s *InfrastructureService) GetConfigQueries(cpc *dbconnpool.CpConn) config.ConfigQueries {
	return cpc.Queries
}

// =====================================================================
// ETag
// =====================================================================

// IncrementETag bumps the ETag version in config and rotates the HTTP cache.
func (s *InfrastructureService) IncrementETag(ctx context.Context, cfgService config.ConfigService) (string, error) {
	cfg, err := cfgService.Load(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}
	newETag := config.IncrementETagVersion(cfg.ETagVersion)
	cfg.ETagVersion = newETag
	if err := cfgService.Validate(cfg); err != nil {
		return "", fmt.Errorf("invalid config after ETag increment: %w", err)
	}
	if err := cfgService.Save(ctx, cfg); err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}
	if err := s.cacheRotator.RotateCacheTable(ctx, s.dbRwPool); err != nil {
		slog.Warn("failed to rotate HTTP cache after ETag increment", "err", err)
	} else {
		s.cacheSizeBytes.Store(0)
		s.cacheEntryCount.Store(0)
	}
	return newETag, nil
}

// =====================================================================
// Diagnostics
// =====================================================================

func (s *InfrastructureService) setDBOptimizeInterval(d time.Duration) {
	s.dbOptimizeInterval.Store(int64(d))
}

func (s *InfrastructureService) logDBPoolConfiguredVsEffective(source string, configuredMax, configuredMinIdle int, configuredInterval time.Duration) {
	if s.dbRwPool == nil || s.dbRoPool == nil {
		return
	}
	rwEffectiveMax := s.dbRwPool.Config.MaxConnections
	rwEffectiveMinIdle := s.dbRwPool.Config.MinIdleConnections
	rwEffectiveInterval := s.dbRwPool.Config.MonitorInterval
	roEffectiveMax := s.dbRoPool.Config.MaxConnections
	roEffectiveMinIdle := s.dbRoPool.Config.MinIdleConnections
	roEffectiveInterval := s.dbRoPool.Config.MonitorInterval

	slog.Info("pool config applied",
		"source", source,
		"rw_configured_max", configuredMax, "rw_effective_max", rwEffectiveMax,
		"rw_configured_min_idle", configuredMinIdle, "rw_effective_min_idle", rwEffectiveMinIdle,
		"rw_configured_monitor_interval", configuredInterval, "rw_effective_monitor_interval", rwEffectiveInterval,
		"ro_configured_max", configuredMax, "ro_effective_max", roEffectiveMax,
		"ro_configured_min_idle", configuredMinIdle, "ro_effective_min_idle", roEffectiveMinIdle,
		"ro_configured_monitor_interval", configuredInterval, "ro_effective_monitor_interval", roEffectiveInterval)
	if configuredMinIdle > configuredMax {
		slog.Warn("invalid DB pool combination",
			"configured_max", configuredMax, "configured_min_idle", configuredMinIdle)
	}
}
