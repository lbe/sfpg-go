package server

import (
	"fmt"
	"io/fs"
	"net"
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
)

// TestApp_Serve_DelegatesToRuntimeManager verifies that Serve reaches the real
// RuntimeManager.Serve path when testHookServe is not set.
func TestApp_Serve_DelegatesToRuntimeManager(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	app.configMu.Lock()
	app.config.ListenerPort = port
	app.configMu.Unlock()

	app.testHookBeforeListen = func() { app.cancel() }

	if err := app.Serve(); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	if app.testHookServe != nil {
		t.Error("testHookServe should not be set in this test")
	}
}

// TestApp_Serve_NilConfig_LoadsDefaults verifies that Serve loads defaults when
// the config is nil.
func TestApp_Serve_NilConfig_LoadsDefaults(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.configMu.Lock()
	app.config = nil
	app.configMu.Unlock()

	app.testHookBeforeListen = func() { app.cancel() }

	if err := app.Serve(); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	if app.GetConfig() == nil {
		t.Fatal("expected config to be loaded after Serve")
	}
}

// TestApp_Serve_NilConfig_LoadConfigErrorFallsBack verifies that Serve falls
// back to DefaultConfig when loadConfig fails.
func TestApp_Serve_NilConfig_LoadConfigErrorFallsBack(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)
	app.setDB()

	app.configMu.Lock()
	app.config = nil
	app.configMu.Unlock()

	app.testHookLoadConfig = func() (*config.Config, error) {
		return nil, fmt.Errorf("load failed")
	}
	app.testHookBeforeListen = func() { app.cancel() }

	if err := app.Serve(); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	if app.GetConfig() == nil {
		t.Fatal("expected fallback config to be set after Serve")
	}
}

// TestApp_Serve_BuildsHandlersWhenNil verifies that Serve lazily builds
// handlers when they have not been constructed yet.
func TestApp_Serve_BuildsHandlersWhenNil(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.authHandlers = nil
	app.galleryHandlers = nil
	app.configHandlers = nil

	app.testHookBeforeListen = func() { app.cancel() }

	if err := app.Serve(); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	if app.authHandlers == nil {
		t.Error("expected authHandlers to be built by Serve")
	}
	if app.galleryHandlers == nil {
		t.Error("expected galleryHandlers to be built by Serve")
	}
}

// TestApp_Serve_BuildHandlersError verifies that Serve returns an error when
// handler construction fails.
func TestApp_Serve_BuildHandlersError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.authHandlers = nil
	app.testHookBuildHandlers = func(fs.FS) error {
		return fmt.Errorf("build failed")
	}

	if err := app.Serve(); err == nil {
		t.Fatal("expected Serve to return build error")
	} else if err.Error() != "build failed" {
		t.Errorf("Serve error = %q, want %q", err.Error(), "build failed")
	}
}
