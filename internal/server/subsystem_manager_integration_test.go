//go:build integration

package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/cachebatch"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

// startTestManager creates a fully wired SubsystemManager for testing.
// The returned *App is managed by t.Cleanup, so callers do not need to shut it down.
func startTestManager(t *testing.T, minPoolWorkers, maxPoolWorkers int, cfg *config.Config, opts ...func(*App)) (*SubsystemManager, *App) {
	t.Helper()
	app := CreateApp(t)
	for _, opt := range opts {
		opt(app)
	}

	app.Start(
		app.RuntimeManager.ctx,
		cfg,
		minPoolWorkers,
		maxPoolWorkers,
		app.imagesDir,
		app.normalizedImagesDir,
		removeImagesDirPrefix,
		app.getRouter,
		app.GetHandlerQueries,
		app.GetETagVersion,
	)

	return app.SubsystemManager, app
}

// withCacheMW returns an option that initializes the HTTP cache middleware before
// SubsystemManager.Start runs, so the preload manager picks up CacheableRoutes from it.
func withCacheMW(cfg *config.Config) func(*App) {
	return func(app *App) {
		app.InitializeHTTPCache(cfg)
	}
}

// expectedAutoPoolWorkers returns the auto-calculated max worker count based on
// the current CPU count, matching workerpool.Pool.getMinMaxPoolWorkers. Min
// idle is never auto: min 0 means no idle workers, so it stays 0.
func expectedAutoPoolWorkers() (minWorkers, maxWorkers int) {
	numCPU := runtime.NumCPU()

	switch {
	case numCPU > 4:
		maxWorkers = numCPU - 2
	case numCPU > 2 && numCPU <= 4:
		maxWorkers = 2
	default:
		maxWorkers = 1
	}

	return 0, maxWorkers
}

// preloadManagerRoutes reads the unexported cacheableRoutes slice from a PreloadManager.
func preloadManagerRoutes(t *testing.T, pm interface{}) []string {
	t.Helper()
	v := reflect.ValueOf(pm).Elem().FieldByName("cacheableRoutes")
	routes := make([]string, v.Len())
	for i := range routes {
		routes[i] = v.Index(i).String()
	}
	return routes
}

// fakeBatchQueries is a minimal cachebatch.BatchLoadQueries implementation for tests.
type fakeBatchQueries struct{}

func (fakeBatchQueries) GetBatchLoadTargets(ctx context.Context) ([]gallerydb.BatchLoadTarget, error) {
	return nil, nil
}

func (fakeBatchQueries) HttpCacheExistsByKey(ctx context.Context, key string) (bool, error) {
	return false, nil
}

// waitForProcessedPaths waits until the recording file processor has recorded
// at least want processed paths. The pool dequeues items before processing
// them, so queue length alone is not a reliable completion signal.
func waitForProcessedPaths(t *testing.T, fp *recordingFileProcessor, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(fp.ProcessedPaths()) < want {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d processed path(s), got %d", want, len(fp.ProcessedPaths()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSubsystemManager_Start(t *testing.T) {
	defaultRoutes := []string{"/gallery/", "/lightbox/", "/info/folder/", "/info/image/"}

	tests := []struct {
		name               string
		cfg                *config.Config
		wantPoolMax        int
		wantPoolMin        int
		wantIdleTime       time.Duration
		wantPreloadEnabled bool
	}{
		{
			name:               "nil config uses defaults",
			cfg:                nil,
			wantPoolMax:        0, // auto
			wantPoolMin:        0, // no idle workers
			wantIdleTime:       10 * time.Second,
			wantPreloadEnabled: true,
		},
		{
			name: "explicit config values are honored",
			cfg: &config.Config{
				WorkerPoolMax:         7,
				WorkerPoolMinIdle:     3,
				WorkerPoolMaxIdleTime: 5 * time.Second,
				EnableCachePreload:    false,
			},
			wantPoolMax:        7,
			wantPoolMin:        3,
			wantIdleTime:       5 * time.Second,
			wantPreloadEnabled: false,
		},
		{
			name: "explicit max with min idle 0",
			cfg: &config.Config{
				WorkerPoolMax:         7,
				WorkerPoolMinIdle:     0,
				WorkerPoolMaxIdleTime: 10 * time.Second,
				EnableCachePreload:    true,
			},
			wantPoolMax:        7,
			wantPoolMin:        0,
			wantIdleTime:       10 * time.Second,
			wantPreloadEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, app := startTestManager(t, 0, 0, tt.cfg)

			if mgr.moduleStateService == nil {
				t.Fatal("moduleStateService should be initialized")
			}

			if mgr.q == nil {
				t.Fatal("queue should be initialized")
			}
			// The discovery backlog is dque-backed (wiped on start): it opens
			// empty rather than sized by QueueSize, and its dedicated directory
			// exists on disk. Capacity assertions do not apply (no Cap on the
			// adapter) and QueueSize does not size the discovery work queue.
			if got := mgr.q.Len(); got != 0 {
				t.Errorf("queue Len = %d, want 0 on fresh start", got)
			}
			if _, err := os.Stat(app.discoveryDQueDirPath); err != nil {
				t.Errorf("discovery dque directory not created at %q: %v", app.discoveryDQueDirPath, err)
			}

			if mgr.fileProcessor == nil {
				t.Error("fileProcessor should be initialized")
			}

			if mgr.preloadManager == nil {
				t.Fatal("preloadManager should be initialized")
			}
			if got := mgr.preloadManager.IsEnabled(); got != tt.wantPreloadEnabled {
				t.Errorf("preloadManager.IsEnabled() = %v, want %v", got, tt.wantPreloadEnabled)
			}
			gotRoutes := preloadManagerRoutes(t, mgr.preloadManager)
			if !reflect.DeepEqual(gotRoutes, defaultRoutes) {
				t.Errorf("preloadManager routes = %v, want %v", gotRoutes, defaultRoutes)
			}

			if mgr.pool == nil {
				t.Fatal("worker pool should be initialized")
			}
			wantMax, wantMin := tt.wantPoolMax, tt.wantPoolMin
			if wantMax == 0 {
				_, wantMax = expectedAutoPoolWorkers()
			}
			if mgr.pool.MaxWorkers != wantMax {
				t.Errorf("pool.MaxWorkers = %d, want %d", mgr.pool.MaxWorkers, wantMax)
			}
			if mgr.pool.MinWorkers != wantMin {
				t.Errorf("pool.MinWorkers = %d, want %d", mgr.pool.MinWorkers, wantMin)
			}
			if mgr.pool.MaxIdleTime != tt.wantIdleTime {
				t.Errorf("pool.MaxIdleTime = %v, want %v", mgr.pool.MaxIdleTime, tt.wantIdleTime)
			}

			if mgr.processingStats == nil {
				t.Error("processingStats should be initialized")
			}
		})
	}
}

func TestSubsystemManager_Start_WorkerPoolDefaults(t *testing.T) {
	mgr, _ := startTestManager(t, 0, 0, nil)

	_, wantMax := expectedAutoPoolWorkers()
	if mgr.pool.MaxWorkers != wantMax {
		t.Errorf("auto MaxWorkers = %d, want %d", mgr.pool.MaxWorkers, wantMax)
	}
	if mgr.pool.MinWorkers != 0 {
		t.Errorf("MinWorkers = %d, want 0 (no idle workers)", mgr.pool.MinWorkers)
	}

	ref := workerpool.NewPool(context.Background(), 0, 0, 10*time.Second)
	if mgr.pool.MaxWorkers != ref.MaxWorkers || mgr.pool.MinWorkers != ref.MinWorkers {
		t.Errorf("pool effective values %d/%d do not match workerpool.NewPool(0,0) values %d/%d",
			mgr.pool.MaxWorkers, mgr.pool.MinWorkers, ref.MaxWorkers, ref.MinWorkers)
	}
}

func TestSubsystemManager_Start_HonorsRunParameters(t *testing.T) {
	cfg := &config.Config{WorkerPoolMax: 0, WorkerPoolMinIdle: 0}
	mgr, _ := startTestManager(t, 3, 7, cfg)

	if mgr.pool.MinWorkers != 3 {
		t.Errorf("pool.MinWorkers = %d, want 3", mgr.pool.MinWorkers)
	}
	if mgr.pool.MaxWorkers != 7 {
		t.Errorf("pool.MaxWorkers = %d, want 7", mgr.pool.MaxWorkers)
	}
}

func TestSubsystemManager_Start_ConfigOverridesRunParameters(t *testing.T) {
	cfg := &config.Config{WorkerPoolMax: 11, WorkerPoolMinIdle: 5}
	mgr, _ := startTestManager(t, 3, 7, cfg)

	if mgr.pool.MinWorkers != 5 {
		t.Errorf("pool.MinWorkers = %d, want 5", mgr.pool.MinWorkers)
	}
	if mgr.pool.MaxWorkers != 11 {
		t.Errorf("pool.MaxWorkers = %d, want 11", mgr.pool.MaxWorkers)
	}
}

func TestSubsystemManager_Start_ZeroValuesAutoMax(t *testing.T) {
	mgr, _ := startTestManager(t, 0, 0, nil)

	_, wantMax := expectedAutoPoolWorkers()
	if mgr.pool.MinWorkers != 0 {
		t.Errorf("pool.MinWorkers = %d, want 0 (min 0 is no idle workers, not auto)", mgr.pool.MinWorkers)
	}
	if mgr.pool.MaxWorkers != wantMax {
		t.Errorf("pool.MaxWorkers = %d, want %d (auto)", mgr.pool.MaxWorkers, wantMax)
	}
}

func TestSubsystemManager_Start_CacheMWRoutes(t *testing.T) {
	cfg := &config.Config{
		EnableHTTPCache:    true,
		CacheMaxSize:       100 * 1024 * 1024,
		CacheMaxTime:       time.Hour,
		CacheMaxEntrySize:  1024 * 1024,
		EnableCachePreload: true,
	}
	mgr, app := startTestManager(t, 0, 0, cfg, withCacheMW(cfg))

	if app.cacheMW == nil {
		t.Fatal("cache middleware should be initialized")
	}
	wantRoutes := app.cacheMW.Config().CacheableRoutes
	gotRoutes := preloadManagerRoutes(t, mgr.preloadManager)
	if !reflect.DeepEqual(gotRoutes, wantRoutes) {
		t.Errorf("preloadManager routes = %v, want %v", gotRoutes, wantRoutes)
	}
}

func TestSubsystemManager_Start_CacheOnGalleryHitWired(t *testing.T) {
	cfg := &config.Config{
		EnableHTTPCache:    true,
		CacheMaxSize:       100 * 1024 * 1024,
		CacheMaxTime:       time.Hour,
		CacheMaxEntrySize:  1024 * 1024,
		EnableCachePreload: false,
	}
	mgr, app := startTestManager(t, 0, 0, cfg, withCacheMW(cfg))

	if app.cacheMW == nil {
		t.Fatal("cache middleware should be initialized")
	}
	if mgr.preloadManager == nil {
		t.Fatal("preloadManager should be initialized")
	}

	cacheCfg := app.cacheMW.Config()
	if cacheCfg.OnGalleryCacheHit == nil {
		t.Fatal("OnGalleryCacheHit callback should be wired")
	}
	cacheCfg.OnGalleryCacheHit(app.RuntimeManager.ctx, 1, "session-id")
}

func TestSubsystemManager_StartPool(t *testing.T) {
	cfg := &config.Config{
		WorkerPoolMax:     2,
		WorkerPoolMinIdle: 1,
	}
	mgr, app := startTestManager(t, 0, 0, cfg)

	done := make(chan struct{})
	mgr.StartPool(context.Background(), done, app.normalizedImagesDir, removeImagesDirPrefix, app.SubsystemManager.fileProcessor, nil)

	app.RuntimeManager.cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("poolDone was not closed after pool context was canceled")
	}
}

func TestSubsystemManager_StartPool_ProcessesFile(t *testing.T) {
	cfg := &config.Config{WorkerPoolMax: 2, WorkerPoolMinIdle: 1}
	app := CreateApp(t)
	fp := &recordingFileProcessor{}
	mgr := NewSubsystemManager(app.InfrastructureService)
	WithFileProcessor(fp)(mgr)

	mgr.Start(app.RuntimeManager.ctx, cfg, 1, 2, app.imagesDir, app.normalizedImagesDir, removeImagesDirPrefix, app.getRouter, app.GetHandlerQueries, app.GetETagVersion)
	defer mgr.Shutdown()

	done := make(chan struct{})
	mgr.StartPool(context.Background(), done, app.normalizedImagesDir, removeImagesDirPrefix, fp, nil)

	relPath := filepath.Join(app.normalizedImagesDir, "test.jpg")
	mgr.q.Enqueue(relPath)

	// Wait until the pool worker has processed the file. The queue can drain
	// before processing completes, so wait on the recorded result instead of
	// the queue length.
	waitForProcessedPaths(t, fp, 1)

	processed := fp.ProcessedPaths()
	if len(processed) != 1 || processed[0] != "test.jpg" {
		t.Errorf("processed paths = %v, want [test.jpg]", processed)
	}

	app.RuntimeManager.cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("poolDone was not closed after pool context was canceled")
	}
}

func TestSubsystemManager_StartPool_UsesProvidedPools(t *testing.T) {
	cfg := &config.Config{WorkerPoolMax: 2, WorkerPoolMinIdle: 1}
	app := CreateApp(t)
	fp := &recordingFileProcessor{}
	mgr := NewSubsystemManager(app.InfrastructureService)
	WithFileProcessor(fp)(mgr)

	mgr.Start(app.RuntimeManager.ctx, cfg, 1, 2, app.imagesDir, app.normalizedImagesDir, removeImagesDirPrefix, app.getRouter, app.GetHandlerQueries, app.GetETagVersion)
	defer mgr.Shutdown()

	done := make(chan struct{})
	mgr.StartPool(context.Background(), done, app.normalizedImagesDir, removeImagesDirPrefix, fp, nil)

	mgr.q.Enqueue(filepath.Join(app.normalizedImagesDir, "test.jpg"))

	// Wait until the file has been processed; this also guarantees the RO
	// connection was passed to ProcessFileWithConn.
	waitForProcessedPaths(t, fp, 1)

	if fp.ConnUsed() == nil {
		t.Fatal("expected a RO connection to be passed to ProcessFileWithConn")
	}
	var n int
	if err := fp.ConnUsed().Conn.QueryRowContext(context.Background(), "SELECT 1").Scan(&n); err != nil {
		t.Errorf("query on provided connection failed: %v", err)
	}

	app.RuntimeManager.cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("poolDone was not closed after pool context was canceled")
	}
}

func TestSubsystemManager_Shutdown(t *testing.T) {
	mgr, _ := startTestManager(t, 0, 0, nil)
	mgr.Shutdown()

	if mgr.preloadManager.GetScheduler() != nil {
		t.Error("preloadManager scheduler should be stopped after Shutdown")
	}
}

func TestSubsystemManager_StartCacheBatchLoad(t *testing.T) {
	t.Run("blocked when discovery active", func(t *testing.T) {
		mgr, app := startTestManager(t, 0, 0, nil)
		if err := mgr.moduleStateService.SetActive(app.RuntimeManager.ctx, "discovery", true); err != nil {
			t.Fatalf("SetActive failed: %v", err)
		}

		res, err := mgr.StartCacheBatchLoad(app.RuntimeManager.ctx)
		if err != nil {
			t.Fatalf("StartCacheBatchLoad error: %v", err)
		}
		if !res.Blocked {
			t.Error("expected Blocked=true")
		}
		if !strings.Contains(res.Message, "discovery active") {
			t.Errorf("unexpected message: %q", res.Message)
		}
	})

	t.Run("not available when manager is nil", func(t *testing.T) {
		mgr, app := startTestManager(t, 0, 0, nil)
		mgr.batchLoadManager = nil

		res, err := mgr.StartCacheBatchLoad(app.RuntimeManager.ctx)
		if err != nil {
			t.Fatalf("StartCacheBatchLoad error: %v", err)
		}
		if res.Blocked {
			t.Error("expected Blocked=false")
		}
		if !strings.Contains(res.Message, "not available") {
			t.Errorf("unexpected message: %q", res.Message)
		}
	})

	t.Run("starts run when discovery inactive", func(t *testing.T) {
		mgr, app := startTestManager(t, 0, 0, nil)

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
		mgr.batchLoadManager = cachebatch.NewManager(cachebatch.Config{
			GetQueries: func() (cachebatch.BatchLoadQueries, func()) {
				return fakeBatchQueries{}, nil
			},
			GetHandler:         func() http.Handler { return handler },
			GetETagVersion:     func() string { return "1" },
			ModuleStateService: mgr.moduleStateService,
		})

		res, err := mgr.StartCacheBatchLoad(app.RuntimeManager.ctx)
		if err != nil {
			t.Fatalf("StartCacheBatchLoad error: %v", err)
		}
		if res.Blocked {
			t.Error("expected Blocked=false")
		}
		if res.Message != "Cache batch load started" {
			t.Errorf("unexpected message: %q", res.Message)
		}

		deadline := time.Now().Add(2 * time.Second)
		for mgr.batchLoadManager.Metrics().Snapshot().LastStartedAt == 0 {
			if time.Now().After(deadline) {
				t.Fatal("batch load Run was not started")
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

func TestSubsystemManager_WireMetrics_WiresAllSources(t *testing.T) {
	cfg := &config.Config{
		EnableHTTPCache:    true,
		CacheMaxSize:       100 * 1024 * 1024,
		CacheMaxTime:       time.Hour,
		CacheMaxEntrySize:  1024 * 1024,
		EnableCachePreload: true,
	}
	mgr, _ := startTestManager(t, 0, 0, cfg, withCacheMW(cfg))
	collector := metrics.NewCollector()

	mgr.WireMetrics(collector)

	snap := collector.Collect(context.Background())
	if snap.WriteBatcher.MaxBatchSize == 0 {
		t.Error("WriteBatcher metrics not wired")
	}
	if snap.WorkerPool.MaxWorkers == 0 {
		t.Error("WorkerPool metrics not wired")
	}
	if !snap.CachePreload.IsEnabled {
		t.Error("CachePreload metrics not wired")
	}
	if !snap.HTTPCache.Enabled {
		t.Error("HTTPCache metrics not wired")
	}
	if snap.FileProcessing.TotalFound != 0 {
		t.Error("FileProcessor metrics not wired")
	}
}
