//go:build e2eweb

package web_testsuite

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/testutil"
)

// =========================================================================
// Section 2 (NoAuth): Unauthenticated access — each auth-protected route
// should return 401 when no session cookie is present.
// =========================================================================

func TestAuthRoutes_NoAuth(t *testing.T) {
	type noAuthTest struct {
		num     int
		name    string
		method  string
		path    string
		body    url.Values
		hx      bool
		expCode int
	}

	tests := []noAuthTest{
		// Config Routes (GET)
		{num: 18, name: "config-get", method: "GET", path: "/config", expCode: http.StatusUnauthorized},
		{num: 20, name: "config-export-download", method: "GET", path: "/config/export/download", expCode: http.StatusUnauthorized},

		// Config Routes (POST)
		{num: 22, name: "config-post", method: "POST", path: "/config", expCode: http.StatusUnauthorized},
		{num: 24, name: "config-themes", method: "POST", path: "/config/themes", expCode: http.StatusUnauthorized},
		{num: 26, name: "config-increment-etag", method: "POST", path: "/config/increment-etag", expCode: http.StatusUnauthorized},
		{num: 28, name: "config-export-tofile", method: "POST", path: "/config/export/to-file", expCode: http.StatusUnauthorized},
		{num: 30, name: "config-import-preview", method: "POST", path: "/config/import/preview", expCode: http.StatusUnauthorized},
		{num: 32, name: "config-import-commit", method: "POST", path: "/config/import/commit", expCode: http.StatusUnauthorized},
		{num: 34, name: "config-restore-preview", method: "POST", path: "/config/restore-last-known-good?action=preview", expCode: http.StatusUnauthorized},
		{num: 36, name: "config-restart", method: "POST", path: "/config/restart", expCode: http.StatusUnauthorized},

		// Dashboard
		{num: 38, name: "dashboard", method: "GET", path: "/dashboard", expCode: http.StatusUnauthorized},

		// Server Management
		{num: 41, name: "server-shutdown", method: "POST", path: "/server/shutdown", expCode: http.StatusUnauthorized},
		{num: 43, name: "server-discovery", method: "POST", path: "/server/discovery", expCode: http.StatusUnauthorized},
		{num: 45, name: "server-cache-batch-load", method: "POST", path: "/server/cache-batch-load", expCode: http.StatusUnauthorized},
		{num: 47, name: "server-restart", method: "POST", path: "/server/restart", expCode: http.StatusUnauthorized},

		// Debug
		{num: 49, name: "pprof", method: "GET", path: "/debug/pprof/", expCode: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(fmt.Sprintf("#%d-unauth-%s", tt.num, tt.name), func(t *testing.T) {
			client := newClient()
			resp := doRequest(t, client, tt.method, tt.path, tt.body, tt.hx)
			defer resp.Body.Close()

			status := "PASS"
			note := ""
			if resp.StatusCode != tt.expCode {
				status = "FAIL"
				note = fmt.Sprintf("expected %d, got %d", tt.expCode, resp.StatusCode)
			}

			reportResult(t, tt.num, tt.path, tt.method, "No", tt.expCode, resp.StatusCode, status, note)
		})
	}
}

// =========================================================================
// Section 2 (Auth): Authenticated access — each auth-protected route should
// return 200 (or expected code) when logged in.
// =========================================================================

func TestAuthRoutes_Auth(t *testing.T) {
	type authTest struct {
		num     int
		name    string
		method  string
		path    string
		bodyFn  func(t *testing.T, client *http.Client) url.Values
		hx      bool
		expCode int
		skip    bool   // mark destructive tests as skip
		note    string // additional note
	}

	tests := []authTest{
		// Config GET
		{num: 19, name: "config-get", method: "GET", path: "/config", expCode: http.StatusOK},
		{num: 21, name: "config-export-download", method: "GET", path: "/config/export/download", expCode: http.StatusOK},

		// Config POST
		{num: 23, name: "config-post", method: "POST", path: "/config",
			expCode: http.StatusOK},
		{num: 25, name: "config-themes", method: "POST", path: "/config/themes",
			bodyFn: func(t *testing.T, client *http.Client) url.Values {
				return url.Values{"themes": {"light", "dark"}}
			}, expCode: http.StatusOK},
		{num: 27, name: "config-increment-etag", method: "POST", path: "/config/increment-etag",
			expCode: http.StatusOK},
		{num: 29, name: "config-export-tofile", method: "POST", path: "/config/export/to-file",
			// No COP enforcement for this endpoint (file download)
			expCode: http.StatusOK},
		{num: 31, name: "config-import-preview", method: "POST", path: "/config/import/preview",
			bodyFn: func(t *testing.T, client *http.Client) url.Values {
				return url.Values{"yaml": {"site_name: SmokeTest"}}
			}, expCode: http.StatusOK},
		{num: 33, name: "config-import-commit", method: "POST", path: "/config/import/commit",
			bodyFn: func(t *testing.T, client *http.Client) url.Values {
				return url.Values{"yaml": {"site_name: SmokeTest"}}
			}, expCode: http.StatusOK},
		{num: 35, name: "config-restore-preview", method: "POST", path: "/config/restore-last-known-good?action=preview",
			expCode: http.StatusOK, note: "may return 400 if no prior config saved (acceptable)"},

		// Dashboard
		{num: 39, name: "dashboard", method: "GET", path: "/dashboard", expCode: http.StatusOK},
		{num: 40, name: "dashboard-partial", method: "GET", path: "/dashboard", hx: true, expCode: http.StatusOK},

		// Server Management
		{num: 42, name: "server-shutdown", method: "POST", path: "/server/shutdown",
			skip: true, note: "SKIP: destructive (shuts down server)"},
		{num: 44, name: "server-discovery", method: "POST", path: "/server/discovery",
			expCode: http.StatusOK},
		{num: 46, name: "server-cache-batch-load", method: "POST", path: "/server/cache-batch-load",
			expCode: http.StatusOK},

		// Debug
		{num: 50, name: "pprof", method: "GET", path: "/debug/pprof/", expCode: http.StatusOK},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(fmt.Sprintf("#%d-auth-%s", tt.num, tt.name), func(t *testing.T) {
			// Skip destructive tests
			if tt.skip {
				reportResult(t, tt.num, tt.path, tt.method, "Yes", tt.expCode, 0, "SKIP", tt.note)
				t.Skip(tt.note)
			}

			client := newClient()
			login(t, client)

			var body url.Values
			if tt.bodyFn != nil {
				body = tt.bodyFn(t, client)
			}

			resp := doRequest(t, client, tt.method, tt.path, body, tt.hx)
			defer resp.Body.Close()

			status := "PASS"
			note := tt.note

			// #35 (restore-preview): 400 is acceptable if no prior config saved
			if tt.num == 35 && resp.StatusCode == http.StatusBadRequest {
				reportResult(t, tt.num, tt.path, tt.method, "Yes", tt.expCode, resp.StatusCode, "PASS", "400: no prior config saved (acceptable)")
				return
			}

			// #46 (cache-batch-load): 409 when discovery is active — e.g. after #44 in this
			// suite, startup discovery on the shared air server, or extended drain/rebuild.
			if tt.num == 46 && resp.StatusCode == http.StatusConflict {
				reportResult(t, tt.num, tt.path, tt.method, "Yes", tt.expCode, resp.StatusCode, "PASS", "409: discovery active (acceptable)")
				return
			}

			if resp.StatusCode != tt.expCode {
				status = "FAIL"
				note = fmt.Sprintf("expected %d, got %d", tt.expCode, resp.StatusCode)
				if tt.note != "" {
					note += "; " + tt.note
				}
			} else if note == "" {
				note = "OK"
			}

			reportResult(t, tt.num, tt.path, tt.method, "Yes", tt.expCode, resp.StatusCode, status, note)
		})
	}
}

// =========================================================================
// Section 3: Logout — 1 test
// =========================================================================

func TestLogout(t *testing.T) {
	// #51: POST /logout (authenticated) → 200, clears session
	t.Run("#51-logout", func(t *testing.T) {
		client := newClient()
		login(t, client)

		resp := doRequest(t, client, "POST", "/logout", nil, false)
		defer resp.Body.Close()

		expected := http.StatusOK
		status := "PASS"
		note := "OK"
		if resp.StatusCode != expected {
			status = "FAIL"
			note = fmt.Sprintf("expected %d, got %d", expected, resp.StatusCode)
		}

		reportResult(t, 51, "/logout", "POST", "Yes", expected, resp.StatusCode, status, note)
	})
}

// =========================================================================
// Section 4: Dashboard Client Login Flow — 4 tests
//
// These tests exercise the sfpg-go-dashboard TUI client login flows.
// COP (Cross-Origin Protection) validates same-origin POST via Origin/Sec-Fetch-Site headers
// rather than requiring a token. The dashboard client sends Origin for COP compliance.
// =========================================================================

func TestDashboardClientLoginFlow(t *testing.T) {
	// #52: Login from a fresh client — COP allows same-origin POST (no token required).

	t.Run("#52-login-fresh", func(t *testing.T) {
		client := newClient()

		form := url.Values{
			"username": {"admin"},
			"password": {"admin"},
		}
		resp := doRequest(t, client, "POST", "/login", form, false)
		defer resp.Body.Close()

		// COP validates via browser headers (Origin/Sec-Fetch-Site);
		// curl/non-browser POSTs with Origin set are allowed.
		expected := http.StatusOK
		status := "PASS"
		note := "OK"
		if resp.StatusCode != expected {
			status = "FAIL"
			note = fmt.Sprintf("expected %d, got %d", expected, resp.StatusCode)
		}
		reportResult(t, 52, "/login", "POST", "No", expected, resp.StatusCode, status, note)
	})

	// #53: Login after prior request — COP allows same-origin POST with Origin header.

	t.Run("#53-login-existing-session", func(t *testing.T) {
		client := newClient()

		// Make a prior request that creates a session
		gResp, err := client.Get(serverURL + "/gallery/1")
		if err != nil {
			t.Fatalf("GET /gallery/1 failed: %v", err)
		}
		gResp.Body.Close()

		// COP allows same-origin POST with Origin header.
		form := url.Values{
			"username": {"admin"},
			"password": {"admin"},
		}
		resp := doRequest(t, client, "POST", "/login", form, false)
		defer resp.Body.Close()

		expected := http.StatusOK
		status := "PASS"
		note := "OK"
		if resp.StatusCode != expected {
			status = "FAIL"
			note = fmt.Sprintf("expected %d, got %d", expected, resp.StatusCode)
		}
		reportResult(t, 53, "/login", "POST", "No", expected, resp.StatusCode, status, note)
	})

	// #54: Dashboard fetch after login — end-to-end flow
	t.Run("#54-dashboard-after-login", func(t *testing.T) {
		client := newClient()

		login(t, client)

		// Fetch dashboard with the session cookie from login
		dashResp := doRequest(t, client, "GET", "/dashboard", nil, false)
		defer dashResp.Body.Close()

		expected := http.StatusOK
		status := "PASS"
		note := "OK"
		if dashResp.StatusCode != expected {
			status = "FAIL"
			note = fmt.Sprintf("expected %d, got %d", expected, dashResp.StatusCode)
		}
		reportResult(t, 54, "/dashboard", "GET", "Yes", expected, dashResp.StatusCode, status, note)
	})

	// #55: Dashboard client Login + FetchDashboard (end-to-end using client.Client)
	// Replicates the exact flow of sfpg-go-dashboard TUI.
	t.Run("#55-dashboard-client-login", func(t *testing.T) {
		dashClient := newDashboardClient(serverURL)

		ctx := t.Context()
		err := dashClient.Login(ctx, "admin", "admin")
		if err != nil {
			t.Fatalf("Login() failed: %v", err)
		}

		html, err := dashClient.FetchDashboard(ctx)
		if err != nil {
			t.Fatalf("FetchDashboard() failed: %v", err)
		}

		// Verify we got actual dashboard HTML
		doc, err := testutil.ParseHTML(strings.NewReader(html))
		if err != nil {
			t.Fatalf("FetchDashboard() response parse error: %v", err)
		}
		if testutil.FindElementByID(doc, "dashboard-container") == nil {
			t.Error("FetchDashboard() response missing #dashboard-container element")
		}

		reportResult(t, 55, "/dashboard", "GET", "Yes", http.StatusOK, http.StatusOK, "PASS", "dashboard client e2e login flow OK")
	})
}

// =========================================================================
// =========================================================================
// =========================================================================
// Section 5: Restart — runs serially, polls for recovery
//
// RestartHandler returns 200 then shuts down asynchronously (~500ms delay).
// Each subtest waits for the old process to stop accepting connections
// (waitForServerDown), then for the new process to respond (waitForServer),
// settles 2s, and verifies login plus an authenticated endpoint.
// =========================================================================

func TestRestart(t *testing.T) {
	// #37: POST /config/restart (authenticated) → 200, server restarts, comes back healthy
	t.Run("#37-config-restart", func(t *testing.T) {
		client := newClient()
		login(t, client)

		resp := doRequest(t, client, "POST", "/config/restart", nil, false)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			reportResult(t, 37, "/config/restart", "POST", "Yes", http.StatusOK, resp.StatusCode, "FAIL", "restart POST failed")
			return
		}

		if !waitForServerDown(t, 15*time.Second) {
			reportResult(t, 37, "/config/restart", "POST", "Yes", http.StatusOK, 0, "FAIL", "server did not go down after restart")
			return
		}

		if !waitForServer(t, 15*time.Second) {
			reportResult(t, 37, "/config/restart", "POST", "Yes", http.StatusOK, 0, "FAIL", "server did not respond after restart")
			return
		}

		// Verify full functionality: login + dashboard
		verifyClient := newClient()
		login(t, verifyClient)
		dashResp := doRequest(t, verifyClient, "GET", "/dashboard", nil, false)
		dashResp.Body.Close()
		if dashResp.StatusCode != http.StatusOK {
			reportResult(t, 37, "/config/restart", "POST", "Yes", http.StatusOK, dashResp.StatusCode, "FAIL", "dashboard not reachable after restart")
			return
		}

		reportResult(t, 37, "/config/restart", "POST", "Yes", http.StatusOK, http.StatusOK, "PASS", "server restarted and full functionality verified")
	})

	// #48: POST /server/restart (authenticated) → 200, server restarts, comes back healthy
	t.Run("#48-server-restart", func(t *testing.T) {
		// Ensure server is settled by polling before starting
		if !waitForServer(t, 5*time.Second) {
			t.Fatal("server not reachable before restart test")
		}

		client := newClient()
		login(t, client)

		resp := doRequest(t, client, "POST", "/server/restart", nil, false)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			reportResult(t, 48, "/server/restart", "POST", "Yes", http.StatusOK, resp.StatusCode, "FAIL", "restart POST failed")
			return
		}

		if !waitForServerDown(t, 15*time.Second) {
			reportResult(t, 48, "/server/restart", "POST", "Yes", http.StatusOK, 0, "FAIL", "server did not go down after restart")
			return
		}

		if !waitForServer(t, 15*time.Second) {
			reportResult(t, 48, "/server/restart", "POST", "Yes", http.StatusOK, 0, "FAIL", "server did not respond after restart")
			return
		}

		// Verify full functionality: login + gallery
		verifyClient := newClient()
		login(t, verifyClient)
		galResp := doRequest(t, verifyClient, "GET", "/gallery/1", nil, false)
		galResp.Body.Close()
		if galResp.StatusCode != http.StatusOK {
			reportResult(t, 48, "/server/restart", "POST", "Yes", http.StatusOK, galResp.StatusCode, "FAIL", "gallery not reachable after restart")
			return
		}

		reportResult(t, 48, "/server/restart", "POST", "Yes", http.StatusOK, http.StatusOK, "PASS", "server restarted and full functionality verified")
	})
}

// TestLoginRateLimit_Returns429 verifies the configured per-IP login limit is
// enforced against the live server: after saving login_rate_limit_per_ip=2
// (which clears prior login history), the third login POST from this IP
// returns 429. Afterwards the limit is restored to 0 — the suite invariant on
// the shared dev server, enforced by TestMain — via the still-authenticated
// admin client (never re-login after the burst, and never restore the
// captured value: that would put the default of 10 back on dev air).
func TestLoginRateLimit_Returns429(t *testing.T) {
	client := newClient()
	login(t, client)

	values, err := parseConfigForm(t, client)
	if err != nil {
		t.Fatalf("failed to parse config form: %v", err)
	}

	// Register restore before mutating (mirrors #72-config-restore-site-name).
	// Restores 0, not the captured value: limit=0 is the suite invariant on
	// shared dev air (TestMain enforces it at package start), so this test
	// must never put the default of 10 back.
	defer func() {
		restoreValues, err := parseConfigForm(t, client)
		if err != nil {
			t.Errorf("restore parse: %v", err)
			return
		}
		restoreValues = cloneValues(restoreValues)
		restoreValues.Set("login_rate_limit_per_ip", "0")
		for _, key := range []string{"admin_current_password", "admin_new_password", "admin_confirm_password", "yaml"} {
			restoreValues.Del(key)
		}
		resp := doRequest(t, client, http.MethodPost, "/config", restoreValues, false)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("restore POST /config: got %d", resp.StatusCode)
		}
	}()

	submission := cloneValues(values)
	submission.Set("login_rate_limit_per_ip", "2")
	for _, key := range []string{"admin_current_password", "admin_new_password", "admin_confirm_password", "yaml"} {
		submission.Del(key)
	}
	resp := doRequest(t, client, http.MethodPost, "/config", submission, false)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /config set limit=2: got %d", resp.StatusCode)
	}
	// Server-side SyncLoginRateLimitMax(2) cleared prior login history, so the
	// login() above does not count toward the burst.

	burstClient := newClient()
	for attempt := 1; attempt <= 3; attempt++ {
		loginResp := doRequest(t, burstClient, http.MethodPost, "/login", url.Values{
			"username": {"rate-limit-probe"}, // NOT admin — avoid lockout side effects
			"password": {"wrongpassword"},
		}, false)
		if attempt <= 2 && loginResp.StatusCode == http.StatusTooManyRequests {
			loginResp.Body.Close()
			t.Fatalf("attempt %d: unexpected 429", attempt)
		}
		if attempt == 3 && loginResp.StatusCode != http.StatusTooManyRequests {
			loginResp.Body.Close()
			t.Fatalf("attempt %d: got %d, want 429", attempt, loginResp.StatusCode)
		}
		loginResp.Body.Close()
	}
}
