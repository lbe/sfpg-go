package server

// RestartRequired returns true if a server restart is required due to
// configuration changes that require restart. The actual restart is
// triggered via the restart channel (restartCh) in server.go's event
// loop, not through this package.
func (app *App) RestartRequired() bool {
	return app.restartRequired
}
