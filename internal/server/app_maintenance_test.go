package server

import (
	"context"
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/server/files"
)

func TestApp_fileProcessingQuiet_DiscoveryRunning(t *testing.T) {
	app := New(getopt.Opt{}, "test")
	ensureProcessingStats(app.SubsystemManager)

	app.discoveryRunning.Store(true)
	if app.fileProcessingQuiet() {
		t.Fatal("expected not quiet while discoveryRunning")
	}

	app.discoveryRunning.Store(false)
	if !app.fileProcessingQuiet() {
		t.Fatal("expected quiet when discovery finished and queue idle")
	}
}

func TestApp_fileProcessingQuiet_QSendersActive(t *testing.T) {
	app := New(getopt.Opt{}, "test")
	ensureProcessingStats(app.SubsystemManager)

	app.SubsystemManager.qSendersActive.Store(1)
	if app.fileProcessingQuiet() {
		t.Fatal("expected not quiet while walk senders active")
	}

	app.SubsystemManager.qSendersActive.Store(0)
	if !app.fileProcessingQuiet() {
		t.Fatal("expected quiet after senders finished")
	}
}

func TestApp_fileProcessingQuiet_QueueLen(t *testing.T) {
	app := New(getopt.Opt{}, "test")
	ensureProcessingStats(app.SubsystemManager)
	app.SubsystemManager.q = queue.NewQueue[files.DiscoveryPathWork](10)

	if err := app.SubsystemManager.q.Enqueue(files.DiscoveryPathWork{Path: []byte("a.jpg"), MtimeUnix: 1, SizeBytes: 1}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if app.fileProcessingQuiet() {
		t.Fatal("expected not quiet while queue has items")
	}
}

func TestApp_fileProcessingQuiet_InFlight(t *testing.T) {
	app := New(getopt.Opt{}, "test")
	ensureProcessingStats(app.SubsystemManager)
	app.SubsystemManager.processingStats.InFlight.Store(1)

	if app.fileProcessingQuiet() {
		t.Fatal("expected not quiet while workers in flight")
	}
}

func TestApp_cacheSizeQuietCheck_BlocksDuringDiscoveryWalk(t *testing.T) {
	app := New(getopt.Opt{}, "test")
	ensureProcessingStats(app.SubsystemManager)
	app.SubsystemManager.qSendersActive.Store(1)

	if app.cacheSizeQuietCheck(context.Background()) {
		t.Fatal("cacheSizeQuietCheck should be false during directory walk")
	}
}
