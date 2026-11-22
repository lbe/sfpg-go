package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
)

// startBlockedDiscovery starts a discovery run that cannot finish on its own:
// the Images directory holds one image and no worker pool runs, so the queued
// file never drains until the app context is canceled. It returns once the
// walk has queued the image; the returned channel closes when the run returns.
func startBlockedDiscovery(t *testing.T, app *App) <-chan struct{} {
	t.Helper()
	if err := os.WriteFile(filepath.Join(app.imagesDir, "blocked.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = app.TriggerDiscovery(context.Background())
	}()
	// No pool runs, so the walk queues the image and the run waits for it;
	// the go test timeout catches a walk that never queues it.
	for app.SubsystemManager.discovery.QueueLen() == 0 {
		runtime.Gosched()
	}
	return done
}

// Without a discovery Service (Start not called), nothing is running.
func TestApp_fileProcessingQuiet_NoDiscoveryService(t *testing.T) {
	app := New(getopt.Opt{}, "test")
	if !app.fileProcessingQuiet() {
		t.Fatal("expected quiet when discovery was never started")
	}
}

func TestApp_fileProcessingQuiet_IdleDiscovery(t *testing.T) {
	app := CreateApp(t)
	if !app.fileProcessingQuiet() {
		t.Fatal("expected quiet when no discovery run is in progress")
	}
}

// TestApp_fileProcessingQuiet_BusyWhileDiscoveryRuns verifies the quiet check
// that gates PRAGMA optimize reports busy while a run is in flight.
func TestApp_fileProcessingQuiet_BusyWhileDiscoveryRuns(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	done := startBlockedDiscovery(t, app)

	if app.fileProcessingQuiet() {
		t.Error("expected not quiet while discovery runs")
	}
	if app.cacheSizeQuietCheck(context.Background()) {
		t.Error("cacheSizeQuietCheck should be false while discovery runs")
	}

	app.RuntimeManager.cancel()
	<-done
}
