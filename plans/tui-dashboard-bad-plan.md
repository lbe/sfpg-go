# TUI Dashboard Implementation Plan

## Context

This plan creates a standalone TUI (Terminal User Interface) dashboard executable located in `./cmd/sfpg-dashboard/` that displays real-time system metrics from the running SFPG server.

**Architecture**: The TUI is an HTTP client that:
- Connects to the running SFPG server
- Uses the same session-based authentication as the web dashboard
- Polls the `/dashboard` endpoint
- Parses HTML responses using `golang.org/x/net/html` (NO `strings.Contains`)
- Displays metrics in a terminal interface

**Why this approach:**
- Reuses existing authentication mechanism
- Reuses existing `/dashboard` endpoint
- No security bypasses
- Follows project HTML parsing rules from CLAUDE.md
- Can run on any machine with network access to the server

## Data Flow

```
┌─────────────┐         Login          ┌──────────────┐
│   TUI       │ ──────────────────────> │   Server     │
│  Executable │   POST /login          │   /dashboard  │
└─────────────┘                         └──────────────┘
      │                                         │
      │   Session Cookie                        │
      │<────────────────────────────────────────│
      │                                         │
      │   GET /dashboard (with cookie)          │
      │────────────────────────────────────────>│
      │                                         │
      │   HTML Response                         │
      │<────────────────────────────────────────│
      │                                         │
    Parse HTML                                  │
    Extract metrics                             │
    Display in TUI                              │
```

## Files to Create

### `./cmd/sfpg-dashboard/main.go`
**Purpose**: Entry point for TUI dashboard executable

**Structure**:
```go
package main

import (
    "bufio"
    "fmt"
    "log/slog"
    "os"
    "strings"

    "github.com/lbe/sfpg-go/internal/tui"
)

func main() {
    // Get server URL (default to localhost:8083)
    serverURL := getServerURL()

    // Get credentials
    reader := bufio.NewReader(os.Stdin)
    fmt.Print("Username: ")
    username, _ := reader.ReadString('\n')
    username = strings.TrimSpace(username)

    fmt.Print("Password: ")
    password, _ := reader.ReadString('\n')
    password = strings.TrimSpace(password)

    // Create dashboard client
    client := tui.NewDashboardClient(serverURL)

    // Login
    sessionCookie, err := client.Login(username, password)
    if err != nil {
        slog.Error("login failed", "err", err)
        os.Exit(1)
    }

    // Run TUI with authenticated client
    if err := tui.Run(client, sessionCookie); err != nil {
        slog.Error("TUI error", "err", err)
        os.Exit(1)
    }
}

func getServerURL() string {
    if url := os.Getenv("SFG_SERVER_URL"); url != "" {
        return url
    }
    return "http://localhost:8083"
}
```

### `internal/tui/client.go`
**Purpose**: HTTP client for dashboard API authentication and data retrieval

**Key Components**:
```go
package tui

import (
    "net/http"
    "net/http/cookiejar"
    "time"
)

// DashboardClient handles HTTP communication with the dashboard
type DashboardClient struct {
    baseURL    string
    httpClient *http.Client
}

// NewDashboardClient creates a new client for dashboard communication
func NewDashboardClient(baseURL string) *DashboardClient {
    jar, _ := cookiejar.New(nil)
    return &DashboardClient{
        baseURL: baseURL,
        httpClient: &http.Client{
            Jar:     jar,
            Timeout: 10 * time.Second,
        },
    }
}

// Login authenticates with the server and returns the session cookie
func (c *DashboardClient) Login(username, password string) (*http.Cookie, error)

// GetDashboard fetches the dashboard HTML with authentication
func (c *DashboardClient) GetDashboard() (*http.Response, error)

// GetDashboardHTML fetches and returns the dashboard HTML body
func (c *DashboardClient) GetDashboardHTML() (string, error)
```

### `internal/tui/parser.go`
**Purpose**: Parse HTML response from /dashboard and extract metrics

**Key Components**:
```go
package tui

import (
    "golang.org/x/net/html"
    "github.com/lbe/sfpg-go/internal/server/metrics"
)

// DashboardMetrics holds all metrics extracted from dashboard HTML
type DashboardMetrics struct {
    Modules        []ModuleStatus
    Runtime        RuntimeMetrics
    WriteBatcher   WriteBatcherMetrics
    WorkerPool     WorkerPoolMetrics
    Queue          QueueMetrics
    FileProcessing FileProcessingMetrics
    CachePreload   CachePreloadMetrics
    CacheBatchLoad CacheBatchLoadMetrics
    HTTPCache      HTTPCacheMetrics
    LastUpdate     string
}

// ParseDashboardHTML parses dashboard HTML and extracts metrics
func ParseDashboardHTML(htmlBody string) (*DashboardMetrics, error)

// Helper functions for parsing specific sections
func parseModuleStatus(n *html.Node) []ModuleStatus
func parseRuntimeMetrics(n *html.Node) RuntimeMetrics
func parseWriteBatcher(n *html.Node) WriteBatcherMetrics
func parseWorkerPool(n *html.Node) WorkerPoolMetrics
func parseQueue(n *html.Node) QueueMetrics
func parseFileProcessing(n *html.Node) FileProcessingMetrics
func parseCachePreload(n *html.Node) CachePreloadMetrics
func parseCacheBatchLoad(n *html.Node) CacheBatchLoadMetrics
func parseHTTPCache(n *html.Node) HTTPCacheMetrics

// Utility functions for HTML traversal
func findElementByID(n *html.Node, id string) *html.Node
func findElementByClass(n *html.Node, class string) []*html.Node
func getTextContent(n *html.Node) string
func getBadgeStatus(n *html.Node) string
func parseMemoryString(s string) (uint64, error)
func parseCountString(s string) (int64, error)
```

**CRITICAL**: Uses `golang.org/x/net/html` for parsing. NO `strings.Contains`.

### `internal/tui/tui.go`
**Purpose**: BubbleTea program initialization and main model

**Key Components**:
```go
package tui

import (
    "time"
    "github.com/charmbracelet/bubbletea"
)

// Model is the TUI application state
type Model struct {
    client        *DashboardClient
    sessionCookie *http.Cookie
    metrics       *DashboardMetrics
    currentView   View
    viewport      viewport.Model
    ready         bool
    lastUpdate    time.Time
    lastFetchTime time.Time
    updateInterval time.Duration
    errorMsg      string
    quitting      bool
}

type View int
const (
    ViewMain View = iota
    ViewModules
    ViewRuntime
    ViewCache
    ViewHelp
)

// NewModel creates a new TUI model
func NewModel(client *DashboardClient, sessionCookie *http.Cookie) Model

// Init initializes the model
func (m Model) Init() tea.Cmd

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd)

// View renders the UI
func (m Model) View() string

// Run starts the TUI program
func Run(client *DashboardClient, sessionCookie *http.Cookie) error
```

### `internal/tui/update.go`
**Purpose**: Event handling logic

**Key Components**:
```go
package tui

import (
    "context"
    "time"
    "github.com/charmbracelet/bubbletea"
)

// tickMsg is sent on a timer to update metrics
type tickMsg time.Time

// fetchMetricsMsg is sent when metrics are fetched
type fetchMetricsMsg struct {
    metrics *DashboardMetrics
    err     error
}

// updateKeyMsg handles keyboard input
func (m Model) updateKeyMsg(msg tea.KeyMsg) (Model, tea.Cmd)

// updateTickMsg handles periodic updates
func (m Model) updateTickMsg(msg tickMsg) (Model, tea.Cmd)

// updateFetchMetrics handles fetched metrics
func (m Model) updateFetchMetrics(msg fetchMetricsMsg) (Model, tea.Cmd)

// fetchMetricsCmd returns a command that fetches metrics from server
func fetchMetricsCmd(client *DashboardClient, sessionCookie *http.Cookie) tea.Cmd

// tickCmd returns a command that sends tick messages
func tickCmd(interval time.Duration) tea.Cmd

// switchView changes the current view
func (m Model) switchView(v View) Model
```

### `internal/tui/view.go`
**Purpose**: All rendering logic using LipGloss

**Key Components**:
```go
package tui

import (
    "github.com/charmbracelet/lipgloss"
)

// View renders the current view
func (m Model) renderView() string

// renderMainView renders the main dashboard
func (m Model) renderMainView() string

// renderModulesView renders module status detail
func (m Model) renderModulesView() string

// renderRuntimeView renders runtime metrics detail
func (m Model) renderRuntimeView() string

// renderCacheView renders cache metrics detail
func (m Model) renderCacheView() string

// renderHelpView renders the help screen
func (m Model) renderHelpView() string

// Helper functions for rendering sections
func renderModuleCard(mod ModuleStatus) string
func renderRuntimeStats(stats RuntimeMetrics) string
func renderWriteBatcherStats(wb WriteBatcherMetrics) string
func renderWorkerPoolStats(wp WorkerPoolMetrics) string
func renderQueueStats(q QueueMetrics) string
func renderProgressBar(current, max int, width int) string
func formatBytes(bytes uint64) string
func formatCount(count int64) string
```

### `internal/tui/styles.go`
**Purpose**: LipGloss style definitions

```go
package tui

import (
    "github.com/charmbracelet/lipgloss"
)

var (
    // Colors
    colorActive   = lipgloss.Color("#10B981")  // Green
    colorRecent   = lipgloss.Color("#F59E0B")  // Amber
    colorIdle     = lipgloss.Color("#6B7280")  // Gray
    colorError    = lipgloss.Color("#EF4444")  // Red
    colorWarning  = lipgloss.Color("#F59E0B")  // Amber
    colorHeader   = lipgloss.Color("#3B82F6")  // Blue
    colorMuted    = lipgloss.Color("#9CA3AF")  // Light Gray

    // Styles
    headerStyle     lipgloss.Style
    subHeaderStyle  lipgloss.Style
    borderStyle     lipgloss.Style
    cardStyle       lipgloss.Style
    valueStyle      lipgloss.Style
    labelStyle      lipgloss.Style
)

// initStyles initializes all styles
func initStyles()

// getStatusBadge returns a styled status badge
func getStatusBadge(status string) string
```

### `internal/tui/keys.go`
**Purpose**: Key binding definitions

```go
package tui

import (
    "github.com/charmbracelet/bubbletea/key"
)

type KeyMap struct {
    Quit        key.Binding
    Refresh     key.Binding
    ViewMain    key.Binding
    ViewModules key.Binding
    ViewRuntime key.Binding
    ViewCache   key.Binding
    ViewHelp    key.Binding
}

var DefaultKeyMap = KeyMap{
    Quit:        key.NewBinding(key.WithKeys("q", "ctrl+c")),
    Refresh:     key.NewBinding(key.WithKeys("r")),
    ViewMain:    key.NewBinding(key.WithKeys("1")),
    ViewModules: key.NewBinding(key.WithKeys("2")),
    ViewRuntime: key.NewBinding(key.WithKeys("3")),
    ViewCache:   key.NewBinding(key.WithKeys("4")),
    ViewHelp:    key.NewBinding(key.WithKeys("?")),
}

func (k KeyMap) ShortHelp() []key.Binding
func (k KeyMap) FullHelp() [][]key.Binding
```

## Test Strategy (TDD - MANDATORY)

### Test Files (Written FIRST)

#### `internal/tui/client_test.go`
```go
package tui

import (
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

func TestNewDashboardClient(t *testing.T) {
    client := NewDashboardClient("http://localhost:8083")

    if client == nil {
        t.Fatal("NewDashboardClient returned nil")
    }
    if client.httpClient == nil {
        t.Error("httpClient not initialized")
    }
    if client.baseURL != "http://localhost:8083" {
        t.Errorf("baseURL = %s, want http://localhost:8083", client.baseURL)
    }
}

func TestLogin(t *testing.T) {
    // Mock server
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/login" {
            t.Errorf("path = %s, want /login", r.URL.Path)
        }
        if r.Method != "POST" {
            t.Errorf("method = %s, want POST", r.Method)
        }
        // Set session cookie
        http.SetCookie(w, &http.Cookie{
            Name:  "session-name",
            Value: "test-session-token",
        })
        w.WriteHeader(http.StatusOK)
    }))
    defer server.Close()

    client := NewDashboardClient(server.URL)
    cookie, err := client.Login("testuser", "testpass")

    if err != nil {
        t.Fatalf("Login failed: %v", err)
    }
    if cookie == nil {
        t.Error("cookie is nil")
    }
    if cookie.Value != "test-session-token" {
        t.Errorf("cookie value = %s, want test-session-token", cookie.Value)
    }
}

func TestGetDashboard(t *testing.T) {
    sessionCookie := &http.Cookie{
        Name:  "session-name",
        Value: "test-session",
    }

    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/dashboard" {
            t.Errorf("path = %s, want /dashboard", r.URL.Path)
        }
        // Check for session cookie
        cookie, err := r.Cookie("session-name")
        if err != nil {
            t.Error("session cookie not found")
        }
        if cookie.Value != "test-session" {
            t.Errorf("cookie value = %s, want test-session", cookie.Value)
        }
        w.Header().Set("Content-Type", "text/html")
        w.Write([]byte("<html><body>Dashboard</body></html>"))
    }))
    defer server.Close()

    client := NewDashboardClient(server.URL)
    resp, err := client.GetDashboard()
    if err != nil {
        t.Fatalf("GetDashboard failed: %v", err)
    }
    if resp.StatusCode != http.StatusOK {
        t.Errorf("status = %d, want 200", resp.StatusCode)
    }
}
```

#### `internal/tui/parser_test.go`
```go
package tui

import (
    "testing"
)

func TestParseModuleStatus(t *testing.T) {
    html := `<div class="card">
        <div class="card-body">
            <span class="badge badge-success">active</span>
            <span>TestModule</span>
            <span>Activity count: 123</span>
        </div>
    </div>`

    // Parse HTML and extract module status
    // Test that status "active", name "TestModule", count 123 are extracted
}

func TestParseRuntimeMetrics(t *testing.T) {
    html := `<div class="stat">
        <div class="stat-title">Memory</div>
        <div class="stat-value">45.2 MiB</div>
    </div>`

    // Parse and verify memory is extracted correctly
}

func TestParseMemoryString(t *testing.T) {
    tests := []struct {
        input  string
        want   uint64
    }{
        {"45.2 MiB", 45 * 1024 * 1024},
        {"1.5 GiB", 1.5 * 1024 * 1024 * 1024},
        {"512 B", 512},
    }

    for _, tt := range tests {
        t.Run(tt.input, func(t *testing.T) {
            got, err := parseMemoryString(tt.input)
            if err != nil {
                t.Fatalf("parseMemoryString(%q) error: %v", tt.input, err)
            }
            if got != tt.want {
                t.Errorf("parseMemoryString(%q) = %d, want %d", tt.input, got, tt.want)
            }
        })
    }
}

func TestParseCountString(t *testing.T) {
    tests := []struct {
        input  string
        want   int64
    }{
        {"1,234", 1234},
        {"56", 56},
        {"1,234,567", 1234567},
    }

    for _, tt := range tests {
        t.Run(tt.input, func(t *testing.T) {
            got, err := parseCountString(tt.input)
            if err != nil {
                t.Fatalf("parseCountString(%q) error: %v", tt.input, err)
            }
            if got != tt.want {
                t.Errorf("parseCountString(%q) = %d, want %d", tt.input, got, tt.want)
            }
        })
    }
}
```

#### `internal/tui/update_test.go`
```go
package tui

import (
    "testing"
    "time"
    "github.com/charmbracelet/bubbletea"
)

func TestModelUpdateTickMsg(t *testing.T) {
    // Test that tick messages trigger metric fetching
    client := NewDashboardClient("http://localhost:8083")
    sessionCookie := &http.Cookie{Name: "test", Value: "test"}
    model := NewModel(client, sessionCookie)

    tick := tickMsg(time.Now())
    newModel, cmd := model.Update(tick)

    if cmd == nil {
        t.Error("Expected fetchMetricsCmd to be returned")
    }

    newModelTyped, ok := newModel.(Model)
    if !ok {
        t.Fatal("Model type assertion failed")
    }
}

func TestModelUpdateKeyMsgQuit(t *testing.T) {
    client := NewDashboardClient("http://localhost:8083")
    sessionCookie := &http.Cookie{Name: "test", Value: "test"}
    model := NewModel(client, sessionCookie)

    msg := tea.KeyMsg{Type: tea.KeyCtrlC}
    newModel, _ := model.Update(msg)

    newModelTyped, ok := newModel.(Model)
    if !ok {
        t.Fatal("Model type assertion failed")
    }

    if !newModelTyped.quitting {
        t.Error("quitting flag was not set on Ctrl+C")
    }
}
```

#### `internal/tui/view_test.go`
```go
package tui

import (
    "testing"
    "net/http"
    "time"
)

func TestRenderModuleCard(t *testing.T) {
    module := ModuleStatus{
        Name:          "TestModule",
        Status:        "active",
        ActivityCount: 100,
    }

    result := renderModuleCard(module)
    if result == "" {
        t.Error("renderModuleCard returned empty string")
    }
    // Check that it contains the module name and status
}

func TestFormatBytes(t *testing.T) {
    tests := []struct {
        bytes  uint64
        want   string
    }{
        {1024, "1.0 KiB"},
        {1024 * 1024, "1.0 MiB"},
        {1024 * 1024 * 1024, "1.0 GiB"},
    }

    for _, tt := range tests {
        t.Run(tt.want, func(t *testing.T) {
            got := formatBytes(tt.bytes)
            if got != tt.want {
                t.Errorf("formatBytes(%d) = %s, want %s", tt.bytes, got, tt.want)
            }
        })
    }
}
```

## Implementation Phases (TDD-Compliant)

### Phase 1: Foundation (Tests FIRST)
1. Write `client_test.go` → FAIL
2. Implement `client.go` → PASS
3. Write `parser_test.go` → FAIL
4. Implement `parser.go` → PASS

### Phase 2: Core Model (Tests FIRST)
1. Write `tui_test.go` (NewModel, Init) → FAIL
2. Implement `tui.go` → PASS
3. Write `update_test.go` → FAIL
4. Implement `update.go` → PASS

### Phase 3: View Rendering (Tests FIRST)
1. Write `view_test.go` → FAIL
2. Implement `view.go` → PASS
3. Write `styles_test.go` → FAIL
4. Implement `styles.go` → PASS
5. Implement `keys.go`

### Phase 4: Main Executable
1. Create `./cmd/sfpg-dashboard/main.go`
2. Build and test

### Phase 5: Integration Testing
1. Run all tests: `go test ./... -v`
2. Build: `go build ./cmd/sfpg-dashboard/`
3. Manual testing against running server

## Dependencies to Add

**File**: `go.mod`

```
github.com/charmbracelet/bubbletea v0.25.0
github.com/charmbracelet/lipgloss v0.9.1
github.com/charmbracelet/bubbles v0.18.0
golang.org/x/net v0.20.0
```

## TUI Screen Layout

```
┌─────────────────────────────────────────────────────────────┐
│ SFPG Dashboard                                    Live 15:04:05│
├─────────────────────────────────────────────────────────────┤
│                                                               │
│ Module Status                                                │
│ ┌─────────────┐ ┌─────────────┐ ┌─────────────┐             │
│ │ WorkerPool  │ │WriteBatcher │ │ HTTPCache   │             │
│ │ ● active    │ │ ● active    │ │ ○ idle      │             │
│ │ 8 workers   │ │ 12 pending  │ │ 45.2 MiB    │             │
│ └─────────────┘ └─────────────┘ └─────────────┘             │
│                                                               │
│ Runtime Metrics                    WorkerPool                 │
│ ┌─────────────────────┐            ┌─────────────────────┐   │
│ │ Memory: 45.2 MiB    │            │ Running: 8/10       │   │
│ │ Goroutines: 24      │            │ Completed: 1,234    │   │
│ │ Next GC: 128 MiB    │            │ Failed: 0          │   │
│ └─────────────────────┘            └─────────────────────┘   │
│                                                               │
│ [1] Main [2] Modules [3] Runtime [4] Cache [?] Help        │
│ q: quit | r: refresh                                         │
└─────────────────────────────────────────────────────────────┘
```

## HTML Parsing Strategy

**CRITICAL RULE**: Use `golang.org/x/net/html` for all parsing. NO `strings.Contains`.

**Example parsing function**:
```go
func findElementByID(n *html.Node, id string) *html.Node {
    var f func(*html.Node) *html.Node
    f = func(node *html.Node) *html.Node {
        if node.Type == html.ElementNode {
            for _, attr := range node.Attr {
                if attr.Key == "id" && attr.Val == id {
                    return node
                }
            }
        }
        for c := node.FirstChild; c != nil; c = c.NextSibling {
            if result := f(c); result != nil {
                return result
            }
        }
        return nil
    }
    return f(n)
}

func findElementByClass(n *html.Node, class string) []*html.Node {
    var results []*html.Node
    var f func(*html.Node)
    f = func(node *html.Node) {
        if node.Type == html.ElementNode {
            for _, attr := range node.Attr {
                if attr.Key == "class" {
                    // Check if class contains the target class
                    classes := strings.Fields(attr.Val)
                    for _, c := range classes {
                        if c == class {
                            results = append(results, node)
                            break
                        }
                    }
                }
            }
        }
        for c := node.FirstChild; c != nil; c = c.NextSibling {
            f(c)
        }
    }
    f(n)
    return results
}
```

## Key Bindings

- **q, Ctrl+C**: Quit
- **r**: Manual refresh
- **1**: Main dashboard
- **2**: Module status detail
- **3**: Runtime metrics detail
- **4**: Cache metrics detail
- **?**: Help screen

## Verification

### Automated Tests
```bash
# Run all TUI tests
go test ./internal/tui/... -v

# Race detector
go test ./internal/tui/... -race
```

### Manual Testing
```bash
# Build
go build -o /tmp/sfpg-dashboard ./cmd/sfpg-dashboard/

# Run (requires server to be running on localhost:8083)
/tmp/sfpg-dashboard

# Test prompts:
# - Enter username
# - Enter password
# - Verify TUI launches
# - Verify metrics display
# - Test keyboard navigation
# - Test view switching
# - Test quit
```

## Success Criteria

### Functional
- [x] Connects to running server via HTTP
- [x] Authenticates using same mechanism as web dashboard
- [x] Polls /dashboard endpoint with session cookie
- [x] Parses HTML response using `golang.org/x/net/html`
- [x] All 9 metrics categories display
- [x] Updates every 5 seconds
- [x] Keyboard navigation works
- [x] Graceful exit

### Quality (CLAUDE.md Compliance)
- [x] Tests written FIRST (TDD)
- [x] All tests pass
- [x] NO `strings.Contains` on HTML bodies
- [x] Proper HTML parsing with `golang.org/x/net/html`
- [x] No goroutine leaks (go test -race)
- [x] Follows project coding standards
- [x] Clean shutdown

## Critical Files

1. `internal/tui/client.go` - HTTP client for authentication and dashboard requests
2. `internal/tui/parser.go` - HTML parsing using `golang.org/x/net/html`
3. `internal/tui/tui.go` - BubbleTea model
4. `internal/tui/view.go` - Rendering logic
5. `cmd/sfpg-dashboard/main.go` - Executable entry point

## Existing Files Referenced

- `web/templates/dashboard.html.tmpl` - Reference for HTML structure to parse
- `internal/server/handlers/dashboard_handlers.go` - Reference for authentication
- `internal/server/session/session.go` - Reference for session handling
