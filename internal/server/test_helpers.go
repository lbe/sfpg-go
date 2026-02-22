package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.local/sfpg/internal/getopt"
	"go.local/sfpg/internal/queue"
	"go.local/sfpg/internal/server/files"
	"go.local/sfpg/internal/workerpool"
	"go.local/sfpg/web"
)

// CreateApp sets up a full, isolated application instance for testing.
func CreateApp(t *testing.T, startPool bool) *App {
	t.Helper()
	return CreateAppWithOpt(t, startPool, getopt.Opt{})
}

// CreateAppWithTB sets up a full, isolated application instance for benchmarks or tests.
// Use CreateApp for regular tests; use CreateAppWithTB(b, false) in benchmarks.
func CreateAppWithTB(tb testing.TB, startPool bool) *App {
	tb.Helper()
	return CreateAppWithOpt(tb, startPool, getopt.Opt{})
}

// CreateAppWithOpt sets up a full, isolated application instance for testing with custom options.
// It creates and wires services in order: ConfigService (setDB), FileProcessor, session/store/restartCh
// (ensureSessionAndRestart), Handlers (buildHandlers). All services are non-nil when CreateApp returns.
func CreateAppWithOpt(tb testing.TB, startPool bool, opt getopt.Opt) *App {
	tb.Helper()
	tempDir := tb.TempDir()

	// Use tb.Setenv to set default session flags for tests that don't explicitly configure them
	// Tests that need specific values should call tb.Setenv() before calling CreateApp
	if val, ok := os.LookupEnv("SEPG_SESSION_SECURE"); !ok {
		tb.Setenv("SEPG_SESSION_SECURE", "false")
	} else {
		// Re-set to preserve the value that was set before this function was called
		tb.Setenv("SEPG_SESSION_SECURE", val)
	}
	if val, ok := os.LookupEnv("SEPG_SESSION_HTTPONLY"); !ok {
		tb.Setenv("SEPG_SESSION_HTTPONLY", "false")
	} else {
		// Re-set to preserve the value that was set before this function was called
		tb.Setenv("SEPG_SESSION_HTTPONLY", val)
	}

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
		if !opt.EnableCompression.IsSet && envOpt.EnableCompression.IsSet {
			opt.EnableCompression = envOpt.EnableCompression
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
	}

	// Ensure SessionSecret is set in opt if not already provided
	if opt.SessionSecret.String == "" {
		opt.SessionSecret.String = "this-is-a-test-secret"
		opt.SessionSecret.IsSet = true
	}
	app := New(opt, "x.y.z")
	app.pool = workerpool.NewPool(app.ctx, 4, 4, 10*time.Second)

	safeTestName := strings.ReplaceAll(tb.Name(), "/", "_")
	testSpecificDir := filepath.Join(tempDir, safeTestName)
	err := os.MkdirAll(testSpecificDir, 0o755)
	if err != nil {
		tb.Fatalf("Failed to create test-specific directory: %v", err)
	}

	app.setRootDir(&testSpecificDir)
	app.setupBootstrapLogging()
	app.setDB()
	app.setConfigDefaults()
	// Load configuration with env vars applied from opt
	if cfgErr := app.loadConfig(); cfgErr != nil {
		tb.Fatalf("Failed to load config: %v", err)
	}
	// Create Images directory for tests (most tests need it to exist)
	// In production, this is done via setImageDirectory() after config is loaded
	app.imagesDir = filepath.Join(testSpecificDir, "Images")
	if mkdirErr := os.MkdirAll(app.imagesDir, 0o755); mkdirErr != nil {
		tb.Fatalf("Failed to create Images directory: %v", mkdirErr)
	}
	app.normalizedImagesDir = filepath.ToSlash(app.imagesDir)

	app.q = queue.NewQueue[string](10_000)

	// Initialize FileProcessor for tests
	app.fileProcessor = files.NewFileProcessor(app.dbRoPool, app.dbRwPool, app.ImporterFactory, app.imagesDir, newBatcherAdapter(app.writeBatcher))

	if startPool {
		app.pool.MinWorkers = 1
		app.pool.MaxWorkers = 1
		app.poolDone = make(chan struct{})
		pf := files.NewPoolFuncWithProcessor(app.fileProcessor, app.q, app.normalizedImagesDir, removeImagesDirPrefix, nil)
		go func() {
			defer close(app.poolDone)
			app.pool.StartWorkerPool(pf, app.dbRoPool, app.dbRwPool, app.q.Len)
		}()
	}

	// Session and restart: ensureSessionAndRestart creates store, sessionManager, restartCh.
	app.ensureSessionAndRestart()
	if err := app.buildHandlers(web.FS); err != nil {
		tb.Fatalf("build handlers: %v", err)
	}

	return app
}

// CreateAppWithRoot sets up a full application instance using an existing root directory.
// Useful for simulating restarts while preserving the same database.
func CreateAppWithRoot(t *testing.T, startPool bool, rootDir string) *App {
	t.Helper()
	return CreateAppWithRootAndOpt(t, startPool, rootDir, getopt.Opt{})
}

// CreateAppWithRootAndOpt sets up a full application instance using an existing root directory and options.
func CreateAppWithRootAndOpt(t *testing.T, startPool bool, rootDir string, opt getopt.Opt) *App {
	t.Helper()

	// Ensure SessionSecret is set in opt if not already provided
	if opt.SessionSecret.String == "" {
		opt.SessionSecret.String = "this-is-a-test-secret"
		opt.SessionSecret.IsSet = true
	}
	app := New(opt, "x.y.z")
	app.pool = workerpool.NewPool(app.ctx, 4, 4, 10*time.Second)

	app.setRootDir(&rootDir)
	app.setupBootstrapLogging()
	app.setDB()
	app.setConfigDefaults()

	// Load and apply config to ensure ETag and other settings are loaded
	if err := app.loadConfig(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	app.applyConfig()

	// Use existing Images directory if it exists, otherwise create it
	app.imagesDir = filepath.Join(rootDir, "Images")
	if mkdirErr := os.MkdirAll(app.imagesDir, 0o755); mkdirErr != nil {
		t.Fatalf("Failed to ensure Images directory: %v", mkdirErr)
	}
	app.normalizedImagesDir = filepath.ToSlash(app.imagesDir)

	app.q = queue.NewQueue[string](10_000)

	// Initialize FileProcessor for tests
	app.fileProcessor = files.NewFileProcessor(app.dbRoPool, app.dbRwPool, app.ImporterFactory, app.imagesDir, newBatcherAdapter(app.writeBatcher))

	if startPool {
		app.pool.MinWorkers = 1
		app.pool.MaxWorkers = 1
		app.poolDone = make(chan struct{})
		pf := files.NewPoolFuncWithProcessor(app.fileProcessor, app.q, app.normalizedImagesDir, removeImagesDirPrefix, nil)
		go func() {
			defer close(app.poolDone)
			app.pool.StartWorkerPool(pf, app.dbRoPool, app.dbRwPool, app.q.Len)
		}()
	}

	// Session and restart: ensureSessionAndRestart creates store, sessionManager, restartCh.
	app.ensureSessionAndRestart()
	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("build handlers: %v", err)
	}

	return app
}

// MakeAuthCookie creates an authenticated session cookie for testing.
// Includes a stable CSRF token to ensure consistent HTML output across requests.
func MakeAuthCookie(t *testing.T, app *App) *http.Cookie {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	session, _ := app.store.Get(req, "session-name")
	session.Values["authenticated"] = true
	// Include a stable CSRF token to ensure consistent HTML output for cache tests
	session.Values["csrf_token"] = "test-csrf-token-for-consistent-caching"
	if err := session.Save(req, rr); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}
	return rr.Result().Cookies()[0]
}
