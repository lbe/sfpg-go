package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/config"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/parser"
)

// TestInitialModel creates model with correct defaults
func TestInitialModel(t *testing.T) {
	cfg := &config.Config{
		ServerURL: "http://localhost:8083",
		Refresh:   5 * time.Second,
		NoRefresh: false,
		Username:  "testuser",
		Password:  "testpass",
	}
	c := client.New(cfg.ServerURL)

	m := initialModel(cfg, c)

	if m.serverURL != "http://localhost:8083" {
		t.Errorf("serverURL = %q, want %q", m.serverURL, "http://localhost:8083")
	}
	if m.refreshInterval != 5*time.Second {
		t.Errorf("refreshInterval = %v, want %v", m.refreshInterval, 5*time.Second)
	}
	if !m.autoRefresh {
		t.Error("autoRefresh should be true")
	}
	if m.username != "testuser" {
		t.Errorf("username = %q, want %q", m.username, "testuser")
	}
	if m.password != "testpass" {
		t.Errorf("password = %q, want %q", m.password, "testpass")
	}
	if m.client == nil {
		t.Error("client should not be nil")
	}
}

// TestModelAuthStates tracks authentication state
func TestModelAuthStates(t *testing.T) {
	m := Model{}

	if m.authState != authStateNone {
		t.Errorf("default authState = %v, want %v", m.authState, authStateNone)
	}
}

// TestUpdateWindowSize sets width and height
func TestUpdateWindowSize(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	model := updated.(Model)

	if model.width != 100 {
		t.Errorf("width = %d, want 100", model.width)
	}
	if model.height != 50 {
		t.Errorf("height = %d, want 50", model.height)
	}
}

// TestUpdateQuit handles quit key
func TestUpdateQuit(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticated

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model := updated.(Model)

	if !model.quitting {
		t.Error("quitting should be true after 'q' key")
	}
	if cmd == nil {
		t.Error("cmd should not be nil for quit")
	}
}

// TestUpdateScrollUp handles scroll up
func TestUpdateScrollUp(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticated
	m.scrollY = 5

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	model := updated.(Model)

	if model.scrollY != 4 {
		t.Errorf("scrollY = %d, want 4", model.scrollY)
	}
}

// TestUpdateScrollDown handles scroll down
func TestUpdateScrollDown(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticated
	m.scrollY = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	model := updated.(Model)

	if model.scrollY != 1 {
		t.Errorf("scrollY = %d, want 1", model.scrollY)
	}
}

// TestUpdateScrollUpMin prevents negative scroll
func TestUpdateScrollUpMin(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticated
	m.scrollY = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	model := updated.(Model)

	if model.scrollY < 0 {
		t.Errorf("scrollY = %d, should not be negative", model.scrollY)
	}
}

// TestUpdatePause toggles pause state
func TestUpdatePause(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticated
	m.paused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	model := updated.(Model)

	if !model.paused {
		t.Error("paused should be true after 'p' key")
	}

	updated2, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	model2 := updated2.(Model)

	if model2.paused {
		t.Error("paused should be false after second 'p' key")
	}
}

// TestViewQuitting shows goodbye message
func TestViewQuitting(t *testing.T) {
	m := Model{quitting: true}
	view := m.View()

	if !strings.Contains(view, "Goodbye") {
		t.Errorf("View for quitting model should contain 'Goodbye', got: %s", view)
	}
}

// TestViewLoading shows loading message
func TestViewLoading(t *testing.T) {
	m := Model{
		serverURL: "http://localhost:8083",
		loading:   true,
	}
	view := m.View()

	if !strings.Contains(view, "Connecting") {
		t.Errorf("View for loading model should contain 'Connecting', got: %s", view)
	}
}

// TestViewDashboard renders metrics
func TestViewDashboard(t *testing.T) {
	m := Model{
		metrics: &parser.DashboardMetrics{
			LastUpdated: "22:30:00",
			Modules: []parser.ModuleStatus{
				{Name: "discovery", Status: "active"},
			},
			Memory: parser.MemoryStats{
				Allocated: "15.0 MiB",
			},
			Runtime: parser.RuntimeStats{
				Uptime: "1m30s",
			},
		},
		width:       100,
		height:      50,
		authState:   authStateAuthenticated,
		autoRefresh: true,
	}
	view := m.View()

	// Check header has timestamp on right
	if !strings.Contains(view, "22:30:00") {
		t.Error("View should contain last updated time")
	}

	// Check module status on one line
	if !strings.Contains(view, "Module Status") {
		t.Error("View should contain Module Status")
	}
}

// TestViewLogin renders login form
func TestViewLogin(t *testing.T) {
	m := Model{
		authState:     authStatePrompting,
		usernameInput: textinput.New(),
		passwordInput: textinput.New(),
	}
	m.usernameInput.Focus()

	view := m.View()

	if !strings.Contains(view, "Username") {
		t.Error("Login view should contain Username")
	}
	if !strings.Contains(view, "Password") {
		t.Error("Login view should contain Password")
	}
	if !strings.Contains(view, "Tab") {
		t.Error("Login view should contain Tab instructions")
	}
}

// TestHandleCredentialInputTab switches focus
func TestHandleCredentialInputTab(t *testing.T) {
	m := Model{
		authState:     authStatePrompting,
		usernameInput: textinput.New(),
		passwordInput: textinput.New(),
		focusPassword: false,
	}
	m.usernameInput.Focus()

	// Use tea.KeyMsg with Type tea.KeyTab
	updated, _ := m.handleCredentialInput(tea.KeyMsg{Type: tea.KeyTab})
	model := updated.(Model)

	if !model.focusPassword {
		t.Error("focusPassword should be true after Tab")
	}
}

// TestHandleCredentialInputEnter returns credentials message
func TestHandleCredentialInputEnter(t *testing.T) {
	m := Model{
		authState:     authStatePrompting,
		usernameInput: textinput.New(),
		passwordInput: textinput.New(),
	}
	m.usernameInput.SetValue("admin")
	m.passwordInput.SetValue("password")

	updated, cmd := m.handleCredentialInput(tea.KeyMsg{Type: tea.KeyEnter})
	model := updated.(Model)

	// handleCredentialInput doesn't change authState directly - it returns a cmd
	// The authState change happens in Update when processing CredentialsSubmittedMsg
	if cmd == nil {
		t.Error("cmd should not be nil for Enter - should return CredentialsSubmittedMsg")
	}
	_ = model // model is returned unchanged by handleCredentialInput for Enter
}

// TestLoginResultSuccess sets authenticated state
func TestLoginResultSuccess(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticating

	updated, _ := m.Update(LoginResultMsg{err: nil})
	model := updated.(Model)

	if model.authState != authStateAuthenticated {
		t.Errorf("authState = %v, want %v", model.authState, authStateAuthenticated)
	}
}

// TestLoginResultFailure shows error
func TestLoginResultFailure(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticating

	updated, _ := m.Update(LoginResultMsg{err: client.ErrUnauthorized})
	model := updated.(Model)

	if model.authState != authStatePrompting {
		t.Errorf("authState = %v, want %v", model.authState, authStatePrompting)
	}
	if model.err == nil {
		t.Error("err should not be nil for failed login")
	}
}

// TestMetricsFetchedSuccess updates metrics
func TestMetricsFetchedSuccess(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticated

	metrics := &parser.DashboardMetrics{
		LastUpdated: "22:30:00",
	}

	updated, _ := m.Update(MetricsFetchedMsg{metrics: metrics, err: nil})
	model := updated.(Model)

	if model.metrics == nil {
		t.Fatal("metrics should not be nil")
	}
	if model.metrics.LastUpdated != "22:30:00" {
		t.Errorf("metrics.LastUpdated = %q, want %q", model.metrics.LastUpdated, "22:30:00")
	}
}

// TestMetricsFetchedUnauthorized prompts for credentials
func TestMetricsFetchedUnauthorized(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticated

	updated, _ := m.Update(MetricsFetchedMsg{metrics: nil, err: client.ErrUnauthorized})
	model := updated.(Model)

	if model.authState != authStatePrompting {
		t.Errorf("authState = %v, want %v", model.authState, authStatePrompting)
	}
}
