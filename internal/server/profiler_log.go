package server

import (
	"log/slog"

	"go.local/sfpg/internal/profiler"
)

// LogProfileLocation logs the profile directory and stops the profiler if active.
// This should be called before shutdown to ensure profile location is logged to both console and file.
func (app *App) LogProfileLocation() {
	if app.stopProfiler != nil {
		app.stopProfiler()
		// Log after stopping to ensure profile is flushed
		if dir := profiler.Dir(); dir != "" {
			slog.Info("Profile artifacts written", "dir", dir)
		}
	}
}
