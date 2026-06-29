//go:build e2eweb

package web_testsuite

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
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
			bodyFn: func(t *testing.T, client *http.Client) url.Values {
				token := csrfTokenFromConfig(t, client)
				return url.Values{"csrf_token": {token}}
			}, expCode: http.StatusOK},
		{num: 25, name: "config-themes", method: "POST", path: "/config/themes",
			bodyFn: func(t *testing.T, client *http.Client) url.Values {
				token := csrfTokenFromConfig(t, client)
				return url.Values{"csrf_token": {token}, "themes": {"light", "dark"}}
			}, expCode: http.StatusOK},
		{num: 27, name: "config-increment-etag", method: "POST", path: "/config/increment-etag",
			bodyFn: func(t *testing.T, client *http.Client) url.Values {
				token := csrfTokenFromConfig(t, client)
				return url.Values{"csrf_token": {token}}
			}, expCode: http.StatusOK},
		{num: 29, name: "config-export-tofile", method: "POST", path: "/config/export/to-file",
			// No CSRF needed for this endpoint
			expCode: http.StatusOK},
		{num: 31, name: "config-import-preview", method: "POST", path: "/config/import/preview",
			bodyFn: func(t *testing.T, client *http.Client) url.Values {
				return url.Values{"yaml": {"site_name: SmokeTest"}}
			}, expCode: http.StatusOK},
		{num: 33, name: "config-import-commit", method: "POST", path: "/config/import/commit",
			bodyFn: func(t *testing.T, client *http.Client) url.Values {
				token := csrfTokenFromConfig(t, client)
				return url.Values{"csrf_token": {token}, "yaml": {"site_name: SmokeTest"}}
			}, expCode: http.StatusOK},
		{num: 35, name: "config-restore-preview", method: "POST", path: "/config/restore-last-known-good?action=preview",
			// No CSRF needed; may return 400 if no prior config saved
			expCode: http.StatusOK, note: "may return 400 if no prior config saved (acceptable)"},
		{num: 37, name: "config-restart", method: "POST", path: "/config/restart",
			bodyFn: func(t *testing.T, client *http.Client) url.Values {
				token := csrfTokenFromConfig(t, client)
				return url.Values{"csrf_token": {token}}
			}, expCode: http.StatusOK, skip: true, note: "SKIP: destructive (triggers server restart)"},

		// Dashboard
		{num: 39, name: "dashboard", method: "GET", path: "/dashboard", expCode: http.StatusOK},
		{num: 40, name: "dashboard-partial", method: "GET", path: "/dashboard", hx: true, expCode: http.StatusOK},

		// Server Management
		{num: 42, name: "server-shutdown", method: "POST", path: "/server/shutdown",
			skip: true, note: "SKIP: destructive (shuts down server)"},
		{num: 44, name: "server-discovery", method: "POST", path: "/server/discovery", expCode: http.StatusOK},
		{num: 46, name: "server-cache-batch-load", method: "POST", path: "/server/cache-batch-load", expCode: http.StatusOK},
		{num: 48, name: "server-restart", method: "POST", path: "/server/restart",
			skip: true, note: "SKIP: destructive (triggers server restart)"},

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
// Section 4: Dashboard Client Login Flow — 3 tests
//
// These tests replicate the exact HTTP flow used by the sfpg-go-dashboard
// TUI client: POST /login with only username+password (NO csrf_token), then
// GET /dashboard. The dashboard client lacks CSRF token extraction, so it
// relies on the server allowing login without CSRF for sessions that have
// no stored token.
// =========================================================================

func TestDashboardClientLoginFlow(t *testing.T) {
	// #52: Login without CSRF from a fresh client (no prior requests)
	t.Run("#52-login-nocsrf-fresh", func(t *testing.T) {
		client := newClient()

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
		} else if resp.Header.Get("Hx-Trigger") != "auth-changed" {
			// Login succeeded (200) but missing HX-Trigger means auth failed
			// and server returned login form with error
			status = "FAIL"
			note = "login returned 200 but missing HX-Trigger: auth-changed — credentials likely rejected"
		}
		reportResult(t, 52, "/login", "POST", "No", expected, resp.StatusCode, status, note)
	})

	// #53: Login without CSRF after a prior request that created a session
	t.Run("#53-login-nocsrf-existing-session", func(t *testing.T) {
		client := newClient()

		// Make a prior request that creates a session
		gResp, err := client.Get(serverURL + "/gallery/1")
		if err != nil {
			t.Fatalf("GET /gallery/1 failed: %v", err)
		}
		gResp.Body.Close()

		// Now login without CSRF — server should still allow this because
		// gallery page doesn't persist CSRF token for unauthenticated sessions
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
		} else if resp.Header.Get("Hx-Trigger") != "auth-changed" {
			status = "FAIL"
			note = "login returned 200 but missing HX-Trigger: auth-changed after prior session creation"
		}
		reportResult(t, 53, "/login", "POST", "No", expected, resp.StatusCode, status, note)
	})

	// #54: Dashboard fetch after login without CSRF (end-to-end client flow)
	t.Run("#54-dashboard-after-nocsrf-login", func(t *testing.T) {
		client := newClient()

		// Login without CSRF (like the dashboard client)
		form := url.Values{
			"username": {"admin"},
			"password": {"admin"},
		}
		loginResp := doRequest(t, client, "POST", "/login", form, false)
		loginResp.Body.Close()

		if loginResp.StatusCode != http.StatusOK || loginResp.Header.Get("Hx-Trigger") != "auth-changed" {
			t.Skip("SKIP: prerequisite login failed, skipping dashboard fetch test")
		}

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
		if !strings.Contains(html, "dashboard-container") {
			t.Error("FetchDashboard() response missing dashboard-container element")
		}

		reportResult(t, 55, "/dashboard", "GET", "Yes", http.StatusOK, http.StatusOK, "PASS", "dashboard client e2e login flow OK")
	})
}
