package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
)

// TestViewLogin_NetworkError shows the network error message.
func TestViewLogin_NetworkError(t *testing.T) {
	m := Model{
		authState:     authStatePrompting,
		usernameInput: textinput.New(),
		passwordInput: textinput.New(),
		err:           client.ErrNetworkError,
	}
	m.usernameInput.Focus()

	view := m.viewLogin()
	if !strings.Contains(view, "Network error - cannot connect to server") {
		t.Errorf("view = %q, want to contain network error message", view)
	}
}
