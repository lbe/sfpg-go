package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/config"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/parser"
)

// execCmd executes a tea.Cmd with a timeout and returns its message.
func execCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	type result struct {
		msg tea.Msg
	}
	ch := make(chan result, 1)
	go func() {
		ch <- result{cmd()}
	}()
	select {
	case r := <-ch:
		return r.msg
	case <-time.After(2 * time.Second):
		t.Fatal("command timed out")
		return nil
	}
}

// galleryPageWithCSRF returns a minimal gallery page HTML containing a CSRF token.
func galleryPageWithCSRF(token string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html><body>
<input type="hidden" name="csrf_token" value="%s" />
</body></html>`, token)
}

// loginTestServer creates an httptest server that simulates the login flow.
func loginTestServer(success bool) *httptest.Server {
	csrfToken := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login-form" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, galleryPageWithCSRF(csrfToken))
		case r.URL.Path == "/login" && r.Method == http.MethodPost:
			if r.FormValue("csrf_token") != csrfToken {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if success {
				w.Header().Set("Hx-Trigger", "auth-changed")
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// dashboardTestServer creates an httptest server that returns dashboard HTML.
func dashboardTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dashboard" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `<!DOCTYPE html>
<html><body>
<div id="dashboard-container">
	<div id="last-updated">22:30:00</div>
</div>
</body></html>`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

// TestTickCmd verifies tickCmd returns a TickMsg after the interval.
func TestTickCmd(t *testing.T) {
	cmd := tickCmd(1 * time.Millisecond)
	msg := execCmd(t, cmd)
	if _, ok := msg.(TickMsg); !ok {
		t.Errorf("msg type = %T, want TickMsg", msg)
	}
}

// TestFetchMetricsCmd_Success verifies successful dashboard fetch and parse.
func TestFetchMetricsCmd_Success(t *testing.T) {
	server := dashboardTestServer()
	defer server.Close()

	c := client.New(server.URL)
	cmd := fetchMetricsCmd(c)
	msg := execCmd(t, cmd)

	result, ok := msg.(MetricsFetchedMsg)
	if !ok {
		t.Fatalf("msg type = %T, want MetricsFetchedMsg", msg)
	}
	if result.err != nil {
		t.Errorf("err = %v, want nil", result.err)
	}
	if result.metrics == nil {
		t.Fatal("metrics should not be nil")
	}
}

// TestFetchMetricsCmd_FetchError verifies network errors are propagated.
func TestFetchMetricsCmd_FetchError(t *testing.T) {
	c := client.New("http://localhost:1")
	cmd := fetchMetricsCmd(c)
	msg := execCmd(t, cmd)

	result, ok := msg.(MetricsFetchedMsg)
	if !ok {
		t.Fatalf("msg type = %T, want MetricsFetchedMsg", msg)
	}
	if result.err == nil {
		t.Error("err should not be nil")
	}
}

// TestFetchMetricsCmd_ParseError verifies ErrDashboardNotFound on bad HTML.
func TestFetchMetricsCmd_ParseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!DOCTYPE html><html><body><div id="other"></div></body></html>`)
	}))
	defer server.Close()

	c := client.New(server.URL)
	cmd := fetchMetricsCmd(c)
	msg := execCmd(t, cmd)

	result, ok := msg.(MetricsFetchedMsg)
	if !ok {
		t.Fatalf("msg type = %T, want MetricsFetchedMsg", msg)
	}
	if !errors.Is(result.err, parser.ErrDashboardNotFound) {
		t.Errorf("err = %v, want ErrDashboardNotFound", result.err)
	}
}

// TestLoginCmd_Success verifies successful authentication.
func TestLoginCmd_Success(t *testing.T) {
	server := loginTestServer(true)
	defer server.Close()

	c := client.New(server.URL)
	cmd := loginCmd(c, "admin", "password")
	msg := execCmd(t, cmd)

	result, ok := msg.(LoginResultMsg)
	if !ok {
		t.Fatalf("msg type = %T, want LoginResultMsg", msg)
	}
	if result.err != nil {
		t.Errorf("err = %v, want nil", result.err)
	}
}

// TestLoginCmd_Failure verifies authentication failure returns ErrUnauthorized.
func TestLoginCmd_Failure(t *testing.T) {
	server := loginTestServer(false)
	defer server.Close()

	c := client.New(server.URL)
	cmd := loginCmd(c, "admin", "wrong")
	msg := execCmd(t, cmd)

	result, ok := msg.(LoginResultMsg)
	if !ok {
		t.Fatalf("msg type = %T, want LoginResultMsg", msg)
	}
	if !errors.Is(result.err, client.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", result.err)
	}
}

// execBatchCmd executes a command and asserts it returns a BatchMsg.
func execBatchCmd(t *testing.T, cmd tea.Cmd) tea.BatchMsg {
	t.Helper()
	msg := execCmd(t, cmd)
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("msg type = %T, want BatchMsg", msg)
	}
	return batch
}

// TestInit_WithCredentials verifies Init triggers automatic login.
func TestInit_WithCredentials(t *testing.T) {
	server := loginTestServer(true)
	defer server.Close()

	cfg := &config.Config{
		ServerURL: server.URL,
		Username:  "admin",
		Password:  "password",
		Refresh:   1 * time.Millisecond,
	}
	m := initialModel(cfg, client.New(server.URL))

	cmd := m.Init()
	batch := execBatchCmd(t, cmd)
	if len(batch) != 2 {
		t.Fatalf("len(batch) = %d, want 2", len(batch))
	}

	msg := execCmd(t, batch[1])
	result, ok := msg.(LoginResultMsg)
	if !ok {
		t.Fatalf("msg type = %T, want LoginResultMsg", msg)
	}
	if result.err != nil {
		t.Errorf("err = %v, want nil", result.err)
	}
}

// TestInit_WithoutCredentials verifies Init prompts for credentials.
func TestInit_WithoutCredentials(t *testing.T) {
	cfg := &config.Config{
		ServerURL: "http://localhost:8083",
		Refresh:   1 * time.Millisecond,
	}
	m := initialModel(cfg, client.New(cfg.ServerURL))

	cmd := m.Init()
	batch := execBatchCmd(t, cmd)
	if len(batch) != 2 {
		t.Fatalf("len(batch) = %d, want 2", len(batch))
	}

	msg := execCmd(t, batch[1])
	if _, ok := msg.(PromptCredentialsMsg); !ok {
		t.Errorf("msg type = %T, want PromptCredentialsMsg", msg)
	}
}

// TestUpdate_TickNotAuthenticated verifies ticks do nothing before login.
func TestUpdate_TickNotAuthenticated(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083", Refresh: 1 * time.Millisecond}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateNone

	updated, cmd := m.Update(TickMsg{})
	model := updated.(Model)

	if model.authState != authStateNone {
		t.Errorf("authState = %v, want %v", model.authState, authStateNone)
	}
	if cmd == nil {
		t.Fatal("cmd should not be nil")
	}
	if _, ok := execCmd(t, cmd).(TickMsg); !ok {
		t.Error("cmd should produce TickMsg")
	}
}

// TestUpdate_TickPaused verifies paused ticks do not fetch.
func TestUpdate_TickPaused(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083", Refresh: 1 * time.Millisecond}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticated
	m.paused = true

	updated, cmd := m.Update(TickMsg{})
	model := updated.(Model)

	if !model.paused {
		t.Error("paused should remain true")
	}
	if _, ok := execCmd(t, cmd).(TickMsg); !ok {
		t.Error("cmd should produce TickMsg")
	}
}

// TestUpdate_TickAutoRefreshDisabled verifies disabled auto-refresh does not fetch.
func TestUpdate_TickAutoRefreshDisabled(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083", Refresh: 1 * time.Millisecond}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticated
	m.autoRefresh = false

	updated, cmd := m.Update(TickMsg{})
	model := updated.(Model)

	if model.autoRefresh {
		t.Error("autoRefresh should remain false")
	}
	if _, ok := execCmd(t, cmd).(TickMsg); !ok {
		t.Error("cmd should produce TickMsg")
	}
}

// TestUpdate_TickAuthenticatedFetches verifies ticks fetch metrics when authenticated.
func TestUpdate_TickAuthenticatedFetches(t *testing.T) {
	server := dashboardTestServer()
	defer server.Close()

	cfg := &config.Config{
		ServerURL: server.URL,
		Refresh:   1 * time.Millisecond,
	}
	m := initialModel(cfg, client.New(server.URL))
	m.authState = authStateAuthenticated

	updated, cmd := m.Update(TickMsg{})
	model := updated.(Model)

	if model.authState != authStateAuthenticated {
		t.Errorf("authState = %v, want %v", model.authState, authStateAuthenticated)
	}

	batch := execBatchCmd(t, cmd)
	if len(batch) != 2 {
		t.Fatalf("len(batch) = %d, want 2", len(batch))
	}

	// Execute fetch command
	msg := execCmd(t, batch[0])
	if _, ok := msg.(MetricsFetchedMsg); !ok {
		t.Errorf("batch[0] msg type = %T, want MetricsFetchedMsg", msg)
	}
	// Execute tick command
	msg = execCmd(t, batch[1])
	if _, ok := msg.(TickMsg); !ok {
		t.Errorf("batch[1] msg type = %T, want TickMsg", msg)
	}
}

// TestUpdate_RefreshKeyNotAuthenticated verifies 'r' does nothing when not authenticated.
func TestUpdate_RefreshKeyNotAuthenticated(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStatePrompting

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model := updated.(Model)

	if model.loading {
		t.Error("loading should remain false")
	}
	// The 'r' keystroke is processed by the username text input, so a command is returned.
	if cmd == nil {
		t.Error("cmd should not be nil")
	}
}

// TestUpdate_RefreshKeyAuthenticated verifies 'r' triggers a fetch.
func TestUpdate_RefreshKeyAuthenticated(t *testing.T) {
	server := dashboardTestServer()
	defer server.Close()

	cfg := &config.Config{ServerURL: server.URL}
	m := initialModel(cfg, client.New(server.URL))
	m.authState = authStateAuthenticated

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model := updated.(Model)

	if !model.loading {
		t.Error("loading should be true")
	}
	if cmd == nil {
		t.Fatal("cmd should not be nil")
	}
	msg := execCmd(t, cmd)
	if _, ok := msg.(MetricsFetchedMsg); !ok {
		t.Errorf("msg type = %T, want MetricsFetchedMsg", msg)
	}
}

// TestUpdate_UnhandledKey verifies unhandled keys leave the model unchanged.
func TestUpdate_UnhandledKey(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticated

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model := updated.(Model)

	if model.authState != authStateAuthenticated {
		t.Errorf("authState changed to %v", model.authState)
	}
	if cmd != nil {
		t.Error("cmd should be nil for unhandled key")
	}
}

// TestUpdate_MetricsFetchedOtherError verifies non-unauthorized errors set err.
func TestUpdate_MetricsFetchedOtherError(t *testing.T) {
	cfg := &config.Config{ServerURL: "http://localhost:8083"}
	m := initialModel(cfg, client.New(cfg.ServerURL))
	m.authState = authStateAuthenticated

	updated, cmd := m.Update(MetricsFetchedMsg{err: errors.New("some error")})
	model := updated.(Model)

	if model.err == nil {
		t.Error("err should not be nil")
	}
	if model.authState != authStateAuthenticated {
		t.Errorf("authState = %v, want %v", model.authState, authStateAuthenticated)
	}
	if cmd != nil {
		t.Error("cmd should be nil")
	}
}

// TestHandleCredentialInput_Quit verifies ctrl+c and esc quit.
func TestHandleCredentialInput_Quit(t *testing.T) {
	for _, key := range []string{"ctrl+c", "esc"} {
		m := Model{
			authState:     authStatePrompting,
			usernameInput: textinput.New(),
			passwordInput: textinput.New(),
		}

		var updated tea.Model
		var cmd tea.Cmd
		// KeyMsg String() for esc/ctrl+c use Type, not Runes
		if key == "esc" {
			updated, cmd = m.handleCredentialInput(tea.KeyMsg{Type: tea.KeyEsc})
		} else {
			updated, cmd = m.handleCredentialInput(tea.KeyMsg{Type: tea.KeyCtrlC})
		}
		model := updated.(Model)

		if !model.quitting {
			t.Errorf("quitting should be true for %s", key)
		}
		if cmd == nil {
			t.Errorf("cmd should not be nil for %s", key)
		}
	}
}

// TestHandleCredentialInput_ShiftTab verifies shift+tab moves focus to username.
func TestHandleCredentialInput_ShiftTab(t *testing.T) {
	m := Model{
		authState:     authStatePrompting,
		usernameInput: textinput.New(),
		passwordInput: textinput.New(),
		focusPassword: true,
	}
	m.passwordInput.Focus()

	updated, _ := m.handleCredentialInput(tea.KeyMsg{Type: tea.KeyShiftTab})
	model := updated.(Model)

	if model.focusPassword {
		t.Error("focusPassword should be false after shift+tab")
	}
}

// TestHandleCredentialInput_TypeUsername verifies typing updates username input.
func TestHandleCredentialInput_TypeUsername(t *testing.T) {
	m := Model{
		authState:     authStatePrompting,
		usernameInput: textinput.New(),
		passwordInput: textinput.New(),
	}
	m.usernameInput.Focus()

	updated, _ := m.handleCredentialInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model := updated.(Model)

	if model.usernameInput.Value() != "a" {
		t.Errorf("username = %q, want %q", model.usernameInput.Value(), "a")
	}
	if model.focusPassword {
		t.Error("focus should remain on username")
	}
}

// TestHandleCredentialInput_TypePassword verifies typing updates password input.
func TestHandleCredentialInput_TypePassword(t *testing.T) {
	m := Model{
		authState:     authStatePrompting,
		usernameInput: textinput.New(),
		passwordInput: textinput.New(),
		focusPassword: true,
	}
	m.passwordInput.Focus()

	updated, _ := m.handleCredentialInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model := updated.(Model)

	if model.passwordInput.Value() != "x" {
		t.Errorf("password = %q, want %q", model.passwordInput.Value(), "x")
	}
}

// TestHandleCredentialInput_EnterSubmits verifies Enter returns CredentialsSubmittedMsg.
func TestHandleCredentialInput_EnterSubmits(t *testing.T) {
	m := Model{
		authState:     authStatePrompting,
		usernameInput: textinput.New(),
		passwordInput: textinput.New(),
	}
	m.usernameInput.SetValue("admin")
	m.passwordInput.SetValue("secret")

	_, cmd := m.handleCredentialInput(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("cmd should not be nil")
	}

	msg := execCmd(t, cmd)
	submitted, ok := msg.(CredentialsSubmittedMsg)
	if !ok {
		t.Fatalf("msg type = %T, want CredentialsSubmittedMsg", msg)
	}
	if submitted.username != "admin" {
		t.Errorf("username = %q, want %q", submitted.username, "admin")
	}
	if submitted.password != "secret" {
		t.Errorf("password = %q, want %q", submitted.password, "secret")
	}
}
