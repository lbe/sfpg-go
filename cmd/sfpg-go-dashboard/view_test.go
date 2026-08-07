package main

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/tuiview"
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
	tuiview.AssertPlainIncludes(t, view, "Network error - cannot connect to server")
}
