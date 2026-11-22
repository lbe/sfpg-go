package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/discovery"
	"github.com/lbe/sfpg-go/internal/memlimit"
	"github.com/lbe/sfpg-go/internal/metadata"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/cachebatch"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/server/modulestate"
	"github.com/lbe/sfpg-go/internal/thumbnail"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

// preloadManager is the subset of cachepreload.PreloadManager used by SubsystemManager.
type preloadManager interface {
	Configure(cfg cachepreload.PreloadConfig)
	IsEnabled() bool
	SetEnabled(enabled bool)
	Shutdown()
	GetScheduler() *scheduler.Scheduler
	ScheduleFolderPreload(ctx context.Context, folderID int64, sessionID string)
	GetMetrics() cachepreload.PreloadMetricsSnapshot
}

// SubsystemManager owns background processing subsystems.
type SubsystemManager struct {
	pool               *workerpool.Pool
	discovery          *discovery.Service
	fileProcessor      files.FileProcessor
	stats              *discovery.Stats
	scheduler          *scheduler.Scheduler
	preloadManager     preloadManager
	batchLoadManager   *cachebatch.Manager
	moduleStateService *modulestate.Service

	infra *InfrastructureService
}

// NewSubsystemManager constructs a subsystem manager bound to infrastructure services.
func NewSubsystemManager(infra *InfrastructureService) *SubsystemManager {
	return &SubsystemManager{
		infra:     infra,
		scheduler: scheduler.NewScheduler(0),
	}
}

// ─── Lifecycle ──────────────────────────────────────────────────────

// Start creates all subsystems from config. Call after config is loaded
// and imagesDir is set but before handler building.
func (m *SubsystemManager) Start(
	ctx context.Context,
	cfg *config.Config,
	minPoolWorkers, maxPoolWorkers int,
	imagesDir, normalizedImagesDir string,
	removeImagesDirPrefixFn func(string, string) (string, error),
	getRouter func() http.Handler,
	getHandlerQueries func(*dbconnpool.CpConn) interfaces.HandlerQueries,
	getETagVersion func() string,
	onRunning func(delta int),
	onFileInserted func(size int64),
) {
	// Worker pool size: App.Run parameters are the base defaults; config
	// overrides only when explicitly positive (> 0). Min idle 0 means no idle
	// workers; max 0 sizes from CPU cores. The memory limit may then cut the
	// workers to 1.
	maxWorkers := maxPoolWorkers
	minIdle := minPoolWorkers
	if cfg != nil {
		if cfg.WorkerPoolMax > 0 {
			maxWorkers = cfg.WorkerPoolMax
		}
		if cfg.WorkerPoolMinIdle > 0 {
			minIdle = cfg.WorkerPoolMinIdle
		}
	}
	if maxWorkers == 0 {
		maxWorkers = workerpool.DefaultMaxWorkers()
	}
	limit := memlimit.Apply(memlimit.Need{
		Workers:         maxWorkers,
		ExifToolServers: metadata.PoolSize(),
		RetainedBytes:   thumbnail.RetainedBytes,
	})
	maxWorkers = limit.Workers
	minIdle = min(minIdle, maxWorkers)

	// Module state service
	m.moduleStateService = modulestate.NewService(m.infra.DBRwPool())

	// File processor (tests may inject a fake via m.fileProcessor)
	if m.fileProcessor == nil {
		m.fileProcessor = files.NewFileProcessor(imagesDir)
	}

	// Cache preload manager (tests may inject a fake via m.preloadManager)
	if m.preloadManager == nil {
		enablePreload := true
		if cfg != nil {
			enablePreload = cfg.EnableCachePreload
		}
		pm := cachepreload.NewPreloadManager(m.infra.preloadRoutes(), enablePreload)
		pm.Configure(cachepreload.PreloadConfig{
			TaskTracker:    &cachepreload.TaskTracker{},
			SessionTracker: &cachepreload.SessionTracker{},
			DBRoPool:       m.infra.DBRoPool(),
			GetQueries:     getHandlerQueries,
			GetHandler:     getRouter,
			GetETagVersion: getETagVersion,
			Metrics:        &cachepreload.PreloadMetrics{},
		})
		m.preloadManager = pm
	}

	// Wire OnGalleryCacheHit
	m.infra.SetCacheOnGalleryHit(func(ctx context.Context, folderID int64, sessionID string) {
		if m.preloadManager != nil {
			m.preloadManager.ScheduleFolderPreload(ctx, folderID, sessionID)
		}
	})

	maxIdleTime := 10 * time.Second
	if cfg != nil {
		maxIdleTime = cfg.WorkerPoolMaxIdleTime
	}
	m.pool = workerpool.NewPool(ctx, maxWorkers, minIdle, maxIdleTime)

	// Discovery. Start runs once per process; a second call replaces the
	// Service, so the first one's backlog is closed to release its lock.
	if m.discovery != nil {
		if err := m.discovery.Close(); err != nil {
			slog.Error("close discovery backlog", "err", err)
		}
	}
	backlogDir := strings.TrimSpace(m.infra.discoveryDQueDirPath)
	if backlogDir == "" {
		slog.Error("discovery dque: discoveryDQueDirPath is empty or whitespace-only; cannot open discovery backlog")
		panic("main")
	}
	m.stats = &discovery.Stats{}
	svc, err := discovery.New(discovery.Config{
		ImagesDir:           imagesDir,
		NormalizedImagesDir: normalizedImagesDir,
		BacklogDir:          backlogDir,
		RemovePrefix:        removeImagesDirPrefixFn,
	}, discovery.Deps{
		Processor:      m.fileProcessor,
		Writer:         m.infra.writer,
		Catalog:        database.NewDiscoveryDirEntMapFunc(m.infra.DBRoPool(), normalizedImagesDir, removeImagesDirPrefixFn),
		State:          m.moduleStateService,
		Pool:           m.pool,
		Stats:          m.stats,
		OnRunning:      onRunning,
		OnFileInserted: onFileInserted,
	})
	if err != nil {
		slog.Error("discovery: failed to open backlog", "path", backlogDir, "err", err)
		panic("main")
	}
	m.discovery = svc
}

// StartPool starts the discovery workers and closes poolDone when they and
// any run have stopped.
func (m *SubsystemManager) StartPool(poolDone chan struct{}) {
	m.discovery.Start()
	go func() {
		defer close(poolDone)
		m.discovery.Wait()
	}()
}

// Shutdown stops preload, closes the discovery backlog and the file
// processor. App.Shutdown calls it after discovery has stopped.
func (m *SubsystemManager) Shutdown() {
	if m.preloadManager != nil {
		m.preloadManager.Shutdown()
	}
	if m.fileProcessor != nil {
		if err := m.fileProcessor.Close(); err != nil {
			slog.Error("close file processor", "err", err)
		}
	}
	if m.discovery != nil {
		if err := m.discovery.Close(); err != nil {
			slog.Error("close discovery backlog", "err", err)
		}
	}
}

// ─── ServerDeps methods ─────────────────────────────────────────────

// StartCacheBatchLoad starts background cache batch loading when discovery is not active.
func (m *SubsystemManager) StartCacheBatchLoad(ctx context.Context) (interfaces.StartCacheBatchLoadResult, error) {
	if m.moduleStateService != nil {
		active, err := m.moduleStateService.IsActive(ctx, "discovery")
		if err != nil {
			return interfaces.StartCacheBatchLoadResult{}, err
		}
		if active {
			return interfaces.StartCacheBatchLoadResult{
				Blocked: true, Message: "Cache batch load blocked: discovery active",
			}, nil
		}
	}
	if m.batchLoadManager == nil {
		return interfaces.StartCacheBatchLoadResult{
			Blocked: false, Message: "Cache batch load not available",
		}, nil
	}
	mgr := m.batchLoadManager
	go func() {
		if err := mgr.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("cache batch load run failed", "err", err)
		}
	}()
	return interfaces.StartCacheBatchLoadResult{Blocked: false, Message: "Cache batch load started"}, nil
}

// SetPreloadEnabled enables or disables cache preload scheduling.
func (m *SubsystemManager) SetPreloadEnabled(enabled bool) {
	if m.preloadManager != nil {
		m.preloadManager.SetEnabled(enabled)
	} else {
		slog.Warn("preloadManager is nil, ignoring SetPreloadEnabled", "enabled", enabled)
	}
}

// ─── Metrics ────────────────────────────────────────────────────────

// WireMetrics connects subsystem components to the metrics collector.
func (m *SubsystemManager) WireMetrics(collector *metrics.Collector) {
	if m.infra.writer != nil {
		collector.SetWriteBatcher(m.infra.writer)
	}
	if m.pool != nil {
		collector.SetWorkerPool(m.pool)
	}
	if m.preloadManager != nil {
		collector.SetCachePreload(m.preloadManager)
	}
	if m.infra.CacheMW() != nil {
		collector.SetHTTPCache(m.infra.CacheMW())
	}
	if m.stats != nil {
		collector.SetFileProcessor(m.stats)
	}
}
