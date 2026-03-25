package main

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/parser"
)

// tickCmd creates a command that sends a TickMsg after the given interval.
// This is used to trigger periodic refreshes of dashboard data.
func tickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// fetchMetricsCmd creates a command that fetches dashboard metrics from the server.
// It returns a MetricsFetchedMsg containing the parsed metrics or an error.
func fetchMetricsCmd(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		html, err := c.FetchDashboard(context.Background())
		if err != nil {
			return MetricsFetchedMsg{metrics: nil, err: err}
		}
		metrics, err := parser.ParseDashboard(strings.NewReader(html))
		if err != nil {
			return MetricsFetchedMsg{metrics: nil, err: err}
		}
		return MetricsFetchedMsg{metrics: metrics, err: nil}
	}
}

// loginCmd creates a command that attempts to authenticate with the server.
// It returns a LoginResultMsg containing any error from the login attempt.
func loginCmd(c *client.Client, username, password string) tea.Cmd {
	return func() tea.Msg {
		err := c.Login(context.Background(), username, password)
		return LoginResultMsg{err: err}
	}
}

// Init initializes the model and returns initial commands.
// It sets up the refresh ticker and, if credentials are provided,
// initiates an automatic login attempt.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	cmds = append(cmds, tickCmd(m.refreshInterval))

	if m.username != "" && m.password != "" {
		cmds = append(cmds, loginCmd(m.client, m.username, m.password))
	}

	return tea.Batch(cmds...)
}

// Update handles incoming messages and updates the model accordingly.
// It processes window resize events, timer ticks, login results,
// metrics fetch results, and keyboard input.
//
// Key bindings (when authenticated):
//   - q / Ctrl+C: Quit the application
//   - r: Manual refresh
//   - p: Pause/resume auto-refresh
//   - up/k: Scroll up
//   - down/j: Scroll down
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case TickMsg:
		if !m.paused && m.autoRefresh && m.authState == authStateAuthenticated {
			return m, tea.Batch(
				fetchMetricsCmd(m.client),
				tickCmd(m.refreshInterval),
			)
		}
		return m, tickCmd(m.refreshInterval)

	case LoginResultMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.authState = authStatePrompting
			m.usernameInput.Focus()
			m.passwordInput.Blur()
			m.focusPassword = false
			return m, nil
		}
		m.authState = authStateAuthenticated
		m.err = nil
		return m, fetchMetricsCmd(m.client)

	case MetricsFetchedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			if msg.err == client.ErrUnauthorized {
				m.authState = authStatePrompting
				m.usernameInput.Focus()
				m.passwordInput.Blur()
				m.focusPassword = false
				return m, nil
			}
		} else {
			m.metrics = msg.metrics
			m.lastUpdated = time.Now()
			m.err = nil
		}
		return m, nil

	case PromptCredentialsMsg:
		m.authState = authStatePrompting
		m.usernameInput.Focus()
		m.passwordInput.Blur()
		m.focusPassword = false
		return m, nil

	case CredentialsSubmittedMsg:
		m.authState = authStateAuthenticating
		m.loading = true
		m.err = nil
		return m, loginCmd(m.client, msg.username, msg.password)

	case tea.KeyMsg:
		if m.authState == authStatePrompting {
			return m.handleCredentialInput(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "r":
			if m.authState == authStateAuthenticated {
				m.loading = true
				return m, fetchMetricsCmd(m.client)
			}
		case "p":
			m.paused = !m.paused
		case "up", "k":
			if m.scrollY > 0 {
				m.scrollY--
			}
		case "down", "j":
			m.scrollY++
		}
	}

	return m, nil
}

// handleCredentialInput processes keyboard input when the login form is displayed.
// It handles field switching (Tab), submission (Enter), and quitting (Esc/Ctrl+C).
//
// Key bindings:
//   - Tab/Shift+Tab: Switch between username and password fields
//   - Enter: Submit credentials
//   - Esc/Ctrl+C: Quit the application
func (m Model) handleCredentialInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		m.quitting = true
		return m, tea.Quit
	case "tab", "shift+tab":
		if m.focusPassword {
			m.passwordInput.Blur()
			m.usernameInput.Focus()
			m.focusPassword = false
		} else {
			m.usernameInput.Blur()
			m.passwordInput.Focus()
			m.focusPassword = true
		}
		return m, nil
	case "enter":
		return m, func() tea.Msg {
			return CredentialsSubmittedMsg{
				username: m.usernameInput.Value(),
				password: m.passwordInput.Value(),
			}
		}
	}

	if m.focusPassword {
		var cmd tea.Cmd
		m.passwordInput, cmd = m.passwordInput.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.usernameInput, cmd = m.usernameInput.Update(msg)
	return m, cmd
}
