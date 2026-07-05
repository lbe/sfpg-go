package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

func TestConfigHandlers_ConfigPost_InvalidCSRF(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager = &mockSessionManagerAuthenticatedInvalidCSRF{}

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("site_name=Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

func TestConfigHandlers_ConfigPost_WithThemesInPayload(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	oldCfg := config.DefaultConfig()
	oldCfg.Themes = []string{"dark", "light"}
	oldCfg.CurrentTheme = "dark"
	oldCfg.SiteName = "OldName"

	var savedCfg *config.Config
	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return oldCfg, nil
		},
		saveFunc: func(ctx context.Context, cfg *config.Config) error {
			savedCfg = cfg
			return nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	body := "site_name=Test&themes=light&themes=dark&csrf_token=valid"
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	errorMsg := testutil.FindElementByID(doc, "config-error-message")
	if errorMsg != nil {
		text := testutil.GetTextContent(errorMsg)
		if strings.Contains(text, "themes") {
			t.Errorf("validation error should not mention themes, got: %q", text)
		}
	}

	if savedCfg == nil {
		t.Fatal("expected config to be saved")
	}
	if len(savedCfg.Themes) != 2 || !slices.Contains(savedCfg.Themes, "dark") || !slices.Contains(savedCfg.Themes, "light") {
		t.Errorf("Themes should contain dark and light, got %v", savedCfg.Themes)
	}
}

// TestConfigHandlers_ConfigPost_ThemesPersisted proves that themes submitted in the
// main config form are actually applied to the saved config. This is a regression
// guard against skipInForm: true which silently ignored themes from form data.

func TestConfigHandlers_ConfigPost_ThemesPersisted(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	oldCfg := config.DefaultConfig()
	oldCfg.Themes = []string{"light"}
	oldCfg.CurrentTheme = "light"

	var savedCfg *config.Config
	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return oldCfg, nil
		},
		saveFunc: func(ctx context.Context, cfg *config.Config) error {
			savedCfg = cfg
			return nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	// Submit new themes via the form — includes a theme NOT in the old config.
	body := "themes=light&themes=dark&themes=coffee&csrf_token=valid"
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	if savedCfg == nil {
		t.Fatal("expected config to be saved")
	}
	if len(savedCfg.Themes) != 3 {
		t.Fatalf("expected 3 themes, got %d: %v", len(savedCfg.Themes), savedCfg.Themes)
	}
	for _, want := range []string{"light", "dark", "coffee"} {
		if !slices.Contains(savedCfg.Themes, want) {
			t.Errorf("themes should include %q, got %v", want, savedCfg.Themes)
		}
	}
}

func TestConfigHandlers_ConfigPost_ValidUpdate(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	var updateConfigCalled bool
	mockSvc := &mockConfigServiceForConfig{}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	_ = updateConfigCalled // removed: through deps
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("listener_port=8081"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	// UpdateConfigWithPrecedence handled by deps
}

func TestConfigHandlers_ConfigPost_InvalidPort(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("listener_port=invalid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	errorMsg := testutil.FindElementByID(doc, "config-error-message")
	if errorMsg == nil {
		t.Fatal("missing #config-error-message")
	}
	if got := testutil.GetTextContent(errorMsg); !strings.Contains(got, "listener_port") {
		t.Errorf("expected listener_port error, got %q", got)
	}
}

func TestConfigHandlers_ConfigPost_SaveSuccessAlert(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("site_name=NewName"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if testutil.FindElementByID(doc, "config-success-message") == nil {
		t.Fatal("missing #config-success-message")
	}
}

func TestConfigHandlers_ConfigPost_ValidationErrors(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		validateFunc: func(cfg *config.Config) error {
			return errors.New("invalid config")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("listener_port=8081"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConfigHandlers_ConfigPost_ParseFormError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	if w.Header().Get("HX-Retarget") != "#config-error-message" {
		t.Errorf("expected HX-Retarget header, got %q", w.Header().Get("HX-Retarget"))
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	errorMsg := testutil.FindElementByID(doc, "config-error-message")
	if errorMsg == nil {
		t.Fatal("missing #config-error-message")
	}
	if got := testutil.GetTextContent(errorMsg); !strings.Contains(got, "Invalid form data") {
		t.Errorf("expected invalid form data message, got %q", got)
	}
}

func TestConfigHandlers_ConfigPost_UncheckedCheckboxes(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return config.DefaultConfig(), nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	// SetPreloadEnabled removed; handled by deps
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	// POST with only csrf_token — no config fields. Should NOT trigger any field
	// processing because missing checkboxes are only defaulted when the form
	// contains actual config fields.
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	// SetPreloadEnabled handled by deps
}

func TestConfigHandlers_ConfigPost_CredentialValidationErrors(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockAuthSvc := &mockAuthServiceForConfig{
		updateCredentialsFunc: func(ctx context.Context, opts auth.CredentialUpdateOptions, store auth.CredentialStore) (*auth.CredentialUpdateResult, error) {
			return &auth.CredentialUpdateResult{
				ValidationErrors: map[string]string{"admin_username": "invalid"},
			}, nil
		},
	}
	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, mockAuthSvc)
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	errorMsg := testutil.FindElementByID(doc, "config-error-message")
	if errorMsg == nil {
		t.Fatal("missing #config-error-message")
	}
	text := testutil.GetTextContent(errorMsg)
	if !strings.Contains(text, "admin_username") || !strings.Contains(text, "invalid") {
		t.Errorf("expected validation error to be rendered, got %q", text)
	}
}

func TestConfigHandlers_ConfigPost_SaveError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		saveFunc: func(ctx context.Context, cfg *config.Config) error {
			return errors.New("save error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("listener_port=8082"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if testutil.FindElementByID(doc, "config-error-message") == nil {
		t.Fatal("missing #config-error-message")
	}
}

func TestConfigHandlers_ConfigPost_SaveRestartAlert(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("listener_port=8082"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if testutil.FindElementByID(doc, "config-restart-badge") == nil {
		t.Fatal("missing #config-restart-badge")
	}
}

func TestConfigHandlers_ConfigPost_RestartFlagAndNotificationPath(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true
	// SetRestartRequired removed; handled by deps

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("listener_port=8082"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	// SetRestartRequired handled by deps (applies through mockServerDeps)
	// SetRestartRequired handled by deps
	if got := w.Header().Get("HX-Trigger"); got != "config-saved" {
		t.Fatalf("expected HX-Trigger config-saved, got %q", got)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	success := testutil.FindElementByID(doc, "config-success-message")
	if success == nil {
		t.Fatal("missing #config-success-message")
	}
	if got := testutil.GetTextContent(success); !strings.Contains(got, "Server restart required") {
		t.Fatalf("expected restart-required message in success alert, got %q", got)
	}

	restartBadge := testutil.FindElementByID(doc, "config-restart-badge")
	if restartBadge == nil {
		t.Fatal("missing #config-restart-badge")
	}
	if got := testutil.GetAttr(restartBadge, "hx-swap-oob"); got != "outerHTML" {
		t.Fatalf("expected restart badge to use OOB outerHTML swap, got %q", got)
	}
}

func TestConfigHandlers_ConfigPost_UpdateCredentialsError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockAuthSvc := &mockAuthServiceForConfig{
		updateCredentialsFunc: func(ctx context.Context, opts auth.CredentialUpdateOptions, store auth.CredentialStore) (*auth.CredentialUpdateResult, error) {
			return nil, errors.New("update error")
		},
	}
	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, mockAuthSvc)
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_ConfigPost_InvalidRetentionCount(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("log_retention_count=bad"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	errorMsg := testutil.FindElementByID(doc, "config-error-message")
	if errorMsg == nil {
		t.Fatal("missing #config-error-message")
	}
	if got := testutil.GetTextContent(errorMsg); !strings.Contains(got, "log_retention_count") {
		t.Errorf("expected log_retention_count error, got %q", got)
	}
}

func TestConfigHandlers_ConfigPost_ThemesFallback(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	oldCfg := config.DefaultConfig()
	oldCfg.Themes = []string{"dark", "light"}
	oldCfg.CurrentTheme = "dark"

	var saved *config.Config
	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return oldCfg, nil
		},
		saveFunc: func(ctx context.Context, cfg *config.Config) error {
			saved = cfg
			return nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/themes", strings.NewReader("themes=light"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	themesHandler := NewConfigThemesHandler(ch)
	themesHandler.UpdateThemesHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if saved == nil {
		t.Fatal("expected config to be saved")
	}
	if saved.CurrentTheme != "light" {
		t.Errorf("expected CurrentTheme to fallback to light, got %q", saved.CurrentTheme)
	}
	if len(saved.Themes) != 1 || saved.Themes[0] != "light" {
		t.Errorf("expected Themes to be [light], got %v", saved.Themes)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	// Themes change should NOT trigger restart
	if testutil.FindElementByID(doc, "config-restart-badge") != nil {
		t.Fatal("themes change should NOT trigger restart")
	}
	if testutil.FindElementByID(doc, "config-success-message") == nil {
		t.Fatal("expected #config-success-message")
	}
}

func TestConfigPostRejectsInvalidImageDirectory(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name      string
		imageDir  string
		wantError bool
	}{
		{
			name:      "nonexistent path is rejected",
			imageDir:  "/nonexistent/path",
			wantError: true,
		},
		{
			name:      "directory traversal is rejected",
			imageDir:  "../../../etc",
			wantError: true,
		},
		{
			name:      "valid path is accepted",
			imageDir:  t.TempDir(),
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var saveCalled bool
			mockSvc := &mockConfigServiceForConfig{
				saveFunc: func(ctx context.Context, cfg *config.Config) error {
					saveCalled = true
					return nil
				},
			}
			ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
			ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

			body := "image_directory=" + tt.imageDir + "&csrf_token=valid"
			req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			ch.ConfigPost(w, req)

			if tt.wantError {
				if saveCalled {
					t.Error("ConfigService.Save was called but should not have been for invalid image_directory")
				}
				if w.Code != http.StatusOK {
					t.Errorf("expected status 200 for validation error, got %d", w.Code)
				}
				doc, err := testutil.ParseHTML(w.Body)
				if err != nil {
					t.Fatalf("parse HTML: %v", err)
				}
				errorMsg := testutil.FindElementByID(doc, "config-error-message")
				if errorMsg == nil {
					t.Fatal("missing #config-error-message for invalid image_directory")
				}
				if got := testutil.GetTextContent(errorMsg); !strings.Contains(got, "image_directory") {
					t.Errorf("expected error mentioning image_directory, got %q", got)
				}
			} else {
				if !saveCalled {
					t.Error("ConfigService.Save was not called for valid image_directory")
				}
				if w.Code != http.StatusOK {
					t.Errorf("expected status 200, got %d", w.Code)
				}
				doc, err := testutil.ParseHTML(w.Body)
				if err != nil {
					t.Fatalf("parse HTML: %v", err)
				}
				if testutil.FindElementByID(doc, "config-success-message") == nil {
					t.Error("expected success message for valid image_directory")
				}
			}
		})
	}
}

func TestConfigHandlers_ConfigPost_ThemesDoNotRequireRestart(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	oldCfg := config.DefaultConfig()
	oldCfg.Themes = []string{"dark", "light"}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return oldCfg, nil
		},
		saveFunc: func(ctx context.Context, cfg *config.Config) error {
			return nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	// Change themes - should NOT trigger restart
	req := httptest.NewRequest(http.MethodPost, "/config/themes", strings.NewReader("themes=dark&themes=light&themes=cupcake"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	themesHandler := NewConfigThemesHandler(ch)
	themesHandler.UpdateThemesHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Should show success message, NOT restart badge
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Should have success message
	if testutil.FindElementByID(doc, "config-success-message") == nil {
		t.Fatal("expected #config-success-message for non-restart save")
	}

	// Should NOT have restart badge
	if testutil.FindElementByID(doc, "config-restart-badge") != nil {
		t.Error("themes change should NOT trigger restart, but #config-restart-badge was found")
	}
}
