package server

import (
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
)

// getHandlerQueries returns either the override (for tests) or the pool's prepared queries.
// hqOverride allows tests to inject erroring queries.
func (app *App) getHandlerQueries(cpc *dbconnpool.CpConn) interfaces.HandlerQueries {
	if app.hqOverride != nil {
		return app.hqOverride
	}
	return cpc.Queries
}
