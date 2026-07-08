package server

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

// readStartupLogs reads the file-backed startup logs from app.logger.
// Use this when production code replaces slog.Default (e.g., during Run).
func readStartupLogs(t *testing.T, app *App) string {
	t.Helper()
	if app.logger == nil {
		t.Fatal("app.logger is nil")
	}
	data, err := os.ReadFile(app.logger.FilePath())
	if err != nil {
		t.Fatalf("failed to read startup log file: %v", err)
	}
	return string(data)
}

// safeLogBuf is a mutex-protected bytes.Buffer safe for concurrent writes
// and reads. It satisfies io.Writer and provides the String/Len accessors
// used by the test helpers below.
type safeLogBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeLogBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeLogBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *safeLogBuf) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// captureLogs replaces the default slog logger for the duration of the test
// and returns the captured output.
func captureLogs(t *testing.T) *safeLogBuf {
	t.Helper()
	logBuf := &safeLogBuf{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	original := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(original)
	})
	return logBuf
}

// recordingServeHook records the handler and address passed to App.Serve.
type recordingServeHook struct {
	called  bool
	handler http.Handler
	addr    string
}

func (r *recordingServeHook) Serve(h http.Handler, addr string) error {
	r.called = true
	r.handler = h
	r.addr = addr
	return nil
}

// recordingMemoryReclaimer records that the memory reclaimer was started.
type recordingMemoryReclaimer struct {
	called bool
	cfg    MemoryReclaimerConfig
}

func (r *recordingMemoryReclaimer) Reclaim(cfg MemoryReclaimerConfig) {
	r.called = true
	r.cfg = cfg
}

// restoreConfigService is a fake ConfigService for testing the
// restore-last-known-good path in App.Run.
type restoreConfigService struct {
	recordingConfigService
	restoredCfg    *config.Config
	restoreErr     error
	validateErr    error
	saveErr        error
	restoreCalled  bool
	validateCalled bool
	saveCalled     bool
}

func (r *restoreConfigService) RestoreLastKnownGood(ctx context.Context) (*config.Config, error) {
	r.restoreCalled = true
	return r.restoredCfg, r.restoreErr
}

func (r *restoreConfigService) Validate(cfg *config.Config) error {
	r.validateCalled = true
	return r.validateErr
}

func (r *restoreConfigService) Save(ctx context.Context, cfg *config.Config) error {
	r.saveCalled = true
	return r.saveErr
}

func (r *restoreConfigService) EnsureDefaults(ctx context.Context, rootDir string) error {
	return nil
}

// validConfigWithImageDir returns a default config with ImageDirectory set.
func validConfigWithImageDir(rootDir string) *config.Config {
	cfg := config.DefaultConfig()
	cfg.ImageDirectory = filepath.Join(rootDir, "Images")
	return cfg
}

// TestApp_Run_DefaultStartup verifies the happy-path startup sequence.
func TestApp_Run_DefaultStartup(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	reclaimer := &recordingMemoryReclaimer{}
	app.testHookMemoryReclaimer = reclaimer.Reclaim

	if err := app.Run(1, 2); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if app.scheduler == nil {
		t.Error("scheduler not initialized")
	}
	if app.dbRwPool == nil || app.dbRoPool == nil {
		t.Error("database pools not initialized")
	}
	if app.config == nil {
		t.Error("config not loaded")
	}
	if app.pool == nil {
		t.Fatal("worker pool not created")
	}
	if app.pool.MinWorkers != 1 || app.pool.MaxWorkers != 2 {
		t.Errorf("pool bounds = (%d, %d), want (1, 2)", app.pool.MinWorkers, app.pool.MaxWorkers)
	}
	if app.fileProcessor == nil {
		t.Error("fileProcessor not initialized")
	}
	if app.preloadManager == nil {
		t.Error("preloadManager not initialized")
	}
	if app.metricsCollector == nil {
		t.Error("metricsCollector not initialized")
	}
	if !serveHook.called {
		t.Error("App.Serve was not called")
	}
	if serveHook.handler == nil {
		t.Error("Serve handler is nil")
	}
	if !strings.Contains(serveHook.addr, ":") {
		t.Errorf("Serve addr = %q, expected listener address", serveHook.addr)
	}
	if !reclaimer.called {
		t.Error("memory reclaimer was not started")
	}

	// Explicit shutdown (defer also runs, but shutdownOnce makes it safe).
	app.Shutdown()

	if app.logger == nil {
		t.Fatal("expected app.logger to be set")
	}
	logsBytes, err := os.ReadFile(app.logger.FilePath())
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	logs := string(logsBytes)
	if !strings.Contains(logs, "startup config summary") {
		t.Errorf("startup summary not logged, got: %s", logs)
	}
	for _, key := range []string{"worker_configured_max", "worker_effective_max", "db_rw_effective_max", "queue_configured_size", "cache_configured_enabled"} {
		if !strings.Contains(logs, key) {
			t.Errorf("expected log key %q missing, got: %s", key, logs)
		}
	}
}

// TestApp_Run_LoadConfigFails_FallsBackToDefaults verifies that Run continues
// with a fallback config when loadConfig reports an error.
func TestApp_Run_LoadConfigFails_FallsBackToDefaults(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	fallbackCalled := false
	fallbackCfg := validConfigWithImageDir(tempDir)
	fallbackCfg.WorkerPoolMax = 7
	app.testHookFallbackConfig = func() *config.Config {
		fallbackCalled = true
		return fallbackCfg
	}

	app.testHookLoadConfig = func() (*config.Config, error) {
		return nil, fmt.Errorf("database load failed")
	}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !fallbackCalled {
		t.Error("fallback config was not used")
	}
	if app.config.WorkerPoolMax != 7 {
		t.Errorf("WorkerPoolMax = %d, want 7", app.config.WorkerPoolMax)
	}
	if !serveHook.called {
		t.Error("Serve was not called")
	}
}

// reconfigureFailConfigService returns a config with non-default DB pool sizes
// so ReconfigurePools does not short-circuit before invoking the recreate hook.
type reconfigureFailConfigService struct {
	recordingConfigService
}

func (r *reconfigureFailConfigService) Load(ctx context.Context) (*config.Config, error) {
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 50       // different from default 100
	cfg.DBMinIdleConnections = 5 // different from default 10
	return cfg, nil
}

// TestApp_Run_ReconfigurePoolsFails verifies that Run returns an error when
// pool reconfiguration fails.
func TestApp_Run_ReconfigurePoolsFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	app.testHookConfigService = &reconfigureFailConfigService{}

	app.testHookRecreatePoolsWithConfig = func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return nil, nil, fmt.Errorf("reconfigure failed")
	}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if serveHook.called {
		t.Error("Serve should not be called when reconfigure fails")
	}
}

// TestApp_Run_RestoreLastKnownGood_Success verifies the restore path.
func TestApp_Run_RestoreLastKnownGood_Success(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:        getopt.OptString{String: "test-secret", IsSet: true},
		RestoreLastKnownGood: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	restoredCfg := validConfigWithImageDir(tempDir)
	restoredCfg.WorkerPoolMax = 9
	fakeSvc := &restoreConfigService{restoredCfg: restoredCfg}
	app.testHookConfigService = fakeSvc

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !fakeSvc.restoreCalled {
		t.Error("RestoreLastKnownGood was not called")
	}
	if !fakeSvc.validateCalled {
		t.Error("Validate was not called")
	}
	if !fakeSvc.saveCalled {
		t.Error("Save was not called")
	}
	if app.config.WorkerPoolMax != 9 {
		t.Errorf("WorkerPoolMax = %d, want 9", app.config.WorkerPoolMax)
	}
	if !serveHook.called {
		t.Error("Serve was not called")
	}
}

// TestApp_Run_RestoreLastKnownGood_ValidateFails verifies that Run returns an
// error when the restored config fails validation.
func TestApp_Run_RestoreLastKnownGood_ValidateFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:        getopt.OptString{String: "test-secret", IsSet: true},
		RestoreLastKnownGood: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	fakeSvc := &restoreConfigService{
		restoredCfg: validConfigWithImageDir(tempDir),
		validateErr: fmt.Errorf("validation failed"),
	}
	app.testHookConfigService = fakeSvc

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if serveHook.called {
		t.Error("Serve should not be called when validation fails")
	}
}

// TestApp_Run_RestoreLastKnownGood_SaveFails verifies that Run returns an error
// when saving the restored config fails.
func TestApp_Run_RestoreLastKnownGood_SaveFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:        getopt.OptString{String: "test-secret", IsSet: true},
		RestoreLastKnownGood: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	fakeSvc := &restoreConfigService{
		restoredCfg: validConfigWithImageDir(tempDir),
		saveErr:     fmt.Errorf("save failed"),
	}
	app.testHookConfigService = fakeSvc

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if serveHook.called {
		t.Error("Serve should not be called when save fails")
	}
}

// TestApp_Run_ProfilerEnabled verifies that the profiler is started when configured.
func TestApp_Run_ProfilerEnabled(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret", IsSet: true},
		Profile:       getopt.OptString{String: "cpu", IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	profilerCalled := false
	app.testHookProfilerStart = func(cfg profiler.Config) (func(), error) {
		profilerCalled = true
		if cfg.Mode != "cpu" {
			t.Errorf("profiler mode = %q, want cpu", cfg.Mode)
		}
		return func() {}, nil
	}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !profilerCalled {
		t.Error("profiler start was not called")
	}
	if app.stopProfiler == nil {
		t.Error("stopProfiler was not set")
	}
}

// TestApp_Run_ProfilerStartError verifies that Run returns an error when the
// profiler fails to start.
func TestApp_Run_ProfilerStartError(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret", IsSet: true},
		Profile:       getopt.OptString{String: "cpu", IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	app.testHookProfilerStart = func(cfg profiler.Config) (func(), error) {
		return nil, fmt.Errorf("profiler failed")
	}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if serveHook.called {
		t.Error("Serve should not be called when profiler fails")
	}
}

// TestApp_Run_BuildHandlersError verifies that Run returns an error when
// handler building fails.
func TestApp_Run_BuildHandlersError(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	app.testHookBuildHandlers = func(fs fs.FS) error {
		return fmt.Errorf("build handlers failed")
	}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if serveHook.called {
		t.Error("Serve should not be called when buildHandlers fails")
	}
}

// TestApp_Run_NoDiscovery_RefreshesGalleryStats verifies that when discovery is
// disabled and a previous discovery run exists, gallery stats are refreshed.
func TestApp_Run_NoDiscovery_RefreshesGalleryStats(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:    getopt.OptString{String: "test-secret", IsSet: true},
		RunFileDiscovery: getopt.OptBool{Bool: false, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	expectedStats := GalleryStats{ImagesSize: 4242}
	app.testHookGetGalleryStatistics = func(ctx context.Context) (GalleryStats, error) {
		return expectedStats, nil
	}

	app.testHookGetLastStartedAt = func(ctx context.Context, module string) (int64, bool, error) {
		return 12345, true, nil
	}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Wait for the async stats refresh goroutine to complete.
	var got *GalleryStats
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got = app.GetGalleryStatsCached(12345)
		if got != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("gallery stats cache was not refreshed for lastStarted=12345")
	}
	if got.ImagesSize != expectedStats.ImagesSize {
		t.Errorf("ImagesSize = %d, want %d", got.ImagesSize, expectedStats.ImagesSize)
	}
}

// TestApp_Run_NoDiscovery_NoPreviousRun verifies that when discovery is
// disabled and no previous run exists, stats are refreshed with timestamp 0.
func TestApp_Run_NoDiscovery_NoPreviousRun(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:    getopt.OptString{String: "test-secret", IsSet: true},
		RunFileDiscovery: getopt.OptBool{Bool: false, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	expectedStats := GalleryStats{ImagesSize: 777}
	app.testHookGetGalleryStatistics = func(ctx context.Context) (GalleryStats, error) {
		return expectedStats, nil
	}

	app.testHookGetLastStartedAt = func(ctx context.Context, module string) (int64, bool, error) {
		return 0, false, nil
	}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var got *GalleryStats
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got = app.GetGalleryStatsCached(0)
		if got != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("gallery stats cache was not refreshed for lastStarted=0")
	}
	if got.ImagesSize != expectedStats.ImagesSize {
		t.Errorf("ImagesSize = %d, want %d", got.ImagesSize, expectedStats.ImagesSize)
	}
}

// TestApp_Run_RestartRequested verifies that a restart request triggers
// ExecRestart after Serve returns. Note: SetRestartRequired alone does not
// satisfy Run's post-Serve restart check; TriggerRestart is required because
// Run inspects restartRequested, not restartRequired.
func TestApp_Run_RestartRequested(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	app.TriggerRestart()

	execCalled := false
	app.RuntimeManager.testHookExecutable = func() (string, error) {
		return "/tmp/test-exe", nil
	}
	app.testHookExecCommand = func(path string, args []string, env []string) error {
		execCalled = true
		return nil
	}
	app.testHookExit = func(code int) {}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !app.IsRestartRequested() {
		t.Error("restart request flag should still be set")
	}
	if !execCalled {
		t.Error("ExecRestart was not invoked")
	}
}

// TestApp_Run_SetRestartRequired_DoesNotExecRestart verifies that
// SetRestartRequired(true) without TriggerRestart does not cause Run to exec a
// restart. Run checks restartRequested, which is only set by TriggerRestart.
func TestApp_Run_SetRestartRequired_DoesNotExecRestart(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)
	app.SetRestartRequired(true)

	execCalled := false
	app.RuntimeManager.testHookExecutable = func() (string, error) {
		return "/tmp/test-exe", nil
	}
	app.testHookExecCommand = func(path string, args []string, env []string) error {
		execCalled = true
		return nil
	}
	app.testHookExit = func(code int) {}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if execCalled {
		t.Error("ExecRestart should not be called when only SetRestartRequired was set")
	}
}

// TestStartCacheBatchLoad_BlockedWhenDiscoveryActive verifies the blocked result.
func TestStartCacheBatchLoad_BlockedWhenDiscoveryActive(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.testHookModuleStateActive = func() (bool, error) {
		return true, nil
	}

	res, err := app.StartCacheBatchLoad()
	if err != nil {
		t.Fatalf("StartCacheBatchLoad failed: %v", err)
	}
	if !res.Blocked {
		t.Errorf("Blocked = %v, want true", res.Blocked)
	}
	if !strings.Contains(res.Message, "discovery active") {
		t.Errorf("Message = %q, want discovery active", res.Message)
	}
}

// TestStartCacheBatchLoad_AllowedWhenIdle verifies that batch load starts when
// discovery is idle.
func TestStartCacheBatchLoad_AllowedWhenIdle(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.testHookModuleStateActive = func() (bool, error) {
		return false, nil
	}

	var runCalled atomic.Bool
	app.testHookBatchLoadManagerRun = func(ctx context.Context) error {
		runCalled.Store(true)
		return nil
	}

	res, err := app.StartCacheBatchLoad()
	if err != nil {
		t.Fatalf("StartCacheBatchLoad failed: %v", err)
	}
	if res.Blocked {
		t.Errorf("Blocked = %v, want false", res.Blocked)
	}
	if !strings.Contains(res.Message, "started") {
		t.Errorf("Message = %q, want started", res.Message)
	}

	// Wait briefly for the goroutine to invoke the hook.
	time.Sleep(50 * time.Millisecond)
	if !runCalled.Load() {
		t.Error("batch load run was not invoked")
	}
}

// TestStartCacheBatchLoad_ModuleStateError verifies error propagation.
func TestStartCacheBatchLoad_ModuleStateError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.testHookModuleStateActive = func() (bool, error) {
		return false, fmt.Errorf("module state error")
	}

	_, err := app.StartCacheBatchLoad()
	if err == nil {
		t.Fatal("Expected StartCacheBatchLoad to return error")
	}
}

// TestStartCacheBatchLoad_ManagerNil verifies the not-available result.
func TestStartCacheBatchLoad_ManagerNil(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.batchLoadManager = nil

	res, err := app.StartCacheBatchLoad()
	if err != nil {
		t.Fatalf("StartCacheBatchLoad failed: %v", err)
	}
	if res.Blocked {
		t.Errorf("Blocked = %v, want false", res.Blocked)
	}
	if !strings.Contains(res.Message, "not available") {
		t.Errorf("Message = %q, want not available", res.Message)
	}
}

// TestLogStartupConfigSummary_EmitsConfiguredVsEffective verifies the summary log.
func TestLogStartupConfigSummary_EmitsConfiguredVsEffective(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	logBuf := captureLogs(t)

	app.configMu.RLock()
	cfg := app.config
	app.configMu.RUnlock()

	// Ensure non-default configured values.
	cfg.DBMaxPoolSize = 50
	cfg.DBMinIdleConnections = 5
	cfg.WorkerPoolMax = 20
	cfg.WorkerPoolMinIdle = 3
	cfg.QueueSize = 5000
	cfg.EnableHTTPCache = true
	cfg.EnableCachePreload = true
	cfg.RunFileDiscovery = false

	// Wire effective values.
	app.pool = workerpool.NewPool(app.ctx, 3, 20, 10*time.Second)

	app.logStartupConfigSummary(5000, false)

	logs := logBuf.String()
	keys := []string{
		"db_configured_max",
		"db_rw_effective_max",
		"worker_configured_max",
		"worker_effective_max",
		"queue_configured_size",
		"queue_effective_size",
		"cache_configured_enabled",
		"preload_configured_enabled",
		"discovery_configured_enabled",
	}
	for _, key := range keys {
		if !strings.Contains(logs, key) {
			t.Errorf("expected log key %q missing, got: %s", key, logs)
		}
	}
}

// TestLogStartupConfigSummary_NilConfig verifies nil config handling.
func TestLogStartupConfigSummary_NilConfig(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	logBuf := captureLogs(t)

	app.configMu.Lock()
	app.config = nil
	app.configMu.Unlock()

	app.logStartupConfigSummary(1000, true)

	if logBuf.Len() != 0 {
		t.Errorf("expected no log output, got: %s", logBuf.String())
	}
}

// TestApp_Run_ServeErrorPanics verifies that Run panics with "main" when Serve
// returns a non-nil error.
func TestApp_Run_ServeErrorPanics(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)
	app.testHookServe = func(http.Handler, string) error {
		return fmt.Errorf("serve failed")
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when Serve fails")
		}
	}()
	defer app.Shutdown()

	_ = app.Run(1, 1)
}

// TestApp_Run_RestoreLastKnownGood_DBGetFails verifies that Run returns an error
// when it cannot get a DB connection for the restore path.
func TestApp_Run_RestoreLastKnownGood_DBGetFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:        getopt.OptString{String: "test-secret", IsSet: true},
		RestoreLastKnownGood: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	// Prevent setConfigDefaults from touching the closed pools.
	app.testHookConfigService = &recordingConfigService{}

	// Create and immediately close real pools so the fake initializer returns
	// pools whose Get() calls fail.
	_, closedRw, closedRo, err := database.Setup(app.ctx, tempDir, nil)
	if err != nil {
		t.Fatalf("failed to setup closed pools: %v", err)
	}
	_ = closedRw.Close()
	_ = closedRo.Close()

	app.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: filepath.Join(tempDir, "test.db")},
		setupRw:    closedRw,
		setupRo:    closedRo,
	}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	runErr := app.Run(1, 1)
	if runErr == nil {
		t.Fatal("expected Run to return error")
	}
	if !strings.Contains(runErr.Error(), "failed to get db connection for restore") {
		t.Errorf("Run error = %q, want restore db get failure", runErr.Error())
	}
	if serveHook.called {
		t.Error("Serve should not be called when restore DB get fails")
	}
}

// TestApp_Run_NoDiscovery_GetLastStartedAtError verifies the error log in the
// no-discovery goroutine when GetLastStartedAt fails.
func TestApp_Run_NoDiscovery_GetLastStartedAtError(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:    getopt.OptString{String: "test-secret", IsSet: true},
		RunFileDiscovery: getopt.OptBool{Bool: false, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	app.testHookGetLastStartedAt = func(context.Context, string) (int64, bool, error) {
		return 0, false, fmt.Errorf("module state error")
	}
	app.testHookGetGalleryStatistics = func(context.Context) (GalleryStats, error) {
		return GalleryStats{ImagesSize: 1}, nil
	}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	logs := readStartupLogs(t, app)
	if !strings.Contains(logs, "failed to get last started at") {
		t.Errorf("expected 'failed to get last started at' log, got: %s", logs)
	}
}

// TestApp_Run_NoDiscovery_RefreshStatsError verifies the error log in the
// no-discovery goroutine when refreshGalleryStatsCache fails.
func TestApp_Run_NoDiscovery_RefreshStatsError(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:    getopt.OptString{String: "test-secret", IsSet: true},
		RunFileDiscovery: getopt.OptBool{Bool: false, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	app.testHookGetLastStartedAt = func(context.Context, string) (int64, bool, error) {
		return 12345, true, nil
	}
	app.testHookGetGalleryStatistics = func(context.Context) (GalleryStats, error) {
		return GalleryStats{}, fmt.Errorf("stats failure")
	}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	logs := readStartupLogs(t, app)
	if !strings.Contains(logs, "failed to refresh gallery stats cache") {
		t.Errorf("expected 'failed to refresh gallery stats cache' log, got: %s", logs)
	}
}

// TestStartCacheBatchLoad_ManagerRunError verifies that a non-canceled run
// error from the batch load manager is logged.
func TestStartCacheBatchLoad_ManagerRunError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.testHookModuleStateActive = func() (bool, error) {
		return false, nil
	}

	runCalled := make(chan struct{}, 1)
	app.testHookBatchLoadManagerRun = func(context.Context) error {
		close(runCalled)
		return fmt.Errorf("run failed")
	}

	logBuf := captureLogs(t)

	res, err := app.StartCacheBatchLoad()
	if err != nil {
		t.Fatalf("StartCacheBatchLoad failed: %v", err)
	}
	if res.Blocked {
		t.Errorf("Blocked = %v, want false", res.Blocked)
	}

	select {
	case <-runCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("batch load run hook was not invoked")
	}

	// Wait for the background goroutine to log the error. logBuf is
	// mutex-protected so polling is race-free.
	var logs string
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs, "cache batch load run failed") {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for 'cache batch load run failed' log, got: %s", logs)
		}
		time.Sleep(10 * time.Millisecond)
		logs = logBuf.String()
	}
}

// TestApp_Run_RestoreLastKnownGood_RestoreFails verifies that Run returns an
// error when RestoreLastKnownGood fails.
func TestApp_Run_RestoreLastKnownGood_RestoreFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:        getopt.OptString{String: "test-secret", IsSet: true},
		RestoreLastKnownGood: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	fakeSvc := &restoreConfigService{
		restoredCfg: validConfigWithImageDir(tempDir),
		restoreErr:  fmt.Errorf("restore failed"),
	}
	app.testHookConfigService = fakeSvc

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if serveHook.called {
		t.Error("Serve should not be called when restore fails")
	}
}

// TestApp_Run_RestoreLastKnownGood_ReconfigureFails verifies that Run returns an
// error when reconfigurePoolsFromConfig fails after a successful restore.
func TestApp_Run_RestoreLastKnownGood_ReconfigureFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:        getopt.OptString{String: "test-secret", IsSet: true},
		RestoreLastKnownGood: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	restoredCfg := validConfigWithImageDir(tempDir)
	restoredCfg.DBMaxPoolSize = 101 // force reconfigure
	fakeSvc := &restoreConfigService{restoredCfg: restoredCfg}
	app.testHookConfigService = fakeSvc

	app.testHookRecreatePoolsWithConfig = func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return nil, nil, fmt.Errorf("reconfigure failed")
	}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if !strings.Contains(err.Error(), "reconfigure pools after restore") {
		t.Errorf("Run error = %q, want reconfigure pools after restore", err.Error())
	}
	if serveHook.called {
		t.Error("Serve should not be called when reconfigure after restore fails")
	}
}

// TestApp_Run_LoadConfigFails_ReconfigureDefaultsFails verifies that Run returns
// an error when loading config fails and reconfiguring with defaults also fails.
func TestApp_Run_LoadConfigFails_ReconfigureDefaultsFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	fallbackCfg := validConfigWithImageDir(tempDir)
	fallbackCfg.DBMaxPoolSize = 101 // force reconfigure
	app.testHookFallbackConfig = func() *config.Config {
		return fallbackCfg
	}

	app.testHookLoadConfig = func() (*config.Config, error) {
		return nil, fmt.Errorf("database load failed")
	}
	app.testHookRecreatePoolsWithConfig = func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return nil, nil, fmt.Errorf("reconfigure defaults failed")
	}

	serveHook := &recordingServeHook{}
	app.testHookServe = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if !strings.Contains(err.Error(), "reconfigure pools with defaults") {
		t.Errorf("Run error = %q, want reconfigure pools with defaults", err.Error())
	}
	if serveHook.called {
		t.Error("Serve should not be called when reconfigure with defaults fails")
	}
}

// TestApp_Run_DiscoveryMonitor_CompletionLog verifies that the discovery
// completion monitor logs completion and exits when all work finishes.
func TestApp_Run_DiscoveryMonitor_CompletionLog(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:    getopt.OptString{String: "test-secret", IsSet: true},
		RunFileDiscovery: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	// Pretend a discovery sender is active so the monitor enters the end-loop.
	app.qSendersActive.Store(1)

	app.testHookServe = func(h http.Handler, addr string) error {
		// Let the monitor enter the end-loop, then clear the sender so it
		// observes completion and logs the summary before cancellation.
		time.Sleep(150 * time.Millisecond)
		app.qSendersActive.Store(0)
		time.Sleep(150 * time.Millisecond)
		app.cancel()
		return nil
	}

	reclaimer := &recordingMemoryReclaimer{}
	app.testHookMemoryReclaimer = reclaimer.Reclaim

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	logs := readStartupLogs(t, app)
	if !strings.Contains(logs, "File processing completed") {
		t.Errorf("expected 'File processing completed' log, got: %s", logs)
	}
}
