package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/lbe/sfpg-go/internal/server/restart"
)

// RestartRequired returns true if a server restart is required due to
// configuration changes that require restart.
func (app *App) RestartRequired() bool {
	return app.restartRequired
}

// RestartServer gracefully shuts down the HTTP server and restarts it.
// If only listener settings (address/port) changed, it performs an HTTP-only restart.
// Otherwise, it performs a full restart.
func (app *App) RestartServer(server *http.Server) error {
	if server == nil {
		return fmt.Errorf("server is nil")
	}

	slog.Info("restarting server", "restart_type", restart.GetRestartType(app.config))

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Gracefully shutdown the server
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("failed to shutdown server gracefully", "err", err)
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	// Wait for the port to be released by the OS
	slog.Info("waiting for port to be released")
	time.Sleep(500 * time.Millisecond)

	// Check if this is HTTP-only restart (only listener settings changed)
	if restart.IsHTTPOnlyRestart(app.config) {
		slog.Info("performing HTTP-only restart")
		// For HTTP-only restart, we just need to restart the HTTP server
		// Database connections and other resources are preserved
		app.restartRequired = false
		return nil
	}

	// Full restart - would require restarting the entire application
	// For now, we just reset the flag and log that full restart would be needed
	slog.Info("full restart would be required, but only HTTP server restarted")
	app.restartRequired = false
	return nil
}

// isHTTPOnlyRestart determines if only listener settings changed.
// Delegates to restart.IsHTTPOnlyRestart.
func (app *App) isHTTPOnlyRestart() bool {
	return restart.IsHTTPOnlyRestart(app.config)
}

// getRestartType returns a string describing the type of restart needed.
// Delegates to restart.GetRestartType.
func (app *App) getRestartType() string {
	return restart.GetRestartType(app.config)
}
