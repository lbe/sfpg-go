package cachepreload

import (
	"context"
	"log/slog"
	"net/http"
	"runtime"
	"sync"
	"time"

	"go.local/sfpg/internal/cachelite"
	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/scheduler"
	"go.local/sfpg/internal/server/interfaces"
)

// PreloadManager manages the cache preload scheduler lifecycle with dynamic enable/disable support.
// It implements PreloadService and can replace the scheduler instance when toggling.
type PreloadManager struct {
	mu              sync.RWMutex
	enabled         bool
	scheduler       *scheduler.Scheduler
	schedulerCtx    context.Context
	schedulerCancel context.CancelFunc
	cacheableRoutes []string
	onSetEnabled    func(bool) // optional callback for tests

	// Dependencies for ScheduleFolderPreload (set via Configure)
	taskTracker    *TaskTracker
	sessionTracker *SessionTracker
	dbRoPool       *dbconnpool.DbSQLConnPool
	getQueries     func(*dbconnpool.CpConn) interfaces.HandlerQueries
	getHandler     func() http.Handler // Lazy: handler chain built after PreloadManager init
	getETagVersion func() string
	metrics        *PreloadMetrics
}

// NewPreloadManager creates a new PreloadManager with the given cacheable routes.
// If initialEnabled is true, the scheduler is started immediately.
func NewPreloadManager(cacheableRoutes []string, initialEnabled bool) *PreloadManager {
	pm := &PreloadManager{
		enabled:         initialEnabled,
		cacheableRoutes: copyRoutes(cacheableRoutes),
	}
	if initialEnabled {
		pm.startScheduler()
	}
	return pm
}

func truncateSessionID(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func copyRoutes(routes []string) []string {
	if routes == nil {
		return nil
	}
	cp := make([]string, len(routes))
	copy(cp, routes)
	return cp
}

// SetOnSetEnabled sets an optional callback invoked when SetEnabled is called (for tests).
func (pm *PreloadManager) SetOnSetEnabled(fn func(bool)) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.onSetEnabled = fn
}

// PreloadConfig holds dependencies for ScheduleFolderPreload.
type PreloadConfig struct {
	TaskTracker    *TaskTracker
	SessionTracker *SessionTracker
	DBRoPool       *dbconnpool.DbSQLConnPool
	GetQueries     func(*dbconnpool.CpConn) interfaces.HandlerQueries
	GetHandler     func() http.Handler // Lazy: full middleware chain
	GetETagVersion func() string
	Metrics        *PreloadMetrics
}

// Configure sets dependencies for ScheduleFolderPreload. Call after creation.
func (pm *PreloadManager) Configure(cfg PreloadConfig) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.taskTracker = cfg.TaskTracker
	pm.sessionTracker = cfg.SessionTracker
	pm.dbRoPool = cfg.DBRoPool
	pm.getQueries = cfg.GetQueries
	pm.getHandler = cfg.GetHandler
	pm.getETagVersion = cfg.GetETagVersion
	pm.metrics = cfg.Metrics
	if pm.taskTracker == nil {
		pm.taskTracker = &TaskTracker{}
	}
	if pm.sessionTracker == nil {
		pm.sessionTracker = &SessionTracker{}
	}
}

func (pm *PreloadManager) startScheduler() {
	ctx, cancel := context.WithCancel(context.Background())
	pm.schedulerCtx = ctx
	pm.schedulerCancel = cancel
	sched := scheduler.NewScheduler(4 * runtime.NumCPU())
	pm.scheduler = sched
	go func() {
		if err := sched.Start(ctx); err != nil && err != context.Canceled {
			slog.Debug("cache preload scheduler stopped", "error", err)
		}
	}()
}

func (pm *PreloadManager) stopScheduler() {
	if pm.schedulerCancel != nil {
		pm.schedulerCancel()
		pm.schedulerCancel = nil
	}
	if pm.scheduler != nil {
		_ = pm.scheduler.Shutdown()
		pm.scheduler = nil
	}
	pm.schedulerCtx = nil
}

// ScheduleFolderPreload schedules background cache preload for a folder.
// acceptEncoding is the triggering request's Accept-Encoding; its normalized value
// is used for preload keys so they match that client's subsequent requests.
// sessionID is used for task cancellation when user navigates away. Non-blocking.
func (pm *PreloadManager) ScheduleFolderPreload(ctx context.Context, folderID int64, sessionID string, acceptEncoding string) {
	pm.mu.RLock()
	enabled := pm.enabled
	sched := pm.scheduler
	taskTracker := pm.taskTracker
	sessionTracker := pm.sessionTracker
	dbRoPool := pm.dbRoPool
	getQueries := pm.getQueries
	getHandler := pm.getHandler
	getETag := pm.getETagVersion
	metrics := pm.metrics
	routes := copyRoutes(pm.cacheableRoutes)
	pm.mu.RUnlock()

	if !enabled || sched == nil {
		slog.Debug("cache preload skipped", "reason", "disabled_or_no_scheduler", "folder_id", folderID)
		return
	}
	if dbRoPool == nil || getQueries == nil || getHandler == nil || getETag == nil {
		slog.Debug("cache preload skipped", "reason", "missing_deps", "folder_id", folderID)
		return
	}
	handler := getHandler()
	if handler == nil {
		slog.Debug("cache preload skipped", "reason", "nil_handler", "folder_id", folderID)
		return
	}

	slog.Debug("cache preload run started", "folder_id", folderID, "session_id", truncateSessionID(sessionID, 8))

	// Cancel previous folder's tasks for this session
	if sessionTracker != nil {
		prevFolderID := sessionTracker.OnFolderOpen(sessionID, folderID)
		if prevFolderID != 0 && taskTracker != nil {
			taskIDs := taskTracker.CancelSessionTasks(sessionID)
			for _, id := range taskIDs {
				_ = sched.RemoveTask(id)
			}
		}
	}

	etagVersion := getETag()
	reqConfig := InternalRequestConfig{
		Handler:     handler,
		ETagVersion: etagVersion,
	}
	// Use triggering request's encoding priority so preload keys match that client.
	preferredEncoding := cachelite.NormalizeAcceptEncoding(acceptEncoding)

	fpt := &FolderPreloadTask{
		FolderID:          folderID,
		SessionID:         sessionID,
		ETagVersion:       etagVersion,
		PreferredEncoding: preferredEncoding,
		CacheableRoutes:   routes,
		DBRoPool:          dbRoPool,
		TaskTracker:       taskTracker,
		Scheduler:         sched,
		RequestConfig:     reqConfig,
		Metrics:           metrics,
		GetQueries:        getQueries,
	}

	// Schedule FolderPreloadTask (fire-and-forget)
	_, err := sched.AddTask(fpt, scheduler.OneTime, time.Now())
	if err != nil {
		slog.Warn("failed to schedule folder preload", "folder_id", folderID, "error", err)
	}
}

// SetEnabled dynamically enables or disables cache preloading.
// When disabled, all pending tasks are cancelled.
func (pm *PreloadManager) SetEnabled(enabled bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.onSetEnabled != nil {
		pm.onSetEnabled(enabled)
	}

	if pm.enabled == enabled {
		return
	}
	pm.enabled = enabled

	if enabled {
		pm.startScheduler()
	} else {
		pm.stopScheduler()
	}
}

// IsEnabled returns whether cache preloading is currently enabled.
func (pm *PreloadManager) IsEnabled() bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.enabled
}

// Shutdown gracefully shuts down the PreloadManager, stopping the scheduler if running.
// Call this during application shutdown.
func (pm *PreloadManager) Shutdown() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.enabled = false
	pm.stopScheduler()
}

// GetScheduler returns the current scheduler for testing. Returns nil if disabled.
func (pm *PreloadManager) GetScheduler() *scheduler.Scheduler {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.scheduler
}

// For test synchronization: wait briefly for scheduler to start.
func (pm *PreloadManager) waitForSchedulerStart() {
	for range 50 {
		pm.mu.RLock()
		sched := pm.scheduler
		pm.mu.RUnlock()
		if sched != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// PreloadMetricsSnapshot holds a snapshot of preload metrics.
type PreloadMetricsSnapshot struct {
	TasksScheduled int64
	TasksCompleted int64
	TasksFailed    int64
	TasksCancelled int64
	TasksSkipped   int64
	TotalDuration  time.Duration
}

// GetMetrics returns the current preload metrics snapshot.
func (pm *PreloadManager) GetMetrics() PreloadMetricsSnapshot {
	pm.mu.RLock()
	metrics := pm.metrics
	pm.mu.RUnlock()

	if metrics == nil {
		return PreloadMetricsSnapshot{}
	}

	return PreloadMetricsSnapshot{
		TasksScheduled: metrics.TasksScheduled.Load(),
		TasksCompleted: metrics.TasksCompleted.Load(),
		TasksFailed:    metrics.TasksFailed.Load(),
		TasksCancelled: metrics.TasksCancelled.Load(),
		TasksSkipped:   metrics.TasksSkipped.Load(),
		TotalDuration:  time.Duration(metrics.TotalDuration.Load()),
	}
}
