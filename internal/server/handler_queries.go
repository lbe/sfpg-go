package server

import (
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
)

// getHandlerQueries returns either the override (for tests) or the pool's prepared queries.
// testHookHandlerQueries allows tests to inject erroring queries.
func (app *App) getHandlerQueries(cpc *dbconnpool.CpConn) interfaces.HandlerQueries {
	if app.testHookHandlerQueries != nil {
		return app.testHookHandlerQueries
	}
	return cpc.Queries
}
