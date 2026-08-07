package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/config"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/parser"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/tuiview"
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

	if view != "Goodbye!\n" {
		t.Errorf("View for quitting model = %q, want %q", view, "Goodbye!\n")
	}
}

// TestViewLoading shows loading message
func TestViewLoading(t *testing.T) {
	m := Model{
		serverURL: "http://localhost:8083",
		loading:   true,
	}
	view := m.View()

	want := "Connecting to http://localhost:8083...\n"
	if view != want {
		t.Errorf("View for loading model = %q, want %q", view, want)
	}
}

// TestViewDashboard renders metrics
func TestViewDashboard(t *testing.T) {
	m := Model{
		metrics: &parser.DashboardMetrics{
			LastUpdated: "22:30:00",
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

	tuiview.AssertPlainIncludes(t, view, "22:30:00")
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

	tuiview.AssertPlainIncludes(t, view, "Username")
	tuiview.AssertPlainIncludes(t, view, "Password")
	tuiview.AssertPlainIncludes(t, view, "Tab")
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

// TestMain_Help exits cleanly when -help is requested.
func TestMain_Help(t *testing.T) {
	origArgs := os.Args
	origExit := osExit
	origRun := runProgram
	defer func() {
		os.Args = origArgs
		osExit = origExit
		runProgram = origRun
	}()

	os.Args = []string{"sfpg-go-dashboard", "-help"}
	var exitCode int
	osExit = func(code int) {
		exitCode = code
	}
	runProgramCalled := false
	runProgram = func(*tea.Program) (tea.Model, error) {
		runProgramCalled = true
		return nil, nil
	}

	main()

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if runProgramCalled {
		t.Error("runProgram should not be called when -help is requested")
	}
}

// TestMain_RunError exits with code 1 when the program fails.
func TestMain_RunError(t *testing.T) {
	origArgs := os.Args
	origExit := osExit
	origRun := runProgram
	origStderr := stderr
	defer func() {
		os.Args = origArgs
		osExit = origExit
		runProgram = origRun
		stderr = origStderr
	}()

	os.Args = []string{"sfpg-go-dashboard", "-server", "http://localhost:8083"}
	var exitCode int
	osExit = func(code int) {
		exitCode = code
	}
	runProgram = func(*tea.Program) (tea.Model, error) {
		return nil, errors.New("tty unavailable")
	}

	var errOut strings.Builder
	stderr = &errOut

	main()

	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(errOut.String(), "tty unavailable") {
		t.Errorf("stderr = %q, want to contain 'tty unavailable'", errOut.String())
	}
}

// TestRunDashboard_Help returns 0 without running the program.
func TestRunDashboard_Help(t *testing.T) {
	runCalled := false
	runProgram := func(*tea.Program) (tea.Model, error) {
		runCalled = true
		return nil, errors.New("should not be called")
	}

	code := runDashboard([]string{"-help"}, runProgram)
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if runCalled {
		t.Error("runProgram should not be called for -help")
	}
}

// TestRunDashboard_RunError returns 1 and logs the error.
func TestRunDashboard_RunError(t *testing.T) {
	origStderr := stderr
	defer func() { stderr = origStderr }()

	var errOut strings.Builder
	stderr = &errOut

	runProgram := func(*tea.Program) (tea.Model, error) {
		return nil, errors.New("tty unavailable")
	}

	code := runDashboard([]string{"-server", "http://localhost:8083"}, runProgram)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "tty unavailable") {
		t.Errorf("stderr = %q, want to contain 'tty unavailable'", errOut.String())
	}
}

// TestRunDashboard_Success returns 0 on successful program run.
func TestRunDashboard_Success(t *testing.T) {
	runProgram := func(*tea.Program) (tea.Model, error) {
		return Model{}, nil
	}

	code := runDashboard([]string{"-server", "http://localhost:8083"}, runProgram)
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
}
