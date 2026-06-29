package server

import (
	"sync"

	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/handlers"
)

// appConfigState groups config state and config handler dependencies into
// one sub-struct, reducing field sprawl on the App god-object.
// Embedded into App to promote its fields.
type appConfigState struct {
	configMu      sync.RWMutex         // protects config from concurrent access (ORDER: 2)
	config        *config.Config       // Application configuration
	configService config.ConfigService // ConfigService for config operations

	configHandlers       *handlers.ConfigHandlers
	configThemesHandler  *handlers.ConfigThemesHandler
	configRestartHandler *handlers.ConfigRestartHandler
	configETagHandler    *handlers.ConfigETagHandler
}

// UpdateConfigWithPrecedence applies the new config and re-applies CLI/env
// overrides for fields NOT in changedFields. This enforces the precedence:
// Defaults → DB → Env → CLI, ensuring user-changed fields persist while
// CLI/env values take precedence for fields the user didn't change.
//
// Lock ordering: must hold configMu (ORDER: 1). The caller must NOT hold
// restartMu (ORDER: 2) when calling this.
func (app *App) UpdateConfigWithPrecedence(c *config.Config, changedFields []string) {
	app.configMu.Lock()
	app.config = c
	c.LoadFromOptExcluding(app.opt, changedFields)
	app.configMu.Unlock()
}
