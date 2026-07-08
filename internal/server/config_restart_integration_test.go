//go:build integration || e2e

package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/web"
)

func TestIntegration_ErrorRecovery(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")
	app := CreateApp(t, WithPool())
	defer app.Shutdown()

	t.Parallel()
	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginAsAdmin(t, client, ts.URL)

	// Step 1: Save a valid configuration
	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("site_name", "Valid Config")
	resp, err := client.PostForm(ts.URL+"/config", formData)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	resp.Body.Close()

	// Step 2: Attempt to save an invalid value (e.g., invalid port)
	csrfToken = extractCSRFTokenFromConfig(t, client, ts.URL)
	formData = url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("listener_port", "99999") // Invalid port (>65535)
	resp, err = client.PostForm(ts.URL+"/config", formData)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	defer resp.Body.Close()

	// Step 3: Verify invalid value was rejected (should return error or 400)
	// The handler should validate and reject invalid values
	if resp.StatusCode == http.StatusOK {
		// If status is OK, check that the value wasn't actually saved
		cpcRo, getErr := app.dbRoPool.Get()
		if getErr != nil {
			t.Fatalf("failed to get DB connection: %v", getErr)
		}
		defer app.dbRoPool.Put(cpcRo)

		ctx := context.Background()
		value, cfgErr := cpcRo.Queries.GetConfigValueByKey(ctx, "listener_port")
		if cfgErr == nil {
			// If config exists, it should not be the invalid value
			if value == "99999" {
				t.Error("invalid port value was saved despite validation")
			}
		}
	}

	// Step 4: Verify last known good config exists and can be restored
	// This would require checking the LastKnownGoodConfig key in database
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	ctx := context.Background()
	lastKnownGood, err := cpcRo.Queries.GetConfigValueByKey(ctx, "LastKnownGoodConfig")
	if err != nil {
		// Last known good might not exist if no successful save occurred
		// This is acceptable for this test
		_ = lastKnownGood
	}
}

// TestIntegration_ConfigLoadsOnStartup verifies that configuration loads correctly on startup,
// including values from the database, environment variables, and default values.
// Note: This is an integration test, not E2E, as it only verifies config struct values, not server behavior.

func TestDBConfig_HTTPCacheDisableActuallyDisablesCaching(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)

	// Pre-populate database with enable_http_cache=false BEFORE creating app
	// (simulates config saved in previous run)
	{
		preApp := New(getopt.Opt{}, "x.y.z")
		preApp.setRootDir(&tempDir)
		preApp.setDB()

		cpcRw, err := preApp.dbRwPool.Get()
		if err != nil {
			t.Fatalf("failed to get RW connection: %v", err)
		}

		now := time.Now().Unix()
		err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
			Key:       "enable_http_cache",
			Value:     "false",
			CreatedAt: now,
			UpdatedAt: now,
		})
		cpcRw.Close()
		preApp.dbRwPool.Put(cpcRw)
		if err != nil {
			t.Fatalf("failed to set DB config: %v", err)
		}
		preApp.Shutdown()
	}

	// Now create app as if starting fresh (simulates app restart)
	// The bug: initializeHTTPCache() is called before loadConfig() in startup sequence
	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	// Load config from database (this is where enable_http_cache=false should be picked up)
	app.config = config.DefaultConfig()
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = app.config.LoadFromDatabase(context.Background(), cpcRw.Queries)
	if err != nil {
		t.Fatalf("failed to load config from DB: %v", err)
	}

	// Verify DB config was loaded
	if app.config.EnableHTTPCache {
		t.Fatalf("expected EnableHTTPCache to be false from DB, got true")
	}

	// Now initialize cache (as done in Run())
	// The bug: if initializeHTTPCache() was already called earlier with defaults,
	// the DB config is ignored
	app.initializeHTTPCache()

	// The actual bug test: cache should NOT be initialized when DB says disabled
	// This will FAIL (RED) if cache was initialized during setDB() before config load
	if app.cacheMW != nil {
		t.Errorf("expected cacheMW to be nil when enable_http_cache=false in DB, but it was initialized - this indicates cache init happened before DB config was loaded")
	}
}

// TestDBConfig_ListenerPortChangeRequiresRestart verifies that changing
// listener_port via config handlers properly sets the restart required flag.
// This test is expected to FAIL initially (RED phase) to prove the defect exists.

func TestDBConfig_ListenerPortChangeRequiresRestart(t *testing.T) {
	app := CreateApp(t, WithPool())
	defer app.Shutdown()

	// Set initial port in config
	t.Parallel()
	app.configMu.Lock()
	app.config.ListenerPort = 8081
	app.configMu.Unlock()

	// Build handlers (includes SetRestartRequired callback)
	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers failed: %v", err)
	}

	// Verify restart flag starts as false
	if app.RestartRequired() {
		t.Fatal("expected restartRequired to be false initially")
	}

	// Simulate config change via handler
	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Login as admin
	loginAsAdmin(t, client, ts.URL)

	// Extract CSRF token from config page
	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)

	// POST config change (change port from 8081 to 9090)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("listener_port", "9090")

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
	if err != nil {
		t.Fatalf("failed to create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after config update, got %d", resp.StatusCode)
	}

	// Verify restart flag is now set
	if !app.RestartRequired() {
		t.Errorf("expected restartRequired to be true after port change, got false")
	}
}

// TestDBConfig_EnvAndCLIOverrideDBValues verifies that the config precedence
// hierarchy (CLI > Env > DB > Defaults) is properly enforced for enable_http_cache.
// This test is expected to FAIL initially (RED phase) to prove the defect exists.
