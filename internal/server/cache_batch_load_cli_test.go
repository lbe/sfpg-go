package server

import (
	"context"
	"flag"
	"os"
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
)

func TestRunCacheBatchLoad_BlockedWhenDiscoveryActive(t *testing.T) {
	// Reset flags so getopt.Parse sees clean state
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}
	t.Setenv("SEPG_SESSION_SECRET", "test-secret")

	opt := getopt.Parse()
	app := New(opt, "test")
	defer app.Shutdown()

	if err := app.InitForBatchLoad(opt); err != nil {
		t.Fatalf("InitForBatchLoad: %v", err)
	}

	ctx := context.Background()
	if err := app.moduleStateService.SetActive(ctx, "discovery", true); err != nil {
		t.Fatalf("SetActive(discovery, true): %v", err)
	}
	defer app.moduleStateService.SetActive(ctx, "discovery", false)

	code := app.RunCacheBatchLoad()
	if code != 2 {
		t.Errorf("RunCacheBatchLoad() = %d, want 2 (blocked)", code)
	}
}

func TestRunCacheBatchLoad_SuccessWhenIdle(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}
	t.Setenv("SEPG_SESSION_SECRET", "test-secret")

	opt := getopt.Parse()
	app := New(opt, "test")
	defer app.Shutdown()

	if err := app.InitForBatchLoad(opt); err != nil {
		t.Fatalf("InitForBatchLoad: %v", err)
	}

	code := app.RunCacheBatchLoad()
	if code != 0 {
		t.Errorf("RunCacheBatchLoad() = %d, want 0 (success)", code)
	}
}

func TestRunCacheBatchLoad_ErrorWhenManagerNil(t *testing.T) {
	app := &App{}
	app.ctxMu.Lock()
	app.ctx = context.Background()
	app.ctxMu.Unlock()

	code := app.RunCacheBatchLoad()
	if code != 1 {
		t.Errorf("RunCacheBatchLoad() with nil manager = %d, want 1 (error)", code)
	}
}
