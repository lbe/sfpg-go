package server

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/handlers"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/server/security"
	"github.com/lbe/sfpg-go/internal/server/session"
)

// HandlerManager owns HTTP handler groups and builds them from application dependencies.
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

	testSeams HandlerManagerTestSeams
}

// NewHandlerManager constructs an empty handler manager.
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
	if m.testSeams.BuildHandlers != nil {
		return m.testSeams.BuildHandlers(templateFS)
	}

	tmpl, err := parseConfigUITemplates(templateFS)
	if err != nil {
		return err
	}

	m.authHandlers = handlers.NewAuthHandlers(authSvc, sm)
	// Wire the per-IP login rate limiter from config (0 = unlimited).
	max := security.EffectiveLoginRateLimitPerIP(0)
	if cfg := app.GetConfig(); cfg != nil {
		max = security.EffectiveLoginRateLimitPerIP(cfg.LoginRateLimitPerIP)
	}
	m.authHandlers.SyncLoginRateLimitMax(max)

	m.configHandlers = handlers.NewConfigHandlers(
		configService, authSvc, sm, dbRoPool, dbRwPool, app, app, app.AddCommonTemplateData, tmpl, ctx,
	)
	m.configThemesHandler = handlers.NewConfigThemesHandler(m.configHandlers)
	m.configRestartHandler = handlers.NewConfigRestartHandler(m.configHandlers)
	m.configETagHandler = handlers.NewConfigETagHandler(m.configHandlers)

	m.galleryHandlers = handlers.NewGalleryHandlers(dbRoPool, ctx, app, app.AddCommonTemplateData, app.ServerError)

	m.healthHandlers = handlers.NewHealthHandlers(getETagVersion)

	if metricsCollector == nil {
		metricsCollector = metrics.NewCollector()
	}
	m.dashboardHandlers = handlers.NewDashboardHandlers(
		sm, metricsCollector, app.AddCommonTemplateData, app.ServerError,
	)

	m.serverHandlers = handlers.NewServerHandlers(sm, app, app.AddCommonTemplateData, app.ServerError)
	m.menuHandlers = handlers.NewMenuHandlers(sm, app.AddCommonTemplateData, app.ServerError)
	m.themeHandlers = handlers.NewThemeHandlers(sm, app.GetConfig, app.AddCommonTemplateData, app.ServerError)

	return nil
}

// SetPreloadService wires cache preload into gallery handlers.
func (m *HandlerManager) SetPreloadService(pm cachepreload.PreloadService) {
	if m.galleryHandlers != nil && pm != nil {
		m.galleryHandlers.PreloadService = pm
	}
}

// parseConfigUITemplates parses all config UI templates from the embedded filesystem.
// Returns a handlers.ConfigTemplates value for direct use in Handlers build.
func parseConfigUITemplates(templateFS fs.FS) (handlers.ConfigTemplates, error) {
	var t handlers.ConfigTemplates
	var err error
	t.SaveRestartAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-save-restart-alert.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-save-restart-alert template: %w", err)
	}
	t.SaveSuccessAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-save-success-alert.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-save-success-alert template: %w", err)
	}
	t.ExportModal, err = template.ParseFS(templateFS, "templates/config-ui/config-export-modal.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-export-modal template: %w", err)
	}
	t.ImportModal, err = template.ParseFS(templateFS, "templates/config-ui/config-import-modal.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-import-modal template: %w", err)
	}
	t.RestoreModal, err = template.ParseFS(templateFS, "templates/config-ui/config-restore-modal.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-restore-modal template: %w", err)
	}
	t.RestoreSuccessAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-restore-success-alert.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-restore-success-alert template: %w", err)
	}
	t.ImportSuccessAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-import-success-alert.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-import-success-alert template: %w", err)
	}
	t.RestartInitiatedAlert, err = template.ParseFS(templateFS, "templates/config-ui/config-restart-initiated-alert.html.tmpl")
	if err != nil {
		return t, fmt.Errorf("failed to parse config-restart-initiated-alert template: %w", err)
	}
	return t, nil
}
