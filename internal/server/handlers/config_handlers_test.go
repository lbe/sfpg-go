package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

type mockAuthServiceForConfig struct {
	updateCredentialsFunc func(ctx context.Context, opts auth.CredentialUpdateOptions, store auth.CredentialStore) (*auth.CredentialUpdateResult, error)
}

func (m *mockAuthServiceForConfig) Authenticate(ctx context.Context, username, password string) (*session.User, error) {
	return nil, auth.ErrInvalidCredentials
}

func (m *mockAuthServiceForConfig) CheckLockout(ctx context.Context, username string) (bool, error) {
	return false, nil
}

func (m *mockAuthServiceForConfig) RecordFailedAttempt(ctx context.Context, username string) error {
	return nil
}

func (m *mockAuthServiceForConfig) ClearAttempts(ctx context.Context, username string) error {
	return nil
}

func (m *mockAuthServiceForConfig) UpdateCredentials(ctx context.Context, opts auth.CredentialUpdateOptions, store auth.CredentialStore) (*auth.CredentialUpdateResult, error) {
	if m.updateCredentialsFunc != nil {
		return m.updateCredentialsFunc(ctx, opts, store)
	}
	return &auth.CredentialUpdateResult{}, nil
}

type mockSessionManagerAuthenticatedInvalidCSRF struct{}

func (m *mockSessionManagerAuthenticatedInvalidCSRF) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerAuthenticatedInvalidCSRF) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return "test-csrf-token"
}

func (m *mockSessionManagerAuthenticatedInvalidCSRF) ValidateCSRFToken(r *http.Request) bool {
	return false
}

func (m *mockSessionManagerAuthenticatedInvalidCSRF) ClearSession(w http.ResponseWriter, r *http.Request) {
}

func (m *mockSessionManagerAuthenticatedInvalidCSRF) GetSession(r *http.Request) (*sessions.Session, error) {
	sess := sessions.NewSession(nil, session.SessionName)
	sess.IsNew = false
	sess.Values["csrf_token"] = "existing-csrf-token"
	return sess, nil
}

func (m *mockSessionManagerAuthenticatedInvalidCSRF) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerAuthenticatedInvalidCSRF) IsAuthenticated(r *http.Request) bool {
	return true
}

func (m *mockSessionManagerAuthenticatedInvalidCSRF) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return nil
}

type mockConfigServiceForConfig struct {
	loadFunc          func(ctx context.Context) (*config.Config, error)
	saveFunc          func(ctx context.Context, cfg *config.Config) error
	validateFunc      func(cfg *config.Config) error
	exportFunc        func() (string, error)
	importFunc        func(yamlContent string, ctx context.Context) error
	restoreFunc       func(ctx context.Context) (*config.Config, error)
	ensureDefaultsFun func(ctx context.Context, rootDir string) error
	getConfigValueFun func(ctx context.Context, key string) (string, error)
	incrementETagFunc func(ctx context.Context) (string, error)
}

func (m *mockConfigServiceForConfig) Load(ctx context.Context) (*config.Config, error) {
	if m.loadFunc != nil {
		return m.loadFunc(ctx)
	}
	return config.DefaultConfig(), nil
}

func (m *mockConfigServiceForConfig) Save(ctx context.Context, cfg *config.Config) error {
	if m.saveFunc != nil {
		return m.saveFunc(ctx, cfg)
	}
	return nil
}

func (m *mockConfigServiceForConfig) Validate(cfg *config.Config) error {
	if m.validateFunc != nil {
		return m.validateFunc(cfg)
	}
	return nil
}

func (m *mockConfigServiceForConfig) Export() (string, error) {
	if m.exportFunc != nil {
		return m.exportFunc()
	}
	return "site_name: Test\n", nil
}

func (m *mockConfigServiceForConfig) Import(yamlContent string, ctx context.Context) error {
	if m.importFunc != nil {
		return m.importFunc(yamlContent, ctx)
	}
	return nil
}

func (m *mockConfigServiceForConfig) RestoreLastKnownGood(ctx context.Context) (*config.Config, error) {
	if m.restoreFunc != nil {
		return m.restoreFunc(ctx)
	}
	return config.DefaultConfig(), nil
}

func (m *mockConfigServiceForConfig) EnsureDefaults(ctx context.Context, rootDir string) error {
	if m.ensureDefaultsFun != nil {
		return m.ensureDefaultsFun(ctx, rootDir)
	}
	return nil
}

func (m *mockConfigServiceForConfig) GetConfigValue(ctx context.Context, key string) (string, error) {
	if m.getConfigValueFun != nil {
		return m.getConfigValueFun(ctx, key)
	}
	return "admin", nil
}

func (m *mockConfigServiceForConfig) IncrementETag(ctx context.Context) (string, error) {
	if m.incrementETagFunc != nil {
		return m.incrementETagFunc(ctx)
	}
	return "20260130-01", nil
}

func TestConfigHandlers_ConfigGet_Unauthenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.DBRoPool = errConnPool{getErr: errors.New("no db")}
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = false

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestConfigHandlers_ConfigGet_Authenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.DBRoPool = errConnPool{getErr: errors.New("no db")}
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConfigHandlers_ConfigGet_LoadError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return nil, errors.New("load error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.DBRoPool = errConnPool{getErr: errors.New("no db")}
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_ConfigPost_Unauthenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = false

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("site_name=Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestConfigHandlers_ConfigPost_InvalidCSRF(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager = &mockSessionManagerAuthenticatedInvalidCSRF{}

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("site_name=Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

func TestConfigHandlers_ConfigPost_ValidUpdate(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	var updateConfigCalled bool
	mockSvc := &mockConfigServiceForConfig{}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.UpdateConfig = func(*config.Config, []string) { updateConfigCalled = true }
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("listener_port=8081"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if !updateConfigCalled {
		t.Error("UpdateConfig callback not called")
	}
}

func TestConfigHandlers_ConfigPost_InvalidPort(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
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

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
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
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("listener_port=8081"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConfigHandlers_ExportConfigDownload_Unauthenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = false

	req := httptest.NewRequest(http.MethodGet, "/config/export/download", nil)
	w := httptest.NewRecorder()

	ch.ExportConfigDownloadHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestConfigHandlers_ExportConfigDownload_Success(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		exportFunc: func() (string, error) {
			return "site_name: TestExport\n", nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config/export/download", nil)
	w := httptest.NewRecorder()

	ch.ExportConfigDownloadHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Error("expected Content-Disposition header with attachment")
	}
}

func TestConfigHandlers_ImportPreview_Unauthenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = false

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", nil)
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportPreview_MissingYAML(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportCommit_InvalidCSRF(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager = &mockSessionManagerAuthenticatedInvalidCSRF{}

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("yaml=site_name: Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportCommit_Success(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	var updateConfigCalled bool
	var applyConfigCalled bool
	mockSvc := &mockConfigServiceForConfig{}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.UpdateConfig = func(*config.Config, []string) { updateConfigCalled = true }
	ch.ApplyConfig = func() { applyConfigCalled = true }
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("yaml=site_name: Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if !updateConfigCalled {
		t.Error("UpdateConfig not called")
	}
	if !applyConfigCalled {
		t.Error("ApplyConfig not called")
	}
}

func TestConfigHandlers_RestoreLastKnownGood_Unauthenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = false

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good", nil)
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_PreviewDBError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.DBRwPool = errConnPool{getErr: errors.New("no db")}
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=preview", nil)
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_CommitInvalidCSRF(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.DBRwPool = errConnPool{getErr: errors.New("no db")}
	ch.SessionManager = &mockSessionManagerAuthenticatedInvalidCSRF{}

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", nil)
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

func TestConfigHandlers_Restart_Unauthenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = false

	req := httptest.NewRequest(http.MethodPost, "/config/restart", nil)
	w := httptest.NewRecorder()

	ch.RestartHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestConfigHandlers_Restart_InvalidCSRF(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager = &mockSessionManagerAuthenticatedInvalidCSRF{}

	req := httptest.NewRequest(http.MethodPost, "/config/restart", nil)
	w := httptest.NewRecorder()

	ch.RestartHandler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

func TestConfigHandlers_Restart_Authenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	restartCh := make(chan struct{}, 1)
	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.GetRestartCh = func() chan struct{} { return restartCh }
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restart", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestartHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	select {
	case <-restartCh:
	case <-time.After(2 * time.Second):
		t.Error("expected restart signal")
	}
}

func TestConfigHandlers_ConfigPost_ParseFormError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
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

	var preloadValue bool
	var preloadCalled bool

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return config.DefaultConfig(), nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SetPreloadEnabled = func(enabled bool) {
		preloadCalled = true
		preloadValue = enabled
	}
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if !preloadCalled {
		t.Fatal("expected SetPreloadEnabled to be called")
	}
	if preloadValue {
		t.Error("expected SetPreloadEnabled(false) when checkbox is unchecked")
	}
}

func TestConfigHandlers_ExportConfigToFileHandler_Error(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		exportFunc: func() (string, error) {
			return "", errors.New("export error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/export/to-file", nil)
	w := httptest.NewRecorder()

	ch.ExportConfigToFileHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportPreview_InvalidExtension(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("yaml", "config.txt")
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := part.Write([]byte("site_name: Test")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportPreview_InvalidYAML(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", strings.NewReader("yaml=listener-port:%20["))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if !strings.HasPrefix(body, "Invalid YAML:") {
		t.Errorf("expected Invalid YAML error, got %s", body)
	}
}

func TestConfigHandlers_ImportCommit_MissingYAML(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportCommit_ImportError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		importFunc: func(yamlContent string, ctx context.Context) error {
			return errors.New("import error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("yaml=site_name: Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	expected := "Import failed: import error"
	if body != expected {
		t.Errorf("expected %q error, got %s", expected, body)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_InvalidAction(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=bad", nil)
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_CommitRestoreError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		restoreFunc: func(ctx context.Context) (*config.Config, error) {
			return nil, errors.New("restore error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	expected := "Failed to restore last known good config: restore error"
	if body != expected {
		t.Errorf("expected %q error, got %s", expected, body)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_CommitRestartRequired(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	var restartRequired bool
	current := config.DefaultConfig()
	restored := config.DefaultConfig()
	restored.ListenerPort = current.ListenerPort + 1

	mockSvc := &mockConfigServiceForConfig{
		restoreFunc: func(ctx context.Context) (*config.Config, error) {
			return restored, nil
		},
		validateFunc: func(cfg *config.Config) error {
			return nil
		},
		saveFunc: func(ctx context.Context, cfg *config.Config) error {
			return nil
		},
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return current, nil
		},
	}

	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SetRestartRequired = func(value bool) { restartRequired = value }
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if !restartRequired {
		t.Error("expected SetRestartRequired(true) to be called")
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if testutil.FindElementByID(doc, "config-success-message") == nil {
		t.Fatal("missing #config-success-message")
	}
}

func TestConfigHandlers_Restart_NilRestartChannel(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.GetRestartCh = func() chan struct{} { return nil }
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restart", nil)
	w := httptest.NewRecorder()

	ch.RestartHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestConfigHandlers_Validate_MissingFields(t *testing.T) {
	ch := &ConfigHandlers{}
	if err := ch.Validate(); err == nil {
		t.Fatal("expected Validate to report missing fields")
	}
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
	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, mockAuthSvc, &mockCredentialStore{})
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
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
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

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
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

	var restartRequiredSet bool
	var restartRequiredValue bool

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true
	ch.SetRestartRequired = func(v bool) {
		restartRequiredSet = true
		restartRequiredValue = v
	}

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("listener_port=8082"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !restartRequiredSet {
		t.Fatal("expected SetRestartRequired to be called for restart-required config change")
	}
	if !restartRequiredValue {
		t.Fatal("expected SetRestartRequired(true) for restart-required config change")
	}
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
	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, mockAuthSvc, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_ExportConfigDownload_Error(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		exportFunc: func() (string, error) {
			return "", errors.New("export error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config/export/download", nil)
	w := httptest.NewRecorder()

	ch.ExportConfigDownloadHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportPreview_MissingFile(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportPreview_LoadError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return nil, errors.New("load error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", strings.NewReader("yaml=site_name: Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_CommitValidateError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		restoreFunc: func(ctx context.Context) (*config.Config, error) {
			return config.DefaultConfig(), nil
		},
		validateFunc: func(cfg *config.Config) error {
			return errors.New("invalid")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	expected := "Restored config is invalid: invalid"
	if body != expected {
		t.Errorf("expected %q error, got %s", expected, body)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_CommitSaveError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		restoreFunc: func(ctx context.Context) (*config.Config, error) {
			return config.DefaultConfig(), nil
		},
		validateFunc: func(cfg *config.Config) error {
			return nil
		},
		saveFunc: func(ctx context.Context, cfg *config.Config) error {
			return errors.New("save error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_CommitNoRestartRequired(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	current := config.DefaultConfig()
	restored := config.DefaultConfig()
	var restartRequired bool

	mockSvc := &mockConfigServiceForConfig{
		restoreFunc: func(ctx context.Context) (*config.Config, error) {
			return restored, nil
		},
		validateFunc: func(cfg *config.Config) error {
			return nil
		},
		saveFunc: func(ctx context.Context, cfg *config.Config) error {
			return nil
		},
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return current, nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SetRestartRequired = func(value bool) { restartRequired = value }
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if restartRequired {
		t.Error("did not expect SetRestartRequired to be called")
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if testutil.FindElementByID(doc, "config-success-message") == nil {
		t.Fatal("missing #config-success-message")
	}
}

func TestConfigHandlers_ConfigPost_InvalidRetentionCount(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
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

func TestConfigHandlers_ImportCommit_Unauthenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = false

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("yaml=site_name: Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportCommit_ParseFormError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_ExportConfigToFileHandler_Unauthenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = false

	req := httptest.NewRequest(http.MethodPost, "/config/export/to-file", nil)
	w := httptest.NewRecorder()

	ch.ExportConfigToFileHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_CommitParseFormError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportPreview_MultipartParseError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", strings.NewReader("invalid"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=invalid")
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
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
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("themes=light"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

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

func TestConfigHandlers_ConfigGet_ThemesSorted(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Create config with unsorted themes
	cfg := config.DefaultConfig()
	cfg.Themes = []string{"dark", "light", "cupcake", "wireframe", "valentine"}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return cfg, nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.DBRoPool = errConnPool{getErr: errors.New("no db")}
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Parse response and verify themes are sorted
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Find the themes select element
	themesSelect := testutil.FindElementByID(doc, "")
	if themesSelect == nil {
		// The themes are rendered in a select element with name="themes"
		// We need to check the order of options in the rendered HTML
		bodyStr := w.Body.String()
		// Look for themes in order they should appear (light, dark first, then alphabetical)
		expectedOrder := []string{"light", "dark", "cupcake", "valentine", "wireframe"}

		// Find positions of each theme in the HTML
		positions := make(map[string]int)
		for _, theme := range expectedOrder {
			pos := strings.Index(bodyStr, fmt.Sprintf("value=\"%s\"", theme))
			if pos == -1 {
				t.Errorf("theme %q not found in response", theme)
				continue
			}
			positions[theme] = pos
		}

		// Verify order - each theme should appear before the next alphabetically
		for i := 0; i < len(expectedOrder)-1; i++ {
			curr := expectedOrder[i]
			next := expectedOrder[i+1]
			if positions[curr] > positions[next] {
				t.Errorf("themes not sorted alphabetically: %q (pos %d) should come before %q (pos %d)",
					curr, positions[curr], next, positions[next])
			}
		}
	}
}

func TestConfigHandlers_ExportConfigToFileHandler_Success(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		exportFunc: func() (string, error) {
			return "site_name: TestExport\n", nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/export/to-file", nil)
	w := httptest.NewRecorder()

	ch.ExportConfigToFileHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	heading := testutil.FindElementByTag(doc, "h3")
	if heading == nil {
		t.Fatal("missing h3 heading")
	}
	if got := testutil.GetTextContent(heading); got != "Export Configuration" {
		t.Errorf("heading = %q, want %q", got, "Export Configuration")
	}
	pre := testutil.FindElementByTag(doc, "pre")
	if pre == nil {
		t.Fatal("missing pre element")
	}
	if got := testutil.GetTextContent(pre); !strings.Contains(got, "site_name: TestExport") {
		t.Errorf("expected YAML content, got %q", got)
	}
}

func TestConfigHandlers_ImportPreview_MultipartSuccess(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("yaml", "config.yaml")
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err = part.Write([]byte("site_name: Test")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err = writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	heading := testutil.FindElementByTag(doc, "h3")
	if heading == nil {
		t.Fatal("missing h3 heading")
	}
	if got := testutil.GetTextContent(heading); got != "Import Configuration Preview" {
		t.Errorf("heading = %q, want %q", got, "Import Configuration Preview")
	}
	if testutil.FindElementByID(doc, "import-yaml-content") == nil {
		t.Fatal("missing #import-yaml-content")
	}
}

func TestConfigHandlers_ImportCommit_LoadError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return nil, errors.New("load error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("yaml=site_name: Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	expected := "Import succeeded but failed to load config"
	if body != expected {
		t.Errorf("expected %q error, got %s", expected, body)
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
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{}, &mockCredentialStore{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	// Change themes - should NOT trigger restart
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader("themes=dark&themes=light&themes=cupcake"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ConfigPost(w, req)

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
