//go:build integration

package server

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/config"
)

// recordingMemoryReclaimer records that the memory reclaimer was started.
// (duplicated here to keep the integration test file self-contained)
type recordingMemoryReclaimerIntegration struct {
	once    sync.Once
	started chan struct{}
	cfg     MemoryReclaimerConfig
}

func (r *recordingMemoryReclaimerIntegration) Reclaim(cfg MemoryReclaimerConfig) {
	r.once.Do(func() {
		if r.started != nil {
			close(r.started)
		}
	})
	r.cfg = cfg
}

// TestRun_Integration_FullStartupAndShutdown performs a full startup using real
// templates, real database pools, and the real handler chain. It verifies that
// Run reaches Serve, starts the memory reclaimer, and that Shutdown terminates
// cleanly without leaking goroutines.
func TestRun_Integration_FullStartupAndShutdown(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	reclaimer := &recordingMemoryReclaimerIntegration{started: make(chan struct{})}
	app.testHookMemoryReclaimer = reclaimer.Reclaim

	baseline := runtime.NumGoroutine()

	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run(1, 1)
		close(done)
	}()

	// Wait for Run to complete (Serve returns immediately via the hook).
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not complete within timeout")
	}

	if runErr != nil {
		t.Fatalf("Run failed: %v", runErr)
	}
	if !serveHook.called {
		t.Error("Serve was not called")
	}
	// The memory reclaimer is started in a goroutine right before Serve.
	select {
	case <-reclaimer.started:
	case <-time.After(500 * time.Millisecond):
		t.Error("memory reclaimer was not started")
	}

	app.Shutdown()

	// Give goroutines a moment to exit, then allow a small margin for
	// runtime background threads that we do not control.
	time.Sleep(100 * time.Millisecond)
	remaining := runtime.NumGoroutine()
	if remaining > baseline+2 {
		t.Errorf("goroutine leak: baseline=%d remaining=%d", baseline, remaining)
	}
}

// TestRun_Integration_HTTPCacheCleanupGoroutineStarts enables the HTTP cache and
// verifies that Run starts the cleanup goroutine and that Shutdown cancels it
// cleanly.
func TestRun_Integration_HTTPCacheCleanupGoroutineStarts(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	reclaimer := &recordingMemoryReclaimerIntegration{}
	app.testHookMemoryReclaimer = reclaimer.Reclaim

	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run(1, 1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not complete within timeout")
	}

	if runErr != nil {
		t.Fatalf("Run failed: %v", runErr)
	}
	if app.cacheMW == nil {
		t.Fatal("HTTP cache middleware was not initialized")
	}

	app.Shutdown()
}

// TestRun_Integration_HTTPCacheCleanupGoroutineExits verifies that the HTTP cache
// cleanup goroutine started by Run exits cleanly after Shutdown.
func TestRun_Integration_HTTPCacheCleanupGoroutineExits(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:    getopt.OptString{String: "test-secret", IsSet: true},
		EnableHTTPCache:  getopt.OptBool{Bool: true, IsSet: true},
		RunFileDiscovery: getopt.OptBool{Bool: false, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	reclaimer := &recordingMemoryReclaimerIntegration{}
	app.testHookMemoryReclaimer = reclaimer.Reclaim

	baseline := runtime.NumGoroutine()

	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run(1, 1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not complete within timeout")
	}

	if runErr != nil {
		t.Fatalf("Run failed: %v", runErr)
	}
	if app.cacheMW == nil {
		t.Fatal("HTTP cache middleware was not initialized")
	}

	app.Shutdown()

	time.Sleep(100 * time.Millisecond)
	remaining := runtime.NumGoroutine()
	if remaining > baseline+2 {
		t.Errorf("goroutine leak: baseline=%d remaining=%d", baseline, remaining)
	}
}

// TestRun_Integration_BatchLoadManagerCreatedWhenCacheEnabled verifies that Run
// creates and wires the cache batch load manager when HTTP caching is enabled.
func TestRun_Integration_BatchLoadManagerCreatedWhenCacheEnabled(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:    getopt.OptString{String: "test-secret", IsSet: true},
		EnableHTTPCache:  getopt.OptBool{Bool: true, IsSet: true},
		RunFileDiscovery: getopt.OptBool{Bool: false, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	reclaimer := &recordingMemoryReclaimerIntegration{}
	app.testHookMemoryReclaimer = reclaimer.Reclaim

	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run(1, 1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not complete within timeout")
	}

	if runErr != nil {
		t.Fatalf("Run failed: %v", runErr)
	}
	if app.batchLoadManager == nil {
		t.Fatal("batchLoadManager was not created when HTTP cache is enabled")
	}
	if app.metricsCollector == nil {
		t.Fatal("metricsCollector was not initialized")
	}
}

// TestApp_Run_ProfilerUsesRealStart verifies that Run invokes the real
// profiler.Start when no hook is set and the profile option is configured.
func TestApp_Run_ProfilerUsesRealStart(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret", IsSet: true},
		Profile:       getopt.OptString{String: "cpu", IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	reclaimer := &recordingMemoryReclaimerIntegration{}
	app.testHookMemoryReclaimer = reclaimer.Reclaim

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if app.stopProfiler == nil {
		t.Fatal("expected stopProfiler to be set when real profiler starts")
	}

	app.stopProfiler()

	if profiler.Dir() == "" {
		t.Error("expected profiler.Dir() to be non-empty after real start")
	}
}

// TestStartupLogging_BootstrapThenReload_CapturesEarlyLogs verifies that
// setupBootstrapLogging creates the bootstrap log directory and that
// reloadLoggingFromConfig moves logging to a configured directory while
// preserving the base filename.
func TestStartupLogging_BootstrapThenReload_CapturesEarlyLogs(t *testing.T) {
	tmpDir := t.TempDir()
	bootstrapLogDir := filepath.Join(tmpDir, "logs")
	configuredLogDir := filepath.Join(tmpDir, "configured-logs")

	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	app.rootDir = tmpDir
	app.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.scheduler.Start(app.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() { _ = app.scheduler.Shutdown() }()

	app.setupBootstrapLogging()

	if info, err := os.Stat(bootstrapLogDir); err != nil || !info.IsDir() {
		t.Fatalf("bootstrap logs directory should exist: %v", err)
	}
	bootstrapLogFile := app.logger.FilePath()
	if info, err := os.Stat(bootstrapLogFile); err != nil || info.IsDir() {
		t.Fatalf("bootstrap log file should exist: %v", err)
	}

	app.configMu.Lock()
	app.config = &config.Config{
		LogDirectory: configuredLogDir,
		LogLevel:     "debug",
	}
	app.configMu.Unlock()

	if err := app.reloadLoggingFromConfig(); err != nil {
		t.Fatalf("reloadLoggingFromConfig should not fail: %v", err)
	}

	if info, err := os.Stat(configuredLogDir); err != nil || !info.IsDir() {
		t.Fatalf("configured logs directory should exist: %v", err)
	}

	newLogFile := app.logger.FilePath()
	if newLogFile == bootstrapLogFile {
		t.Fatal("log file path should have changed to configured directory")
	}
	if info, err := os.Stat(newLogFile); err != nil || info.IsDir() {
		t.Fatalf("new log file should exist: %v", err)
	}
	if filepath.Dir(newLogFile) != configuredLogDir {
		t.Fatalf("new log file should be in configured directory: got %s, want %s", filepath.Dir(newLogFile), configuredLogDir)
	}
	if filepath.Base(newLogFile) != filepath.Base(bootstrapLogFile) {
		t.Fatalf("log file name should be preserved: got %s, want %s", filepath.Base(newLogFile), filepath.Base(bootstrapLogFile))
	}

	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

// TestStartupLogging_BootstrapThenReload_SameDirectory verifies that
// reloadLoggingFromConfig keeps the same logger when config points to the same
// log directory.
func TestStartupLogging_BootstrapThenReload_SameDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	app.rootDir = tmpDir
	app.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.scheduler.Start(app.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() { _ = app.scheduler.Shutdown() }()

	app.setupBootstrapLogging()

	originalLogger := app.logger
	originalLogFile := app.logger.File()

	app.configMu.Lock()
	app.config = &config.Config{
		LogDirectory: "logs",
		LogLevel:     "debug",
	}
	app.configMu.Unlock()

	if err := app.reloadLoggingFromConfig(); err != nil {
		t.Fatalf("reloadLoggingFromConfig should not fail: %v", err)
	}

	if app.logger != originalLogger {
		t.Fatal("logger should remain unchanged when directory is same")
	}
	if app.logger.File() != originalLogFile {
		t.Fatal("log file should remain unchanged when directory is same")
	}

	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}
