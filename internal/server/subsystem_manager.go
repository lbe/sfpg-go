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

// SubsystemManager owns background processing subsystems.
type SubsystemManager struct {
	pool               *workerpool.Pool
	q                  *queue.Queue[string]
	qSendersActive     atomic.Int64
	fileProcessor      files.FileProcessor
	processingStats    *files.ProcessingStats
	scheduler          *scheduler.Scheduler
	preloadManager     *cachepreload.PreloadManager
	batchLoadManager   *cachebatch.Manager
	moduleStateService *modulestate.Service

	infra *InfrastructureService
}

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
	if cfg != nil {
		queueSize = cfg.QueueSize
	}
	m.q = queue.NewQueue[string](queueSize)

	// File processor
	batcherAdapter := newBatcherAdapter(m.infra.WriteBatcher())
	m.fileProcessor = files.NewFileProcessor(
		m.infra.DBRoPool(), m.infra.DBRwPool(),
		m.infra.ImporterFactory, imagesDir, batcherAdapter,
	)

	// Cache preload manager
	enablePreload := true
	if cfg != nil {
		enablePreload = cfg.EnableCachePreload
	}
	routes := []string{"/gallery/", "/lightbox/", "/info/folder/", "/info/image/"}
	if m.infra.CacheMW() != nil {
		routes = m.infra.CacheMW().Config().CacheableRoutes
	}
	m.preloadManager = cachepreload.NewPreloadManager(routes, enablePreload)
	m.preloadManager.Configure(cachepreload.PreloadConfig{
		TaskTracker:    &cachepreload.TaskTracker{},
		SessionTracker: &cachepreload.SessionTracker{},
		DBRoPool:       m.infra.DBRoPool(),
		GetQueries:     getHandlerQueries,
		GetHandler:     getRouter,
		GetETagVersion: getETagVersion,
		Metrics:        &cachepreload.PreloadMetrics{},
	})

	// Wire OnGalleryCacheHit
	m.infra.SetCacheOnGalleryHit(func(ctx context.Context, folderID int64, sessionID, acceptEncoding string) {
		if m.preloadManager != nil {
			m.preloadManager.ScheduleFolderPreload(ctx, folderID, sessionID, acceptEncoding)
		}
	})

	// Worker pool
	maxPoolWorkers := 100
	minPoolWorkers := 10
	if cfg != nil {
		if cfg.WorkerPoolMax > 0 {
			maxPoolWorkers = cfg.WorkerPoolMax
		}
		if cfg.WorkerPoolMinIdle > 0 {
			minPoolWorkers = cfg.WorkerPoolMinIdle
		}
	}
	maxIdleTime := 10 * time.Second
	if cfg != nil {
		maxIdleTime = cfg.WorkerPoolMaxIdleTime
	}
	m.pool = workerpool.NewPool(ctx, maxPoolWorkers, minPoolWorkers, maxIdleTime)

	m.processingStats = &files.ProcessingStats{}
}

// StartPool launches the worker pool goroutine.
func (m *SubsystemManager) StartPool(ctx context.Context, poolDone chan struct{}, normalizedImagesDir string, removeImagesDirPrefixFn func(string, string) (string, error)) {
	pf := files.NewPoolFuncWithProcessor(
		m.fileProcessor, m.q, normalizedImagesDir, removeImagesDirPrefixFn, m.processingStats,
	)
	go func() {
		defer close(poolDone)
		m.pool.StartWorkerPool(pf, m.infra.DBRoPool(), m.infra.DBRwPool(), m.q.Len)
	}()
}

func (m *SubsystemManager) Shutdown() {
	if m.preloadManager != nil {
		m.preloadManager.Shutdown()
	}
	if m.fileProcessor != nil {
		m.fileProcessor.Close()
	}
}

// ─── ServerDeps methods ─────────────────────────────────────────────

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

func (m *SubsystemManager) ResetStats() {
	if m.processingStats != nil {
		m.processingStats.Reset()
	}
}

func (m *SubsystemManager) SetPreloadEnabled(enabled bool) {
	if m.preloadManager != nil {
		m.preloadManager.SetEnabled(enabled)
	} else {
		slog.Warn("preloadManager is nil, ignoring SetPreloadEnabled", "enabled", enabled)
	}
}

// ─── Metrics ────────────────────────────────────────────────────────

func (m *SubsystemManager) WireMetrics(collector *metrics.Collector) {
	if m.infra.WriteBatcher() != nil {
		collector.SetWriteBatcher(&writeBatcherAdapter{wb: m.infra.WriteBatcher()})
	}
	if m.pool != nil {
		collector.SetWorkerPool(&workerPoolAdapter{pool: m.pool})
	}
	if m.preloadManager != nil {
		collector.SetCachePreload(&cachePreloadAdapter{pm: m.preloadManager})
	}
	if m.infra.CacheMW() != nil {
		collector.SetHTTPCache(&httpCacheAdapter{cache: m.infra.CacheMW()})
	}
	if m.processingStats != nil {
		collector.SetFileProcessor(&fileProcessorAdapter{stats: m.processingStats})
	}
}
