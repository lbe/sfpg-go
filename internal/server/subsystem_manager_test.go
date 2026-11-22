package server

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/server/modulestate"
)

// recordingFileProcessor is a test fake that records ProcessDiscoveryFile calls.
type recordingFileProcessor struct {
	mu             sync.Mutex
	processedPaths []string
	closed         bool
	events         *[]string
}

func (f *recordingFileProcessor) ProcessDiscoveryFile(_ context.Context, path string, _, _ int64) (*files.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.processedPaths = append(f.processedPaths, path)
	return &files.File{Path: path}, nil
}

func (f *recordingFileProcessor) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	if f.events != nil {
		*f.events = append(*f.events, "fileProcessor")
	}
	return nil
}

func (f *recordingFileProcessor) ProcessedPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.processedPaths))
	copy(out, f.processedPaths)
	return out
}

// recordingPreloadManager is a test fake that records shutdown calls.
type recordingPreloadManager struct {
	mu            sync.Mutex
	shutdownCalls int
	events        *[]string
}

func (p *recordingPreloadManager) Configure(cfg cachepreload.PreloadConfig) {}
func (p *recordingPreloadManager) IsEnabled() bool                          { return false }
func (p *recordingPreloadManager) SetEnabled(enabled bool)                  {}
func (p *recordingPreloadManager) GetScheduler() *scheduler.Scheduler       { return nil }
func (p *recordingPreloadManager) ScheduleFolderPreload(ctx context.Context, folderID int64, sessionID string) {
}
func (p *recordingPreloadManager) GetMetrics() cachepreload.PreloadMetricsSnapshot {
	return cachepreload.PreloadMetricsSnapshot{}
}

func (p *recordingPreloadManager) Shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shutdownCalls++
	if p.events != nil {
		*p.events = append(*p.events, "preload")
	}
}

// WithFileProcessor injects a fake file processor into a SubsystemManager.
func WithFileProcessor(fp files.FileProcessor) func(*SubsystemManager) {
	return func(m *SubsystemManager) { m.fileProcessor = fp }
}

// WithPreloadManager injects a fake preload manager into a SubsystemManager.
func WithPreloadManager(pm preloadManager) func(*SubsystemManager) {
	return func(m *SubsystemManager) { m.preloadManager = pm }
}

// newUnitSubsystemManager creates a SubsystemManager with no real database.
// Inject mock fileProcessor and preloadManager before use.
func newUnitSubsystemManager(t *testing.T) *SubsystemManager {
	t.Helper()
	infra := NewInfrastructureService()
	// No SetupDB in unit tests: point the discovery dque at a dedicated
	// subdirectory (never the bare temp dir, which is the RemoveAll wipe root).
	infra.discoveryDQueDirPath = filepath.Join(t.TempDir(), "discovery-dque")
	return NewSubsystemManager(infra)
}

// startUnitSubsystemManager calls Start on a SubsystemManager built from mock
// deps. The SubsystemManager must have had its fileProcessor and preloadManager
// injected via WithFileProcessor/WithPreloadManager before this call.
func startUnitSubsystemManager(t *testing.T, mgr *SubsystemManager, cfg *config.Config) {
	t.Helper()
	ctx := context.Background()
	mgr.Start(ctx, cfg, 1, 2, "", "", nil, nil, nil, nil, nil, nil)
}

// TestSubsystemManager_Shutdown_NilFields verifies Shutdown doesn't panic with nil fields.
func TestSubsystemManager_Shutdown_NilFields(t *testing.T) {
	mgr := newUnitSubsystemManager(t)
	mgr.Shutdown() // should not panic
}

// TestSubsystemManager_Shutdown_CallsSubsystems verifies that Shutdown calls
// preloadManager.Shutdown and fileProcessor.Close.
func TestSubsystemManager_Shutdown_CallsSubsystems(t *testing.T) {
	fp := &recordingFileProcessor{}
	pm := &recordingPreloadManager{}
	mgr := newUnitSubsystemManager(t)
	WithFileProcessor(fp)(mgr)
	WithPreloadManager(pm)(mgr)

	startUnitSubsystemManager(t, mgr, nil)
	mgr.Shutdown()

	if pm.shutdownCalls != 1 {
		t.Errorf("preloadManager shutdown calls = %d, want 1", pm.shutdownCalls)
	}
	if !fp.closed {
		t.Error("fileProcessor should be closed after Shutdown")
	}
}

// TestSubsystemManager_Shutdown_Order verifies shutdown order: preload first, then fileProcessor.
func TestSubsystemManager_Shutdown_Order(t *testing.T) {
	events := &[]string{}
	fp := &recordingFileProcessor{events: events}
	pm := &recordingPreloadManager{events: events}
	mgr := newUnitSubsystemManager(t)
	WithFileProcessor(fp)(mgr)
	WithPreloadManager(pm)(mgr)

	startUnitSubsystemManager(t, mgr, nil)
	mgr.Shutdown()

	want := []string{"preload", "fileProcessor"}
	if len(*events) != len(want) {
		t.Fatalf("shutdown order = %v, want %v", *events, want)
	}
	for i := range *events {
		if (*events)[i] != want[i] {
			t.Errorf("shutdown event %d = %q, want %q", i, (*events)[i], want[i])
		}
	}
}

// TestSubsystemManager_Shutdown_Idempotent verifies that calling Shutdown twice
// does not panic.
func TestSubsystemManager_Shutdown_Idempotent(t *testing.T) {
	mgr := newUnitSubsystemManager(t)
	WithFileProcessor(&recordingFileProcessor{})(mgr)
	WithPreloadManager(&recordingPreloadManager{})(mgr)

	startUnitSubsystemManager(t, mgr, nil)
	mgr.Shutdown()
	mgr.Shutdown() // should not panic (adapter Close is idempotent)
}

// TestSubsystemManager_Start_WipesPreviousDiscoveryBacklog verifies that Start
// wipes the discovery backlog directory: a stray file left there is gone after
// Start, which opens a fresh backlog.
func TestSubsystemManager_Start_WipesPreviousDiscoveryBacklog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "discovery-dque")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "leftover.txt"), []byte("junk"), 0o644); err != nil {
		t.Fatalf("seed stray file: %v", err)
	}

	mgr := newUnitSubsystemManager(t)
	mgr.infra.discoveryDQueDirPath = dir
	WithFileProcessor(&recordingFileProcessor{})(mgr)
	WithPreloadManager(&recordingPreloadManager{})(mgr)

	startUnitSubsystemManager(t, mgr, nil)

	if got := mgr.discovery.QueueLen(); got != 0 {
		t.Errorf("QueueLen after Start = %d, want 0", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "leftover.txt")); !os.IsNotExist(err) {
		t.Errorf("leftover file should be wiped; stat err = %v", err)
	}
}

// TestSubsystemManager_Start_ReopensBacklog verifies a second Start closes the
// open discovery backlog before it reopens the directory; otherwise the
// backlog's lock would make the second Start fail.
func TestSubsystemManager_Start_ReopensBacklog(t *testing.T) {
	mgr := newUnitSubsystemManager(t)
	WithFileProcessor(&recordingFileProcessor{})(mgr)
	WithPreloadManager(&recordingPreloadManager{})(mgr)

	startUnitSubsystemManager(t, mgr, nil)
	first := mgr.discovery

	startUnitSubsystemManager(t, mgr, nil)

	if mgr.discovery == nil || mgr.discovery == first {
		t.Fatal("second Start should replace the discovery Service")
	}
	if got := mgr.discovery.QueueLen(); got != 0 {
		t.Errorf("QueueLen after second Start = %d, want 0", got)
	}
}

// TestSubsystemManager_Start_PanicsOnEmptyDiscoveryDQueDirPath verifies Start
// fail-fasts before wiping when the discovery queue directory path is empty or
// whitespace-only (no soft-skip leaving m.q nil).
func TestSubsystemManager_Start_PanicsOnEmptyDiscoveryDQueDirPath(t *testing.T) {
	for _, path := range []string{"", "   "} {
		t.Run("path="+strconv.Quote(path), func(t *testing.T) {
			infra := NewInfrastructureService()
			infra.discoveryDQueDirPath = path
			mgr := NewSubsystemManager(infra)

			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("expected panic on empty discovery dque path")
				}
				if r != "main" {
					t.Errorf("panic value = %v, want %q", r, "main")
				}
			}()
			mgr.Start(context.Background(), nil, 1, 2, "", "", nil, nil, nil, nil, nil, nil)
		})
	}
}

// TestSubsystemManager_SetPreloadEnabled verifies SetPreloadEnabled works.
func TestSubsystemManager_SetPreloadEnabled(t *testing.T) {
	pm := &recordingPreloadManager{}
	mgr := newUnitSubsystemManager(t)
	WithPreloadManager(pm)(mgr)

	startUnitSubsystemManager(t, mgr, &config.Config{EnableCachePreload: true})

	// SetPreloadEnabled calls SetEnabled on the preloadManager.
	// With a mock, we just verify no panic; the recording preloadManager
	// tracks shutdown calls, not SetEnabled calls.
	mgr.SetPreloadEnabled(false)
	mgr.SetPreloadEnabled(true)
}

// TestSubsystemManager_SetPreloadEnabled_Nil verifies SetPreloadEnabled doesn't
// panic when preloadManager is nil.
func TestSubsystemManager_SetPreloadEnabled_Nil(t *testing.T) {
	mgr := newUnitSubsystemManager(t)
	mgr.SetPreloadEnabled(true) // should not panic
}

// TestSubsystemManager_WireMetrics_SkipsNilFields verifies WireMetrics handles
// nil infra components without panicking.
func TestSubsystemManager_WireMetrics_SkipsNilFields(t *testing.T) {
	mgr := newUnitSubsystemManager(t)
	collector := metrics.NewCollector()

	mgr.WireMetrics(collector)

	snap := collector.Collect(context.Background())
	if snap.WriteBatcher.MaxBatchSize != 0 || snap.WorkerPool.MaxWorkers != 0 {
		t.Error("expected empty metrics when sources are nil")
	}
}

// TestSubsystemManager_StartCacheBatchLoad_NotAvailable exercises the not-available
// branch of StartCacheBatchLoad without needing a live batch-load manager.
func TestSubsystemManager_StartCacheBatchLoad_NotAvailable(t *testing.T) {
	app := newAppForUnlock(t)
	mgr := NewSubsystemManager(app.InfrastructureService)
	mgr.moduleStateService = modulestate.NewService(app.dbRwPool)

	res, err := mgr.StartCacheBatchLoad(context.Background())
	if err != nil {
		t.Fatalf("StartCacheBatchLoad: %v", err)
	}
	if res.Blocked {
		t.Error("expected not blocked when discovery is inactive")
	}
	if res.Message != "Cache batch load not available" {
		t.Errorf("message = %q, want %q", res.Message, "Cache batch load not available")
	}
}
