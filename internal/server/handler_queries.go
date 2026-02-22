package server

import (
	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/server/interfaces"
)

// getHandlerQueries returns either the override (for tests) or the pool's prepared queries.
// hqOverride allows tests to inject erroring queries.
func (app *App) getHandlerQueries(cpc *dbconnpool.CpConn) interfaces.HandlerQueries {
	if app.hqOverride != nil {
		return app.hqOverride
	}
	return cpc.Queries
}
