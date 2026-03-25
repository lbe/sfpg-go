package main

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/config"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/parser"
)

// AuthState represents the current authentication state of the application.
type AuthState int

// Authentication states for the login flow.
const (
	// authStateNone indicates no authentication has been attempted.
	authStateNone AuthState = iota
	// authStatePrompting shows the login prompt to the user.
	authStatePrompting
	// authStateAuthenticating indicates credentials are being validated.
	authStateAuthenticating
	// authStateAuthenticated indicates successful authentication.
	authStateAuthenticated
)

// Model is the main application state following the Bubble Tea pattern.
// It contains all data needed to render and update the dashboard.
type Model struct {
	// Configuration
	serverURL       string        // URL of the sfpg-go server
	refreshInterval time.Duration // Interval for auto-refresh
	autoRefresh     bool          // Whether auto-refresh is enabled

	// Dashboard data
	metrics     *parser.DashboardMetrics // Parsed dashboard metrics
	lastUpdated time.Time                // When metrics were last updated
	loading     bool                     // Whether a refresh is in progress
	err         error                    // Last error encountered
	authState   AuthState                // Current authentication state

	// Login form
	usernameInput textinput.Model // Username input field
	passwordInput textinput.Model // Password input field
	focusPassword bool            // Whether password field has focus

	// Credentials from environment (for automatic login)
	username string
	password string

	// UI state
	width   int  // Terminal width
	height  int  // Terminal height
	paused  bool // Whether auto-refresh is paused
	scrollY int  // Vertical scroll position

	quitting bool // Whether the application is quitting

	client *client.Client // HTTP client for server communication
}

// MetricsFetchedMsg is sent when dashboard metrics are fetched from the server.
type MetricsFetchedMsg struct {
	metrics *parser.DashboardMetrics
	err     error
}

// TickMsg is sent on each refresh interval tick.
type TickMsg time.Time

// PromptCredentialsMsg is sent to show the login prompt.
type PromptCredentialsMsg struct{}

// CredentialsSubmittedMsg is sent when the user submits login credentials.
type CredentialsSubmittedMsg struct {
	username string
	password string
}

// LoginResultMsg is sent after a login attempt completes.
type LoginResultMsg struct {
	err error
}

// initialModel creates a new Model with the given configuration.
// It initializes the text input fields and sets up the HTTP client.
// If username and password are provided in the config, automatic login
// will be attempted during Init().
func initialModel(cfg *config.Config, c *client.Client) Model {
	usernameInput := textinput.New()
	usernameInput.Placeholder = "Username"
	usernameInput.Focus()

	passwordInput := textinput.New()
	passwordInput.Placeholder = "Password"
	passwordInput.EchoMode = textinput.EchoPassword
	passwordInput.EchoCharacter = '•'

	return Model{
		serverURL:       cfg.ServerURL,
		refreshInterval: cfg.Refresh,
		autoRefresh:     !cfg.NoRefresh,
		usernameInput:   usernameInput,
		passwordInput:   passwordInput,
		username:        cfg.Username,
		password:        cfg.Password,
		client:          c,
	}
}
