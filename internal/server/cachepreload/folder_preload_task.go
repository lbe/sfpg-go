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
	FolderID          int64                     // folder to preload (direct children only)
	SessionID         string                    // for task cancellation when user navigates away
	ETagVersion       string                    // cache-busting query (e.g. "v=20260201-01")
	PreferredEncoding string                    // from triggering request's Accept-Encoding (normalized); HTMX preloads use it so keys match that client
	CacheableRoutes   []string                  // route prefixes that are cacheable (e.g. "/gallery/", "/info/")
	DBRoPool          *dbconnpool.DbSQLConnPool // read-only pool for GetPreloadRoutesByFolderID
	TaskTracker       *TaskTracker              // deduplication; TryClaimTask before scheduling
	Scheduler         *scheduler.Scheduler      // schedules per-path PreloadTask
	RequestConfig     InternalRequestConfig     // handler and ETag version for internal requests
	Metrics           *PreloadMetrics           // optional; records skipped/scheduled
	GetQueries        func(*dbconnpool.CpConn) interfaces.HandlerQueries
}

// Run implements scheduler.Task.
// Uses GetPreloadRoutesByFolderID as the source of truth (direct children only).
func (t *FolderPreloadTask) Run(ctx context.Context) error {
	slog.Debug("folder preload task running", "folder_id", t.FolderID, "session_id", truncateSessionID(t.SessionID, 8))

	cpc, err := t.DBRoPool.Get()
	if err != nil {
		return fmt.Errorf("get db connection: %w", err)
	}
	defer t.DBRoPool.Put(cpc)

	q := t.GetQueries(cpc)
	rows, err := q.GetPreloadRoutesByFolderID(ctx,
		sql.NullInt64{Int64: t.FolderID, Valid: true})
	if err != nil {
		return fmt.Errorf("get preload routes: %w", err)
	}
	defer rows.Close()

	query := fmt.Sprintf("v=%s", t.ETagVersion)
	scheduled := 0
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			slog.Warn("failed to scan preload route", "error", err)
			continue
		}
		if path == "" {
			continue
		}
		if !isCacheablePath(path, t.CacheableRoutes) {
			continue
		}
		if t.schedulePreload(ctx, path, query, cpc.Queries) {
			scheduled++
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate preload routes: %w", err)
	}

	if scheduled > 0 {
		slog.Debug("folder preload scheduled", "folder_id", t.FolderID, "count", scheduled)
	}

	return nil
}

// schedulePreload checks cache existence and TaskTracker, then schedules a PreloadTask if needed.
// For HTMX paths the encoding comes from PreferredEncoding (triggering request's
// Accept-Encoding); otherwise from cachelite.VariantForPath. Returns true if a task was scheduled.
func (t *FolderPreloadTask) schedulePreload(ctx context.Context, path, query string, queries *gallerydb.CustomQueries) bool {
	hxTarget, defaultEncoding := cachelite.VariantForPath(path)
	encoding := defaultEncoding
	if hxTarget != "" && t.PreferredEncoding != "" {
		encoding = t.PreferredEncoding
	}
	params := cachelite.NewCacheKeyForPreload(path, query, encoding, "", hxTarget != "")
	cacheKey := cachelite.NewCacheKey(params)

	// Check if cache entry already exists (lightweight check, no body loaded)
	exists, err := queries.HttpCacheExistsByKey(ctx, cacheKey)
	if err == nil && exists != 0 {
		// Cache entry exists (exists is 1 for true, 0 for false in SQLite)
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
	preloadTask.Encoding = encoding
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

	_, err = t.Scheduler.AddTask(preloadTask, scheduler.OneTime, time.Now())
	if err != nil {
		t.TaskTracker.UnregisterTask(cacheKey)
		slog.Warn("failed to schedule preload task", "path", path, "error", err)
		return false
	}

	return true
}
