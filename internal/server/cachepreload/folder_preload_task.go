package cachepreload

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/scheduler"
	"go.local/sfpg/internal/server/interfaces"
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

// variantForPath returns (hxTarget, defaultEncoding) for preload. HTMX paths get
// defaultEncoding "gzip" only when PreferredEncoding is not set; otherwise the task
// uses PreferredEncoding (from the triggering request). Full-page paths use identity.
func variantForPath(path string) (hxTarget string, defaultEncoding string) {
	if strings.HasPrefix(path, "/info/image/") || strings.HasPrefix(path, "/info/folder/") {
		return "box_info", "gzip"
	}
	if strings.HasPrefix(path, "/lightbox/") {
		return "lightbox_content", "gzip"
	}
	if strings.HasPrefix(path, "/gallery/") {
		return "gallery-content", "gzip"
	}
	return "", "identity"
}

// FolderPreloadTask checks a folder's contents and schedules individual preload tasks.
// It respects CacheableRoutes and uses TaskTracker for deduplication.
// PreferredEncoding is the normalized Accept-Encoding from the request that triggered
// this preload; HTMX-variant preloads use it so cache keys match that client.
type FolderPreloadTask struct {
	FolderID          int64
	SessionID         string
	ETagVersion       string
	PreferredEncoding string // from triggering request's Accept-Encoding (normalized)
	CacheableRoutes   []string
	DBRoPool          *dbconnpool.DbSQLConnPool
	TaskTracker       *TaskTracker
	Scheduler         *scheduler.Scheduler
	RequestConfig     InternalRequestConfig
	Metrics           *PreloadMetrics
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
// Accept-Encoding); otherwise from variantForPath. Returns true if a task was scheduled.
func (t *FolderPreloadTask) schedulePreload(ctx context.Context, path, query string, queries *gallerydb.CustomQueries) bool {
	hxTarget, defaultEncoding := variantForPath(path)
	encoding := defaultEncoding
	if hxTarget != "" && t.PreferredEncoding != "" {
		encoding = t.PreferredEncoding
	}
	var cacheKey string
	if hxTarget != "" {
		cacheKey = GenerateCacheKeyWithHX("GET", path, query, "true", hxTarget, encoding)
	} else {
		cacheKey = GenerateCacheKey("GET", path, query, encoding)
	}

	// Check if cache entry already exists (lightweight check, no body loaded)
	exists, err := queries.HttpCacheExistsByKey(ctx, cacheKey)
	if err == nil && exists != 0 {
		// Cache entry exists (exists is 1 for true, 0 for false in SQLite)
		if t.Metrics != nil {
			t.Metrics.RecordSkipped("already_cached")
		}
		return false
	}

	if t.TaskTracker.IsTaskPending(cacheKey) {
		if t.Metrics != nil {
			t.Metrics.RecordSkipped("deduplicated")
		}
		return false
	}

	taskID := fmt.Sprintf("%s-%d", t.SessionID, time.Now().UnixNano())
	if !t.TaskTracker.RegisterTask(cacheKey, t.SessionID, taskID) {
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
