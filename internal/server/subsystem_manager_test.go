package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/server/modulestate"
)

// recordingFileProcessor is a test fake that records ProcessFileWithConn calls.
type recordingFileProcessor struct {
	mu             sync.Mutex
	processedPaths []string
	connUsed       *dbconnpool.CpConn
	closed         bool
	events         *[]string
}

func (f *recordingFileProcessor) ProcessFile(ctx context.Context, path string) (*files.File, error) {
	return nil, errors.New("ProcessFile should not be called by the pool")
}

func (f *recordingFileProcessor) ProcessFileWithConn(ctx context.Context, path string, cpcRo *dbconnpool.CpConn) (*files.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.processedPaths = append(f.processedPaths, path)
	f.connUsed = cpcRo
	return &files.File{Path: path, Ok: true, Exists: false}, nil
}

func (f *recordingFileProcessor) CheckIfModified(ctx context.Context, path string) (bool, error) {
	return false, nil
}

func (f *recordingFileProcessor) GenerateThumbnail(ctx context.Context, file *files.File) error {
	return nil
}

func (f *recordingFileProcessor) RecordInvalidFile(ctx context.Context, path string, mtime, size int64, reason string) error {
	return nil
}

func (f *recordingFileProcessor) SubmitFileForWrite(file *files.File) error {
	return nil
}

func (f *recordingFileProcessor) PendingWriteCount() int64 {
	return 0
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

func (f *recordingFileProcessor) ConnUsed() *dbconnpool.CpConn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connUsed
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
	mgr.Start(ctx, cfg, 1, 2, "", "", nil, nil, nil, nil)
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
// discards a previous run's discovery backlog: a seeded dque with items plus a
// stray file in the queue directory are gone after wipe+reopen, and the new
// queue is usable for fresh discovery.
func TestSubsystemManager_Start_WipesPreviousDiscoveryBacklog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "discovery-dque")

	// Seed a previous run's backlog: a real dque containing an item plus a
	// stray file in the queue directory.
	seed, seedErr := newDiscoveryDQueAdapter(dir)
	if seedErr != nil {
		t.Fatalf("seed adapter: %v", seedErr)
	}
	if err := seed.Enqueue("stale-path.jpg"); err != nil {
		t.Fatalf("seed enqueue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "leftover.txt"), []byte("junk"), 0o644); err != nil {
		t.Fatalf("seed stray file: %v", err)
	}
	seed.Close() // release the flock so Start can wipe and reopen

	mgr := newUnitSubsystemManager(t)
	// Reuse the seeded directory as the discovery queue directory.
	mgr.infra.discoveryDQueDirPath = dir
	WithFileProcessor(&recordingFileProcessor{})(mgr)
	WithPreloadManager(&recordingPreloadManager{})(mgr)

	startUnitSubsystemManager(t, mgr, nil)

	// Old queue contents are gone: the queue opens empty and the stray file
	// was removed by the wipe.
	if got := mgr.q.Len(); got != 0 {
		t.Errorf("queue Len after wipe = %d, want 0 (previous backlog discarded)", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "leftover.txt")); !os.IsNotExist(err) {
		t.Errorf("leftover file should be wiped; stat err = %v", err)
	}

	// The fresh queue is usable for new discovery.
	if err := mgr.q.Enqueue("fresh.jpg"); err != nil {
		t.Fatalf("enqueue after wipe: %v", err)
	}
	got, err := mgr.q.Dequeue()
	if err != nil {
		t.Fatalf("dequeue after wipe: %v", err)
	}
	if got != "fresh.jpg" {
		t.Errorf("dequeued %q, want %q", got, "fresh.jpg")
	}
}

// TestSubsystemManager_Start_DoubleStart verifies Start re-entry: when a
// discovery queue is already open, a second Start closes it first, then wipes
// and reopens (once-per-process lifetime for the discovery dque).
func TestSubsystemManager_Start_DoubleStart(t *testing.T) {
	mgr := newUnitSubsystemManager(t)
	WithFileProcessor(&recordingFileProcessor{})(mgr)
	WithPreloadManager(&recordingPreloadManager{})(mgr)

	startUnitSubsystemManager(t, mgr, nil)
	if mgr.q == nil {
		t.Fatal("queue should be initialized after first Start")
	}
	if err := mgr.q.Enqueue("first.jpg"); err != nil {
		t.Fatalf("enqueue after first Start: %v", err)
	}

	startUnitSubsystemManager(t, mgr, nil) // must close the open queue before wipe

	if mgr.q == nil {
		t.Fatal("queue should be re-initialized after second Start")
	}
	// Wipe-on-start: items from the first run are discarded.
	if got := mgr.q.Len(); got != 0 {
		t.Errorf("queue Len after second Start = %d, want 0", got)
	}
	if err := mgr.q.Enqueue("second.jpg"); err != nil {
		t.Fatalf("enqueue after second Start: %v", err)
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
			mgr.Start(context.Background(), nil, 1, 2, "", "", nil, nil, nil, nil)
		})
	}
}

// TestSubsystemManager_ResetStats verifies ResetStats resets all counters.
func TestSubsystemManager_ResetStats(t *testing.T) {
	mgr := newUnitSubsystemManager(t)

	// Wire processing stats directly (no Start needed).
	mgr.processingStats = &files.ProcessingStats{}
	mgr.processingStats.TotalFound.Store(5)
	mgr.processingStats.AlreadyExisting.Store(4)
	mgr.processingStats.NewlyInserted.Store(3)
	mgr.processingStats.SkippedInvalid.Store(2)
	mgr.processingStats.InFlight.Store(1)

	mgr.ResetStats()

	if mgr.processingStats.TotalFound.Load() != 0 {
		t.Errorf("TotalFound = %d, want 0", mgr.processingStats.TotalFound.Load())
	}
	if mgr.processingStats.AlreadyExisting.Load() != 0 {
		t.Errorf("AlreadyExisting = %d, want 0", mgr.processingStats.AlreadyExisting.Load())
	}
	if mgr.processingStats.NewlyInserted.Load() != 0 {
		t.Errorf("NewlyInserted = %d, want 0", mgr.processingStats.NewlyInserted.Load())
	}
	if mgr.processingStats.SkippedInvalid.Load() != 0 {
		t.Errorf("SkippedInvalid = %d, want 0", mgr.processingStats.SkippedInvalid.Load())
	}
	if mgr.processingStats.InFlight.Load() != 0 {
		t.Errorf("InFlight = %d, want 0", mgr.processingStats.InFlight.Load())
	}
}

// TestSubsystemManager_ResetStats_Nil verifies ResetStats doesn't panic when stats are nil.
func TestSubsystemManager_ResetStats_Nil(t *testing.T) {
	mgr := newUnitSubsystemManager(t)
	mgr.ResetStats() // should not panic
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
