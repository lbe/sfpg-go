package server

import (
	"fmt"
	"log/slog"

	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/modulestate"
	"github.com/lbe/sfpg-go/web"
)

// setDB initializes and configures the database using the database package.
func (app *App) setDB() {
	app.SetupDB(app.RuntimeManager.ctx, app.ConfigManager.Config)

	if app.testSeams.ConfigService != nil {
		app.SetConfigService(app.testSeams.ConfigService)
	} else {
		app.SetConfigService(config.NewService(app.dbRwPool, app.dbRoPool))
	}
	app.SessionAuthFacade.authService = auth.NewService(app)
	app.SubsystemManager.moduleStateService = modulestate.NewService(app.dbRwPool)

	if app.HandlerManager.authHandlers != nil {
		app.ensureSession()
		if err := app.buildHandlers(web.FS); err != nil {
			slog.Error("failed to rebuild handlers after setDB", "err", err)
			panic(fmt.Sprintf("rebuild handlers after setDB: %v", err))
		}
	}
}

// reconfigurePoolsFromConfig recreates database pools with settings from the
// given config and reinitializes dependent services. Returns an error if the
// pool recreation itself fails (non-nil errors from dependent reinit are logged).
func (app *App) reconfigurePoolsFromConfig() error {
	app.ConfigManager.ConfigMu.RLock()
	cfg := app.ConfigManager.Config
	app.ConfigManager.ConfigMu.RUnlock()
	if cfg == nil {
		return nil
	}

	app.setDBOptimizeInterval(cfg.DBOptimizeInterval)

	if err := app.ReconfigurePools(app.RuntimeManager.ctx, cfg); err != nil {
		return err
	}

	if app.testSeams.ConfigService != nil {
		app.SetConfigService(app.testSeams.ConfigService)
	} else {
		app.SetConfigService(config.NewService(app.dbRwPool, app.dbRoPool))
	}
	app.SubsystemManager.moduleStateService = modulestate.NewService(app.dbRwPool)
	app.SessionAuthFacade.authService = auth.NewService(app)

	if app.cacheMW != nil {
		app.cacheMW.UpdatePool(app.dbRwPool)
	}
	if app.HandlerManager.authHandlers != nil {
		if err := app.buildHandlers(web.FS); err != nil {
			return fmt.Errorf("rebuild handlers: %w", err)
		}
	}
	return nil
}
