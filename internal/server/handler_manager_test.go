package server

import (
	"testing"
	"testing/fstest"

	"github.com/lbe/sfpg-go/web"
)

// TestHandlerManager_Build_Success verifies that HandlerManager.Build constructs
// all handler groups when using real dependencies.
func TestHandlerManager_Build_Success(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	hm := NewHandlerManager()
	if err := hm.Build(
		web.FS,
		app,
		app.authService,
		app.sessionManager,
		app.dbRoPool,
		app.dbRwPool,
		app.ctx,
		app.configService,
		app.GetETagVersion,
		app.metricsCollector,
	); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if hm.authHandlers == nil {
		t.Error("expected authHandlers to be built")
	}
	if hm.configHandlers == nil {
		t.Error("expected configHandlers to be built")
	}
	if hm.galleryHandlers == nil {
		t.Error("expected galleryHandlers to be built")
	}
	if hm.healthHandlers == nil {
		t.Error("expected healthHandlers to be built")
	}
	if hm.dashboardHandlers == nil {
		t.Error("expected dashboardHandlers to be built")
	}
	if hm.serverHandlers == nil {
		t.Error("expected serverHandlers to be built")
	}
	if hm.menuHandlers == nil {
		t.Error("expected menuHandlers to be built")
	}
	if hm.themeHandlers == nil {
		t.Error("expected themeHandlers to be built")
	}
	if hm.configThemesHandler == nil {
		t.Error("expected configThemesHandler to be built")
	}
	if hm.configRestartHandler == nil {
		t.Error("expected configRestartHandler to be built")
	}
	if hm.configETagHandler == nil {
		t.Error("expected configETagHandler to be built")
	}
}

// TestHandlerManager_Build_TemplateError verifies that Build returns an error
// when the template filesystem does not contain the required config UI templates.
func TestHandlerManager_Build_TemplateError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	hm := NewHandlerManager()
	err := hm.Build(
		fstest.MapFS{},
		app,
		app.authService,
		app.sessionManager,
		app.dbRoPool,
		app.dbRwPool,
		app.ctx,
		app.configService,
		app.GetETagVersion,
		app.metricsCollector,
	)
	if err == nil {
		t.Fatal("expected Build to return error for invalid template FS")
	}
}
