//go:generate ./scripts/gen_version.sh

// Package main is the entry point for SFPG (Simple Fast Photo Gallery).
// It parses command-line options, handles special commands (--unlock-account,
// --increment-etag), and starts the HTTP server with graceful shutdown handling.
package main

import (
	"fmt"
	"io"
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

// appServer captures the server methods that main needs.  Using an interface
// lets tests inject a lightweight fake instead of building a real *server.App.
type appServer interface {
	InitForUnlock() error
	UnlockAccount(username string) error
	InitForIncrementETag(opt getopt.Opt) error
	IncrementETag() (string, error)
	InitForBatchLoad(opt getopt.Opt) error
	RunCacheBatchLoad() int
	Run(minPoolWorkers, maxPoolWorkers int) error
	LogProfileLocation()
	Shutdown()
}

// defaultNewApp returns the real server implementation.  It is assigned to a
// variable so tests can replace it with a fake.
func defaultNewApp(opt getopt.Opt, version string) appServer {
	return server.New(opt, version)
}

var (
	// parseOptions is a hook for getopt.Parse.
	parseOptions = getopt.Parse

	// newApp is a hook for server construction.
	newApp = defaultNewApp

	// notifySignals is a hook for signal.Notify.
	notifySignals = signal.Notify

	// stdout is the writer used for CLI output (e.g. --increment-etag).
	stdout io.Writer = os.Stdout

	// osExit is a hook for os.Exit so main can be exercised in tests.
	osExit = os.Exit
)

// main is the application entry point. It handles command-line parsing for
// special operations (account unlock, ETag increment) or starts the main
// HTTP server with graceful shutdown handling.
//
// The Version variable is declared in version.go and injected by the
// //go:generate directive (./scripts/gen_version.sh).
func main() {
	osExit(runMain())
}

func runMain() (exitCode int) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered from panic", "error", r)
			exitCode = 1
		}
	}()

	opt := parseOptions()

	// --unlock-account
	if opt.UnlockAccount.IsSet && opt.UnlockAccount.String != "" {
		app := newApp(opt, Version)
		if err := app.InitForUnlock(); err != nil {
			slog.Error("failed to initialize app for unlock", "err", err)
			return 1
		}
		if err := app.UnlockAccount(opt.UnlockAccount.String); err != nil {
			slog.Error("failed to unlock account", "username", opt.UnlockAccount.String, "err", err)
			return 1
		}
		slog.Info("account unlocked successfully", "username", opt.UnlockAccount.String)
		return 0
	}

	// --increment-etag
	if opt.IncrementETag.IsSet && opt.IncrementETag.Bool {
		app := newApp(opt, Version)
		if err := app.InitForIncrementETag(opt); err != nil {
			slog.Error("failed to initialize app for increment-etag", "err", err)
			return 1
		}
		newETag, err := app.IncrementETag()
		if err != nil {
			slog.Error("failed to increment etag", "err", err)
			return 1
		}
		fmt.Fprintf(stdout, "ETag version incremented to: %s\n", newETag)
		slog.Info("etag version incremented", "new_version", newETag)
		return 0
	}

	// --cache-batch-load
	if opt.CacheBatchLoad.IsSet && opt.CacheBatchLoad.Bool {
		app := newApp(opt, Version)
		if err := app.InitForBatchLoad(opt); err != nil {
			slog.Error("failed to initialize app for cache batch load", "err", err)
			return 1
		}
		code := app.RunCacheBatchLoad()
		app.Shutdown()
		return code
	}

	// Normal server run
	app := newApp(opt, Version)

	sigChan := make(chan os.Signal, 1)
	notifySignals(sigChan, syscall.SIGINT, syscall.SIGTERM)

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
	if runErr != nil {
		return 1
	}
	return 0
}
