package cachepreload

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
)

var (
	// dbPoolGetFn wraps DbSQLConnPool.Get so tests can simulate pool errors
	// or return a fake connection without a real SQLite database.
	dbPoolGetFn = (*dbconnpool.DbSQLConnPool).Get

	// dbPoolPutFn wraps DbSQLConnPool.Put so tests can no-op release.
	dbPoolPutFn = (*dbconnpool.DbSQLConnPool).Put

	// getPreloadRoutesByFolderIDFn wraps the query so tests can inject routes or errors.
	getPreloadRoutesByFolderIDFn = func(q interfaces.HandlerQueries, ctx context.Context, folderID int64) ([]string, error) {
		return q.GetPreloadRoutesByFolderID(ctx, sql.NullInt64{Int64: folderID, Valid: true})
	}

	// httpCacheExistsByKeyFn wraps HttpCacheExistsByKey so tests can control cache
	// hit/miss without populating a real cache table.
	httpCacheExistsByKeyFn = func(queries *gallerydb.CustomQueries, ctx context.Context, key string) (bool, error) {
		return queries.HttpCacheExistsByKey(ctx, key)
	}

	// folderSchedulerAddTaskFn wraps Scheduler.AddTask used by FolderPreloadTask.
	// Tests override it to exercise the AddTask error path.
	folderSchedulerAddTaskFn = func(s *scheduler.Scheduler, task scheduler.Task, mode scheduler.ExecutionMode, start time.Time) (string, error) {
		return s.AddTask(task, mode, start)
	}
)

// isCacheablePath returns true if path matches any cacheable route.
func isCacheablePath(path string, routes []string) bool {
	for _, route := range routes {
		if strings.HasPrefix(path, route) {
			return true
		}
	}
	return false
}

// FolderPreloadTask checks a folder's contents and schedules individual preload tasks.
// It respects CacheableRoutes and uses TaskTracker for deduplication.
type FolderPreloadTask struct {
	FolderID        int64                     // folder to preload (direct children only)
	SessionID       string                    // for task cancellation when user navigates away
	ETagVersion     string                    // cache-busting query (e.g. "v=20260201-01")
	CacheableRoutes []string                  // route prefixes that are cacheable (e.g. "/gallery/", "/info/")
	DBRoPool        *dbconnpool.DbSQLConnPool // read-only pool for GetPreloadRoutesByFolderID
	TaskTracker     *TaskTracker              // deduplication; TryClaimTask before scheduling
	Scheduler       *scheduler.Scheduler      // schedules per-path PreloadTask
	RequestConfig   InternalRequestConfig     // handler and ETag version for internal requests
	Metrics         *PreloadMetrics           // optional; records skipped/scheduled
	GetQueries      func(*dbconnpool.CpConn) interfaces.HandlerQueries
}

// Run implements scheduler.Task.
// Uses GetPreloadRoutesByFolderID as the source of truth (direct children only).
func (t *FolderPreloadTask) Run(ctx context.Context) error {
	slog.Debug("folder preload task running", "folder_id", t.FolderID, "session_id", truncateSessionID(t.SessionID, 8))

	cpcRo, err := dbPoolGetFn(t.DBRoPool)
	if err != nil {
		return fmt.Errorf("get db connection: %w", err)
	}
	defer dbPoolPutFn(t.DBRoPool, cpcRo)

	q := t.GetQueries(cpcRo)
	paths, err := getPreloadRoutesByFolderIDFn(q, ctx, t.FolderID)
	if err != nil {
		return fmt.Errorf("get preload routes: %w", err)
	}

	query := fmt.Sprintf("v=%s", t.ETagVersion)
	scheduled := 0
	for _, path := range paths {
		if path == "" {
			continue
		}
		if !isCacheablePath(path, t.CacheableRoutes) {
			continue
		}
		if t.schedulePreload(ctx, path, query, cpcRo.Queries) {
			scheduled++
		}
	}

	if scheduled > 0 {
		slog.Debug("folder preload scheduled", "folder_id", t.FolderID, "count", scheduled)
	}

	return nil
}

// schedulePreload checks cache existence and TaskTracker, then schedules a PreloadTask if needed.
// Encoding is no longer part of the cache key — all requests share one entry.
// Returns true if a task was scheduled.
func (t *FolderPreloadTask) schedulePreload(ctx context.Context, path, query string, queries *gallerydb.CustomQueries) bool {
	hxTarget := cachelite.PreloadVariantForPath(path)
	params := cachelite.NewCacheKeyForPreload(path, query, hxTarget)
	cacheKey := cachelite.NewCacheKey(params)

	// Check if cache entry already exists (lightweight check, no body loaded)
	exists, err := httpCacheExistsByKeyFn(queries, ctx, cacheKey)
	if err == nil && exists {
		// Cache entry exists
		if t.Metrics != nil {
			t.Metrics.RecordSkipped("already_cached")
		}
		return false
	}

	if !t.TaskTracker.TryClaimTask(cacheKey) {
		if t.Metrics != nil {
			t.Metrics.RecordSkipped("already_claimed")
		}
		return false
	}

	preloadTask := GetPreloadTask()
	preloadTask.CacheKey = cacheKey
	preloadTask.Path = path
	preloadTask.HXTarget = hxTarget
	preloadTask.TaskTracker = t.TaskTracker
	// Make a deep copy of RequestConfig to avoid data races with shared struct
	preloadTask.RequestConfig = InternalRequestConfig{
		Handler:     t.RequestConfig.Handler,
		ETagVersion: t.RequestConfig.ETagVersion,
	}
	preloadTask.Metrics = t.Metrics

	if t.Metrics != nil {
		t.Metrics.TasksScheduled.Add(1)
	}

	_, err = folderSchedulerAddTaskFn(t.Scheduler, preloadTask, scheduler.OneTime, time.Now())
	if err != nil {
		t.TaskTracker.UnregisterTask(cacheKey)
		slog.Warn("failed to schedule preload task", "path", path, "error", err)
		return false
	}

	return true
}
