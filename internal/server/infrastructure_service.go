package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// InfrastructureService owns database pools, HTTP cache, write batcher,
// and file-system paths. No context is stored — ctx is received as a
// parameter where needed.
type InfrastructureService struct {
	dbPaths                database.DatabasePaths
	dbRwPool               *dbconnpool.DbSQLConnPool
	dbRoPool               *dbconnpool.DbSQLConnPool
	cacheStore             cachelite.CacheStore
	cacheSizeBytes         atomic.Int64
	cacheMW                *cachelite.HTTPCacheMiddleware
	writeBatcher           *writebatcher.WriteBatcher[BatchedWrite]
	batcherQueries         *gallerydb.CustomQueries
	dqueDirPath            string
	rootDir                string
	imagesDir              string
	normalizedImagesDir    string
	ImporterFactory        func(conn *sql.Conn, q *gallerydb.CustomQueries) files.Importer
	testHookHandlerQueries interfaces.HandlerQueries
}

func NewInfrastructureService() *InfrastructureService {
	return &InfrastructureService{
		ImporterFactory: func(conn *sql.Conn, q *gallerydb.CustomQueries) files.Importer {
			return &gallerylib.Importer{Conn: conn, Q: q}
		},
	}
}

// =====================================================================
// Accessors (used by SubsystemManager)
// =====================================================================

func (s *InfrastructureService) DBRwPool() *dbconnpool.DbSQLConnPool { return s.dbRwPool }
func (s *InfrastructureService) DBRoPool() *dbconnpool.DbSQLConnPool { return s.dbRoPool }
func (s *InfrastructureService) WriteBatcher() *writebatcher.WriteBatcher[BatchedWrite] {
	return s.writeBatcher
}
func (s *InfrastructureService) CacheMW() *cachelite.HTTPCacheMiddleware { return s.cacheMW }

// =====================================================================
// DB pool lifecycle
// =====================================================================

// SetupDB creates database pools, write batcher, cache store, and cache
// size counter. Called early in startup before config is loaded.
func (s *InfrastructureService) SetupDB(ctx context.Context, config *config.Config) {
	var err error
	s.dbPaths, s.dbRwPool, s.dbRoPool, err = database.Setup(ctx, s.rootDir, config)
	if err != nil {
		slog.Error("failed to setup database", "err", err)
		panic("main")
	}

	s.dqueDirPath = filepath.Join(
		filepath.Dir(s.dbPaths.Main),
		filepath.Base(s.dbPaths.Main)+"-dque",
	)

	configuredMax := 100
	configuredMinIdle := 10
	if config != nil {
		configuredMax = config.DBMaxPoolSize
		configuredMinIdle = config.DBMinIdleConnections
	}
	slog.Info("database pools initialized")
	s.logDBPoolConfiguredVsEffective("SetupDB", configuredMax, configuredMinIdle)

	s.writeBatcher, err = s.buildWriteBatcher(ctx, 10000, 50*time.Millisecond)
	if err != nil {
		slog.Error("failed to create unified WriteBatcher", "err", err)
		panic("failed to create unified WriteBatcher")
	}
	slog.Info("unified WriteBatcher initialized",
		"max_batch_size", 10000, "max_batch_bytes", 8*1024*1024,
		"flush_interval_ms", 50, "channel_size", 4096,
		"dque_dir", s.dqueDirPath, "dque_enabled", s.dqueDirPath != "")

	s.cacheStore = cachelite.NewSQLiteCacheStore(s.dbRwPool)
	if size, sizeErr := s.cacheStore.SizeBytes(ctx); sizeErr == nil {
		s.cacheSizeBytes.Store(size)
	} else {
		slog.Warn("Failed to initialize cache size counter", "err", sizeErr)
	}
}

// ReconfigurePools recreates database pools from loaded config.
func (s *InfrastructureService) ReconfigurePools(ctx context.Context, config *config.Config) error {
	if config == nil {
		return nil
	}
	oldMaxConns := s.dbRwPool.Config.MaxConnections
	oldMinIdle := s.dbRwPool.Config.MinIdleConnections
	newMaxConns := config.DBMaxPoolSize
	newMinIdle := config.DBMinIdleConnections

	if oldMaxConns == int64(newMaxConns) && oldMinIdle == int64(newMinIdle) {
		return nil
	}
	slog.Info("reconfiguring database pools from loaded config",
		"old_max", oldMaxConns, "new_max", newMaxConns,
		"old_min_idle", oldMinIdle, "new_min_idle", newMinIdle)

	newRw, newRo, rErr := database.RecreatePoolsWithConfig(
		ctx, s.dbPaths, config, s.dbRwPool, s.dbRoPool,
	)
	if rErr != nil {
		return rErr
	}
	s.dbRwPool = newRw
	s.dbRoPool = newRo
	s.cacheStore = cachelite.NewSQLiteCacheStore(s.dbRwPool)

	if s.writeBatcher != nil {
		s.writeBatcher.Close()
	}
	var bwErr error
	s.writeBatcher, bwErr = s.buildWriteBatcher(ctx, 1000, 200*time.Millisecond)
	if bwErr != nil {
		slog.Error("failed to recreate write batcher", "err", bwErr)
	}
	if s.cacheMW != nil {
		s.cacheMW.UpdatePool(s.dbRwPool)
	}

	slog.Info("database pools reconfigured successfully")
	s.logDBPoolConfiguredVsEffective("ReconfigurePools", newMaxConns, newMinIdle)
	return nil
}

func (s *InfrastructureService) Shutdown() {
	if s.writeBatcher != nil {
		if err := s.writeBatcher.Close(); err != nil {
			slog.Error("error closing write batcher", "err", err)
		}
	}
}

// =====================================================================
// Write batcher
// =====================================================================

func (s *InfrastructureService) buildWriteBatcher(ctx context.Context, maxBatchSize int, flushInterval time.Duration) (*writebatcher.WriteBatcher[BatchedWrite], error) {
	var cpcRw *dbconnpool.CpConn

	return writebatcher.New(ctx, writebatcher.Config[BatchedWrite]{
		MaxBatchSize:        maxBatchSize,
		MaxBatchBytes:       int64(8 * 1024 * 1024),
		FlushInterval:       flushInterval,
		ChannelSize:         4096,
		DQueDirPath:         s.dqueDirPath,
		DQueItemsPerSegment: 250,
		SizeFunc:            func(bw BatchedWrite) int64 { return bw.Size() },
		BeginTx: func(ctx context.Context) (*sql.Tx, error) {
			var getErr error
			cpcRw, getErr = s.dbRwPool.Get()
			if getErr != nil {
				return nil, getErr
			}
			s.batcherQueries = cpcRw.Queries
			return cpcRw.Conn.BeginTx(ctx, nil)
		},
		Flush: s.flushBatchedWrites,
		OnSuccess: func(batch []BatchedWrite) {
			if cpcRw != nil {
				s.dbRwPool.Put(cpcRw)
				cpcRw = nil
			}
			s.batcherQueries = nil
			var totalAdded int64
			for _, bw := range batch {
				if bw.CacheEntry != nil && bw.CacheEntry.ContentLength.Valid {
					totalAdded += bw.CacheEntry.ContentLength.Int64
				}
			}
			if totalAdded > 0 {
				s.cacheSizeBytes.Add(totalAdded)
			}
			s.maybeEvictCacheEntries(batch)
			cleanupBatchedWriteResources(batch)
		},
		OnError: func(err error, batch []BatchedWrite) {
			if cpcRw != nil {
				s.dbRwPool.Put(cpcRw)
				cpcRw = nil
			}
			s.batcherQueries = nil
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
				"err", err, "files", filesCount, "cache_entries", cacheEntriesCount)
			cleanupBatchedWriteResources(batch)
			if cacheEntriesCount > 0 {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if size, sizeErr := cachelite.GetCacheSizeBytes(ctx, s.dbRwPool); sizeErr == nil {
					s.cacheSizeBytes.Store(size)
				}
			}
		},
		OnAfterCommit:       s.walCheckpointAfterCommit,
		MaintenanceInterval: 5 * time.Minute,
	})
}

// =====================================================================
// Batched write flush
// =====================================================================

func (s *InfrastructureService) flushBatchedWrites(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
	fileWrites := make([]*files.File, 0, len(batch))
	galleryCache := make([]*cachelite.HTTPCacheEntry, 0, len(batch))
	otherCache := make([]*cachelite.HTTPCacheEntry, 0, len(batch))

	for _, bw := range batch {
		switch {
		case bw.File != nil:
			fileWrites = append(fileWrites, bw.File)
		case bw.CacheEntry != nil:
			if strings.HasPrefix(bw.CacheEntry.Path, "/gallery/") {
				galleryCache = append(galleryCache, bw.CacheEntry)
			} else {
				otherCache = append(otherCache, bw.CacheEntry)
			}
		}
	}

	qtx := s.batcherQueries.WithTx(tx)
	imp := &gallerylib.Importer{Q: qtx}
	for _, f := range fileWrites {
		if err := files.WriteFileInTx(ctx, imp, f); err != nil {
			return fmt.Errorf("write file %s: %w", f.Path, err)
		}
	}
	for _, entry := range galleryCache {
		if err := cachelite.StoreCacheEntryInTx(ctx, tx, entry); err != nil {
			return fmt.Errorf("store gallery cache %s: %w", entry.Path, err)
		}
	}
	for _, entry := range otherCache {
		if err := cachelite.StoreCacheEntryInTx(ctx, tx, entry); err != nil {
			return fmt.Errorf("store cache %s: %w", entry.Path, err)
		}
	}
	if len(fileWrites) > 0 {
		maxDepth := 0
		uniqueDirs := make(map[string]struct{}, len(fileWrites))
		for _, f := range fileWrites {
			dir := filepath.Dir(f.Path)
			uniqueDirs[dir] = struct{}{}
			if dir == "." {
				continue
			}
			depth := len(strings.FieldsFunc(dir, func(c rune) bool { return c == '/' }))
			if depth > maxDepth {
				maxDepth = depth
			}
		}
		slog.Debug("batched flush: file batch path stats",
			"files", len(fileWrites), "gallery_cache", len(galleryCache),
			"other_cache", len(otherCache), "max_depth", maxDepth,
			"unique_dirs", len(uniqueDirs))
	}
	return nil
}

func (s *InfrastructureService) maybeEvictCacheEntries(batch []BatchedWrite) {
	hasCacheEntries := false
	for _, bw := range batch {
		if bw.CacheEntry != nil {
			hasCacheEntries = true
			break
		}
	}
	if !hasCacheEntries || s.cacheMW == nil {
		return
	}

	cfg := s.cacheMW.Config()
	if cfg.MaxTotalSize <= 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	currentSize, err := cachelite.GetCacheSizeBytes(ctx, s.dbRwPool)
	if err != nil {
		slog.Warn("failed to get cache size for eviction check", "err", err)
		return
	}
	if currentSize > cfg.MaxTotalSize {
		targetFree := currentSize - cfg.MaxTotalSize + cfg.MaxTotalSize/10
		freed, evErr := cachelite.EvictLRU(ctx, s.dbRwPool, targetFree)
		if evErr != nil {
			slog.Warn("cache eviction failed", "err", evErr, "target", targetFree, "freed", freed)
		}
		if freed > 0 {
			s.cacheSizeBytes.Add(-freed)
		}
	}
}

// =====================================================================
// WAL checkpoint
// =====================================================================

func (s *InfrastructureService) walCheckpointAfterCommit(ctx context.Context, lastWalCheckpointTime, lastOptimizeTime time.Time, totalCommitted int64) {
	const walSizeThreshold = 256 * 1024 * 1024
	walPath := s.dbPaths.Main + "-wal"
	info, err := os.Stat(walPath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("failed to stat WAL file", "path", walPath, "err", err)
		}
	} else if info.Size() > walSizeThreshold {
		slog.Info("WAL file exceeds threshold, forcing checkpoint")
		s.performWALCheckpoint(ctx)
		return
	}
	if !lastWalCheckpointTime.IsZero() && time.Since(lastWalCheckpointTime) >= 5*time.Minute {
		slog.Info("WAL checkpoint: 5 minutes elapsed")
		s.performWALCheckpoint(ctx)
	}
	if !lastOptimizeTime.IsZero() && time.Since(lastOptimizeTime) >= 1*time.Hour {
		slog.Info("PRAGMA optimize: 1 hour elapsed")
		cpcRw, poolErr := s.dbRwPool.Get()
		if poolErr != nil {
			slog.Warn("failed to get connection for PRAGMA optimize", "err", poolErr)
			return
		}
		cpcRw.PragmaOptimize(ctx)
		s.dbRwPool.Put(cpcRw)
	}
}

func (s *InfrastructureService) performWALCheckpoint(ctx context.Context) {
	cpcRw, err := s.dbRwPool.Get()
	if err != nil {
		slog.Error("failed to get connection for WAL checkpoint", "err", err)
		return
	}
	defer s.dbRwPool.Put(cpcRw)
	result, qErr := cpcRw.Conn.QueryContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
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

// =====================================================================
// HTTP cache
// =====================================================================

func (s *InfrastructureService) InitializeHTTPCache(config *config.Config) {
	if config == nil || !config.EnableHTTPCache {
		return
	}

	cfg := cachelite.CacheConfig{
		Enabled:      true,
		MaxEntrySize: config.CacheMaxEntrySize,
		MaxTotalSize: config.CacheMaxSize,
		DefaultTTL:   config.CacheMaxTime,
		CacheableRoutes: []string{
			"/gallery/", "/lightbox/", "/info/folder/", "/info/image/",
		},
		OnGalleryCacheHit: func(ctx context.Context, folderID int64, sessionID string, acceptEncoding string) {
			// preloadManager callback set later via SetCacheOnGalleryHit
		},
		SessionCookieName:     session.SessionName,
		SkipPreloadWhenHeader: cachepreload.InternalPreloadHeader,
		SkipPreloadWhenValue:  "true",
	}
	var submitFunc func(*cachelite.HTTPCacheEntry)
	if s.writeBatcher != nil {
		submitFunc = s.submitCacheWrite
	}
	s.cacheMW = cachelite.NewHTTPCacheMiddleware(s.dbRoPool, cfg, &s.cacheSizeBytes, submitFunc)
}

// SetCacheOnGalleryHit replaces the OnGalleryCacheHit callback (wired by
// SubsystemManager after preloadManager is created).
func (s *InfrastructureService) SetCacheOnGalleryHit(fn func(ctx context.Context, folderID int64, sessionID, acceptEncoding string)) {
	if s.cacheMW != nil {
		s.cacheMW.SetOnGalleryCacheHit(fn)
	}
}

func (s *InfrastructureService) InvalidateHTTPCache() {
	if s.dbRwPool == nil {
		return
	}
	if err := cachelite.RotateCacheTable(context.Background(), s.dbRwPool); err != nil {
		slog.Error("failed to invalidate HTTP cache", "err", err)
		return
	}
	s.cacheSizeBytes.Store(0)
}

func (s *InfrastructureService) submitCacheWrite(entry *cachelite.HTTPCacheEntry) {
	if s.writeBatcher == nil {
		slog.Warn("unified batcher not available, dropping cache write", "path", entry.Path)
		cachelite.PutHTTPCacheEntry(entry)
		return
	}
	adapter := &batcherAdapter{wb: s.writeBatcher}
	if err := adapter.SubmitCache(entry); err != nil {
		slog.Debug("failed to submit cache write", "path", entry.Path, "err", err)
		cachelite.PutHTTPCacheEntry(entry)
	}
}

// =====================================================================
// Handler queries
// =====================================================================

func (s *InfrastructureService) GetHandlerQueries(cpc *dbconnpool.CpConn) interfaces.HandlerQueries {
	if s.testHookHandlerQueries != nil {
		return s.testHookHandlerQueries
	}
	return cpc.Queries
}

func (s *InfrastructureService) GetMetadataQueries(cpc *dbconnpool.CpConn) interfaces.MetadataQueries {
	return cpc.Queries
}

// =====================================================================
// ETag
// =====================================================================

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
	if err := cachelite.RotateCacheTable(ctx, s.dbRwPool); err != nil {
		slog.Warn("failed to rotate HTTP cache after ETag increment", "err", err)
	} else {
		s.cacheSizeBytes.Store(0)
	}
	return newETag, nil
}

// =====================================================================
// Diagnostics
// =====================================================================

func (s *InfrastructureService) logDBPoolConfiguredVsEffective(source string, configuredMax, configuredMinIdle int) {
	if s.dbRwPool == nil || s.dbRoPool == nil {
		return
	}
	rwEffectiveMax := s.dbRwPool.Config.MaxConnections
	rwEffectiveMinIdle := s.dbRwPool.Config.MinIdleConnections
	roEffectiveMax := s.dbRoPool.Config.MaxConnections
	roEffectiveMinIdle := s.dbRoPool.Config.MinIdleConnections

	slog.Info("pool config applied",
		"source", source,
		"rw_configured_max", configuredMax, "rw_effective_max", rwEffectiveMax,
		"rw_configured_min_idle", configuredMinIdle, "rw_effective_min_idle", rwEffectiveMinIdle,
		"ro_configured_max", configuredMax, "ro_effective_max", roEffectiveMax,
		"ro_configured_min_idle", configuredMinIdle, "ro_effective_min_idle", roEffectiveMinIdle)
	if configuredMinIdle > configuredMax {
		slog.Warn("invalid DB pool combination",
			"configured_max", configuredMax, "configured_min_idle", configuredMinIdle)
	}
}
