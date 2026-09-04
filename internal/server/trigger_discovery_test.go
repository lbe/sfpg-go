package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/server/modulestate"
)

// TestTriggerDiscovery_PersistsFileProcessingAfterDrain verifies P4 and Task 5
// together: a TriggerDiscovery whose drain completes writes the run's own
// counters to module_state.payload["discovery"]["file_processing"] so a
// skip-startup-discovery restart can hydrate them, and the counters are the
// post-reset totals — a seeded last-run never leaks into the persisted payload.
// CreateApp leaves processingStats and moduleStateService nil, so the persist
// must first be a no-op (no panic, nothing written); the allocated stats then
// drive the production walk/drain/persist path.
func TestTriggerDiscovery_PersistsFileProcessingAfterDrain(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Nil stats + nil service: persist after a nil drain must be a no-op and
	// must not call GetStats() on a nil pointer.
	if err := app.persistFileProcessingStats(context.Background()); err != nil {
		t.Fatalf("nil-stats persist should be a no-op, got error: %v", err)
	}

	app.SubsystemManager.processingStats = &files.ProcessingStats{}
	app.SubsystemManager.moduleStateService = modulestate.NewService(app.dbRwPool)

	// Simulate a hydrated last-run (Task 4) that a manual run then triggers:
	// Task 5's ResetStats after the successful CAS must zero these before the
	// walk/drain/persist, so the persisted payload holds this run's own totals
	// (0 on the empty Images/ dir) and never the last-run seed. The seed is also
	// what GetStats() at persist time would have written without the reset.
	app.SubsystemManager.processingStats.TotalFound.Store(15666608)
	app.SubsystemManager.processingStats.AlreadyExisting.Store(15620677)
	app.SubsystemManager.processingStats.NewlyInserted.Store(40000)
	app.SubsystemManager.processingStats.SkippedInvalid.Store(5931)

	var rebuildCalls atomic.Int32
	app.testSeams.RebuildFileFolderIndex = func(context.Context, *dbconnpool.DbSQLConnPool) error {
		rebuildCalls.Add(1)
		return nil
	}

	// TriggerDiscovery must remain nil — the production walk/drain/rebuild body
	// runs on the empty Images/ dir and then persists after the drain.
	if err := app.TriggerDiscovery(context.Background()); err != nil {
		t.Fatalf("TriggerDiscovery: %v", err)
	}
	if rebuildCalls.Load() != 1 {
		t.Errorf("expected 1 rebuild call, got %d", rebuildCalls.Load())
	}

	fp, err := app.SubsystemManager.moduleStateService.LoadFileProcessing(context.Background(), "discovery")
	if err != nil {
		t.Fatalf("LoadFileProcessing: %v", err)
	}
	// P4: persist-after-drain writes a payload. P6/Task 5: the last-run seed was
	// reset by TriggerDiscovery's CAS win, so none of it leaks into the payload
	// — a manual run never persists last-run + this-run.
	if fp.TotalFound != 0 {
		t.Errorf("TotalFound = %d, want 0 (last-run seed must not leak after CAS reset)", fp.TotalFound)
	}
	if fp.AlreadyExisting != 0 {
		t.Errorf("AlreadyExisting = %d, want 0 (last-run seed must not leak after CAS reset)", fp.AlreadyExisting)
	}
	if fp.NewlyInserted != 0 {
		t.Errorf("NewlyInserted = %d, want 0 (last-run seed must not leak after CAS reset)", fp.NewlyInserted)
	}
	if fp.SkippedInvalid != 0 {
		t.Errorf("SkippedInvalid = %d, want 0 (last-run seed must not leak after CAS reset)", fp.SkippedInvalid)
	}
	if fp.InFlight != 0 {
		t.Errorf("InFlight = %d, want 0 (live state is not persisted)", fp.InFlight)
	}
}

// TestTriggerDiscovery_InFlightDoesNotResetStats verifies P6: when discovery
// is already in flight, TriggerDiscovery's failed CAS returns without calling
// ResetStats — the live counters and the persisted payload stay unchanged.
// CreateApp leaves processingStats nil and ResetStats() nil-guards, so this
// test must allocate the stats pointer before Store (a Store on the nil
// CreateApp pointer would panic before the CAS/reset behavior is exercised).
func TestTriggerDiscovery_InFlightDoesNotResetStats(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.SubsystemManager.processingStats = &files.ProcessingStats{}
	app.SubsystemManager.processingStats.TotalFound.Store(99)

	// Seed a persisted payload so "payload unchanged if present" holds: the
	// failed CAS returns before any persist-after-drain can run.
	app.SubsystemManager.moduleStateService = modulestate.NewService(app.dbRwPool)
	seed := metrics.FileProcessingMetrics{TotalFound: 77, AlreadyExisting: 66, NewlyInserted: 11, SkippedInvalid: 2}
	if err := app.SubsystemManager.moduleStateService.SaveFileProcessing(context.Background(), "discovery", seed); err != nil {
		t.Fatalf("SaveFileProcessing: %v", err)
	}

	app.discoveryRunning.Store(true)

	if err := app.TriggerDiscovery(context.Background()); err != nil {
		t.Fatalf("TriggerDiscovery with discovery already in flight: %v", err)
	}

	if got := app.SubsystemManager.processingStats.TotalFound.Load(); got != 99 {
		t.Errorf("TotalFound = %d, want 99 (in-flight CAS must not reset)", got)
	}
	// Failed CAS returns before the deferred Store(false) runs (server.go).
	if !app.discoveryRunning.Load() {
		t.Error("discoveryRunning should remain true: failed CAS does not run the defer")
	}
	fp, err := app.SubsystemManager.moduleStateService.LoadFileProcessing(context.Background(), "discovery")
	if err != nil {
		t.Fatalf("LoadFileProcessing: %v", err)
	}
	if fp.TotalFound != 77 || fp.AlreadyExisting != 66 || fp.NewlyInserted != 11 || fp.SkippedInvalid != 2 {
		t.Errorf("persisted payload changed by in-flight TriggerDiscovery: %+v", fp)
	}

	// Clear the test-primed flag before the deferred Shutdown: it polls
	// discoveryRunning with no timeout (app_lifecycle.go), and the failed CAS
	// returns before the defer that would Store(false).
	app.discoveryRunning.Store(false)
}

// TestTriggerDiscovery_ResetsStatsWhenStarting verifies P6: when
// TriggerDiscovery wins the in-flight CAS, it immediately resets
// processingStats so a starting run does not add onto hydrated last-run
// counters. The reset must live before the testSeams.TriggerDiscovery check,
// so this test uses the seam — the walk is not the proof. CreateApp leaves
// processingStats nil; allocate before Store.
func TestTriggerDiscovery_ResetsStatsWhenStarting(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.SubsystemManager.processingStats = &files.ProcessingStats{}
	app.SubsystemManager.processingStats.TotalFound.Store(5)

	// No-op stub that returns nil immediately. The reset must already have run
	// when this stub fires (app.ResetStats() precedes the seam check).
	app.testSeams.TriggerDiscovery = func(context.Context) error { return nil }

	if err := app.TriggerDiscovery(context.Background()); err != nil {
		t.Fatalf("TriggerDiscovery: %v", err)
	}
	if got := app.SubsystemManager.processingStats.TotalFound.Load(); got != 0 {
		t.Errorf("TotalFound = %d, want 0 (successful CAS must reset before start)", got)
	}
}

func TestTriggerDiscovery_RebuildOnceAfterDrain(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	var rebuildCalls atomic.Int32
	app.testSeams.RebuildFileFolderIndex = func(context.Context, *dbconnpool.DbSQLConnPool) error {
		rebuildCalls.Add(1)
		return nil
	}

	// TriggerDiscovery must remain nil — exercise production walk/drain/rebuild path.
	// WalkImageDir runs on empty Images/; walk and drain return immediately.
	// RebuildFileFolderIndex is then called once.
	if err := app.TriggerDiscovery(context.Background()); err != nil {
		t.Fatalf("TriggerDiscovery: %v", err)
	}
	if rebuildCalls.Load() != 1 {
		t.Errorf("expected 1 rebuild call, got %d", rebuildCalls.Load())
	}
}

func TestTriggerDiscovery_ReentrancySkipsSecondCall(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	blocker := make(chan struct{})
	var rebuildCalls atomic.Int32
	app.testSeams.RebuildFileFolderIndex = func(context.Context, *dbconnpool.DbSQLConnPool) error {
		rebuildCalls.Add(1)
		<-blocker // block until released
		return nil
	}
	// TriggerDiscovery must remain nil — production walk/drain body runs.

	// First call blocks inside the rebuild seam.
	go func() {
		_ = app.TriggerDiscovery(context.Background())
	}()

	// Wait for discoveryRunning to be set to true.
	for !app.discoveryRunning.Load() {
	}

	// Second call must return nil immediately — CAS guard prevents re-entry.
	if err := app.TriggerDiscovery(context.Background()); err != nil {
		t.Errorf("second TriggerDiscovery returned error: %v", err)
	}

	// Release the first call.
	close(blocker)

	// Wait for first call to finish.
	for app.discoveryRunning.Load() {
		time.Sleep(10 * time.Millisecond)
	}

	if rebuildCalls.Load() != 1 {
		t.Errorf("expected 1 rebuild call, got %d", rebuildCalls.Load())
	}
	if app.discoveryRunning.Load() {
		t.Error("discoveryRunning should be false after both calls complete")
	}
}

func TestTriggerDiscovery_ShutdownDuringDrainSkipsRebuild(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	var rebuildCalls atomic.Int32
	app.testSeams.RebuildFileFolderIndex = func(context.Context, *dbconnpool.DbSQLConnPool) error {
		rebuildCalls.Add(1)
		return nil
	}

	// processingStats must be non-nil so waitForFileProcessingDrain does not
	// short-circuit. CreateApp sets q and fileProcessor but not processingStats.
	app.SubsystemManager.processingStats = &files.ProcessingStats{}

	// Prevent drain from completing: active sender blocks the poll loop.
	app.SubsystemManager.qSendersActive.Store(1)

	ctx := app.getCtx()
	errCh := make(chan error, 1)
	go func() {
		errCh <- app.TriggerDiscovery(ctx)
	}()

	// Cancel after discovery enters the drain wait.
	app.RuntimeManager.cancel()

	err := <-errCh
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
	if rebuildCalls.Load() != 0 {
		t.Errorf("expected 0 rebuild calls after shutdown, got %d", rebuildCalls.Load())
	}
	if app.discoveryRunning.Load() {
		t.Error("discoveryRunning should be false after return")
	}
}

// TestTriggerDiscovery_DoesNotRequestRestartWhenFlagOn is the POST
// /server/discovery invariant: TriggerDiscovery itself never auto-restarts.
// Startup restart is the Run() wrapper around the automatic walk only.
func TestTriggerDiscovery_DoesNotRequestRestartWhenFlagOn(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.RestartAfterDiscovery = true
	app.ConfigManager.ConfigMu.Unlock()

	app.testSeams.RebuildFileFolderIndex = func(context.Context, *dbconnpool.DbSQLConnPool) error {
		return nil
	}

	if err := app.TriggerDiscovery(context.Background()); err != nil {
		t.Fatalf("TriggerDiscovery: %v", err)
	}
	if app.IsRestartRequested() {
		t.Error("TriggerDiscovery must not request restart; that is startup-only")
	}
}

func TestTriggerDiscovery_RebuildErrorIsReturned(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.testSeams.RebuildFileFolderIndex = func(context.Context, *dbconnpool.DbSQLConnPool) error {
		return files.ErrFolderIndexRebuild
	}

	if err := app.TriggerDiscovery(context.Background()); err == nil {
		t.Fatal("expected TriggerDiscovery to return the rebuild error")
	} else if !errors.Is(err, files.ErrFolderIndexRebuild) {
		t.Errorf("expected errors.Is(err, files.ErrFolderIndexRebuild), got %v", err)
	}
	if app.IsRestartRequested() {
		t.Error("TriggerDiscovery must not request restart on rebuild failure")
	}
}

func TestTriggerDiscovery_NilPoolSkipsRebuild(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	var rebuildCalls atomic.Int32
	app.testSeams.RebuildFileFolderIndex = func(context.Context, *dbconnpool.DbSQLConnPool) error {
		rebuildCalls.Add(1)
		return nil
	}

	app.dbRwPool = nil

	if err := app.TriggerDiscovery(context.Background()); err != nil {
		t.Fatalf("TriggerDiscovery with nil pool: %v", err)
	}
	if rebuildCalls.Load() != 0 {
		t.Errorf("expected 0 rebuild calls with nil pool, got %d", rebuildCalls.Load())
	}
}
