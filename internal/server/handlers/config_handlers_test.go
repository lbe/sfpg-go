package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"

	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/session"
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
