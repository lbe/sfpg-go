package server

import "github.com/lbe/sfpg-go/internal/server/handlers"

// appHandlerDeps groups all remaining HTTP handler instances into one
// sub-struct, reducing field sprawl on the App god-object.
// Embedded into App to promote its fields.
type appHandlerDeps struct {
	authHandlers      *handlers.AuthHandlers
	galleryHandlers   *handlers.GalleryHandlers
	healthHandlers    *handlers.HealthHandlers
	dashboardHandlers *handlers.DashboardHandlers
	serverHandlers    *handlers.ServerHandlers
	themeHandlers     *handlers.ThemeHandlers
}
