//go:generate ./scripts/gen_version.sh

// Package main is the entry point for SFPG (Simple Fast Photo Gallery).
// It parses command-line options, handles special commands (--unlock-account,
// --increment-etag), and starts the HTTP server with graceful shutdown handling.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	// _ "net/http/pprof" // imported only for side effects

	// _ "github.com/golang-migrate/migrate/v4/database/sqlite3" // Import the SQLite database driver

	// _ "github.com/ncruces/go-sqlite3/driver"
	// _ "github.com/ncruces/go-sqlite3/embed"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server"
)

// main is the application entry point. It handles command-line parsing for
// special operations (account unlock, ETag increment) or starts the main
// HTTP server with graceful shutdown handling.
//
// The Version variable is declared in version.go and injected by the
// //go:generate directive (./scripts/gen_version.sh).
func main() {
	opt := getopt.Parse()

	// Handle unlock-account flag - exit early if set
	if opt.UnlockAccount.IsSet && opt.UnlockAccount.String != "" {
		app := server.New(opt, Version)
		err := app.InitForUnlock()
		if err != nil {
			slog.Error("failed to initialize app for unlock", "err", err)
			os.Exit(1)
		}
		err = app.UnlockAccount(opt.UnlockAccount.String)
		if err != nil {
			slog.Error("failed to unlock account", "username", opt.UnlockAccount.String, "err", err)
			os.Exit(1)
		}
		slog.Info("account unlocked successfully", "username", opt.UnlockAccount.String)
		os.Exit(0)
	}

	// Handle --increment-etag flag - exit early if set
	if opt.IncrementETag.IsSet && opt.IncrementETag.Bool {
		app := server.New(opt, Version)

		err := app.InitForIncrementETag(opt)
		if err != nil {
			slog.Error("failed to initialize app for increment-etag", "err", err)
			os.Exit(1)
		}

		newETag, err := app.IncrementETag()
		if err != nil {
			slog.Error("failed to increment etag", "err", err)
			os.Exit(1)
		}

		fmt.Printf("ETag version incremented to: %s\n", newETag)
		slog.Info("etag version incremented", "new_version", newETag)
		os.Exit(0)
	}

	app := server.New(opt, Version)
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered from panic", "error", r)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	errChan := make(chan error, 1)
	go func() { errChan <- app.Run(0, 0) }()

	var runErr error
	var sig os.Signal
	select {
	case sig = <-sigChan:
		slog.Info("received signal, initiating shutdown", "signal", sig)
		app.LogProfileLocation()
	case runErr = <-errChan:
		if runErr != nil {
			slog.Error("application error", "err", runErr)
		}
		app.LogProfileLocation()
	}

	app.Shutdown()
}
