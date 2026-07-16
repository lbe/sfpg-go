package handlers

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
	"golang.org/x/net/html"
)

// setupTestConfigHandlers creates a ConfigHandlers instance for testing.
// It provides minimal mocks for templates and dependencies.
func setupTestConfigHandlers(t *testing.T, mockSvc config.ConfigService, mockAuthSvc auth.AuthService) *ConfigHandlers {
	t.Helper()

	if mockSvc == nil {
		mockSvc = &mockConfigServiceForETag{}
	}
	if mockAuthSvc == nil {
		mockAuthSvc = &mockAuthServiceForConfig{}
	}

	// Parse templates
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tmplSaveRestart, err := template.ParseFS(web.FS, "templates/config-ui/config-save-restart-alert.html.tmpl")
	if err != nil {
		t.Fatalf("failed to parse save-restart template: %v", err)
	}
	tmplSaveSuccess, err := template.ParseFS(web.FS, "templates/config-ui/config-save-success-alert.html.tmpl")
	if err != nil {
		t.Fatalf("failed to parse save-success template: %v", err)
	}
	tmplExport, err := template.ParseFS(web.FS, "templates/config-ui/config-export-modal.html.tmpl")
	if err != nil {
		t.Fatalf("failed to parse export template: %v", err)
	}
	tmplImport, err := template.ParseFS(web.FS, "templates/config-ui/config-import-modal.html.tmpl")
	if err != nil {
		t.Fatalf("failed to parse import template: %v", err)
	}
	tmplImportSuccess, err := template.ParseFS(web.FS, "templates/config-ui/config-import-success-alert.html.tmpl")
	if err != nil {
		t.Fatalf("failed to parse import success template: %v", err)
	}
	tmplRestore, err := template.ParseFS(web.FS, "templates/config-ui/config-restore-modal.html.tmpl")
	if err != nil {
		t.Fatalf("failed to parse restore template: %v", err)
	}
	tmplRestoreSuccess, err := template.ParseFS(web.FS, "templates/config-ui/config-restore-success-alert.html.tmpl")
	if err != nil {
		t.Fatalf("failed to parse restore success template: %v", err)
	}
	tmplRestart, err := template.ParseFS(web.FS, "templates/config-ui/config-restart-initiated-alert.html.tmpl")
	if err != nil {
		t.Fatalf("failed to parse restart template: %v", err)
	}

	templates := ConfigTemplates{
		SaveRestartAlert:      tmplSaveRestart,
		SaveSuccessAlert:      tmplSaveSuccess,
		ExportModal:           tmplExport,
		ImportModal:           tmplImport,
		ImportSuccessAlert:    tmplImportSuccess,
		RestoreModal:          tmplRestore,
		RestoreSuccessAlert:   tmplRestoreSuccess,
		RestartInitiatedAlert: tmplRestart,
	}

	sm := &mockSessionManagerAuth{}
	ctx := context.Background()

	credStore := &fakeCredentialStore{}
	cfgOps := &mockConfigOps{}
	helper := &mockTemplateHelpers{
		CSRFToken: "test-csrf-token",
		Authed:    true,
	}

	ch := NewConfigHandlers(
		mockSvc,
		mockAuthSvc,
		sm,
		nil,       // DBRoPool
		nil,       // DBRwPool
		credStore, // credStore
		cfgOps,    // cfgOps
		helper.AddCommonTemplateData,
		templates,
		ctx,
	)

	return ch
}

// mockConfigService for config_etag_test.go
type mockConfigServiceForETag struct {
	incrementETagFunc func(ctx context.Context) (string, error)
	loadFunc          func(ctx context.Context) (*config.Config, error)
}

func (m *mockConfigServiceForETag) Load(ctx context.Context) (*config.Config, error) {
	if m.loadFunc != nil {
		return m.loadFunc(ctx)
	}
	return config.DefaultConfig(), nil
}

func (m *mockConfigServiceForETag) Save(ctx context.Context, cfg *config.Config) error {
	return nil
}

func (m *mockConfigServiceForETag) Validate(cfg *config.Config) error {
	return nil
}

func (m *mockConfigServiceForETag) Export(ctx context.Context) (string, error) {
	return "site_name: Test\n", nil
}

func (m *mockConfigServiceForETag) Import(yamlContent string, ctx context.Context) error {
	return nil
}

func (m *mockConfigServiceForETag) RestoreLastKnownGood(ctx context.Context) (*config.Config, error) {
	return config.DefaultConfig(), nil
}

func (m *mockConfigServiceForETag) EnsureDefaults(ctx context.Context, rootDir string) error {
	return nil
}

func (m *mockConfigServiceForETag) GetConfigValue(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (m *mockConfigServiceForETag) IncrementETag(ctx context.Context) (string, error) {
	if m.incrementETagFunc != nil {
		return m.incrementETagFunc(ctx)
	}
	return "20260130-01", nil
}

func (m *mockConfigServiceForETag) UpdateAdminPassword(ctx context.Context, newPwdHash string) error {
	return nil
}

// --- Test cases ---

func TestConfigIncrementETag_UpdatesInMemoryConfig(t *testing.T) {
	// Initialize templates for RenderTemplate
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("Parse templates: %v", err)
	}

	// Setup mocks — load returns the new ETag (simulating post-increment reload)
	newETag := "20260130-02"
	defaultETag := config.DefaultConfig().ETagVersion
	mockSvc := &mockConfigServiceForETag{
		incrementETagFunc: func(ctx context.Context) (string, error) {
			return newETag, nil
		},
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			cfg := config.DefaultConfig()
			cfg.ETagVersion = newETag
			return cfg, nil
		},
	}

	h := setupTestConfigHandlers(t, mockSvc, nil)
	h.Ctx = context.Background()

	// Create authenticated request with CSRF
	formData := strings.NewReader("csrf_token=valid-token")
	req := httptest.NewRequest("POST", "/config/increment-etag", formData)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	// Set session to authenticated
	h.SessionManager.(*mockSessionManagerAuth).authenticated = true

	// Call handler
	NewConfigETagHandler(h).ConfigIncrementETag(w, req)

	// Verify status - should be 200 OK
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Parse HTML response to verify ETag value is rendered
	doc, err := html.Parse(strings.NewReader(w.Body.String()))
	if err != nil {
		t.Fatalf("Parse HTML: %v", err)
	}

	// Verify response contains #config-etag-version element with correct ETag value
	etagElem := testutil.FindElementByID(doc, "config-etag-version")
	if etagElem == nil {
		t.Fatal("Element #config-etag-version not found in response")
	}
	gotETag := testutil.GetAttr(etagElem, "value")
	if gotETag != newETag {
		t.Errorf("ETag value = %q, want %q", gotETag, newETag)
	}

	// Verify ETag format matches the expected pattern (YYMMDD-NN)
	pattern := `^\d{8}-\d{2}$`
	matched, _ := regexp.MatchString(pattern, gotETag)
	if !matched {
		t.Errorf("ETag %q does not match pattern %s", gotETag, pattern)
	}

	// Verify ETag actually changed from the default (new != default)
	if gotETag == defaultETag {
		t.Errorf("ETag did not change from default: got %q, default was %q", gotETag, defaultETag)
	}

	// Verify in-memory config was updated via UpdateConfigWithPrecedence
	cfgOps, ok := h.cfgOps.(*mockConfigOps)
	if !ok {
		t.Fatalf("cfgOps is not *mockConfigOps, got %T", h.cfgOps)
	}
	if cfgOps.Cfg == nil {
		t.Fatal("cfgOps.Cfg is nil, UpdateConfigWithPrecedence was not called")
	}
	if cfgOps.Cfg.ETagVersion != newETag {
		t.Errorf("cfgOps.Cfg.ETagVersion = %q, want %q", cfgOps.Cfg.ETagVersion, newETag)
	}
}

func TestConfigIncrementETag_Error(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("Parse templates: %v", err)
	}

	mockSvc := &mockConfigServiceForETag{
		incrementETagFunc: func(ctx context.Context) (string, error) {
			return "", errors.New("increment error")
		},
	}

	h := setupTestConfigHandlers(t, mockSvc, nil)
	h.Ctx = context.Background()
	h.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest("POST", "/config/increment-etag", strings.NewReader("csrf_token=valid-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	NewConfigETagHandler(h).ConfigIncrementETag(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if w.Header().Get("HX-Retarget") != "#config-error-message" {
		t.Errorf("expected HX-Retarget header, got %q", w.Header().Get("HX-Retarget"))
	}
}

func TestConfigIncrementETag_ParseFormError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("Parse templates: %v", err)
	}

	h := setupTestConfigHandlers(t, &mockConfigServiceForETag{}, nil)
	h.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest("POST", "/config/increment-etag", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	NewConfigETagHandler(h).ConfigIncrementETag(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestConfigIncrementETag_InvalidCSRF(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("Parse templates: %v", err)
	}

	h := setupTestConfigHandlers(t, &mockConfigServiceForETag{}, nil)
	h.SessionManager = &mockSessionManagerAuthenticatedInvalidCSRF{}

	req := httptest.NewRequest("POST", "/config/increment-etag", strings.NewReader("csrf_token=invalid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	NewConfigETagHandler(h).ConfigIncrementETag(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestConfigIncrementETag_LoadError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("Parse templates: %v", err)
	}

	mockSvc := &mockConfigServiceForETag{
		incrementETagFunc: func(ctx context.Context) (string, error) {
			return "20260130-03", nil
		},
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return nil, errors.New("load error")
		},
	}

	h := setupTestConfigHandlers(t, mockSvc, nil)
	h.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest("POST", "/config/increment-etag", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	NewConfigETagHandler(h).ConfigIncrementETag(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// TestConfigIncrementETag_InvalidatesHTTPCache verifies that ConfigIncrementETag
// invokes InvalidateHTTPCache when ETag is incremented, so the HTTP cache is cleared.
func TestConfigIncrementETag_InvalidatesHTTPCache(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("Parse templates: %v", err)
	}

	newETag := "20260130-03"
	mockSvc := &mockConfigServiceForETag{
		incrementETagFunc: func(ctx context.Context) (string, error) {
			return newETag, nil
		},
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			cfg := config.DefaultConfig()
			cfg.ETagVersion = newETag
			return cfg, nil
		},
	}

	h := setupTestConfigHandlers(t, mockSvc, nil)
	h.Ctx = context.Background()

	formData := strings.NewReader("csrf_token=valid-token")
	req := httptest.NewRequest("POST", "/config/increment-etag", formData)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	// Set session to authenticated
	h.SessionManager.(*mockSessionManagerAuth).authenticated = true

	// Reset the call tracker before invoking
	cfgOps := h.cfgOps.(*mockConfigOps)
	cfgOps.InvalidateHTTPCacheCalled = false

	NewConfigETagHandler(h).ConfigIncrementETag(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify that InvalidateHTTPCache was called
	if !cfgOps.InvalidateHTTPCacheCalled {
		t.Error("InvalidateHTTPCache was not called after ETag increment")
	}
}
