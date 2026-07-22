package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/cachebatch"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/server/modulestate"
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
	q                  *queue.Queue[string]
	qSendersActive     atomic.Int64
	fileProcessor      files.FileProcessor
	processingStats    *files.ProcessingStats
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
) {
	// Module state service
	m.moduleStateService = modulestate.NewService(m.infra.DBRwPool())

	// Queue
	queueSize := 10000
	discoveryQueueMax := 0
	if cfg != nil {
		queueSize = cfg.QueueSize
		discoveryQueueMax = cfg.DiscoveryQueueMax
	}
	if discoveryQueueMax > 0 {
		m.q = queue.NewBoundedQueue[string](queueSize, discoveryQueueMax)
	} else {
		m.q = queue.NewQueue[string](queueSize)
	}

	// File processor (tests may inject a fake via m.fileProcessor)
	if m.fileProcessor == nil {
		m.fileProcessor = files.NewFileProcessor(
			m.infra.DBRoPool(), m.infra.DBRwPool(),
			m.infra.ImporterFactory, imagesDir, newFileBatcher(m.infra.WriteBatcher()),
		)
	}

	// Cache preload manager (tests may inject a fake via m.preloadManager)
	if m.preloadManager == nil {
		enablePreload := true
		if cfg != nil {
			enablePreload = cfg.EnableCachePreload
		}
		routes := []string{"/gallery/", "/lightbox/", "/info/folder/", "/info/image/"}
		if m.infra.CacheMW() != nil {
			routes = m.infra.CacheMW().Config().CacheableRoutes
		}
		pm := cachepreload.NewPreloadManager(routes, enablePreload)
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

	// Worker pool: App.Run parameters are the base defaults; config overrides
	// only when explicitly positive (> 0). 0/0 means auto-calculate based on CPU.
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
	maxIdleTime := 10 * time.Second
	if cfg != nil {
		maxIdleTime = cfg.WorkerPoolMaxIdleTime
	}
	m.pool = workerpool.NewPool(ctx, maxWorkers, minIdle, maxIdleTime)

	m.processingStats = &files.ProcessingStats{}
}

// StartPool launches the worker pool goroutine.
func (m *SubsystemManager) StartPool(ctx context.Context, poolDone chan struct{}, normalizedImagesDir string, removeImagesDirPrefixFn func(string, string) (string, error), processor files.FileProcessor) {
	pf := files.NewPoolFuncWithProcessor(
		processor, m.q, normalizedImagesDir, removeImagesDirPrefixFn, m.processingStats,
	)
	go func() {
		defer close(poolDone)
		m.pool.StartWorkerPool(pf, m.infra.DBRoPool(), m.infra.DBRwPool(), m.q.Len)
	}()
}

// Shutdown stops preload and file-processing subsystems.
func (m *SubsystemManager) Shutdown() {
	if m.preloadManager != nil {
		m.preloadManager.Shutdown()
	}
	if m.fileProcessor != nil {
		m.fileProcessor.Close()
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

// ResetStats clears file-processing counters.
func (m *SubsystemManager) ResetStats() {
	if m.processingStats != nil {
		m.processingStats.Reset()
	}
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
	if m.infra.WriteBatcher() != nil {
		collector.SetWriteBatcher(m.infra.WriteBatcher())
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
	if m.processingStats != nil {
		collector.SetFileProcessor(m.processingStats)
	}
}
