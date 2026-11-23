package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/discovery"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/metadata"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/modulestate"
	"github.com/lbe/sfpg-go/internal/workerpool"
	"github.com/lbe/sfpg-go/web"
)

// testExtractor is the one metadata.Extractor the package's tests share. It
// runs one ExifTool process, which TestMain closes after the tests.
var testExtractor *metadata.Extractor //nolint:gochecknoglobals // R9 allows unexported test-file identifiers; TestMain sets it once.

func TestMain(m *testing.M) {
	testExtractor = metadata.New()
	code := m.Run()
	if err := testExtractor.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close test extractor: %v\n", err)
	}
	os.Exit(code)
}

func init() {
	seedTestDatabase = seedTestDBIfAbsent
}

// seedTestDBIfAbsent copies main and thumbs test database files into rootDir when
// sfpg.db is absent.
func seedTestDBIfAbsent(rootDir string) error {
	dbPath := filepath.Join(rootDir, "DB", "sfpg.db")
	_, statErr := os.Stat(dbPath)
	if statErr == nil {
		return nil
	}
	if !os.IsNotExist(statErr) {
		return statErr
	}
	thumbsPath := filepath.Join(rootDir, "DB", "thumbs", "thumbs.db")
	if err := dbconnpool.CopyMainTestDatabaseTo(dbPath); err != nil {
		return err
	}
	if err := dbconnpool.CopyThumbsTestDatabaseTo(thumbsPath); err != nil {
		_ = os.Remove(dbPath)
		_ = os.Remove(thumbsPath)
		return err
	}
	return disableRestartAfterDiscovery(dbPath)
}

// disableRestartAfterDiscovery stores restart_after_discovery = false in the
// seeded database. The default is true, and a test app that requested a
// restart would re-exec the test binary; tests that want the restart set it.
func disableRestartAfterDiscovery(dbPath string) error {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO config (key, value) VALUES ('restart_after_discovery', 'false')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	return err
}

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

// failOnExec makes a re-exec of the test binary fail the test. Run reads the
// developer's config, which can enable restart_after_discovery, so a test that
// does not expect a restart must not reach syscall.Exec. Exit is stubbed too:
// a failed exec would otherwise end the test process.
func failOnExec(t *testing.T, app *App) {
	t.Helper()
	app.RuntimeManager.testSeams.ExecCommand = func(string, []string, []string) error {
		t.Error("unexpected process exec")
		return errors.New("unexpected process exec")
	}
	app.RuntimeManager.testSeams.Exit = func(int) {}
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

// CreateApp sets up a fast, isolated application instance for testing.
// It wires ConfigService (setDB), writebatcher, the discovery Service,
// session/store (ensureSession), and handlers (buildHandlers). It deliberately
// does not call SubsystemManager.Start (no preload manager or pool
// reconfiguration) so most tests stay lightweight.
//
// Tests that need full production subsystem wiring should use
// startTestManager (subsystem_manager_integration_test.go).
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
		envOpt := getopt.Opt{}
		getopt.ApplyEnvVars(&envOpt)
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
		if !opt.LogLevel.IsSet && envOpt.LogLevel.IsSet {
			opt.LogLevel = envOpt.LogLevel
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
	app.StartWriteBatcher(app.RuntimeManager.ctx, true, config.DefaultDQueMaxDiskBytes)

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

	wireDiscovery(app)

	if cfg.startPool {
		app.SubsystemManager.pool.MinWorkers = 1
		app.SubsystemManager.pool.MaxWorkers = 1
		app.RuntimeManager.poolDone = make(chan struct{})
		app.SubsystemManager.StartPool(app.RuntimeManager.poolDone)
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

// wireDiscovery builds the discovery Service the way SubsystemManager.Start
// does, without the preload manager and pool reconfiguration. Discovery runs
// need a pool, so it reads app.SubsystemManager.pool. CreateApp calls it once.
func wireDiscovery(app *App) {
	sm := app.SubsystemManager
	sm.fileProcessor = files.NewFileProcessor(app.imagesDir, testExtractor)
	sm.moduleStateService = modulestate.NewService(app.dbRwPool)
	sm.stats = &discovery.Stats{}
	svc, err := discovery.New(discovery.Config{
		ImagesDir:           app.imagesDir,
		NormalizedImagesDir: app.normalizedImagesDir,
		BacklogDir:          app.discoveryDQueDirPath,
		RemovePrefix:        removeImagesDirPrefix,
	}, discovery.Deps{
		Processor: sm.fileProcessor,
		Writer:    app.writer,
		Catalog:   database.NewDiscoveryDirEntMapFunc(app.dbRoPool, app.normalizedImagesDir, removeImagesDirPrefix),
		State:     sm.moduleStateService,
		Pool:      sm.pool,
		Stats:     sm.stats,
	})
	if err != nil {
		panic(err)
	}
	sm.discovery = svc
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
