package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

// fakeConfigQueries is a test double for config.ConfigQueries.
type fakeConfigQueries struct {
	configs []gallerydb.Config
	err     error
}

func (f fakeConfigQueries) GetConfigs(ctx context.Context) ([]gallerydb.Config, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.configs, nil
}

func TestConfigHandlers_RestoreLastKnownGood_PreviewDBError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.DBRwPool = errConnPool{getErr: errors.New("no db")}
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=preview", nil)
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_PreviewInvalidCSRF(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager = &mockSessionManagerAuthenticatedInvalidCSRF{}

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=preview", nil)
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_CommitInvalidCSRF(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.DBRwPool = errConnPool{getErr: errors.New("no db")}
	ch.SessionManager = &mockSessionManagerAuthenticatedInvalidCSRF{}

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", nil)
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_InvalidAction(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
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
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	expected := "Failed to restore last known good config"
	if body != expected {
		t.Errorf("expected %q error, got %s", expected, body)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_CommitRestartRequired(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

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

	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	// SetRestartRequired removed; handled by deps
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	// SetRestartRequired handled by deps
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if testutil.FindElementByID(doc, "config-success-message") == nil {
		t.Fatal("missing #config-success-message")
	}
}

// flushTrackingResponseWriter wraps httptest.ResponseRecorder to track Flush calls.
type flushTrackingResponseWriter struct {
	*httptest.ResponseRecorder
	flushed bool
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
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	expected := "Restored config is invalid"
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
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
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
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	// SetRestartRequired removed; handled by deps
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	// SetRestartRequired handled by deps
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if testutil.FindElementByID(doc, "config-success-message") == nil {
		t.Fatal("missing #config-success-message")
	}
}

func TestConfigHandlers_RestoreLastKnownGood_CommitParseFormError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_PreviewLoadError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return nil, errors.New("load error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.DBRwPool = &testConnPool{}
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=preview", nil)
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_PreviewDiffError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.DBRwPool = &testConnPool{}
	ch.getConfigQueries = func(cpc *dbconnpool.CpConn) config.ConfigQueries {
		return fakeConfigQueries{err: errors.New("db unavailable")}
	}
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=preview", nil)
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if !strings.HasPrefix(body, "Failed to get last known good config") {
		t.Errorf("expected last known good error, got %s", body)
	}
}

func TestConfigHandlers_RestoreLastKnownGood_PreviewSuccess(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return config.DefaultConfig(), nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.DBRwPool = &testConnPool{}
	ch.getConfigQueries = func(cpc *dbconnpool.CpConn) config.ConfigQueries {
		return fakeConfigQueries{
			configs: []gallerydb.Config{{
				Key:   "LastKnownGoodConfig",
				Value: "listener-port: 8081\nsite-name: Backup",
			}},
		}
	}
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=preview", nil)
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	yamlEl := testutil.FindElementByID(doc, "restore-yaml-content")
	if yamlEl == nil {
		t.Fatal("missing #restore-yaml-content")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(yamlEl)); got == "" {
		t.Error("expected #restore-yaml-content to have backup YAML content")
	}
}

func TestConfigHandlers_RestoreLastKnownGood_CommitLoadError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return nil, errors.New("load error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/restore-last-known-good?action=commit", strings.NewReader("csrf_token=valid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.RestoreLastKnownGoodHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestFakeConfigSaver_SaveToDatabase(t *testing.T) {
	t.Parallel()

	// Proof-of-pattern: fakeConfigSaver captures all saved config values
	// without requiring a real database connection.
	saver := NewFakeConfigSaver()
	ctx := context.Background()

	cfg := config.DefaultConfig()
	cfg.SiteName = "Test Gallery"

	if err := cfg.SaveToDatabase(ctx, saver); err != nil {
		t.Fatalf("SaveToDatabase failed: %v", err)
	}

	// Verify key config values were persisted
	if got := saver.Saved["site_name"]; got != "Test Gallery" {
		t.Errorf("site_name = %q, want %q", got, "Test Gallery")
	}

	// Verify LastKnownGoodConfig was saved (YAML export)
	if yamlContent := saver.Saved["LastKnownGoodConfig"]; yamlContent == "" {
		t.Error("LastKnownGoodConfig should be saved")
	}

	// fakeConfigSaver with SaveErr simulates database failures
	failingSaver := NewFakeConfigSaver()
	failingSaver.SaveErr = errors.New("disk full")
	if err := cfg.SaveToDatabase(ctx, failingSaver); err == nil {
		t.Error("expected error from failing saver, got nil")
	}
}
