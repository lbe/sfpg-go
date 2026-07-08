package server

import (
	"context"
	"io/fs"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/handlers"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/server/session"
)

type HandlerManager struct {
	authHandlers         *handlers.AuthHandlers
	configHandlers       *handlers.ConfigHandlers
	galleryHandlers      *handlers.GalleryHandlers
	healthHandlers       *handlers.HealthHandlers
	dashboardHandlers    *handlers.DashboardHandlers
	serverHandlers       *handlers.ServerHandlers
	themeHandlers        *handlers.ThemeHandlers
	menuHandlers         *handlers.MenuHandlers
	configThemesHandler  *handlers.ConfigThemesHandler
	configRestartHandler *handlers.ConfigRestartHandler
	configETagHandler    *handlers.ConfigETagHandler

	// Test seam (nil in production). When set, Build delegates to this function
	// instead of constructing the real handlers.
	testHookBuildHandlers func(fs fs.FS) error
}

func NewHandlerManager() *HandlerManager { return &HandlerManager{} }

// Build creates all handler instances using ServerDeps.
func (m *HandlerManager) Build(
	templateFS fs.FS,
	app interfaces.ServerDeps,
	authSvc auth.AuthService,
	sm session.SessionManager,
	dbRoPool, dbRwPool *dbconnpool.DbSQLConnPool,
	ctx context.Context,
	configService config.ConfigService,
	getETagVersion func() string,
	metricsCollector *metrics.Collector,
) error {
	if m.testHookBuildHandlers != nil {
		return m.testHookBuildHandlers(templateFS)
	}

	tmpl, err := parseConfigUITemplates(templateFS)
	if err != nil {
		return err
	}

	m.authHandlers = handlers.NewAuthHandlers(authSvc, sm)

	m.configHandlers = handlers.NewConfigHandlers(
		configService, authSvc, sm, dbRoPool, dbRwPool, app, tmpl, ctx,
	)
	m.configThemesHandler = handlers.NewConfigThemesHandler(m.configHandlers)
	m.configRestartHandler = handlers.NewConfigRestartHandler(m.configHandlers)
	m.configETagHandler = handlers.NewConfigETagHandler(m.configHandlers)

	m.galleryHandlers = handlers.NewGalleryHandlers(dbRoPool, ctx, app)

	m.healthHandlers = handlers.NewHealthHandlers(getETagVersion)

	if metricsCollector == nil {
		metricsCollector = metrics.NewCollector()
	}
	m.dashboardHandlers = handlers.NewDashboardHandlers(
		sm, metricsCollector, app.AddCommonTemplateData, app.ServerError,
	)

	m.serverHandlers = handlers.NewServerHandlers(sm, app)
	m.menuHandlers = handlers.NewMenuHandlers(sm, app)
	m.themeHandlers = handlers.NewThemeHandlers(app)

	return nil
}

func (m *HandlerManager) SetPreloadService(pm cachepreload.PreloadService) {
	if m.galleryHandlers != nil && pm != nil {
		m.galleryHandlers.PreloadService = pm
	}
}
