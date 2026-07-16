package server

import (
	"context"
	"errors"
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
func (p *recordingPreloadManager) ScheduleFolderPreload(ctx context.Context, folderID int64, sessionID, acceptEncoding string) {
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
	mgr.Shutdown() // should not panic
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
