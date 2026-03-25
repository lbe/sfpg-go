package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/config"
)

// main is the entry point for the sfpg-go-dashboard TUI application.
// It parses configuration, creates the HTTP client, and starts the
// Bubble Tea program with an alternate screen buffer.
//
// Exit codes:
//   - 0: Normal exit (help shown or user quit)
//   - 1: Error during program execution
func main() {
	cfg := config.Parse()

	if cfg.ShowHelp {
		os.Exit(0)
	}

	c := client.New(cfg.ServerURL)

	p := tea.NewProgram(
		initialModel(cfg, c),
		tea.WithAltScreen(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
