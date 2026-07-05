//go:build integration || e2e

package server

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/testutil"
)

// TestAuthenticatedConfigSaveAndRestartFlowWorksEndToEnd verifies that an
// authenticated admin can save config changes via POST /config and then
// initiate a restart via POST /server/restart. The config POST must persist
// the new value and render the save-success template; the restart POST must
// render the restart-initiated template and request a real process restart.
func TestAuthenticatedConfigSaveAndRestartFlowWorksEndToEnd(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	app := CreateApp(t)
	defer app.Shutdown()

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Step 1: Authenticate as admin.
	loginAsAdmin(t, client, ts.URL)

	// Step 2: Load the config page and extract a CSRF token.
	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)

	// Step 3: POST /config with a valid, non-restart-required change.
	// Include the default-true checkbox fields so they are not implicitly
	// reset to false (which would trigger a spurious restart-required response).
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("site_name", "End-to-End Config Save Test Site")
	formData.Set("server_compression_enable", "on")
	formData.Set("enable_http_cache", "on")
	formData.Set("enable_cache_preload", "on")
	formData.Set("session_http_only", "on")
	formData.Set("session_secure", "on")
	formData.Set("run_file_discovery", "on")

	resp, body := postFormExpectOK(t, client, ts.URL+"/config", ts.URL, formData)

	if got := resp.Header.Get("HX-Trigger"); got != "config-saved" {
		t.Errorf("POST /config HX-Trigger header = %q, want %q", got, "config-saved")
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("failed to parse /config response HTML: %v", err)
	}
	requireAlertContains(t, doc, "config-success-message", "Configuration saved successfully")

	// Step 4: Verify the change was persisted to the database.
	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}
	savedSiteName, err := cpcRo.Queries.GetConfigValueByKey(app.ctx, "site_name")
	app.dbRoPool.Put(cpcRo)
	if err != nil {
		t.Fatalf("failed to read saved site_name from database: %v", err)
	}
	if savedSiteName != "End-to-End Config Save Test Site" {
		t.Errorf("site_name persisted value = %q, want %q", savedSiteName, "End-to-End Config Save Test Site")
	}

	// Step 5: POST /server/restart to initiate a restart.
	csrfToken = extractCSRFTokenFromConfig(t, client, ts.URL)
	formData = url.Values{}
	formData.Set("csrf_token", csrfToken)

	_, body = postFormExpectOK(t, client, ts.URL+"/server/restart", ts.URL, formData)

	doc, err = html.Parse(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("failed to parse /server/restart response HTML: %v", err)
	}
	requireAlertContains(t, doc, "config-success-message", "Server restart initiated")

	// Step 6: Verify a process restart was requested.
	// The handler flushes the response and then triggers the restart in a
	// background goroutine, so we poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if app.restartRequested.Load() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !app.restartRequested.Load() {
		t.Errorf("restart was not requested after POST /server/restart")
	}
}

// postFormExpectOK submits a POST request with form-encoded data and fails the
// test if the response status is not 200 or the body cannot be read. It returns
// the response and its body.
func postFormExpectOK(t *testing.T, client *http.Client, url, origin string, form url.Values) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("failed to create POST request to %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", origin)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s failed: %v", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read %s response body: %v", url, err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s returned status %d, want %d; body: %s", url, resp.StatusCode, http.StatusOK, body)
	}
	return resp, body
}

// requireAlertContains fails the test if the element with the given ID is not
// present in doc or its visible text does not contain substr.
func requireAlertContains(t *testing.T, doc *html.Node, id, substr string) {
	t.Helper()
	alert := testutil.FindElementByID(doc, id)
	if alert == nil {
		t.Fatalf("alert #%s not found in response", id)
	}
	text := strings.TrimSpace(testutil.GetTextContent(alert))
	if !strings.Contains(text, substr) {
		t.Errorf("alert #%s text = %q, want substring %q", id, text, substr)
	}
}
