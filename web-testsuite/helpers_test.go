//go:build e2eweb

package web_testsuite

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	dashclient "github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
	"golang.org/x/net/html"
)

// ---------------------------------------------------------------------------
// Shared state (used by all test files in this suite)
// ---------------------------------------------------------------------------

var (
	serverURL  = "http://localhost:8083"
	dbPath     = "" // resolved in init()
	moduleRoot = "" // project root (parent of go.mod), resolved in init()
	folderID   = ""
	fileID     = ""
	reportMu   sync.Mutex
	passCount  int
	failCount  int
	skipCount  int
)

func init() {
	// Resolve moduleRoot and dbPath relative to the module root.
	// Since go test sets CWD to the package directory, find the go.mod
	// file and use its parent as the module root.
	resolveModuleRoot()
}

// resolveModuleRoot walks up from CWD to find go.mod and sets moduleRoot
// and dbPath relative to it. Falls back to ./tmp relative to CWD if go.mod
// is not found (unusual for this project).
func resolveModuleRoot() {
	wd, err := os.Getwd()
	if err != nil {
		moduleRoot = "."
		dbPath = "tmp/DB/sfpg.db"
		return
	}
	// Walk up from CWD to find go.mod
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// Found module root
			moduleRoot = dir
			dbPath = filepath.Join(dir, "tmp", "DB", "sfpg.db")
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding go.mod
			moduleRoot = wd
			dbPath = filepath.Join(wd, "tmp", "DB", "sfpg.db")
			return
		}
		dir = parent
	}
}

// ---------------------------------------------------------------------------
// HTTP Client & Request Helpers
// ---------------------------------------------------------------------------

// newClient creates an http.Client with a fresh cookie jar and no redirect
// following. All requests go through a single client so the cookie jar
// automatically tracks session cookie updates (including Set-Cookie from
// gorilla/sessions on every request).
func newClient() *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(fmt.Sprintf("failed to create cookie jar: %v", err))
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// doRequest sends an HTTP request through the provided client.
// If body is non-nil, it is encoded as application/x-www-form-urlencoded.
// If hx is true, Hx-Request: true header is added.
// Origin header is always set to serverURL.
func doRequest(t *testing.T, client *http.Client, method, path string, body url.Values, hx bool) *http.Response {
	t.Helper()

	var reqBody io.Reader
	if body != nil {
		reqBody = strings.NewReader(body.Encode())
	}
	req, err := http.NewRequest(method, serverURL+path, reqBody)
	if err != nil {
		t.Fatalf("failed to create %s %s request: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Origin", serverURL)
	if hx {
		req.Header.Set("Hx-Request", "true")
		req.Header.Set("Hx-Target", "dashboard-container")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s request failed: %v", method, path, err)
	}
	return resp
}

// ---------------------------------------------------------------------------
// Authentication Helpers
// ---------------------------------------------------------------------------

// login logs in with admin/admin credentials and stores the session cookie
// in the client's jar.
//
//  1. GET /login-form -> extract CSRF token from the login form
//     (input[name="csrf_token"] value)
//  2. POST /login with username=admin&password=admin&csrf_token=<token>
//  3. Verify HX-Trigger: auth-changed header is present
//
// /login-form is used instead of /gallery/1 because the gallery page is
// HTTP-cached (up to 30 days), which can serve a stale CSRF token.
func login(t *testing.T, client *http.Client) {
	t.Helper()

	// Step 1: GET login form and extract CSRF token
	resp, err := client.Get(serverURL + "/login-form")
	if err != nil {
		t.Fatalf("GET /login-form failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /login-form expected 200, got %d", resp.StatusCode)
	}

	csrfToken := extractCSRFFromBody(t, resp.Body)
	if csrfToken == "" {
		t.Fatal("could not extract CSRF token from /login-form")
	}

	// Step 2: POST login
	form := url.Values{
		"username":   {"admin"},
		"password":   {"admin"},
		"csrf_token": {csrfToken},
	}

	loginResp := doRequest(t, client, http.MethodPost, "/login", form, false)
	defer loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("POST /login expected 200, got %d. Body: %s", loginResp.StatusCode, string(body))
	}

	// Step 3: Verify HX-Trigger header
	if loginResp.Header.Get("Hx-Trigger") != "auth-changed" {
		t.Errorf("POST /login: expected Hx-Trigger: auth-changed, got %q",
			loginResp.Header.Get("Hx-Trigger"))
	}
}

// csrfTokenFromConfig fetches GET /config and extracts the CSRF token
// from the hidden input (input[name="csrf_token"]).
// The client must be authenticated (logged in) already.
//
// IMPORTANT: The GET /config response includes a Set-Cookie header that
// updates the session cookie. The http.Client's CookieJar handles this
// automatically, ensuring subsequent POST requests use the updated session.
func csrfTokenFromConfig(t *testing.T, client *http.Client) string {
	t.Helper()

	resp, err := client.Get(serverURL + "/config")
	if err != nil {
		t.Fatalf("GET /config failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /config expected 200, got %d. Body: %s", resp.StatusCode, string(body))
	}

	token := extractCSRFFromBody(t, resp.Body)
	if token == "" {
		t.Fatal("could not extract CSRF token from /config page")
	}
	return token
}

// ---------------------------------------------------------------------------
// HTML Parsing Helpers (structural assertions)
// ---------------------------------------------------------------------------

// ParseHTML parses an io.Reader into an html.Node document root.
func ParseHTML(r io.Reader) (*html.Node, error) {
	return html.Parse(r)
}

// FindElementByID finds an element by its id attribute.
func FindElementByID(n *html.Node, id string) *html.Node {
	return FindElement(n, func(n *html.Node) bool {
		return GetAttr(n, "id") == id
	})
}

// FindElementByClass finds the first element containing the given class.
func FindElementByClass(n *html.Node, class string) *html.Node {
	return FindElement(n, func(n *html.Node) bool {
		classes := strings.Fields(GetAttr(n, "class"))
		for _, c := range classes {
			if c == class {
				return true
			}
		}
		return false
	})
}

// FindElementByTag finds the first element with the given tag name.
func FindElementByTag(n *html.Node, tag string) *html.Node {
	return FindElement(n, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == tag
	})
}

// FindElement traverses the tree and returns the first node matching the predicate.
func FindElement(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := FindElement(c, match); found != nil {
			return found
		}
	}
	return nil
}

// FindAllElements returns all nodes matching the predicate.
func FindAllElements(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var results []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && match(n) {
			results = append(results, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return results
}

// GetAttr returns the value of an attribute, or empty string if not found.
func GetAttr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

// GetTextContent returns all text content within a node.
func GetTextContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// extractCSRFFromBody finds the CSRF token from an HTML response body.
// It looks for: name="csrf_token" value="<hex>" in the HTML.
func extractCSRFFromBody(t *testing.T, body io.Reader) string {
	t.Helper()

	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	// Try regex first (faster for smoke tests)
	re := regexp.MustCompile(`csrf_token"\s*value="([a-f0-9]+)"`)
	matches := re.FindStringSubmatch(string(bodyBytes))
	if len(matches) >= 2 {
		return matches[1]
	}

	// Fallback: parse HTML and find the input element
	doc, err := html.Parse(strings.NewReader(string(bodyBytes)))
	if err != nil {
		return ""
	}
	input := FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "input" && GetAttr(n, "name") == "csrf_token"
	})
	if input == nil {
		return ""
	}
	return GetAttr(input, "value")
}

// ---------------------------------------------------------------------------
// Config Snapshot / Restore (used by TestMain for test isolation)
// ---------------------------------------------------------------------------

// configSnapshotYAML holds a copy of the server config exported before tests
// run, so it can be restored after all tests complete.
var configSnapshotYAML string

// loginRaw logs in with admin/admin and returns the session cookie jar.
// Unlike login(), this does not use *testing.T and returns errors directly.
func loginRaw(client *http.Client) error {
	// GET login form to extract CSRF token (uncached, unlike /gallery/1)
	resp, err := client.Get(serverURL + "/login-form")
	if err != nil {
		return fmt.Errorf("GET /login-form: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read /login-form: %w", err)
	}

	re := regexp.MustCompile(`csrf_token"\s*value="([a-f0-9]+)"`)
	matches := re.FindStringSubmatch(string(body))
	if len(matches) < 2 {
		return fmt.Errorf("CSRF token not found in /login-form")
	}
	csrfToken := matches[1]

	form := url.Values{
		"username":   {"admin"},
		"password":   {"admin"},
		"csrf_token": {csrfToken},
	}

	// POST login with proper headers (PostForm doesn't set Origin)
	encoded := form.Encode()
	loginReq, err := http.NewRequest(http.MethodPost, serverURL+"/login", strings.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create login request: %w", err)
	}
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("Origin", serverURL)

	loginResp, err := client.Do(loginReq)
	if err != nil {
		return fmt.Errorf("POST /login: %w", err)
	}
	defer loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /login: expected 200, got %d", loginResp.StatusCode)
	}
	if loginResp.Header.Get("Hx-Trigger") != "auth-changed" {
		return fmt.Errorf("login failed: missing Hx-Trigger header")
	}
	return nil
}

// exportConfigRaw fetches the current server config as YAML.
// Requires an authenticated client.
func exportConfigRaw(client *http.Client) (string, error) {
	resp, err := client.Get(serverURL + "/config/export/download")
	if err != nil {
		return "", fmt.Errorf("GET /config/export/download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("config export: expected 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read config export: %w", err)
	}
	return string(body), nil
}

// importConfigRaw imports a YAML config string into the server.
// Requires an authenticated client. Extracts a fresh CSRF token
// from the config page before posting.
func importConfigRaw(client *http.Client, yamlContent string) error {
	// GET /config to extract CSRF token
	resp, err := client.Get(serverURL + "/config")
	if err != nil {
		return fmt.Errorf("GET /config: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read /config: %w", err)
	}

	re := regexp.MustCompile(`csrf_token"\s*value="([a-f0-9]+)"`)
	matches := re.FindStringSubmatch(string(body))
	if len(matches) < 2 {
		return fmt.Errorf("CSRF token not found in /config")
	}
	csrfToken := matches[1]

	// POST /config/import/commit
	form := url.Values{
		"csrf_token": {csrfToken},
		"yaml":       {yamlContent},
	}

	// POST /config/import/commit with Origin header
	encoded2 := form.Encode()
	importReq, err := http.NewRequest(http.MethodPost, serverURL+"/config/import/commit", strings.NewReader(encoded2))
	if err != nil {
		return fmt.Errorf("create import request: %w", err)
	}
	importReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	importReq.Header.Set("Origin", serverURL)

	importResp, err := client.Do(importReq)
	if err != nil {
		return fmt.Errorf("POST /config/import/commit: %w", err)
	}
	defer importResp.Body.Close()

	if importResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(importResp.Body)
		return fmt.Errorf("config restore: expected 200, got %d: %s", importResp.StatusCode, string(body))
	}
	return nil
}

// newDashboardClient creates a dashboard client connected to the test server.
// Used by #55 for end-to-end testing of the client library.
func newDashboardClient(serverURL string) *dashclient.Client {
	return dashclient.New(serverURL)
}

// snapshotConfig exports the current config and stores it in
// configSnapshotYAML for restoration after tests.
func snapshotConfig() {
	client := newClient()
	if err := loginRaw(client); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Config snapshot: login failed: %v\n", err)
		return
	}
	yaml, err := exportConfigRaw(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Config snapshot: export failed: %v\n", err)
		return
	}
	configSnapshotYAML = yaml
	fmt.Printf("📸 Config snapshot: %d bytes\n", len(yaml))
}

// restoreConfig restores the server config from configSnapshotYAML.
func restoreConfig() {
	if configSnapshotYAML == "" {
		fmt.Println("⚠️  No config snapshot to restore")
		return
	}
	client := newClient()
	if err := loginRaw(client); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Config restore: login failed: %v\n", err)
		return
	}
	if err := importConfigRaw(client, configSnapshotYAML); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Config restore failed: %v\n", err)
		return
	}
	fmt.Printf("📸 Config restored from snapshot (%d bytes)\n", len(configSnapshotYAML))
}

// ensureLoginRateLimitDisabled sets login_rate_limit_per_ip=0 at the start of
// an e2eweb run so the ~20 admin logins the suite performs from one IP are
// not throttled by the default per-IP login rate limit. TestMain calls it
// before snapshotConfig() so the snapshot captures limit=0 and restoreConfig()
// puts 0 back, keeping the shared dev server (air on :8083) unlimited between
// runs. Failures are warn-only: the suite still runs, but login-heavy tests
// may start returning 429 mid-run.
func ensureLoginRateLimitDisabled() {
	client := newClient()
	if err := loginRaw(client); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Disable login rate limit: login failed: %v\n", err)
		return
	}
	values, csrfToken, err := parseConfigFormRaw(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Disable login rate limit: %v\n", err)
		return
	}
	if values.Get("login_rate_limit_per_ip") == "0" {
		fmt.Println("🔓 Login rate limit already disabled (login_rate_limit_per_ip=0)")
		return
	}

	// Submit the full form: POST /config forces absent checkboxes to false,
	// so a partial submission would clobber enabled checkbox settings.
	values = cloneValues(values)
	values.Set("login_rate_limit_per_ip", "0")
	for _, key := range []string{"admin_current_password", "admin_new_password", "admin_confirm_password", "yaml"} {
		values.Del(key)
	}
	values.Set("csrf_token", csrfToken)

	req, err := http.NewRequest(http.MethodPost, serverURL+"/config", strings.NewReader(values.Encode()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Disable login rate limit: create request: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", serverURL)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Disable login rate limit: POST /config: %v\n", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "⚠️  Disable login rate limit: POST /config got %d\n", resp.StatusCode)
		return
	}
	fmt.Println("🔓 Login rate limit disabled for e2eweb run (login_rate_limit_per_ip=0)")
}

// idParam returns folderID or fileID with a skip message if empty.
func folderParam(t *testing.T) string {
	t.Helper()
	if folderID == "" {
		t.Skip("SKIP: no folder ID in database")
	}
	return folderID
}

func fileParam(t *testing.T) string {
	t.Helper()
	if fileID == "" {
		t.Skip("SKIP: no file ID in database")
	}
	return fileID
}

// serverDownTransportError reports whether err indicates the server is not
// accepting connections (old process gone, new process not yet listening).
func serverDownTransportError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) || errors.Is(opErr.Err, syscall.ECONNRESET) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(err.Error()), "connection refused")
}

// waitForServerDown polls /gallery/1 until a transport error indicates the
// server stopped accepting connections, up to the given timeout. Returns true
// if the server went down, false on timeout. Call after restart POSTs before
// waitForServer so the first 200 is from the new process, not the dying old one.
func waitForServerDown(t *testing.T, timeout time.Duration) bool {
	t.Helper()

	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}

	for time.Now().Before(deadline) {
		resp, err := client.Get(serverURL + "/gallery/1")
		if err != nil && serverDownTransportError(err) {
			return true
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}

	return false
}

// waitForServer polls /gallery/1 every second until the server responds with 200,
// up to the given timeout. Returns true if the server came back, false otherwise.
func waitForServer(t *testing.T, timeout time.Duration) bool {
	t.Helper()

	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}

	for time.Now().Before(deadline) {
		resp, err := client.Get(serverURL + "/gallery/1")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(1 * time.Second)
	}

	return false
}
