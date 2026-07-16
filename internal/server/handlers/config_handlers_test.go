package handlers

import (
	"bytes"
	"context"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

// safeBuffer is a thread-safe wrapper around bytes.Buffer for use in tests
// where log output is written by a goroutine and read by the test.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *safeBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *safeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

var _ io.Writer = (*safeBuffer)(nil)

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

func (m *mockSessionManagerAuthenticatedInvalidCSRF) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	sess := sessions.NewSession(nil, session.SessionName)
	sess.IsNew = false
	sess.Values["csrf_token"] = "existing-csrf-token"
	return sess, nil
}

func (m *mockSessionManagerAuthenticatedInvalidCSRF) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerAuthenticatedInvalidCSRF) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	return true
}

func (m *mockSessionManagerAuthenticatedInvalidCSRF) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return nil
}

type mockConfigServiceForConfig struct {
	loadFunc          func(ctx context.Context) (*config.Config, error)
	saveFunc          func(ctx context.Context, cfg *config.Config) error
	validateFunc      func(cfg *config.Config) error
	exportFunc        func(ctx context.Context) (string, error)
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

func (m *mockConfigServiceForConfig) Export(ctx context.Context) (string, error) {
	if m.exportFunc != nil {
		return m.exportFunc(ctx)
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

// Auth enforcement is handled by authMiddleware at the router level,
// not by individual handlers. See TestAuthMiddleware_* in server_test.go.

func (w *flushTrackingResponseWriter) Flush() {
	w.flushed = true
}

// TestConfigHandlers_RestartHandler_FlushesResponseAndTriggersRestart verifies
// that the restart handler flushes the HTTP response before invoking the
// asynchronous process-restart callback.

func TestConfigHandlers_disableConfigCaching(t *testing.T) {
	h := NewConfigHandlers(nil, nil, nil, nil, nil, nil, nil, nil, ConfigTemplates{}, context.Background())

	rr := httptest.NewRecorder()

	h.disableConfigCaching(rr)

	if got := rr.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, private" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-store, no-cache, must-revalidate, private")
	}
	if got := rr.Header().Get("Pragma"); got != "no-cache" {
		t.Errorf("Pragma = %q, want %q", got, "no-cache")
	}
	if got := rr.Header().Get("Expires"); got != "0" {
		t.Errorf("Expires = %q, want %q", got, "0")
	}
}

func TestConfigHandlers_ConfigAuthMiddleware(t *testing.T) {
	h := NewConfigHandlers(nil, nil, nil, nil, nil, nil, nil, nil, ConfigTemplates{}, context.Background())

	called := false
	next := func(w http.ResponseWriter, r *http.Request) {
		called = true
	}

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	rr := httptest.NewRecorder()

	middleware := h.ConfigAuthMiddleware(next)
	middleware(rr, req)

	if !called {
		t.Error("inner handler was not called")
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, private" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-store, no-cache, must-revalidate, private")
	}
	if got := rr.Header().Get("Pragma"); got != "no-cache" {
		t.Errorf("Pragma = %q, want %q", got, "no-cache")
	}
	if got := rr.Header().Get("Expires"); got != "0" {
		t.Errorf("Expires = %q, want %q", got, "0")
	}
}

func TestConfigHandlers_executeConfigTemplate_ExecuteError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tmpl, err := template.New("error-template").Parse(`{{ template "nonexistent-template" . }}`)
	if err != nil {
		t.Fatalf("failed to parse error template: %v", err)
	}

	h := NewConfigHandlers(nil, nil, nil, nil, nil, nil, nil, nil, ConfigTemplates{SaveSuccessAlert: tmpl}, context.Background())

	rr := httptest.NewRecorder()
	h.executeConfigTemplate(rr, h.Templates.SaveSuccessAlert, "error-template", nil)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, rr.Code)
	}
	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}
	body := testutil.FindElementByTag(doc, "body")
	if body == nil {
		t.Fatal("missing body element")
	}
	if got := strings.TrimSpace(testutil.GetTextContent(body)); got != "Internal Server Error" {
		t.Errorf("expected body text %q, got %q", "Internal Server Error", got)
	}
}
