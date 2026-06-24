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
