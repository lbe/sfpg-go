package main

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/client"
	"github.com/lbe/sfpg-go/cmd/sfpg-go-dashboard/config"
)

// osExit is a testable hook for os.Exit.
var osExit = os.Exit

// stderr is a testable hook for error output.
var stderr io.Writer = os.Stderr

// runProgram is a testable hook for the Bubble Tea program runner.
var runProgram = (*tea.Program).Run

// main is the entry point for the sfpg-go-dashboard TUI application.
// It parses configuration, creates the HTTP client, and starts the
// Bubble Tea program with an alternate screen buffer.
//
// Exit codes:
//   - 0: Normal exit (help shown or user quit)
//   - 1: Error during program execution
func main() {
	osExit(runDashboard(os.Args[1:], runProgram))
}

// runDashboard parses configuration and runs the Bubble Tea program.
// It returns the process exit code so tests can invoke main() safely.
func runDashboard(args []string, runProgram func(*tea.Program) (tea.Model, error)) int {
	cfg := config.ParseArgs(args)

	if cfg.ShowHelp {
		return 0
	}

	c := client.New(cfg.ServerURL)

	p := tea.NewProgram(
		initialModel(cfg, c),
		tea.WithAltScreen(),
	)

	if _, err := runProgram(p); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}
