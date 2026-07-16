//go:build e2eweb

package web_testsuite

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/testutil"
	"golang.org/x/net/html"
)

// =========================================================================
// Menu Functionality Tests
//
// These tests verify that the hamburger menu correctly reflects
// authentication state and that the session persists across the
// typical user flow (login → navigate → back).
//
// Each test uses the same HTTP client (with cookie jar) to simulate
// a browser session. The menu state is checked via the /hamburger-menu
// endpoint which returns only <li> elements.
// =========================================================================

// menuItemByLabel returns the first menu element with the given aria-label.
func menuItemByLabel(doc *html.Node, label string) *html.Node {
	return testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && testutil.GetAttr(n, "aria-label") == label
	})
}

// assertMenuItem fails the test if the menu does not contain an element with
// the given aria-label.
func assertMenuItem(t *testing.T, doc *html.Node, label string) {
	t.Helper()
	if menuItemByLabel(doc, label) == nil {
		t.Errorf("expected menu to contain item with aria-label=%q", label)
	}
}

// assertNoMenuItem fails the test if the menu contains an element with the
// given aria-label.
func assertNoMenuItem(t *testing.T, doc *html.Node, label string) {
	t.Helper()
	if menuItemByLabel(doc, label) != nil {
		t.Errorf("expected menu NOT to contain item with aria-label=%q", label)
	}
}

// parseMenuResponse parses the response body from /hamburger-menu into an
// html.Node for structural assertions.
func parseMenuResponse(t *testing.T, resp *http.Response) *html.Node {
	t.Helper()
	doc, err := testutil.ParseHTML(resp.Body)
	if err != nil {
		t.Fatalf("failed to parse menu HTML: %v", err)
	}
	return doc
}

// TestMenu_Unauthenticated_ShowsLogin verifies that unauthenticated users
// see "Login" and DO NOT see "Dashboard" in the menu.
func TestMenu_Unauthenticated_ShowsLogin(t *testing.T) {
	client := newClient()

	// Make a prior request to establish a session (like browsing to gallery)
	resp, err := client.Get(serverURL + "/gallery/1")
	if err != nil {
		t.Fatalf("GET /gallery/1 failed: %v", err)
	}
	resp.Body.Close()

	menuResp := doHamburgerMenu(t, client)
	defer menuResp.Body.Close()

	doc := parseMenuResponse(t, menuResp)
	assertNoMenuItem(t, doc, "Dashboard")
	assertMenuItem(t, doc, "Login")
}

// TestMenu_Authenticated_ShowsDashboard verifies that after login,
// the menu shows "Dashboard" and does NOT show "Login".
func TestMenu_Authenticated_ShowsDashboard(t *testing.T) {
	client := newClient()
	login(t, client)

	menuResp := doHamburgerMenu(t, client)
	defer menuResp.Body.Close()

	doc := parseMenuResponse(t, menuResp)
	assertMenuItem(t, doc, "Dashboard")
	assertNoMenuItem(t, doc, "Login")
}

// TestMenu_ShowsCorrectState_AfterMultipleRequests verifies that the
// session persists and the menu stays in the authenticated state
// across multiple requests to various endpoints.
func TestMenu_ShowsCorrectState_AfterMultipleRequests(t *testing.T) {
	client := newClient()
	login(t, client)

	// Make several authenticated requests (simulating Dashboard polling + navigation)
	for i := 0; i < 5; i++ {
		dashResp, err := client.Get(serverURL + "/dashboard")
		if err != nil {
			t.Fatalf("attempt %d: GET /dashboard failed: %v", i, err)
		}
		dashResp.Body.Close()
		if dashResp.StatusCode != http.StatusOK {
			t.Fatalf("attempt %d: GET /dashboard expected 200, got %d", i, dashResp.StatusCode)
		}
	}

	// Verify session is still valid and menu shows authenticated state
	menuResp := doHamburgerMenu(t, client)
	defer menuResp.Body.Close()

	doc := parseMenuResponse(t, menuResp)
	assertMenuItem(t, doc, "Dashboard")
	assertNoMenuItem(t, doc, "Login")
}

// TestMenu_SessionSurvives_ConfigModalAccess verifies that opening and
// getting the config modal content does not invalidate the session.
func TestMenu_SessionSurvives_ConfigModalAccess(t *testing.T) {
	client := newClient()
	login(t, client)

	// GET /config (simulates opening config modal via HTMX)
	configResp, err := client.Get(serverURL + "/config")
	if err != nil {
		t.Fatalf("GET /config failed: %v", err)
	}
	configResp.Body.Close()
	if configResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /config expected 200, got %d", configResp.StatusCode)
	}

	// Verify session is still valid
	menuResp := doHamburgerMenu(t, client)
	defer menuResp.Body.Close()

	doc := parseMenuResponse(t, menuResp)
	assertMenuItem(t, doc, "Dashboard")
}

// TestMenu_SessionSurvives_DashboardNavigation verifies that a full page
// navigation to the dashboard does not invalidate the session.
func TestMenu_SessionSurvives_DashboardNavigation(t *testing.T) {
	client := newClient()
	login(t, client)

	// Simulate full page navigation to dashboard
	dashResp, err := client.Get(serverURL + "/dashboard")
	if err != nil {
		t.Fatalf("GET /dashboard failed: %v", err)
	}
	dashResp.Body.Close()
	if dashResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /dashboard expected 200, got %d", dashResp.StatusCode)
	}

	// Verify session is still valid after navigation
	menuResp := doHamburgerMenu(t, client)
	defer menuResp.Body.Close()

	doc := parseMenuResponse(t, menuResp)
	assertMenuItem(t, doc, "Dashboard")
}

// TestMenu_SimulatesBackNavigation verifies that after the full user flow
// (login, config modal, dashboard, cache preload), navigating "back" to
// the gallery (simulated by a fresh GET /gallery/1) preserves the session
// and the menu shows the authenticated state.
//
// This simulates the user's exact reported flow: Home → Login → Config
// (cancel) → Dashboard → Cache Preload → Back → Check menu.
func TestMenu_SimulatesBackNavigation(t *testing.T) {
	client := newClient()

	// Step 1: Home (GET gallery)
	resp, err := client.Get(serverURL + "/gallery/1")
	if err != nil {
		t.Fatalf("GET /gallery/1 failed: %v", err)
	}
	resp.Body.Close()

	// Step 2: Login
	login(t, client)

	// Step 3: Config modal (GET /config)
	configResp, err := client.Get(serverURL + "/config")
	if err != nil {
		t.Fatalf("GET /config failed: %v", err)
	}
	configResp.Body.Close()
	if configResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /config expected 200, got %d", configResp.StatusCode)
	}
	// Cancel is client-side only (no HTTP request)

	// Step 4: Navigate to Dashboard
	dashResp, err := client.Get(serverURL + "/dashboard")
	if err != nil {
		t.Fatalf("GET /dashboard failed: %v", err)
	}
	dashResp.Body.Close()
	if dashResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /dashboard expected 200, got %d", dashResp.StatusCode)
	}

	// Step 5: Server actions (Cache preload, discovery)
	// Run discovery
	discResp := doRequest(t, client, "POST", "/server/discovery", url.Values{"csrf_token": {csrfTokenFromConfig(t, client)}}, false)
	discResp.Body.Close()
	if discResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /server/discovery expected 200, got %d", discResp.StatusCode)
	}

	// Run cache batch load (may be blocked by discovery - 409 is acceptable)
	cacheResp := doRequest(t, client, "POST", "/server/cache-batch-load", url.Values{"csrf_token": {csrfTokenFromConfig(t, client)}}, false)
	cacheResp.Body.Close()
	if cacheResp.StatusCode != http.StatusOK && cacheResp.StatusCode != http.StatusConflict {
		t.Fatalf("POST /server/cache-batch-load expected 200 or 409, got %d", cacheResp.StatusCode)
	}

	// Simulate dashboard polling (multiple partial requests)
	for i := 0; i < 5; i++ {
		pollResp := doRequest(t, client, "GET", "/dashboard", nil, true)
		pollResp.Body.Close()
		if pollResp.StatusCode != http.StatusOK {
			t.Fatalf("poll %d: expected 200, got %d", i, pollResp.StatusCode)
		}
	}

	// Step 6: "Back" to gallery (simulated by GET /gallery/1)
	backResp, err := client.Get(serverURL + "/gallery/1")
	if err != nil {
		t.Fatalf("GET /gallery/1 (back) failed: %v", err)
	}
	backResp.Body.Close()
	if backResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /gallery/1 expected 200, got %d", backResp.StatusCode)
	}

	// Step 7: Menu should still show authenticated state
	menuResp := doHamburgerMenu(t, client)
	defer menuResp.Body.Close()

	doc := parseMenuResponse(t, menuResp)
	assertMenuItem(t, doc, "Dashboard")
	assertNoMenuItem(t, doc, "Login")
}

// TestMenu_SessionSurvives_DashboardPollingThenRecheck verifies that
// after multiple dashboard polling requests (simulating the 5s poll),
// the session is still valid and the menu shows authenticated state.
func TestMenu_SessionSurvives_DashboardPollingThenRecheck(t *testing.T) {
	client := newClient()
	login(t, client)

	// Simulate multiple dashboard partial requests (like the HTMX polling)
	for i := 0; i < 10; i++ {
		dashResp := doRequest(t, client, "GET", "/dashboard", nil, true) // HX-Request
		dashResp.Body.Close()
		if dashResp.StatusCode != http.StatusOK {
			t.Fatalf("poll %d: GET /dashboard expected 200, got %d", i, dashResp.StatusCode)
		}
	}

	// Verify session is still valid
	menuResp := doHamburgerMenu(t, client)
	defer menuResp.Body.Close()

	doc := parseMenuResponse(t, menuResp)
	assertMenuItem(t, doc, "Dashboard")
}

// TestMenu_LoginLogout_Cycle verifies that the menu correctly toggles
// between Login and Dashboard states when the user logs in and out.
func TestMenu_LoginLogout_Cycle(t *testing.T) {
	client := newClient()

	// Before login: should show Login
	menuResp := doHamburgerMenu(t, client)
	doc := parseMenuResponse(t, menuResp)
	assertMenuItem(t, doc, "Login")
	menuResp.Body.Close()

	// Login
	login(t, client)

	// After login: should show Dashboard
	menuResp = doHamburgerMenu(t, client)
	doc = parseMenuResponse(t, menuResp)
	assertMenuItem(t, doc, "Dashboard")
	menuResp.Body.Close()

	// Logout
	logout(t, client)

	// After logout: should show Login again
	menuResp = doHamburgerMenu(t, client)
	doc = parseMenuResponse(t, menuResp)
	assertMenuItem(t, doc, "Login")
	assertNoMenuItem(t, doc, "Dashboard")
	menuResp.Body.Close()
}

// TestMenu_ETagVersionParam verifies that menu links include the
// cacheVersion query parameter (important for cache busting).
func TestMenu_ETagVersionParam(t *testing.T) {
	client := newClient()
	login(t, client)

	menuResp := doHamburgerMenu(t, client)
	defer menuResp.Body.Close()

	doc := parseMenuResponse(t, menuResp)
	dash := menuItemByLabel(doc, "Dashboard")
	if dash == nil {
		t.Fatal("missing Dashboard menu item")
	}

	href := testutil.GetAttr(dash, "href")
	u, err := url.Parse(href)
	if err != nil {
		t.Fatalf("could not parse Dashboard href %q: %v", href, err)
	}
	if u.Query().Get("v") == "" {
		t.Errorf("expected Dashboard link href %q to contain ?v= cache version parameter", href)
	}
}

// TestMenu_ServerManagement_Items verifies that authenticated menu
// includes server management items (Discovery, Cache Batch Load).
func TestMenu_ServerManagement_Items(t *testing.T) {
	client := newClient()
	login(t, client)

	menuResp := doHamburgerMenu(t, client)
	defer menuResp.Body.Close()

	doc := parseMenuResponse(t, menuResp)
	assertMenuItem(t, doc, "Run Discovery")
	assertMenuItem(t, doc, "Run Cache Batch Load")
	assertMenuItem(t, doc, "Configuration")
}

// TestMenu_Logout_ClearsSession verifies that the session cookie is
// invalidated after logout, and the menu shows unauthenticated state.
func TestMenu_Logout_ClearsSession(t *testing.T) {
	client := newClient()
	login(t, client)

	// Verify authenticated first
	menuResp := doHamburgerMenu(t, client)
	doc := parseMenuResponse(t, menuResp)
	if menuItemByLabel(doc, "Dashboard") == nil {
		t.Fatal("precondition: login failed, menu should contain Dashboard")
	}
	menuResp.Body.Close()

	// Logout
	logout(t, client)

	// Verify unauthenticated
	menuResp = doHamburgerMenu(t, client)
	doc = parseMenuResponse(t, menuResp)
	assertMenuItem(t, doc, "Login")
	menuResp.Body.Close()

	// Verify that accessing protected routes returns 401
	dashResp, err := client.Get(serverURL + "/dashboard")
	if err != nil {
		t.Fatalf("GET /dashboard failed: %v", err)
	}
	dashResp.Body.Close()
	if dashResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("after logout, GET /dashboard should return 401, got %d", dashResp.StatusCode)
	}
}

// TestMenu_CacheControlHeaders verifies that the /hamburger-menu endpoint
// sets no-cache headers to prevent stale menu state.
func TestMenu_CacheControlHeaders(t *testing.T) {
	client := newClient()
	login(t, client)

	menuResp := doHamburgerMenu(t, client)
	defer menuResp.Body.Close()

	cc := menuResp.Header.Get("Cache-Control")
	if cc == "" || (!strings.Contains(cc, "no-store") && !strings.Contains(cc, "no-cache")) {
		t.Errorf("expected Cache-Control with no-store/no-cache, got %q", cc)
	}

	ct := menuResp.Header.Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("expected Content-Type: text/html; charset=utf-8, got %q", ct)
	}
}

// =========================================================================
// Helpers
// =========================================================================

// doHamburgerMenu fetches the /hamburger-menu endpoint.
func doHamburgerMenu(t *testing.T, client *http.Client) *http.Response {
	t.Helper()
	resp, err := client.Get(serverURL + "/hamburger-menu")
	if err != nil {
		t.Fatalf("GET /hamburger-menu failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("GET /hamburger-menu expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	return resp
}

// logout performs a POST /logout and verifies success.
func logout(t *testing.T, client *http.Client) {
	t.Helper()

	// POST /logout (requires CSRF token)
	resp := doRequest(t, client, "POST", "/logout", url.Values{"csrf_token": {csrfTokenFromConfig(t, client)}}, false)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /logout expected 200, got %d", resp.StatusCode)
	}

	// Check HX-Trigger header
	if resp.Header.Get("Hx-Trigger") != "auth-changed" {
		t.Errorf("POST /logout expected Hx-Trigger: auth-changed, got %q",
			resp.Header.Get("Hx-Trigger"))
	}
}

// =========================================================================
// Section-based Tests (for formatted report output)
// =========================================================================

func TestMenuFunctionalityReport(t *testing.T) {
	// Section 10: Menu — Unauthenticated
	t.Run("#60-menu-unauth-shows-login", func(t *testing.T) {
		client := newClient()
		resp, err := client.Get(serverURL + "/hamburger-menu")
		if err != nil {
			reportResult(t, 60, "/hamburger-menu", "GET", "No", 200, 0, "FAIL", fmt.Sprintf("request failed: %v", err))
			return
		}
		defer resp.Body.Close()

		status := "PASS"
		note := "OK"
		if resp.StatusCode != 200 {
			status = "FAIL"
			note = fmt.Sprintf("expected 200, got %d", resp.StatusCode)
		} else {
			doc := parseMenuResponse(t, resp)
			if menuItemByLabel(doc, "Dashboard") != nil {
				status = "FAIL"
				note = "menu shows Dashboard for unauthenticated"
			} else if menuItemByLabel(doc, "Login") == nil {
				status = "FAIL"
				note = "menu missing Login for unauthenticated"
			}
		}
		reportResult(t, 60, "/hamburger-menu", "GET", "No", 200, resp.StatusCode, status, note)
	})

	// Section 10: Menu — Authenticated
	t.Run("#61-menu-auth-shows-dashboard", func(t *testing.T) {
		client := newClient()
		login(t, client)

		resp, err := client.Get(serverURL + "/hamburger-menu")
		if err != nil {
			reportResult(t, 61, "/hamburger-menu", "GET", "Yes", 200, 0, "FAIL", fmt.Sprintf("request failed: %v", err))
			return
		}
		defer resp.Body.Close()

		status := "PASS"
		note := "OK"
		if resp.StatusCode != 200 {
			status = "FAIL"
			note = fmt.Sprintf("expected 200, got %d", resp.StatusCode)
		} else {
			doc := parseMenuResponse(t, resp)
			if menuItemByLabel(doc, "Dashboard") == nil {
				status = "FAIL"
				note = "menu missing Dashboard for authenticated"
			}
			if menuItemByLabel(doc, "Login") != nil {
				status = "FAIL"
				note = "menu shows Login for authenticated"
			}
		}
		reportResult(t, 61, "/hamburger-menu", "GET", "Yes", 200, resp.StatusCode, status, note)
	})

	// Section 10: Menu — Login then menu check
	t.Run("#62-menu-after-login", func(t *testing.T) {
		client := newClient()
		login(t, client)

		resp, err := client.Get(serverURL + "/hamburger-menu")
		if err != nil {
			reportResult(t, 62, "/hamburger-menu", "GET", "Yes", 200, 0, "FAIL", fmt.Sprintf("request failed: %v", err))
			return
		}
		defer resp.Body.Close()

		status := "PASS"
		note := "OK"
		if resp.StatusCode != 200 {
			status = "FAIL"
			note = fmt.Sprintf("expected 200, got %d", resp.StatusCode)
		} else {
			doc := parseMenuResponse(t, resp)
			if menuItemByLabel(doc, "Login") != nil {
				status = "FAIL"
				note = "menu shows Login after successful login"
			}
		}
		reportResult(t, 62, "/hamburger-menu", "GET", "Yes", 200, resp.StatusCode, status, note)
	})

	// Section 10: Menu — Dashboard navigation doesn't invalidate session
	t.Run("#63-menu-after-dashboard", func(t *testing.T) {
		client := newClient()
		login(t, client)

		// Navigate to dashboard (full page request)
		dashResp, err := client.Get(serverURL + "/dashboard")
		if err != nil {
			reportResult(t, 63, "/hamburger-menu", "GET", "Yes", 200, 0, "FAIL", fmt.Sprintf("/dashboard failed: %v", err))
			return
		}
		dashResp.Body.Close()

		// Check menu after navigation
		resp, err := client.Get(serverURL + "/hamburger-menu")
		if err != nil {
			reportResult(t, 63, "/hamburger-menu", "GET", "Yes", 200, 0, "FAIL", fmt.Sprintf("request failed: %v", err))
			return
		}
		defer resp.Body.Close()

		status := "PASS"
		note := "OK"
		if resp.StatusCode != 200 {
			status = "FAIL"
			note = fmt.Sprintf("expected 200, got %d", resp.StatusCode)
		} else {
			doc := parseMenuResponse(t, resp)
			if menuItemByLabel(doc, "Login") != nil {
				status = "FAIL"
				note = "menu shows Login after dashboard navigation"
			}
		}
		reportResult(t, 63, "/hamburger-menu", "GET", "Yes", 200, resp.StatusCode, status, note)
	})

	// Section 10: Menu — Polling then recheck
	t.Run("#64-menu-after-polling", func(t *testing.T) {
		client := newClient()
		login(t, client)

		// Simulate 10 HTMX polling requests to dashboard
		for i := 0; i < 10; i++ {
			pollResp := doRequest(t, client, "GET", "/dashboard", nil, true)
			pollResp.Body.Close()
			if pollResp.StatusCode != http.StatusOK {
				t.Fatalf("poll %d failed: expected 200, got %d", i, pollResp.StatusCode)
			}
		}

		// Check menu after polling
		resp, err := client.Get(serverURL + "/hamburger-menu")
		if err != nil {
			reportResult(t, 64, "/hamburger-menu", "GET", "Yes", 200, 0, "FAIL", fmt.Sprintf("request failed: %v", err))
			return
		}
		defer resp.Body.Close()

		status := "PASS"
		note := "OK"
		if resp.StatusCode != 200 {
			status = "FAIL"
			note = fmt.Sprintf("expected 200, got %d", resp.StatusCode)
		} else {
			doc := parseMenuResponse(t, resp)
			if menuItemByLabel(doc, "Login") != nil {
				status = "FAIL"
				note = "menu shows Login after dashboard polling"
			}
		}
		reportResult(t, 64, "/hamburger-menu", "GET", "Yes", 200, resp.StatusCode, status, note)
	})

	// Section 10: Menu — Login + Logout + Re-Login cycle
	t.Run("#65-menu-login-logout-relogin", func(t *testing.T) {
		client := newClient()

		// Login
		login(t, client)

		// Check menu: should show Dashboard
		menuResp := doHamburgerMenu(t, client)
		doc := parseMenuResponse(t, menuResp)
		if menuItemByLabel(doc, "Login") != nil {
			menuResp.Body.Close()
			reportResult(t, 65, "/hamburger-menu", "GET", "Yes", 200, 200, "FAIL", "menu shows Login after login")
			return
		}
		menuResp.Body.Close()

		// Logout
		logoutResp := doRequest(t, client, "POST", "/logout", url.Values{"csrf_token": {csrfTokenFromConfig(t, client)}}, false)
		logoutResp.Body.Close()
		if logoutResp.StatusCode != http.StatusOK {
			reportResult(t, 65, "/hamburger-menu", "GET", "No", 200, logoutResp.StatusCode, "FAIL", "logout failed")
			return
		}

		// Check menu after logout: should show Login
		menuResp2, err := client.Get(serverURL + "/hamburger-menu")
		if err != nil {
			reportResult(t, 65, "/hamburger-menu", "GET", "No", 200, 0, "FAIL", fmt.Sprintf("request failed: %v", err))
			return
		}
		doc2 := parseMenuResponse(t, menuResp2)
		if menuItemByLabel(doc2, "Login") == nil {
			menuResp2.Body.Close()
			reportResult(t, 65, "/hamburger-menu", "GET", "No", 200, 200, "FAIL", "menu missing Login after logout")
			return
		}
		menuResp2.Body.Close()

		// Re-login
		login(t, client)

		// Check menu after re-login: should show Dashboard again
		menuResp3, err := client.Get(serverURL + "/hamburger-menu")
		if err != nil {
			reportResult(t, 65, "/hamburger-menu", "GET", "Yes", 200, 0, "FAIL", fmt.Sprintf("request failed: %v", err))
			return
		}
		doc3 := parseMenuResponse(t, menuResp3)

		status := "PASS"
		note := "login-logout-relogin cycle OK"
		if menuItemByLabel(doc3, "Login") != nil {
			status = "FAIL"
			note = "menu shows Login after re-login"
		}
		menuResp3.Body.Close()
		reportResult(t, 65, "/hamburger-menu", "GET", "Yes", 200, 200, status, note)
	})
}
