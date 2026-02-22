package handlers

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/web"
)

// setupTestConfigHandlers creates a ConfigHandlers instance for testing.
// It provides minimal mocks for templates and dependencies.
func setupTestConfigHandlers(t *testing.T, mockSvc config.ConfigService, mockAuthSvc auth.AuthService, mockCredStore auth.CredentialStore) *ConfigHandlers {
	t.Helper()

	if mockSvc == nil {
		mockSvc = &mockConfigServiceForETag{}
	}
	if mockAuthSvc == nil {
		mockAuthSvc = &mockAuthService{}
	}
	if mockCredStore == nil {
		mockCredStore = &mockCredentialStore{}
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

	ch := NewConfigHandlers(
		mockSvc,
		mockAuthSvc,
		mockCredStore,
		sm,
		nil, // DBRoPool
		nil, // DBRwPool
		templates,
		ctx,
	)

	// Set required callbacks with sensible defaults
	ch.UpdateConfig = func(*config.Config, []string) {}
	ch.ApplyConfig = func() {}
	ch.IncrementETag = func() (string, error) { return "test-etag", nil }
	ch.InvalidateHTTPCache = func() {}
	ch.SetPreloadEnabled = func(bool) {}
	ch.SetRestartRequired = func(bool) {}
	ch.GetRestartCh = func() chan struct{} { return make(chan struct{}, 1) }
	ch.AddCommonTemplateData = func(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
		if data == nil {
			data = make(map[string]any)
		}
		data["IsAuthenticated"] = true
		data["CSRFToken"] = "test-csrf-token"
		if _, ok := data["HelpText"]; !ok {
			data["HelpText"] = map[string]string{}
		}
		if _, ok := data["ExampleValue"]; !ok {
			data["ExampleValue"] = map[string]string{}
		}
		return data
	}
	ch.ServerError = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	return ch
}

// mockCredentialStore implements auth.CredentialStore for testing
type mockCredentialStore struct{}

func (m *mockCredentialStore) GetAdminUsername(ctx context.Context) (string, error) {
	return "admin", nil
}

func (m *mockCredentialStore) GetUser(ctx context.Context, username string) (*session.User, error) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.DefaultCost)
	return &session.User{Username: username, Password: string(hash)}, nil
}

func (m *mockCredentialStore) UpsertUser(ctx context.Context, username string, passwordHash string) error {
	return nil
}

func (m *mockCredentialStore) CheckAccountLockout(ctx context.Context, username string) (bool, error) {
	return false, nil
}

func (m *mockCredentialStore) RecordFailedLoginAttempt(ctx context.Context, username string) error {
	return nil
}

func (m *mockCredentialStore) ClearLoginAttempts(ctx context.Context, username string) error {
	return nil
}

func (m *mockCredentialStore) UpdateUsername(ctx context.Context, username string) error {
	return nil
}

func (m *mockCredentialStore) UpdatePassword(ctx context.Context, passwordHash string) error {
	return nil
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

func (m *mockConfigServiceForETag) Export() (string, error) {
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

	// Setup mocks
	newETag := "20260130-02"
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

	var updateConfigCalled bool
	h := setupTestConfigHandlers(t, mockSvc, nil, nil)
	h.UpdateConfig = func(c *config.Config, changedFields []string) {
		updateConfigCalled = true
	}
	h.Ctx = context.Background()

	// Create authenticated request with CSRF
	formData := strings.NewReader("csrf_token=valid-token")
	req := httptest.NewRequest("POST", "/config/increment-etag", formData)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	// Set session to authenticated
	h.SessionManager.(*mockSessionManagerAuth).authenticated = true

	// Call handler
	h.ConfigIncrementETag(w, req)

	// Verify status - should be 200 OK
	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify UpdateConfig was called to update in-memory config
	if !updateConfigCalled {
		t.Fatal("UpdateConfig callback was not called")
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

	h := setupTestConfigHandlers(t, mockSvc, nil, nil)
	h.Ctx = context.Background()
	h.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest("POST", "/config/increment-etag", strings.NewReader("csrf_token=valid-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ConfigIncrementETag(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if w.Header().Get("HX-Retarget") != "#config-error-message" {
		t.Errorf("expected HX-Retarget header, got %q", w.Header().Get("HX-Retarget"))
	}
}

func TestConfigIncrementETag_Unauthenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("Parse templates: %v", err)
	}

	h := setupTestConfigHandlers(t, &mockConfigServiceForETag{}, nil, nil)
	h.SessionManager.(*mockSessionManagerAuth).authenticated = false

	req := httptest.NewRequest("POST", "/config/increment-etag", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ConfigIncrementETag(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestConfigIncrementETag_ParseFormError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("Parse templates: %v", err)
	}

	h := setupTestConfigHandlers(t, &mockConfigServiceForETag{}, nil, nil)
	h.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest("POST", "/config/increment-etag", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ConfigIncrementETag(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestConfigIncrementETag_InvalidCSRF(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("Parse templates: %v", err)
	}

	h := setupTestConfigHandlers(t, &mockConfigServiceForETag{}, nil, nil)
	h.SessionManager = &mockSessionManagerAuthenticatedInvalidCSRF{}

	req := httptest.NewRequest("POST", "/config/increment-etag", strings.NewReader("csrf_token=invalid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ConfigIncrementETag(w, req)

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

	h := setupTestConfigHandlers(t, mockSvc, nil, nil)
	h.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest("POST", "/config/increment-etag", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.ConfigIncrementETag(w, req)

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

	var invalidateCallCount int
	h := setupTestConfigHandlers(t, mockSvc, nil, nil)
	h.InvalidateHTTPCache = func() {
		invalidateCallCount++
	}
	h.IncrementETag = func() (string, error) {
		return newETag, nil
	}
	h.Ctx = context.Background()

	formData := strings.NewReader("csrf_token=valid-token")
	req := httptest.NewRequest("POST", "/config/increment-etag", formData)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	// Set session to authenticated
	h.SessionManager.(*mockSessionManagerAuth).authenticated = true

	h.ConfigIncrementETag(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if invalidateCallCount != 1 {
		t.Errorf("InvalidateHTTPCache called %d times, want 1", invalidateCallCount)
	}
}
