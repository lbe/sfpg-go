package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/workerpool"
	"github.com/lbe/sfpg-go/web"
)

// Thumb is a minimal test struct used across server test files.
type Thumb struct {
	ID        int64
	Path      string
	ThumbPath string
	DispName  string
	IsImage   bool
}

// setenvForTest sets an environment variable and registers a cleanup to restore
// the original value. Unlike t.Setenv, this does not block t.Parallel(), allowing
// tests to use both environment configuration and parallel execution.
func setenvForTest(tb testing.TB, key, value string) {
	tb.Helper()
	prev, ok := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		tb.Fatalf("failed to set env %s: %v", key, err)
	}
	tb.Cleanup(func() {
		if ok {
			os.Setenv(key, prev)
		} else {
			os.Unsetenv(key)
		}
	})
}

// AppOption configures the application test instance created by CreateApp.
type AppOption func(*appCfg)

type appCfg struct {
	startPool bool
	rootDir   string // empty = use TempDir
	opt       getopt.Opt
}

// WithPool enables the worker pool in the test App instance.
func WithPool() AppOption {
	return func(cfg *appCfg) {
		cfg.startPool = true
	}
}

// WithRoot uses an existing root directory instead of a temporary directory.
// Useful for simulating restarts while preserving the same database.
func WithRoot(path string) AppOption {
	return func(cfg *appCfg) {
		cfg.rootDir = path
	}
}

// WithGetoptOpt sets startup options (getopt.Opt) for the test App instance.
func WithGetoptOpt(opt getopt.Opt) AppOption {
	return func(cfg *appCfg) {
		cfg.opt = opt
	}
}

// CreateApp sets up a full, isolated application instance for testing.
// It creates and wires services in order: ConfigService (setDB), FileProcessor, session/store
// (ensureSession), Handlers (buildHandlers). All services are non-nil when CreateApp returns.
//
// Options:
//   - WithPool() — start the background worker pool
//   - WithRoot(dir) — use an existing root directory
//   - WithGetoptOpt(opt) — supply getopt.Opt values
func CreateApp(t testing.TB, opts ...AppOption) *App {
	t.Helper()

	cfg := appCfg{
		startPool: false,
		rootDir:   "",
		opt:       getopt.Opt{},
	}
	for _, o := range opts {
		o(&cfg)
	}

	var tempDir string
	if cfg.rootDir == "" {
		tempDir = t.TempDir()
	}

	// Set default session flags without using t.Setenv (which blocks t.Parallel).
	// Use os.Setenv + t.Cleanup instead to allow tests to use t.Parallel().
	setenvForTest(t, "SEPG_SESSION_SECURE", func() string {
		if val, ok := os.LookupEnv("SEPG_SESSION_SECURE"); ok {
			return val
		}
		return "false"
	}())
	setenvForTest(t, "SEPG_SESSION_HTTPONLY", func() string {
		if val, ok := os.LookupEnv("SEPG_SESSION_HTTPONLY"); ok {
			return val
		}
		return "false"
	}())

	opt := cfg.opt

	// Parse environment variables into opt if SessionSecret wasn't explicitly provided
	// This allows tests to use t.Setenv() and have those values applied
	if !opt.SessionSecret.IsSet {
		envOpt := getopt.ParseEnvOnly()
		// Merge env opt with provided opt, giving precedence to explicitly set values in opt
		if !opt.Port.IsSet && envOpt.Port.IsSet {
			opt.Port = envOpt.Port
		}
		if !opt.RunFileDiscovery.IsSet && envOpt.RunFileDiscovery.IsSet {
			opt.RunFileDiscovery = envOpt.RunFileDiscovery
		}
		if !opt.DebugDelayMS.IsSet && envOpt.DebugDelayMS.IsSet {
			opt.DebugDelayMS = envOpt.DebugDelayMS
		}
		if !opt.Profile.IsSet && envOpt.Profile.IsSet {
			opt.Profile = envOpt.Profile
		}
		if !opt.EnableHTTPCache.IsSet && envOpt.EnableHTTPCache.IsSet {
			opt.EnableHTTPCache = envOpt.EnableHTTPCache
		}
		if !opt.SessionSecret.IsSet && envOpt.SessionSecret.IsSet {
			opt.SessionSecret = envOpt.SessionSecret
		}
		if !opt.SessionSecure.IsSet && envOpt.SessionSecure.IsSet {
			opt.SessionSecure = envOpt.SessionSecure
		}
		if !opt.SessionHttpOnly.IsSet && envOpt.SessionHttpOnly.IsSet {
			opt.SessionHttpOnly = envOpt.SessionHttpOnly
		}
		if !opt.SessionMaxAge.IsSet && envOpt.SessionMaxAge.IsSet {
			opt.SessionMaxAge = envOpt.SessionMaxAge
		}
		if !opt.SessionSameSite.IsSet && envOpt.SessionSameSite.IsSet {
			opt.SessionSameSite = envOpt.SessionSameSite
		}
		// Merge LoginRateLimitPerIP so setenvForTest(t, "SEPG_LOGIN_RATE_LIMIT_PER_IP", ...)
		// applies in integration tests the same way as other SEPG_* session/security vars.
		if !opt.LoginRateLimitPerIP.IsSet && envOpt.LoginRateLimitPerIP.IsSet {
			opt.LoginRateLimitPerIP = envOpt.LoginRateLimitPerIP
		}
	}

	// Ensure SessionSecret is set in opt if not already provided
	if opt.SessionSecret.String == "" {
		opt.SessionSecret.String = "this-is-a-test-secret-with-min-32-bytes"
		opt.SessionSecret.IsSet = true
	}
	app := New(opt, "x.y.z")
	app.SubsystemManager.pool = workerpool.NewPool(app.RuntimeManager.ctx, 4, 4, 10*time.Second)

	if cfg.rootDir != "" {
		app.setRootDir(&cfg.rootDir)
	} else {
		safeTestName := strings.ReplaceAll(t.Name(), "/", "_")
		testSpecificDir := filepath.Join(tempDir, safeTestName)
		if err := os.MkdirAll(testSpecificDir, 0o755); err != nil {
			t.Fatalf("Failed to create test-specific directory: %v", err)
		}
		app.setRootDir(&testSpecificDir)
	}

	app.setupBootstrapLogging()
	app.setDB()
	app.setConfigDefaults()

	// Load configuration with env vars applied from opt
	if err := app.loadConfig(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := app.reconfigurePoolsFromConfig(); err != nil {
		t.Fatalf("reconfigure pools: %v", err)
	}
	app.StartWriteBatcher(app.RuntimeManager.ctx, true)

	// When using an existing root directory, apply config to ensure ETag and other settings
	if cfg.rootDir != "" {
		app.ApplyConfig()
	}

	// Create Images directory for tests (most tests need it to exist)
	app.imagesDir = filepath.Join(app.rootDir, "Images")
	if mkdirErr := os.MkdirAll(app.imagesDir, 0o755); mkdirErr != nil {
		t.Fatalf("Failed to ensure Images directory: %v", mkdirErr)
	}
	app.normalizedImagesDir = filepath.ToSlash(app.imagesDir)

	app.SubsystemManager.q = queue.NewQueue[string](10_000)

	// Initialize FileProcessor for tests
	app.SubsystemManager.fileProcessor = files.NewFileProcessor(app.dbRoPool, app.dbRwPool, app.ImporterFactory, app.imagesDir, newFileBatcher(app.writeBatcher))

	if cfg.startPool {
		app.SubsystemManager.pool.MinWorkers = 1
		app.SubsystemManager.pool.MaxWorkers = 1
		app.RuntimeManager.poolDone = make(chan struct{})
		pf := files.NewPoolFuncWithProcessor(app.SubsystemManager.fileProcessor, app.SubsystemManager.q, app.normalizedImagesDir, removeImagesDirPrefix, nil)
		go func() {
			defer close(app.RuntimeManager.poolDone)
			app.SubsystemManager.pool.StartWorkerPool(pf, app.dbRoPool, app.dbRwPool, app.SubsystemManager.q.Len)
		}()
	}

	// Session: ensureSession creates store and sessionManager.
	app.ensureSession()
	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("build handlers: %v", err)
	}

	// Register cleanup to shut down background goroutines (writebatcher, DB pools, worker pool).
	// This prevents goroutine leaks across tests, which otherwise cause timeouts under -race.
	t.Cleanup(func() { app.Shutdown() })

	return app
}

// MakeAuthCookie creates an authenticated session cookie for testing.
func MakeAuthCookie(t *testing.T, app *App) *http.Cookie {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	session, err := app.SessionAuthFacade.store.Get(req, "session-name")
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	session.Values["authenticated"] = true
	if err := session.Save(req, rr); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}
	return rr.Result().Cookies()[0]
}
