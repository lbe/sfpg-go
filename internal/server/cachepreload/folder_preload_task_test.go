package cachepreload

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
)

// mockFolderQueries returns predefined subfolders and images for FolderPreloadTask tests.
type mockFolderQueries struct {
	subfolders []gallerydb.FolderView
	images     []gallerydb.FileView
	db         *sql.DB // needed to create mock rows
}

func (m *mockFolderQueries) GetFolderViewByID(ctx context.Context, id int64) (gallerydb.FolderView, error) {
	return gallerydb.FolderView{}, sql.ErrNoRows
}

func (m *mockFolderQueries) GetFoldersViewsByParentIDOrderByName(ctx context.Context, parent sql.NullInt64) ([]gallerydb.FolderView, error) {
	return m.subfolders, nil
}

func (m *mockFolderQueries) GetFileViewsByFolderIDOrderByFileName(ctx context.Context, folderID sql.NullInt64) ([]gallerydb.FileView, error) {
	return m.images, nil
}

func (m *mockFolderQueries) GetFileViewByID(ctx context.Context, id int64) (gallerydb.FileView, error) {
	return gallerydb.FileView{}, sql.ErrNoRows
}

func (m *mockFolderQueries) GetFolderByID(ctx context.Context, id int64) (gallerydb.Folder, error) {
	return gallerydb.Folder{}, sql.ErrNoRows
}

func (m *mockFolderQueries) GetThumbnailsByFileID(ctx context.Context, fileID int64) (gallerydb.Thumbnail, error) {
	return gallerydb.Thumbnail{}, sql.ErrNoRows
}

func (m *mockFolderQueries) GetThumbnailBlobDataByID(ctx context.Context, id int64) ([]byte, error) {
	return nil, sql.ErrNoRows
}

func (m *mockFolderQueries) GetPreloadRoutesByFolderID(ctx context.Context, parentID sql.NullInt64) (*sql.Rows, error) {
	// Build routes from mock data
	var routes []string
	for _, sf := range m.subfolders {
		routes = append(routes, fmt.Sprintf("/gallery/%d", sf.ID), fmt.Sprintf("/info/folder/%d", sf.ID))
	}
	for _, img := range m.images {
		routes = append(routes, fmt.Sprintf("/info/image/%d", img.ID), fmt.Sprintf("/lightbox/%d", img.ID))
	}

	// Create a temporary table and insert routes (without transaction)
	_, err := m.db.Exec("CREATE TEMP TABLE IF NOT EXISTS mock_preload_routes (route TEXT)")
	if err != nil {
		return nil, err
	}

	// Clear any previous data
	_, err = m.db.Exec("DELETE FROM mock_preload_routes")
	if err != nil {
		return nil, err
	}

	for _, route := range routes {
		_, err = m.db.Exec("INSERT INTO mock_preload_routes VALUES (?)", route)
		if err != nil {
			return nil, err
		}
	}

	return m.db.Query("SELECT route FROM mock_preload_routes")
}

func (m *mockFolderQueries) GetGalleryStatistics(ctx context.Context) (gallerydb.GetGalleryStatisticsRow, error) {
	return gallerydb.GetGalleryStatisticsRow{}, sql.ErrNoRows
}

// TestFolderPreloadTask_ImagesPreloadInfoAndLightbox verifies that FolderPreloadTask
// schedules preloads for both /info/image/{id} and /lightbox/{id} for each image.
func TestFolderPreloadTask_ImagesPreloadInfoAndLightbox(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	_, dbRwPool, dbRoPool, err := database.Setup(ctx, tempDir, nil)
	if err != nil {
		t.Fatalf("database setup: %v", err)
	}
	defer dbRwPool.Close()
	defer dbRoPool.Close()

	// Get DB handle for creating mock rows
	mockDB := dbRoPool.DB()

	var requestedPaths []string
	var pathsMu sync.Mutex
	handlerFunc := func(w http.ResponseWriter, r *http.Request) {
		pathsMu.Lock()
		requestedPaths = append(requestedPaths, r.URL.Path)
		pathsMu.Unlock()
		w.WriteHeader(200)
	}

	mock := &mockFolderQueries{
		subfolders: nil,
		images:     []gallerydb.FileView{{ID: 42, Filename: "test.jpg"}},
		db:         mockDB,
	}

	sched := scheduler.NewScheduler(4)
	schedCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go sched.Start(schedCtx)
	time.Sleep(20 * time.Millisecond)

	task := &FolderPreloadTask{
		FolderID:        1,
		SessionID:       "test-session",
		ETagVersion:     "v1",
		CacheableRoutes: []string{"/gallery/", "/lightbox/", "/info/folder/", "/info/image/"},
		DBRoPool:        dbRoPool,
		TaskTracker:     &TaskTracker{},
		Scheduler:       sched,
		RequestConfig: InternalRequestConfig{
			Handler:     http.HandlerFunc(handlerFunc),
			ETagVersion: "v1",
		},
		Metrics:    &PreloadMetrics{},
		GetQueries: func(*dbconnpool.CpConn) interfaces.HandlerQueries { return mock },
	}

	err = task.Run(ctx)
	if err != nil {
		t.Fatalf("FolderPreloadTask.Run: %v", err)
	}

	// Wait for PreloadTasks to run (they're scheduled async)
	time.Sleep(200 * time.Millisecond)

	pathsMu.Lock()
	paths := append([]string(nil), requestedPaths...)
	pathsMu.Unlock()

	hasInfo := false
	hasLightbox := false
	for _, p := range paths {
		if p == "/info/image/42" {
			hasInfo = true
		}
		if p == "/lightbox/42" {
			hasLightbox = true
		}
	}
	if !hasInfo {
		t.Errorf("expected /info/image/42 to be preloaded, got paths: %v", paths)
	}
	if !hasLightbox {
		t.Errorf("expected /lightbox/42 to be preloaded, got paths: %v", paths)
	}
}

// TestFolderPreloadTask_SkipsAlreadyCachedRoutes verifies that routes with existing
// cache entries are skipped (not preloaded) to avoid unnecessary HTTP requests.
func TestFolderPreloadTask_SkipsAlreadyCachedRoutes(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	_, dbRwPool, dbRoPool, err := database.Setup(ctx, tempDir, nil)
	if err != nil {
		t.Fatalf("database setup: %v", err)
	}
	defer dbRwPool.Close()
	defer dbRoPool.Close()

	mockDB := dbRoPool.DB()

	var requestedPaths []string
	var pathsMu sync.Mutex
	handlerFunc := func(w http.ResponseWriter, r *http.Request) {
		pathsMu.Lock()
		requestedPaths = append(requestedPaths, r.URL.Path)
		pathsMu.Unlock()
		w.WriteHeader(200)
	}

	mock := &mockFolderQueries{
		subfolders: nil,
		images:     []gallerydb.FileView{{ID: 42, Filename: "test.jpg"}},
		db:         mockDB,
	}

	// Pre-populate cache for /info/image/42 (HTMX variant with gzip encoding)
	// This route should be skipped during preload
	etagVersion := "v1"
	query := fmt.Sprintf("v=%s", etagVersion)
	cacheKeyInfo := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method:  "GET",
		Path:    "/info/image/42",
		Query:   query,
		Variant: "box_info",
	})
	now := time.Now().Unix()
	cachedEntry := &cachelite.HTTPCacheEntry{
		Key:           cacheKeyInfo,
		Method:        "GET",
		Path:          "/info/image/42",
		QueryString:   sql.NullString{String: query, Valid: true},
		Status:        200,
		Body:          []byte("cached response"),
		ContentLength: sql.NullInt64{Int64: int64(len("cached response")), Valid: true},
		CreatedAt:     now,
		ExpiresAt:     sql.NullInt64{Int64: now + 3600, Valid: true},
	}
	if storeErr := cachelite.StoreCacheEntry(ctx, dbRwPool, cachedEntry); storeErr != nil {
		t.Fatalf("failed to store cache entry: %v", err)
	}

	sched := scheduler.NewScheduler(4)
	schedCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go sched.Start(schedCtx)
	time.Sleep(20 * time.Millisecond)

	task := &FolderPreloadTask{
		FolderID:        1,
		SessionID:       "test-session",
		ETagVersion:     etagVersion,
		CacheableRoutes: []string{"/gallery/", "/lightbox/", "/info/folder/", "/info/image/"},
		DBRoPool:        dbRoPool,
		TaskTracker:     &TaskTracker{},
		Scheduler:       sched,
		RequestConfig: InternalRequestConfig{
			Handler:     http.HandlerFunc(handlerFunc),
			ETagVersion: etagVersion,
		},
		Metrics:    &PreloadMetrics{},
		GetQueries: func(*dbconnpool.CpConn) interfaces.HandlerQueries { return mock },
	}

	err = task.Run(ctx)
	if err != nil {
		t.Fatalf("FolderPreloadTask.Run: %v", err)
	}

	// Wait for PreloadTasks to run
	time.Sleep(200 * time.Millisecond)

	pathsMu.Lock()
	paths := append([]string(nil), requestedPaths...)
	pathsMu.Unlock()

	// /info/image/42 should NOT be requested (already cached)
	hasInfo := slices.Contains(paths, "/info/image/42")
	if hasInfo {
		t.Errorf("expected /info/image/42 to be skipped (already cached), but it was requested. Got paths: %v", paths)
	}

	// /lightbox/42 SHOULD be requested (not cached)
	hasLightbox := slices.Contains(paths, "/lightbox/42")
	if !hasLightbox {
		t.Errorf("expected /lightbox/42 to be preloaded (not cached), got paths: %v", paths)
	}
}

func TestIsCacheablePath(t *testing.T) {
	cases := []struct {
		path     string
		routes   []string
		expected bool
	}{
		{path: "/gallery/1", routes: []string{"/gallery/"}, expected: true},
		{path: "/info/image/1", routes: []string{"/gallery/", "/info/"}, expected: true},
		{path: "/admin/1", routes: []string{"/gallery/", "/info/"}, expected: false},
		{path: "/gallery/1", routes: []string{}, expected: false},
		{path: "", routes: []string{"/gallery/"}, expected: false},
		{path: "/gallery", routes: []string{"/gallery/"}, expected: false},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("path=%q_routes=%v", tc.path, tc.routes), func(t *testing.T) {
			got := isCacheablePath(tc.path, tc.routes)
			if got != tc.expected {
				t.Errorf("isCacheablePath(%q, %v) = %v, want %v", tc.path, tc.routes, got, tc.expected)
			}
		})
	}
}

func TestFolderPreloadTask_Run_DbPoolGetError(t *testing.T) {
	origGet := dbPoolGetFn
	origPut := dbPoolPutFn
	defer func() {
		dbPoolGetFn = origGet
		dbPoolPutFn = origPut
	}()

	dbPoolGetFn = func(_ *dbconnpool.DbSQLConnPool) (*dbconnpool.CpConn, error) {
		return nil, errors.New("pool closed")
	}
	dbPoolPutFn = func(_ *dbconnpool.DbSQLConnPool, _ *dbconnpool.CpConn) {}

	task := &FolderPreloadTask{DBRoPool: &dbconnpool.DbSQLConnPool{}}
	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from Run")
	}
	if !strings.Contains(err.Error(), "get db connection") {
		t.Errorf("expected error to contain 'get db connection', got: %v", err)
	}
}

func TestFolderPreloadTask_Run_GetRoutesError(t *testing.T) {
	origGet := dbPoolGetFn
	origPut := dbPoolPutFn
	origRoutes := getPreloadRoutesByFolderIDFn
	defer func() {
		dbPoolGetFn = origGet
		dbPoolPutFn = origPut
		getPreloadRoutesByFolderIDFn = origRoutes
	}()

	dbPoolGetFn = func(_ *dbconnpool.DbSQLConnPool) (*dbconnpool.CpConn, error) {
		return &dbconnpool.CpConn{}, nil
	}
	dbPoolPutFn = func(_ *dbconnpool.DbSQLConnPool, _ *dbconnpool.CpConn) {}
	getPreloadRoutesByFolderIDFn = func(_ interfaces.HandlerQueries, _ context.Context, _ int64) ([]string, error) {
		return nil, errors.New("query denied")
	}

	task := &FolderPreloadTask{
		DBRoPool: &dbconnpool.DbSQLConnPool{},
		GetQueries: func(*dbconnpool.CpConn) interfaces.HandlerQueries {
			return &mockFolderQueries{}
		},
	}
	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from Run")
	}
	if !strings.Contains(err.Error(), "get preload routes") {
		t.Errorf("expected error to contain 'get preload routes', got: %v", err)
	}
}

func TestFolderPreloadTask_Run_RowsErrError(t *testing.T) {
	origGet := dbPoolGetFn
	origPut := dbPoolPutFn
	origRoutes := getPreloadRoutesByFolderIDFn
	defer func() {
		dbPoolGetFn = origGet
		dbPoolPutFn = origPut
		getPreloadRoutesByFolderIDFn = origRoutes
	}()

	dbPoolGetFn = func(_ *dbconnpool.DbSQLConnPool) (*dbconnpool.CpConn, error) {
		return &dbconnpool.CpConn{}, nil
	}
	dbPoolPutFn = func(_ *dbconnpool.DbSQLConnPool, _ *dbconnpool.CpConn) {}
	getPreloadRoutesByFolderIDFn = func(_ interfaces.HandlerQueries, _ context.Context, _ int64) ([]string, error) {
		return nil, errors.New("rows iteration failed")
	}

	task := &FolderPreloadTask{
		DBRoPool: &dbconnpool.DbSQLConnPool{},
		GetQueries: func(*dbconnpool.CpConn) interfaces.HandlerQueries {
			return &mockFolderQueries{}
		},
	}
	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from Run")
	}
	if !strings.Contains(err.Error(), "get preload routes") {
		t.Errorf("expected error to contain 'get preload routes', got: %v", err)
	}
}

func TestFolderPreloadTask_Run_EmptyPathSkipped(t *testing.T) {
	origGet := dbPoolGetFn
	origPut := dbPoolPutFn
	origRoutes := getPreloadRoutesByFolderIDFn
	origCache := httpCacheExistsByKeyFn
	origAdd := folderSchedulerAddTaskFn
	defer func() {
		dbPoolGetFn = origGet
		dbPoolPutFn = origPut
		getPreloadRoutesByFolderIDFn = origRoutes
		httpCacheExistsByKeyFn = origCache
		folderSchedulerAddTaskFn = origAdd
	}()

	dbPoolGetFn = func(_ *dbconnpool.DbSQLConnPool) (*dbconnpool.CpConn, error) {
		return &dbconnpool.CpConn{}, nil
	}
	dbPoolPutFn = func(_ *dbconnpool.DbSQLConnPool, _ *dbconnpool.CpConn) {}
	getPreloadRoutesByFolderIDFn = func(_ interfaces.HandlerQueries, _ context.Context, _ int64) ([]string, error) {
		return []string{"", "/gallery/1"}, nil
	}
	httpCacheExistsByKeyFn = func(_ *gallerydb.CustomQueries, _ context.Context, _ string) (bool, error) {
		return false, nil
	}

	var scheduledPaths []string
	folderSchedulerAddTaskFn = func(_ *scheduler.Scheduler, task scheduler.Task, _ scheduler.ExecutionMode, _ time.Time) (string, error) {
		pt, ok := task.(*PreloadTask)
		if !ok {
			t.Fatalf("expected *PreloadTask, got %T", task)
		}
		scheduledPaths = append(scheduledPaths, pt.Path)
		return "task-id", nil
	}

	task := &FolderPreloadTask{
		DBRoPool:        &dbconnpool.DbSQLConnPool{},
		CacheableRoutes: []string{"/gallery/"},
		TaskTracker:     &TaskTracker{},
		Scheduler:       scheduler.NewScheduler(1),
		ETagVersion:     "v1",
		GetQueries: func(*dbconnpool.CpConn) interfaces.HandlerQueries {
			return &mockFolderQueries{}
		},
	}

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(scheduledPaths) != 1 || scheduledPaths[0] != "/gallery/1" {
		t.Errorf("expected only /gallery/1 scheduled, got %v", scheduledPaths)
	}
}

func TestFolderPreloadTask_Run_NonCacheablePathSkipped(t *testing.T) {
	origGet := dbPoolGetFn
	origPut := dbPoolPutFn
	origRoutes := getPreloadRoutesByFolderIDFn
	origCache := httpCacheExistsByKeyFn
	origAdd := folderSchedulerAddTaskFn
	defer func() {
		dbPoolGetFn = origGet
		dbPoolPutFn = origPut
		getPreloadRoutesByFolderIDFn = origRoutes
		httpCacheExistsByKeyFn = origCache
		folderSchedulerAddTaskFn = origAdd
	}()

	dbPoolGetFn = func(_ *dbconnpool.DbSQLConnPool) (*dbconnpool.CpConn, error) {
		return &dbconnpool.CpConn{}, nil
	}
	dbPoolPutFn = func(_ *dbconnpool.DbSQLConnPool, _ *dbconnpool.CpConn) {}
	getPreloadRoutesByFolderIDFn = func(_ interfaces.HandlerQueries, _ context.Context, _ int64) ([]string, error) {
		return []string{"/admin/1", "/gallery/1"}, nil
	}
	httpCacheExistsByKeyFn = func(_ *gallerydb.CustomQueries, _ context.Context, _ string) (bool, error) {
		return false, nil
	}

	var scheduledPaths []string
	folderSchedulerAddTaskFn = func(_ *scheduler.Scheduler, task scheduler.Task, _ scheduler.ExecutionMode, _ time.Time) (string, error) {
		pt, ok := task.(*PreloadTask)
		if !ok {
			t.Fatalf("expected *PreloadTask, got %T", task)
		}
		scheduledPaths = append(scheduledPaths, pt.Path)
		return "task-id", nil
	}

	task := &FolderPreloadTask{
		DBRoPool:        &dbconnpool.DbSQLConnPool{},
		CacheableRoutes: []string{"/gallery/"},
		TaskTracker:     &TaskTracker{},
		Scheduler:       scheduler.NewScheduler(1),
		ETagVersion:     "v1",
		GetQueries: func(*dbconnpool.CpConn) interfaces.HandlerQueries {
			return &mockFolderQueries{}
		},
	}

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(scheduledPaths) != 1 || scheduledPaths[0] != "/gallery/1" {
		t.Errorf("expected only /gallery/1 scheduled, got %v", scheduledPaths)
	}
}

func TestFolderPreloadTask_Run_ScheduleAddTaskError(t *testing.T) {
	origGet := dbPoolGetFn
	origPut := dbPoolPutFn
	origRoutes := getPreloadRoutesByFolderIDFn
	origCache := httpCacheExistsByKeyFn
	origAdd := folderSchedulerAddTaskFn
	defer func() {
		dbPoolGetFn = origGet
		dbPoolPutFn = origPut
		getPreloadRoutesByFolderIDFn = origRoutes
		httpCacheExistsByKeyFn = origCache
		folderSchedulerAddTaskFn = origAdd
	}()

	dbPoolGetFn = func(_ *dbconnpool.DbSQLConnPool) (*dbconnpool.CpConn, error) {
		return &dbconnpool.CpConn{}, nil
	}
	dbPoolPutFn = func(_ *dbconnpool.DbSQLConnPool, _ *dbconnpool.CpConn) {}
	getPreloadRoutesByFolderIDFn = func(_ interfaces.HandlerQueries, _ context.Context, _ int64) ([]string, error) {
		return []string{"/gallery/1"}, nil
	}
	httpCacheExistsByKeyFn = func(_ *gallerydb.CustomQueries, _ context.Context, _ string) (bool, error) {
		return false, nil
	}
	folderSchedulerAddTaskFn = func(_ *scheduler.Scheduler, _ scheduler.Task, _ scheduler.ExecutionMode, _ time.Time) (string, error) {
		return "", errors.New("scheduler full")
	}

	tt := &TaskTracker{}
	task := &FolderPreloadTask{
		DBRoPool:        &dbconnpool.DbSQLConnPool{},
		CacheableRoutes: []string{"/gallery/"},
		TaskTracker:     tt,
		Scheduler:       scheduler.NewScheduler(1),
		ETagVersion:     "v1",
		GetQueries: func(*dbconnpool.CpConn) interfaces.HandlerQueries {
			return &mockFolderQueries{}
		},
	}

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("Run should not return error when schedule fails per path: %v", err)
	}

	// The cache key should be unregistered after the failed schedule.
	if !tt.TryClaimTask(cachelite.NewCacheKey(cachelite.NewCacheKeyForPreload("/gallery/1", "v=v1", "gallery-content"))) {
		t.Error("expected cache key to be unregistered after failed schedule")
	}
}

func TestFolderPreloadTask_Run_AlreadyCachedSkip(t *testing.T) {
	origGet := dbPoolGetFn
	origPut := dbPoolPutFn
	origRoutes := getPreloadRoutesByFolderIDFn
	origCache := httpCacheExistsByKeyFn
	origAdd := folderSchedulerAddTaskFn
	defer func() {
		dbPoolGetFn = origGet
		dbPoolPutFn = origPut
		getPreloadRoutesByFolderIDFn = origRoutes
		httpCacheExistsByKeyFn = origCache
		folderSchedulerAddTaskFn = origAdd
	}()

	dbPoolGetFn = func(_ *dbconnpool.DbSQLConnPool) (*dbconnpool.CpConn, error) {
		return &dbconnpool.CpConn{}, nil
	}
	dbPoolPutFn = func(_ *dbconnpool.DbSQLConnPool, _ *dbconnpool.CpConn) {}
	getPreloadRoutesByFolderIDFn = func(_ interfaces.HandlerQueries, _ context.Context, _ int64) ([]string, error) {
		return []string{"/info/image/1"}, nil
	}
	httpCacheExistsByKeyFn = func(_ *gallerydb.CustomQueries, _ context.Context, _ string) (bool, error) {
		return true, nil
	}

	var scheduledPaths []string
	folderSchedulerAddTaskFn = func(_ *scheduler.Scheduler, task scheduler.Task, _ scheduler.ExecutionMode, _ time.Time) (string, error) {
		pt, ok := task.(*PreloadTask)
		if !ok {
			t.Fatalf("expected *PreloadTask, got %T", task)
		}
		scheduledPaths = append(scheduledPaths, pt.Path)
		return "task-id", nil
	}

	metrics := &PreloadMetrics{}
	task := &FolderPreloadTask{
		DBRoPool:        &dbconnpool.DbSQLConnPool{},
		CacheableRoutes: []string{"/info/"},
		TaskTracker:     &TaskTracker{},
		Scheduler:       scheduler.NewScheduler(1),
		ETagVersion:     "v1",
		Metrics:         metrics,
		GetQueries: func(*dbconnpool.CpConn) interfaces.HandlerQueries {
			return &mockFolderQueries{}
		},
	}

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if metrics.TasksSkipped.Load() != 1 {
		t.Errorf("expected TasksSkipped = 1, got %d", metrics.TasksSkipped.Load())
	}
	if len(scheduledPaths) != 0 {
		t.Errorf("expected no tasks scheduled, got %v", scheduledPaths)
	}
}
