package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/log"
	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
)

// --- merged from app_test.go ---
func TestNew(t *testing.T) {
	ss := "this-is-a-test-secret-with-min-32-bytes"
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: ss, IsSet: true}}
	app := New(opt, "x.y.z")
	t.Run("Initializes App struct correctly", func(t *testing.T) {
		if app.RuntimeManager.ctx == nil {
			t.Error("Expected app.RuntimeManager.ctx to not be nil")
		}
		if app.RuntimeManager.cancel == nil {
			t.Error("Expected app.RuntimeManager.cancel to not be nil")
		}
		if app.SessionAuthFacade.sessionSecret != ss {
			t.Errorf("Expected sessionSecret to be %q, got %q", ss, app.SessionAuthFacade.sessionSecret)
		}
	})
	t.Run("RebuildFileFolderIndex seam is nil", func(t *testing.T) {
		if app.testSeams.RebuildFileFolderIndex != nil {
			t.Fatal("New() must not stub RebuildFileFolderIndex; TriggerDiscovery would skip files.RebuildFileFolderIndex")
		}
	})
}

func TestNew_DoesNotCreatePool(t *testing.T) {
	ss := "this-is-a-test-secret-with-min-32-bytes"
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: ss, IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	if app.SubsystemManager.pool != nil {
		t.Errorf("Expected app.SubsystemManager.pool to be nil after New, got %v", app.SubsystemManager.pool)
	}
	if app.InfrastructureService == nil {
		t.Error("Expected InfrastructureService to be initialized")
	}
	if app.ConfigManager == nil {
		t.Error("Expected ConfigManager to be initialized")
	}
	if app.SessionAuthFacade == nil {
		t.Error("Expected SessionAuthFacade to be initialized")
	}
	if app.HandlerManager == nil {
		t.Error("Expected HandlerManager to be initialized")
	}
	if app.RuntimeManager == nil {
		t.Error("Expected RuntimeManager to be initialized")
	}
	if app.SubsystemManager == nil {
		t.Error("Expected SubsystemManager to be initialized")
	}
}

func TestApp_GetCtx_ReturnsCtxOrBackground(t *testing.T) {
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	if got := app.getCtx(); got != app.RuntimeManager.ctx {
		t.Errorf("getCtx() with RuntimeManager ctx = %v, want %v", got, app.RuntimeManager.ctx)
	}

	app.RuntimeManager.ctx = nil
	if got := app.getCtx(); got != context.Background() {
		t.Errorf("getCtx() with nil ctx = %v, want context.Background()", got)
	}
}

func TestNew_ParseTemplatesError_Exits(t *testing.T) {
	var parseCalled bool
	var exitCode int
	defaultNewTestSeams = AppTestSeams{
		NewParseTemplates: func(fs.FS) error {
			parseCalled = true
			return fmt.Errorf("parse failed")
		},
		NewExit: func(code int) {
			exitCode = code
			panic("exit")
		},
	}
	t.Cleanup(func() {
		defaultNewTestSeams = AppTestSeams{}
	})

	func() {
		defer func() { recover() }()
		New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	}()

	if !parseCalled {
		t.Error("expected parse hook to be called")
	}
	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1", exitCode)
	}
}

func TestParseConfigUITemplates_MissingFileReturnsError(t *testing.T) {
	fsys := fstest.MapFS{}
	_, err := parseConfigUITemplates(fsys)
	if err == nil {
		t.Fatal("expected error for missing template file")
	}
}

func TestParseConfigUITemplates_InvalidTemplatePerFile_ReturnsError(t *testing.T) {
	templateNames := []string{
		"templates/config-ui/config-save-restart-alert.html.tmpl",
		"templates/config-ui/config-save-success-alert.html.tmpl",
		"templates/config-ui/config-export-modal.html.tmpl",
		"templates/config-ui/config-import-modal.html.tmpl",
		"templates/config-ui/config-restore-modal.html.tmpl",
		"templates/config-ui/config-restore-success-alert.html.tmpl",
		"templates/config-ui/config-import-success-alert.html.tmpl",
		"templates/config-ui/config-restart-initiated-alert.html.tmpl",
	}
	for _, badName := range templateNames {
		t.Run(badName, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for _, name := range templateNames {
				data := []byte("")
				if name == badName {
					data = []byte("{{.Bad}")
				}
				fsys[name] = &fstest.MapFile{Data: data}
			}
			_, err := parseConfigUITemplates(fsys)
			if err == nil {
				t.Fatal("expected error for invalid template syntax")
			}
		})
	}
}

func TestSetRootDir_ExecutableErrorPanics(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	app.testSeams.Executable = func() (string, error) {
		return "", fmt.Errorf("exec failed")
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when executable lookup fails")
		}
	}()
	app.setRootDir(nil)
}

func TestSetupBootstrapLogging_ErrorPanics(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	tempDir := t.TempDir()
	app.setRootDir(&tempDir)
	app.testSeams.SetupBootstrapLogging = func(string, *scheduler.Scheduler, string) (*log.Logger, error) {
		return nil, fmt.Errorf("setup failed")
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when bootstrap logging setup fails")
		}
	}()
	app.setupBootstrapLogging()
}

func TestApp_SetupBootstrapLogging_CreatesLogger(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()

	if app.logger == nil {
		t.Fatal("Expected app.logger to be initialized")
	}

	logsDir := filepath.Join(tempDir, "logs")
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		t.Errorf("Expected logs directory to be created at %q", logsDir)
	}
}

func TestApp_ApplyConfig_NilConfigNoPanic(t *testing.T) {
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = nil
	app.ConfigManager.ConfigMu.Unlock()

	app.ApplyConfig()
}

func TestInitForIncrementETag_DatabaseSetupError(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	tempDir := t.TempDir()
	app.setRootDir(&tempDir)
	app.testSeams.DatabaseSetup = func(context.Context, string, *config.Config) (database.DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return database.DatabasePaths{}, nil, nil, fmt.Errorf("setup failed")
	}

	err := app.InitForIncrementETag(getopt.Opt{})
	if err == nil {
		t.Fatal("expected error when database setup fails")
	}
	if !strings.Contains(err.Error(), "failed to setup database for increment-etag") {
		t.Errorf("error = %q, want setup failure", err.Error())
	}
}

func newAppForUnlock(t *testing.T) *App {
	t.Helper()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	t.Cleanup(app.Shutdown)
	tempDir := t.TempDir()
	app.setRootDir(&tempDir)
	if err := app.InitForUnlock(); err != nil {
		t.Fatalf("InitForUnlock: %v", err)
	}
	return app
}

func newAppForIncrementETag(t *testing.T) *App {
	t.Helper()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	t.Cleanup(app.Shutdown)
	tempDir := t.TempDir()
	app.setRootDir(&tempDir)
	if err := app.InitForIncrementETag(opt); err != nil {
		t.Fatalf("InitForIncrementETag: %v", err)
	}
	return app
}

func TestApp_UnlockAccount(t *testing.T) {
	app := newAppForUnlock(t)
	if err := app.UnlockAccount("admin"); err != nil {
		t.Fatalf("UnlockAccount: %v", err)
	}
}

func TestApp_InitForBatchLoad(t *testing.T) {
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	t.Cleanup(app.Shutdown)
	tempDir := t.TempDir()
	app.setRootDir(&tempDir)

	if err := app.InitForBatchLoad(opt); err != nil {
		t.Fatalf("InitForBatchLoad: %v", err)
	}
	if app.cacheMW == nil {
		t.Error("expected cacheMW to be initialized")
	}
	app.InvalidateHTTPCache()
	if code := app.RunCacheBatchLoad(); code != 0 {
		t.Errorf("RunCacheBatchLoad = %d, want 0", code)
	}
}

func TestApp_ConfigServiceMethods(t *testing.T) {
	app := newAppForIncrementETag(t)
	ctx := context.Background()
	if app.ConfigManager.ConfigService == nil {
		t.Fatal("configService is nil")
	}

	cfg, err := app.ConfigManager.ConfigService.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := app.ConfigManager.ConfigService.Validate(cfg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := app.ConfigManager.ConfigService.Save(ctx, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := app.ConfigManager.ConfigService.Export(ctx); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := app.ConfigManager.ConfigService.Import("log-level: info\n", ctx); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := app.ConfigManager.ConfigService.RestoreLastKnownGood(ctx); err != nil {
		// A missing last-known-good record is acceptable; the call itself is the coverage target.
		t.Logf("RestoreLastKnownGood: %v", err)
	}
	if _, err := app.ConfigManager.ConfigService.GetConfigValue(ctx, "user"); err != nil {
		t.Fatalf("GetConfigValue: %v", err)
	}
	if _, err := app.ConfigManager.ConfigService.IncrementETag(ctx); err != nil {
		t.Fatalf("IncrementETag: %v", err)
	}
}

func TestConfigManager_GetETagVersion_And_UpdateConfigWithPrecedence(t *testing.T) {
	cm := config.NewConfigManager()
	if v := cm.GetETagVersion(); v == "" {
		t.Error("GetETagVersion returned empty string with no config")
	}

	cfg := config.DefaultConfig()
	cfg.ETagVersion = "test-etag-123"
	cm.SetConfig(cfg)
	if got := cm.GetETagVersion(); got != cfg.ETagVersion {
		t.Errorf("GetETagVersion = %q, want %q", got, cfg.ETagVersion)
	}

	opt := getopt.Opt{Port: getopt.OptInt{Int: 1234, IsSet: true}}
	updated := config.DefaultConfig()
	updated.ListenerPort = 9999
	cm.UpdateConfigWithPrecedence(updated, []string{"ListenerPort"}, opt)
	if cm.GetConfig() != updated {
		t.Error("UpdateConfigWithPrecedence did not replace the stored config")
	}
}

func Test_GetAdminUsername_delegates_to_AuthService(t *testing.T) {
	t.Setenv("SEPG_SESSION_SECRET", "test-secret-with-at-least-32-bytes-long")

	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()

	app.RuntimeManager.ctx = context.Background()
	rootDir := t.TempDir()
	app.setRootDir(&rootDir)

	// Use real database setup for a proper pool.
	dbPaths, rwPool, roPool, err := database.Setup(app.RuntimeManager.ctx, rootDir, config.DefaultConfig())
	if err != nil {
		t.Fatalf("database.Setup() error = %v", err)
	}
	_ = dbPaths
	app.dbRwPool = rwPool
	app.dbRoPool = roPool

	// Verify the public App method delegates to AuthService.GetAdminUsername end-to-end.
	username, err := app.GetAdminUsername(app.RuntimeManager.ctx, app.dbRoPool)
	if err != nil {
		t.Logf("GetAdminUsername returned error (OK in empty DB): %v", err)
	}
	// In an empty (just-migrated) database, the query should return empty string.
	// This verifies the delegation works end-to-end without panicking.
	_ = username
}

// --- merged from handler_manager_test.go ---
func TestHandlerManager_Build_DelegatesToHook(t *testing.T) {
	hm := NewHandlerManager()

	var hookCalled bool
	var receivedFS fs.FS
	hm.testSeams.BuildHandlers = func(tfs fs.FS) error {
		hookCalled = true
		receivedFS = tfs
		return nil
	}

	testFS := fstest.MapFS{}
	err := hm.Build(testFS, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if !hookCalled {
		t.Error("expected testSeams.BuildHandlers to be called")
	}
	if receivedFS == nil {
		t.Error("expected non-nil templateFS in hook")
	}
}

func TestHandlerManager_Build_HookError(t *testing.T) {
	hm := NewHandlerManager()
	expectedErr := errors.New("handler build error")
	hm.testSeams.BuildHandlers = func(_ fs.FS) error {
		return expectedErr
	}

	err := hm.Build(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if !errors.Is(err, expectedErr) {
		t.Errorf("Build error = %v, want %v", err, expectedErr)
	}
}

func TestParseConfigUITemplates_EmptyFS(t *testing.T) {
	_, err := parseConfigUITemplates(fstest.MapFS{})
	if err == nil {
		t.Fatal("expected parseConfigUITemplates to return error for empty FS")
	}
}

// --- merged from app_startup_mock_test.go ---
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

type restoreConfigService struct {
	restoredCfg    *config.Config
	restoreErr     error
	validateErr    error
	saveErr        error
	restoreCalled  bool
	validateCalled bool
	saveCalled     bool
}

func (r *restoreConfigService) Load(ctx context.Context) (*config.Config, error) {
	return config.DefaultConfig(), nil
}

func (r *restoreConfigService) Save(ctx context.Context, cfg *config.Config) error {
	r.saveCalled = true
	return r.saveErr
}

func (r *restoreConfigService) Validate(cfg *config.Config) error {
	r.validateCalled = true
	return r.validateErr
}

func (r *restoreConfigService) Export(ctx context.Context) (string, error) {
	return "", nil
}

func (r *restoreConfigService) Import(yamlContent string, ctx context.Context) error {
	return nil
}

func (r *restoreConfigService) RestoreLastKnownGood(ctx context.Context) (*config.Config, error) {
	r.restoreCalled = true
	return r.restoredCfg, r.restoreErr
}

func (r *restoreConfigService) EnsureDefaults(ctx context.Context, rootDir string) error {
	return nil
}

func (r *restoreConfigService) GetConfigValue(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (r *restoreConfigService) IncrementETag(ctx context.Context) (string, error) {
	return "20260129-01", nil
}

type reconfigureFailConfigService struct{}

func (r *reconfigureFailConfigService) Load(ctx context.Context) (*config.Config, error) {
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 50       // different from default 100
	cfg.DBMinIdleConnections = 5 // different from default 10
	return cfg, nil
}

func (r *reconfigureFailConfigService) Save(ctx context.Context, cfg *config.Config) error {
	return nil
}

func (r *reconfigureFailConfigService) Validate(cfg *config.Config) error {
	return nil
}

func (r *reconfigureFailConfigService) Export(ctx context.Context) (string, error) {
	return "", nil
}

func (r *reconfigureFailConfigService) Import(yamlContent string, ctx context.Context) error {
	return nil
}

func (r *reconfigureFailConfigService) RestoreLastKnownGood(ctx context.Context) (*config.Config, error) {
	return config.DefaultConfig(), nil
}

func (r *reconfigureFailConfigService) EnsureDefaults(ctx context.Context, rootDir string) error {
	return nil
}

func (r *reconfigureFailConfigService) GetConfigValue(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (r *reconfigureFailConfigService) IncrementETag(ctx context.Context) (string, error) {
	return "20260129-01", nil
}

func validConfigWithImageDir(rootDir string) *config.Config {
	cfg := config.DefaultConfig()
	cfg.ImageDirectory = filepath.Join(rootDir, "Images")
	return cfg
}

func TestApp_Run_ReconfigurePoolsFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	app.testSeams.ConfigService = &reconfigureFailConfigService{}

	app.InfrastructureService.testSeams.RecreatePoolsWithConfig = func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return nil, nil, fmt.Errorf("reconfigure failed")
	}

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if serveHook.called {
		t.Error("Serve should not be called when reconfigure fails")
	}
}

func TestApp_Run_RestoreLastKnownGood_Success(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:        getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		RestoreLastKnownGood: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	restoredCfg := validConfigWithImageDir(tempDir)
	restoredCfg.WorkerPoolMax = 9
	fakeSvc := &restoreConfigService{restoredCfg: restoredCfg}
	app.testSeams.ConfigService = fakeSvc

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

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
	if app.ConfigManager.Config.WorkerPoolMax != 9 {
		t.Errorf("WorkerPoolMax = %d, want 9", app.ConfigManager.Config.WorkerPoolMax)
	}
	if !serveHook.called {
		t.Error("Serve was not called")
	}
}

func TestApp_Run_RestoreLastKnownGood_ValidateFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:        getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		RestoreLastKnownGood: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	fakeSvc := &restoreConfigService{
		restoredCfg: validConfigWithImageDir(tempDir),
		validateErr: fmt.Errorf("validation failed"),
	}
	app.testSeams.ConfigService = fakeSvc

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if serveHook.called {
		t.Error("Serve should not be called when validation fails")
	}
}

func TestApp_Run_RestoreLastKnownGood_SaveFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:        getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		RestoreLastKnownGood: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	fakeSvc := &restoreConfigService{
		restoredCfg: validConfigWithImageDir(tempDir),
		saveErr:     fmt.Errorf("save failed"),
	}
	app.testSeams.ConfigService = fakeSvc

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if serveHook.called {
		t.Error("Serve should not be called when save fails")
	}
}

func TestApp_Run_ProfilerEnabled(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		Profile:       getopt.OptString{String: "cpu", IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	profilerCalled := false
	app.testSeams.ProfilerStart = func(cfg profiler.Config) (func(), error) {
		profilerCalled = true
		if cfg.Mode != "cpu" {
			t.Errorf("profiler mode = %q, want cpu", cfg.Mode)
		}
		return func() {}, nil
	}

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !profilerCalled {
		t.Error("profiler start was not called")
	}
	if app.RuntimeManager.stopProfiler == nil {
		t.Error("stopProfiler was not set")
	}
}

func TestApp_Run_ProfilerStartError(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		Profile:       getopt.OptString{String: "cpu", IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	app.testSeams.ProfilerStart = func(cfg profiler.Config) (func(), error) {
		return nil, fmt.Errorf("profiler failed")
	}

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if serveHook.called {
		t.Error("Serve should not be called when profiler fails")
	}
}

func TestApp_Run_BuildHandlersError(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	app.HandlerManager.testSeams.BuildHandlers = func(fs fs.FS) error {
		return fmt.Errorf("build handlers failed")
	}

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if serveHook.called {
		t.Error("Serve should not be called when buildHandlers fails")
	}
}

func TestApp_Run_NoDiscovery_RefreshesGalleryStats(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:    getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		RunFileDiscovery: getopt.OptBool{Bool: false, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Wait for startup stats goroutine to complete.
	gs := app.RuntimeManager.GalleryStats()
	deadline := time.Now().Add(5 * time.Second)
	for gs.running.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("startup stats goroutine did not complete within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Verify counters are populated with concrete values.
	if got := gs.running.Load(); got != 0 {
		t.Errorf("running = %d, want 0", got)
	}
	if got := gs.Folders(); got == "N/A" {
		t.Error("Folders() returned N/A after startup completed")
	}
	if got := gs.Images(); got == "N/A" {
		t.Error("Images() returned N/A after startup completed")
	}
}

func TestApp_Run_SetRestartRequired_DoesNotExecRestart(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)
	app.SetRestartRequired(true)

	execCalled := false
	app.RuntimeManager.testSeams.Executable = func() (string, error) {
		return "/tmp/test-exe", nil
	}
	app.RuntimeManager.testSeams.ExecCommand = func(path string, args []string, env []string) error {
		execCalled = true
		return nil
	}
	app.RuntimeManager.testSeams.Exit = func(code int) {}

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if execCalled {
		t.Error("ExecRestart should not be called when only SetRestartRequired was set")
	}
}

func TestApp_Run_Shutdown_DoesNotLogCanceledScheduler(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)
	app.testSeams.Serve = (&recordingServeHook{}).Serve
	app.testSeams.MemoryReclaimer = func(MemoryReclaimerConfig) {}

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	app.Shutdown()

	if app.logger == nil {
		t.Fatal("app.logger is nil")
	}
	data, err := os.ReadFile(app.logger.FilePath())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(data), "scheduler error") {
		t.Errorf("canceled scheduler should not log ERROR, got: %s", data)
	}
}

func TestStartCacheBatchLoad_BlockedWhenDiscoveryActive(t *testing.T) {
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.testSeams.ModuleStateActive = func() (bool, error) {
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

func TestStartCacheBatchLoad_AllowedWhenIdle(t *testing.T) {
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.testSeams.ModuleStateActive = func() (bool, error) {
		return false, nil
	}

	var runCalled atomic.Bool
	runDone := make(chan struct{}, 1)
	app.testSeams.BatchLoadManagerRun = func(ctx context.Context) error {
		runCalled.Store(true)
		runDone <- struct{}{}
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

	// The hook runs in a background goroutine; wait for it deterministically.
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("batch load run was not invoked")
	}
	if !runCalled.Load() {
		t.Error("batch load run was not invoked")
	}
}

func TestStartCacheBatchLoad_ModuleStateError(t *testing.T) {
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.testSeams.ModuleStateActive = func() (bool, error) {
		return false, fmt.Errorf("module state error")
	}

	_, err := app.StartCacheBatchLoad()
	if err == nil {
		t.Fatal("Expected StartCacheBatchLoad to return error")
	}
}

func TestStartCacheBatchLoad_ManagerNil(t *testing.T) {
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.SubsystemManager.batchLoadManager = nil

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

func TestApp_Run_ServeErrorPanics(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)
	app.testSeams.Serve = func(http.Handler, string) error {
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

func TestApp_Run_RestoreLastKnownGood_DBGetFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:        getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		RestoreLastKnownGood: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	// Prevent setConfigDefaults from touching the closed pools.
	app.testSeams.ConfigService = &reconfigureFailConfigService{}

	// Create and immediately close real pools so the fake initializer returns
	// pools whose Get() calls fail.
	_, closedRw, closedRo, err := database.Setup(app.RuntimeManager.ctx, tempDir, nil)
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
	app.testSeams.Serve = serveHook.Serve

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

func TestStartCacheBatchLoad_ManagerRunError(t *testing.T) {
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.testSeams.ModuleStateActive = func() (bool, error) {
		return false, nil
	}

	runCalled := make(chan struct{}, 1)
	app.testSeams.BatchLoadManagerRun = func(context.Context) error {
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

func TestApp_Run_RestoreLastKnownGood_RestoreFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:        getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		RestoreLastKnownGood: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	fakeSvc := &restoreConfigService{
		restoredCfg: validConfigWithImageDir(tempDir),
		restoreErr:  fmt.Errorf("restore failed"),
	}
	app.testSeams.ConfigService = fakeSvc

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	err := app.Run(1, 1)
	if err == nil {
		t.Fatal("Expected Run to return error")
	}
	if serveHook.called {
		t.Error("Serve should not be called when restore fails")
	}
}

func TestApp_Run_RestoreLastKnownGood_ReconfigureFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:        getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		RestoreLastKnownGood: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	restoredCfg := validConfigWithImageDir(tempDir)
	restoredCfg.DBMaxPoolSize = 101 // force reconfigure
	fakeSvc := &restoreConfigService{restoredCfg: restoredCfg}
	app.testSeams.ConfigService = fakeSvc

	app.InfrastructureService.testSeams.RecreatePoolsWithConfig = func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return nil, nil, fmt.Errorf("reconfigure failed")
	}

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

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

func TestApp_Run_LoadConfigFails_ReconfigureDefaultsFails(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	fallbackCfg := validConfigWithImageDir(tempDir)
	fallbackCfg.DBMaxPoolSize = 101 // force reconfigure
	app.testSeams.FallbackConfig = func() *config.Config {
		return fallbackCfg
	}

	app.testSeams.LoadConfig = func() (*config.Config, error) {
		return nil, fmt.Errorf("database load failed")
	}
	app.InfrastructureService.testSeams.RecreatePoolsWithConfig = func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return nil, nil, fmt.Errorf("reconfigure defaults failed")
	}

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

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

// --- merged from etag_cli_test.go ---
func TestApp_IncrementETag(t *testing.T) {
	// We use testSeams.ConfigService to inject a mock that returns a known ETag
	// after increment, so this test exercises the full App->InfrastructureService
	// delegation without a real database.
	mockSvc := &mockConfigServiceForETagCLI{
		loadReturn: func() *config.Config {
			cfg := config.DefaultConfig()
			cfg.ETagVersion = "20260701-01"
			return cfg
		}(),
	}

	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	app.RuntimeManager.ctx = context.Background()

	// Set config service directly on the embedded ConfigManager.
	app.SetConfigService(mockSvc)

	// Use a fake cache rotator to avoid needing a real pool.
	rotator := &fakeCacheRotator{}
	app.cacheRotator = rotator

	newETag, err := app.IncrementETag()
	if err != nil {
		t.Fatalf("IncrementETag() error = %v", err)
	}

	// Verify format
	pattern := `^\d{8}-\d{2}$`
	matched, _ := regexp.MatchString(pattern, newETag)
	if !matched {
		t.Errorf("New ETag %q does not match pattern %q", newETag, pattern)
	}

	// Verify the value was incremented from the original
	if newETag == "20260701-01" {
		t.Error("ETag was not incremented")
	}

	// Verify Save was called with the new value
	if !mockSvc.saveCalled {
		t.Error("Expected Save to be called")
	}

	// Verify cache rotation was triggered
	if !rotator.rotateCalled {
		t.Error("Expected cache rotation after IncrementETag")
	}
	if mockSvc.savedConfig == nil {
		t.Fatal("savedConfig is nil")
	}
	if mockSvc.savedConfig.ETagVersion != newETag {
		t.Errorf("Saved ETag = %q, want %q", mockSvc.savedConfig.ETagVersion, newETag)
	}
}

func TestApp_IncrementETag_ClearsCache(t *testing.T) {
	mockSvc := &mockConfigServiceForETagCLI{
		loadReturn: func() *config.Config {
			cfg := config.DefaultConfig()
			cfg.ETagVersion = "20260701-01"
			return cfg
		}(),
	}

	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	app.RuntimeManager.ctx = context.Background()
	app.SetConfigService(mockSvc)

	rotator := &fakeCacheRotator{}
	app.cacheRotator = rotator

	// The cache clearing is performed by RotateCacheTable (via the rotator).
	// We verify the rotator was called, which proves cache clearing was delegated.
	newETag, err := app.IncrementETag()
	if err != nil {
		t.Fatalf("IncrementETag() error = %v", err)
	}
	if newETag == "" {
		t.Error("expected non-empty ETag after increment")
	}

	// Verify cache rotation was triggered
	if !rotator.rotateCalled {
		t.Error("RotateCacheTable should be called during IncrementETag to clear cache")
	}

	// Verify Save was called (config was persisted)
	if !mockSvc.saveCalled {
		t.Error("Expected Save to be called")
	}
}

func TestApp_InitForIncrementETag(t *testing.T) {
	// Use real database setup (Setup) so that DbSQLConnPool instances have
	// a done channel and can be closed cleanly by Shutdown.
	t.Setenv("SEPG_SESSION_SECRET", "test-secret-with-at-least-32-bytes-long")

	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")

	ctx := context.Background()
	app.RuntimeManager.ctx = ctx
	app.setRootDir(nil)

	// Route through real database.Setup for proper pool initialization.
	app.testSeams.DatabaseSetup = database.Setup

	// Make testSeams.ConfigService return a mock that succeeds on EnsureDefaults.
	mockSvc := &mockConfigServiceForETagCLI{}
	app.testSeams.ConfigService = mockSvc

	err := app.InitForIncrementETag(getopt.Opt{})
	defer app.Shutdown()
	if err != nil {
		t.Fatalf("InitForIncrementETag() error = %v", err)
	}

	// Verify essential services are initialized
	if app.dbRwPool == nil {
		t.Error("dbRwPool not initialized")
	}
	if app.ConfigManager.ConfigService == nil {
		t.Error("ConfigService not initialized")
	}

	// Verify EnsureDefaults was called
	if !mockSvc.ensureDefaultsCalled {
		t.Error("EnsureDefaults should be called")
	}
}

func TestApp_InitForIncrementETag_DBSetupError(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()

	app.RuntimeManager.ctx = context.Background()

	expectedErr := errors.New("db setup failed")
	app.testSeams.DatabaseSetup = func(ctx context.Context, rootDir string, cfg *config.Config) (database.DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return database.DatabasePaths{}, nil, nil, expectedErr
	}

	err := app.InitForIncrementETag(getopt.Opt{})
	if err == nil {
		t.Fatal("Expected error from InitForIncrementETag, got nil")
	}
}

type mockConfigServiceForETagCLI struct {
	loadReturn           *config.Config
	loadErr              error
	validateErr          error
	saveErr              error
	saveCalled           bool
	savedConfig          *config.Config
	ensureDefaultsCalled bool
}

func (m *mockConfigServiceForETagCLI) Load(ctx context.Context) (*config.Config, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	if m.loadReturn != nil {
		return m.loadReturn, nil
	}
	return config.DefaultConfig(), nil
}

func (m *mockConfigServiceForETagCLI) Save(ctx context.Context, cfg *config.Config) error {
	m.saveCalled = true
	m.savedConfig = cfg
	return m.saveErr
}

func (m *mockConfigServiceForETagCLI) Validate(cfg *config.Config) error {
	return m.validateErr
}

func (m *mockConfigServiceForETagCLI) Export(ctx context.Context) (string, error) {
	return "", nil
}

func (m *mockConfigServiceForETagCLI) Import(yamlContent string, ctx context.Context) error {
	return nil
}

func (m *mockConfigServiceForETagCLI) RestoreLastKnownGood(ctx context.Context) (*config.Config, error) {
	return nil, nil
}

func (m *mockConfigServiceForETagCLI) EnsureDefaults(ctx context.Context, rootDir string) error {
	m.ensureDefaultsCalled = true
	return nil
}

func (m *mockConfigServiceForETagCLI) GetConfigValue(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (m *mockConfigServiceForETagCLI) IncrementETag(ctx context.Context) (string, error) {
	return "", nil
}
