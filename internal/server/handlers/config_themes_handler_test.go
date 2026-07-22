package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

func TestConfigHandlers_UpdateThemesHandler_ParseFormError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	th := NewConfigThemesHandler(ch)
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/themes", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	th.UpdateThemesHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_UpdateThemesHandler_LoadError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return nil, errors.New("load error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	th := NewConfigThemesHandler(ch)
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/themes", strings.NewReader("themes=light"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	th.UpdateThemesHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_UpdateThemesHandler_ApplyError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		saveFunc: func(ctx context.Context, cfg *config.Config) error {
			return errors.New("save error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	th := NewConfigThemesHandler(ch)
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/themes", strings.NewReader("themes=light&themes=dark"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	th.UpdateThemesHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if !strings.HasPrefix(body, "Failed to save themes") {
		t.Errorf("expected themes save error, got %s", body)
	}
}

func TestConfigHandlers_UpdateThemesHandler_Success(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Themes = []string{"dark"}
	cfg.CurrentTheme = "cupcake"

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return cfg, nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	th := NewConfigThemesHandler(ch)
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/themes", strings.NewReader("themes=light&themes=dark"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	th.UpdateThemesHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	cfgOps := ch.cfgOps.(*mockConfigOps)
	if cfgOps.Cfg == nil {
		t.Fatal("expected UpdateConfigWithPrecedence to be invoked")
	}
	if len(cfgOps.Cfg.Themes) != 2 || cfgOps.Cfg.Themes[0] != "light" || cfgOps.Cfg.Themes[1] != "dark" {
		t.Errorf("expected themes [light dark], got %v", cfgOps.Cfg.Themes)
	}
	if cfgOps.Cfg.CurrentTheme != "light" {
		t.Errorf("expected current theme adjusted to 'light', got %q", cfgOps.Cfg.CurrentTheme)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if testutil.FindElementByID(doc, "config-success-message") == nil {
		t.Error("missing #config-success-message")
	}
}
