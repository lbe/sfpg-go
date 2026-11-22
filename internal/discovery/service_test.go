package discovery

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/lbe/sfpg-go/internal/server/metrics"
)

// TestRunSecondConcurrentRunReturnsNil starts a run that holds in its drain,
// then calls Run again: the second call returns nil and leaves the counters
// the first run is using.
func TestRunSecondConcurrentRunReturnsNil(t *testing.T) {
	dir := t.TempDir()
	normalized := filepath.ToSlash(dir)
	db := openTestDatabase(t, normalized)
	w := &fakeWriter{}
	w.pending.Store(1)
	stats := &Stats{}
	svc := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: normalized}, Deps{
		Writer: w,
		State:  db.state,
		Stats:  stats,
	})

	synctest.Test(t, func(t *testing.T) {
		done := make(chan error, 1)
		go func() { done <- svc.Run(t.Context()) }()
		synctest.Wait() // the first run is now waiting for the writer

		stats.TotalFound.Store(42)
		if err := svc.Run(t.Context()); err != nil {
			t.Errorf("second Run = %v, want nil", err)
		}
		if got := stats.TotalFound.Load(); got != 42 {
			t.Errorf("TotalFound after second Run = %d, want 42", got)
		}

		w.pending.Store(0)
		if err := <-done; err != nil {
			t.Errorf("first Run = %v, want nil", err)
		}
	})
}

// TestRunCancelledDuringDrainSkipsPersistAndRebuild cancels a run while it
// waits for the writer. The run returns the context error, and neither saves
// its counters nor rebuilds the folder index.
func TestRunCancelledDuringDrainSkipsPersistAndRebuild(t *testing.T) {
	dir := t.TempDir()
	normalized := filepath.ToSlash(dir)
	db := openTestDatabase(t, normalized)
	prior := metrics.FileProcessingMetrics{TotalFound: 7, NewlyInserted: 7}
	if err := db.state.SaveFileProcessing(t.Context(), moduleName, prior); err != nil {
		t.Fatalf("SaveFileProcessing: %v", err)
	}
	w := &fakeWriter{}
	w.pending.Store(1)
	svc := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: normalized}, Deps{
		Writer: w,
		State:  db.state,
	})

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- svc.Run(ctx) }()
		synctest.Wait() // the run is waiting for the writer

		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want %v", err, context.Canceled)
		}
		if w.rebuilt {
			t.Error("folder index rebuilt after cancellation")
		}
		saved, err := db.state.LoadFileProcessing(t.Context(), moduleName)
		if err != nil {
			t.Fatalf("LoadFileProcessing: %v", err)
		}
		if saved != prior {
			t.Errorf("saved counters = %+v, want the earlier %+v", saved, prior)
		}
	})
}

func TestRunRefusesNonEmptyBacklog(t *testing.T) {
	dir := t.TempDir()
	normalized := filepath.ToSlash(dir)
	db := openTestDatabase(t, normalized)
	w := &fakeWriter{}
	stats := &Stats{}
	stats.TotalFound.Store(9) // left from the previous run
	svc := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: normalized}, Deps{
		Writer: w,
		State:  db.state,
		Stats:  stats,
	})
	if err := svc.backlog.enqueue(PathWork{Path: []byte(filepath.Join(dir, "left.jpg")), MtimeUnix: 1, SizeBytes: 1}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	err := svc.Run(t.Context())
	if err == nil || !strings.Contains(err.Error(), "discovery walk refused") {
		t.Fatalf("Run with a backlog = %v, want the walk refusal", err)
	}
	if n := stats.TotalFound.Load(); n != 0 {
		t.Errorf("TotalFound = %d after a refused run, want the counters reset", n)
	}
	if got := svc.QueueLen(); got != 1 {
		t.Errorf("QueueLen() = %d, want the leftover item kept", got)
	}
	if w.rebuilt {
		t.Error("folder index rebuilt by a refused run")
	}
}

// TestRunRebuildErrorIsReturnedAfterCountersSaved checks that a failed
// rebuild is the run's error, and that the counters were saved before it.
func TestRunRebuildErrorIsReturnedAfterCountersSaved(t *testing.T) {
	dir := t.TempDir()
	normalized := filepath.ToSlash(dir)
	db := openTestDatabase(t, normalized)
	if err := db.state.SaveFileProcessing(t.Context(), moduleName, metrics.FileProcessingMetrics{TotalFound: 7}); err != nil {
		t.Fatalf("SaveFileProcessing: %v", err)
	}
	rebuildErr := errors.New("rebuild failed")
	w := &fakeWriter{rebuildErr: rebuildErr}
	svc := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: normalized}, Deps{
		Writer: w,
		State:  db.state,
	})

	if err := svc.Run(t.Context()); !errors.Is(err, rebuildErr) {
		t.Fatalf("Run = %v, want %v", err, rebuildErr)
	}
	if !w.rebuilt {
		t.Error("folder index not rebuilt")
	}
	saved, err := db.state.LoadFileProcessing(t.Context(), moduleName)
	if err != nil {
		t.Fatalf("LoadFileProcessing: %v", err)
	}
	if saved != (metrics.FileProcessingMetrics{}) {
		t.Errorf("saved counters = %+v, want the empty run's zeros", saved)
	}
}

func TestHydrateLoadsSavedCounters(t *testing.T) {
	dir := t.TempDir()
	normalized := filepath.ToSlash(dir)
	db := openTestDatabase(t, normalized)
	saved := metrics.FileProcessingMetrics{TotalFound: 5, AlreadyExisting: 2, NewlyInserted: 2, SkippedInvalid: 1}
	if err := db.state.SaveFileProcessing(t.Context(), moduleName, saved); err != nil {
		t.Fatalf("SaveFileProcessing: %v", err)
	}
	svc := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: normalized}, Deps{State: db.state})

	svc.Hydrate(t.Context())
	if got := svc.deps.Stats.GetStats(); got != saved {
		t.Errorf("counters after Hydrate = %+v, want %+v", got, saved)
	}
}

func TestIdleAndQueueLen(t *testing.T) {
	dir := t.TempDir()
	normalized := filepath.ToSlash(dir)
	db := openTestDatabase(t, normalized)
	svc := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: normalized}, Deps{
		Writer: &fakeWriter{},
		State:  db.state,
	})

	if !svc.Idle() {
		t.Error("Idle() = false on a new service, want true")
	}
	if got := svc.QueueLen(); got != 0 {
		t.Errorf("QueueLen() on a new service = %d, want 0", got)
	}
	if err := svc.backlog.enqueue(PathWork{Path: []byte("/x.jpg"), MtimeUnix: 1, SizeBytes: 1}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if got := svc.QueueLen(); got != 1 {
		t.Errorf("QueueLen() = %d, want 1", got)
	}
	if svc.Idle() {
		t.Error("Idle() = true with a queued file, want false")
	}
}

// TestWaitWaitsForRun holds a run in its folder-index rebuild: Wait does not
// return until the run ends, so shutdown does not close the pools under it.
func TestWaitWaitsForRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		normalized := filepath.ToSlash(dir)
		db := openTestDatabase(t, normalized)
		release := make(chan struct{})
		w := &blockingRebuildWriter{fakeWriter: &fakeWriter{}, release: release}
		svc := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: normalized}, Deps{Writer: w, State: db.state})

		runDone := make(chan error, 1)
		go func() { runDone <- svc.Run(t.Context()) }()
		synctest.Wait() // the run is held in the rebuild

		waited := make(chan struct{})
		go func() {
			svc.Wait()
			close(waited)
		}()
		synctest.Wait()
		select {
		case <-waited:
			t.Fatal("Wait returned while a run was in progress")
		default:
		}

		close(release)
		<-waited
		if err := <-runDone; err != nil {
			t.Errorf("Run: %v", err)
		}
		if err := svc.Run(t.Context()); err != nil {
			t.Errorf("Run after Wait = %v, want nil: no new run is admitted", err)
		}
	})
}

// blockingRebuildWriter holds RebuildFolderIndex until release is closed.
type blockingRebuildWriter struct {
	*fakeWriter
	release chan struct{}
}

func (w *blockingRebuildWriter) RebuildFolderIndex(ctx context.Context) error {
	<-w.release
	return w.fakeWriter.RebuildFolderIndex(ctx)
}
