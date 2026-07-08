package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

// --- Mock implementations for auth handler tests ---

// mockAuthService implements auth.AuthService for testing.
type mockAuthService struct {
	authenticateFunc        func(ctx context.Context, username, password string) (*session.User, error)
	checkLockoutFunc        func(ctx context.Context, username string) (bool, error)
	recordFailedAttemptFunc func(ctx context.Context, username string) error
	clearAttemptsFunc       func(ctx context.Context, username string) error
}

func (m *mockAuthService) Authenticate(ctx context.Context, username, password string) (*session.User, error) {
	if m.authenticateFunc != nil {
		return m.authenticateFunc(ctx, username, password)
	}
	return nil, auth.ErrInvalidCredentials
}

func (m *mockAuthService) CheckLockout(ctx context.Context, username string) (bool, error) {
	if m.checkLockoutFunc != nil {
		return m.checkLockoutFunc(ctx, username)
	}
	return false, nil
}

func (m *mockAuthService) RecordFailedAttempt(ctx context.Context, username string) error {
	if m.recordFailedAttemptFunc != nil {
		return m.recordFailedAttemptFunc(ctx, username)
	}
	return nil
}

func (m *mockAuthService) ClearAttempts(ctx context.Context, username string) error {
	if m.clearAttemptsFunc != nil {
		return m.clearAttemptsFunc(ctx, username)
	}
	return nil
}

func (m *mockAuthService) UpdateCredentials(ctx context.Context, opts auth.CredentialUpdateOptions, store auth.CredentialStore) (*auth.CredentialUpdateResult, error) {
	return &auth.CredentialUpdateResult{}, nil
}

// mockSessionManagerAuth provides basic session management for auth tests.
type mockSessionManagerAuth struct {
	authenticated bool
}

func (m *mockSessionManagerAuth) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerAuth) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return "test-csrf-token"
}

func (m *mockSessionManagerAuth) ValidateCSRFToken(r *http.Request) bool {
	return true
}

func (m *mockSessionManagerAuth) ClearSession(w http.ResponseWriter, r *http.Request) {
}

func (m *mockSessionManagerAuth) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	sess := sessions.NewSession(nil, session.SessionName)
	sess.IsNew = true // New session by default
	return sess, nil
}

func (m *mockSessionManagerAuth) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerAuth) IsAuthenticated(r *http.Request) bool {
	return m.authenticated
}

func (m *mockSessionManagerAuth) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	m.authenticated = authenticated
	return nil
}

// mockSessionManagerWithExistingSession simulates an existing session WITH a CSRF token.
// This is used when we want to trigger 403 on CSRF failure for non-new sessions.
type mockSessionManagerWithExistingSession struct{}

func (m *mockSessionManagerWithExistingSession) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerWithExistingSession) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return "test-csrf-token"
}

func (m *mockSessionManagerWithExistingSession) ValidateCSRFToken(r *http.Request) bool {
	return false // Simulate CSRF validation failure
}

func (m *mockSessionManagerWithExistingSession) ClearSession(w http.ResponseWriter, r *http.Request) {
}

func (m *mockSessionManagerWithExistingSession) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	sess := sessions.NewSession(nil, session.SessionName)
	sess.IsNew = false // This is an existing session
	sess.Values["csrf_token"] = "existing-csrf-token"
	return sess, nil
}

func (m *mockSessionManagerWithExistingSession) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerWithExistingSession) IsAuthenticated(r *http.Request) bool {
	return false
}

func (m *mockSessionManagerWithExistingSession) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return nil
}

// mockSessionManagerNewSessionInvalidCSRF allows login when CSRF fails but session is new.
type mockSessionManagerNewSessionInvalidCSRF struct{}

func (m *mockSessionManagerNewSessionInvalidCSRF) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerNewSessionInvalidCSRF) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return "test-csrf-token"
}

func (m *mockSessionManagerNewSessionInvalidCSRF) ValidateCSRFToken(r *http.Request) bool {
	return false
}

func (m *mockSessionManagerNewSessionInvalidCSRF) ClearSession(w http.ResponseWriter, r *http.Request) {
}

func (m *mockSessionManagerNewSessionInvalidCSRF) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	sess := sessions.NewSession(nil, session.SessionName)
	sess.IsNew = true
	return sess, nil
}

func (m *mockSessionManagerNewSessionInvalidCSRF) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerNewSessionInvalidCSRF) IsAuthenticated(r *http.Request) bool {
	return false
}

func (m *mockSessionManagerNewSessionInvalidCSRF) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return nil
}

// mockSessionManagerExistingSessionNoToken simulates CSRF failure with an existing session missing a token.
type mockSessionManagerExistingSessionNoToken struct{}

func (m *mockSessionManagerExistingSessionNoToken) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerExistingSessionNoToken) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return "test-csrf-token"
}

func (m *mockSessionManagerExistingSessionNoToken) ValidateCSRFToken(r *http.Request) bool {
	return false
}

func (m *mockSessionManagerExistingSessionNoToken) ClearSession(w http.ResponseWriter, r *http.Request) {
}

func (m *mockSessionManagerExistingSessionNoToken) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	sess := sessions.NewSession(nil, session.SessionName)
	sess.IsNew = false
	return sess, nil
}

func (m *mockSessionManagerExistingSessionNoToken) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerExistingSessionNoToken) IsAuthenticated(r *http.Request) bool {
	return false
}

func (m *mockSessionManagerExistingSessionNoToken) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return nil
}

// mockSessionManagerWithCSRFValid implements SessionManager where CSRF validation passes.
type mockSessionManagerWithCSRFValid struct{}

func (m *mockSessionManagerWithCSRFValid) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerWithCSRFValid) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return "test-csrf-token"
}

func (m *mockSessionManagerWithCSRFValid) ValidateCSRFToken(r *http.Request) bool {
	return true // CSRF validation passes
}

func (m *mockSessionManagerWithCSRFValid) ClearSession(w http.ResponseWriter, r *http.Request) {}

func (m *mockSessionManagerWithCSRFValid) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	return sessions.NewSession(nil, session.SessionName), nil
}

func (m *mockSessionManagerWithCSRFValid) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerWithCSRFValid) IsAuthenticated(r *http.Request) bool {
	return true
}

func (m *mockSessionManagerWithCSRFValid) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return nil
}

// mockSessionManagerAuthenticatedCSRFFailed implements SessionManager for testing
// an authenticated session where CSRF validation fails (for logout CSRF tests).
type mockSessionManagerAuthenticatedCSRFFailed struct{}

func (m *mockSessionManagerAuthenticatedCSRFFailed) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerAuthenticatedCSRFFailed) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return "test-csrf-token"
}

func (m *mockSessionManagerAuthenticatedCSRFFailed) ValidateCSRFToken(r *http.Request) bool {
	return false // CSRF validation fails
}

func (m *mockSessionManagerAuthenticatedCSRFFailed) ClearSession(w http.ResponseWriter, r *http.Request) {
}

func (m *mockSessionManagerAuthenticatedCSRFFailed) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	return sessions.NewSession(nil, session.SessionName), nil
}

func (m *mockSessionManagerAuthenticatedCSRFFailed) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerAuthenticatedCSRFFailed) IsAuthenticated(r *http.Request) bool {
	return true // Authenticated
}

func (m *mockSessionManagerAuthenticatedCSRFFailed) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return nil
}

// errorResponseWriter simulates a writer that fails on Write.
// Used by tests across the handlers package for render-error paths.
type errorResponseWriter struct {
	header http.Header
	status int
}

func (w *errorResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *errorResponseWriter) WriteHeader(status int) {
	w.status = status
}

func (w *errorResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return 0, errors.New("write error")
}

// mockSessionManagerWithError simulates session errors.
type mockSessionManagerWithError struct{}

func (m *mockSessionManagerWithError) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerWithError) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return "test-csrf-token"
}

func (m *mockSessionManagerWithError) ValidateCSRFToken(r *http.Request) bool {
	return true
}

func (m *mockSessionManagerWithError) ClearSession(w http.ResponseWriter, r *http.Request) {
}

func (m *mockSessionManagerWithError) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	return sessions.NewSession(nil, session.SessionName), nil
}

func (m *mockSessionManagerWithError) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerWithError) IsAuthenticated(r *http.Request) bool {
	return false
}

func (m *mockSessionManagerWithError) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return errors.New("session error")
}

// --- Test cases ---

func TestNewAuthHandlers(t *testing.T) {
	authSvc := &mockAuthService{}
	sm := &mockSessionManagerAuth{}

	h := NewAuthHandlers(authSvc, sm)

	if h == nil {
		t.Fatal("NewAuthHandlers returned nil")
	}
	if h.AuthService != authSvc {
		t.Error("AuthService not set correctly")
	}
	if h.SessionManager == nil {
		t.Error("SessionManager not set correctly")
	}
}

func TestAuthHandlers_Login_Success(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	authSvc := &mockAuthService{
		checkLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		authenticateFunc: func(ctx context.Context, username, password string) (*session.User, error) {
			// Return success for any credentials (mock)
			return &session.User{Username: username, Password: "hashedpassword"}, nil
		},
	}

	authHandlers := NewAuthHandlers(authSvc, &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=testuser&password=anypassword"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	// Should return OK since mock auth succeeds
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Should have HX-Trigger header for successful login
	if w.Header().Get("HX-Trigger") != "auth-changed" {
		t.Errorf("expected HX-Trigger 'auth-changed', got '%s'", w.Header().Get("HX-Trigger"))
	}
}

func TestAuthHandlers_Login_InvalidCredentials(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	authSvc := &mockAuthService{
		checkLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		authenticateFunc: func(ctx context.Context, username, password string) (*session.User, error) {
			return nil, auth.ErrInvalidCredentials
		},
	}

	authHandlers := NewAuthHandlers(authSvc, &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=testuser&password=wrongpassword"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	// Should return 200 with error message in body
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	errorMsg := testutil.FindElementByID(doc, "login-error-message")
	if errorMsg == nil {
		t.Fatal("missing #login-error-message")
	}
	if got := testutil.GetTextContent(errorMsg); got != "Invalid credentials" {
		t.Errorf("error message = %q, want %q", got, "Invalid credentials")
	}
}

func TestAuthHandlers_Login_AccountLocked(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	authSvc := &mockAuthService{
		checkLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return true, nil
		},
	}

	authHandlers := NewAuthHandlers(authSvc, &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=lockeduser&password=anypassword"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	// Should return 200 with lockout message in body
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	errorMsg := testutil.FindElementByID(doc, "login-error-message")
	if errorMsg == nil {
		t.Fatal("missing #login-error-message")
	}
	if got := testutil.GetTextContent(errorMsg); got != "Account locked. Please try again later." {
		t.Errorf("error message = %q, want %q", got, "Account locked. Please try again later.")
	}
}

func TestAuthHandlers_Login_InvalidCSRF(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	authSvc := &mockAuthService{}
	// Use a session manager that rejects CSRF on existing session
	authHandlers := NewAuthHandlers(authSvc, &mockSessionManagerWithExistingSession{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=testuser&password=password"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	// Should return 403 Forbidden
	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthHandlers_Logout(t *testing.T) {
	authSvc := &mockAuthService{}
	authHandlers := NewAuthHandlers(authSvc, &mockSessionManagerWithCSRFValid{})

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	w := httptest.NewRecorder()

	authHandlers.Logout(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Should have HX-Trigger header for auth-changed event
	if w.Header().Get("HX-Trigger") != "auth-changed" {
		t.Errorf("expected HX-Trigger 'auth-changed', got '%s'", w.Header().Get("HX-Trigger"))
	}
}

func TestAuthHandlers_Logout_CSRFFailed(t *testing.T) {
	authSvc := &mockAuthService{}
	// Use a mock that returns false for ValidateCSRFToken (authenticated session with invalid CSRF)
	authHandlers := NewAuthHandlers(authSvc, &mockSessionManagerAuthenticatedCSRFFailed{})

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	w := httptest.NewRecorder()

	authHandlers.Logout(w, req)

	// Should return 403 Forbidden on CSRF failure
	if w.Code != http.StatusForbidden {
		t.Errorf("expected status %d, got %d: %s", http.StatusForbidden, w.Code, w.Body.String())
	}
}

func TestAuthHandlers_Logout_SessionError(t *testing.T) {
	authSvc := &mockAuthService{}
	// Use a mock that returns error on SetAuthenticated
	authHandlers := NewAuthHandlers(authSvc, &mockSessionManagerWithError{})

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	w := httptest.NewRecorder()

	authHandlers.Logout(w, req)

	// Should return 500 on error
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthHandlers_Login_InvalidCSRF_NewSessionAllowed(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	authSvc := &mockAuthService{
		checkLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		authenticateFunc: func(ctx context.Context, username, password string) (*session.User, error) {
			return nil, auth.ErrInvalidCredentials
		},
	}

	authHandlers := NewAuthHandlers(authSvc, &mockSessionManagerNewSessionInvalidCSRF{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=testuser&password=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	errorMsg := testutil.FindElementByID(doc, "login-error-message")
	if errorMsg == nil {
		t.Fatal("missing #login-error-message")
	}
	if got := testutil.GetTextContent(errorMsg); got != "Invalid credentials" {
		t.Errorf("error message = %q, want %q", got, "Invalid credentials")
	}
}

func TestAuthHandlers_Login_InvalidCSRF_ExistingSessionNoTokenAllowed(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	authSvc := &mockAuthService{
		checkLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		authenticateFunc: func(ctx context.Context, username, password string) (*session.User, error) {
			return nil, auth.ErrInvalidCredentials
		},
	}

	authHandlers := NewAuthHandlers(authSvc, &mockSessionManagerExistingSessionNoToken{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=testuser&password=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	errorMsg := testutil.FindElementByID(doc, "login-error-message")
	if errorMsg == nil {
		t.Fatal("missing #login-error-message")
	}
	if got := testutil.GetTextContent(errorMsg); got != "Invalid credentials" {
		t.Errorf("error message = %q, want %q", got, "Invalid credentials")
	}
}

func TestAuthHandlers_Login_CheckLockoutError(t *testing.T) {
	authSvc := &mockAuthService{
		checkLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, errors.New("lockout error")
		},
	}

	authHandlers := NewAuthHandlers(authSvc, &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=testuser&password=any"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestAuthHandlers_Login_AuthServiceError(t *testing.T) {
	authSvc := &mockAuthService{
		checkLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		authenticateFunc: func(ctx context.Context, username, password string) (*session.User, error) {
			return nil, errors.New("auth error")
		},
	}

	authHandlers := NewAuthHandlers(authSvc, &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=testuser&password=any"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestAuthHandlers_Login_SetAuthenticatedError(t *testing.T) {
	authSvc := &mockAuthService{
		checkLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		authenticateFunc: func(ctx context.Context, username, password string) (*session.User, error) {
			return &session.User{Username: username, Password: "hashed"}, nil
		},
	}

	authHandlers := NewAuthHandlers(authSvc, &mockSessionManagerWithError{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=testuser&password=any"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestAuthHandlers_LoginFormHandler(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	authHandlers := NewAuthHandlers(&mockAuthService{}, &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodGet, "/login-form", nil)
	w := httptest.NewRecorder()

	authHandlers.LoginFormHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-store, no-cache, must-revalidate")
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	formNode := testutil.FindElementByID(doc, "login-form")
	if formNode == nil {
		t.Fatal("missing #login-form")
	}
}

func TestAuthHandlers_LoginFormHandler_RenderError(t *testing.T) {
	authHandlers := NewAuthHandlers(&mockAuthService{}, &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodGet, "/login-form", nil)
	w := &errorResponseWriter{}

	authHandlers.LoginFormHandler(w, req)

	if w.status != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.status)
	}
}

func TestAuthHandlers_LogoutFormHandler(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	authHandlers := NewAuthHandlers(&mockAuthService{}, &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodGet, "/logout-form", nil)
	w := httptest.NewRecorder()

	authHandlers.LogoutFormHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-store, no-cache, must-revalidate")
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	formNode := testutil.FindElementByID(doc, "logout-form")
	if formNode == nil {
		t.Fatal("missing #logout-form")
	}
}

func TestAuthHandlers_LogoutFormHandler_RenderError(t *testing.T) {
	authHandlers := NewAuthHandlers(&mockAuthService{}, &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodGet, "/logout-form", nil)
	w := &errorResponseWriter{}

	authHandlers.LogoutFormHandler(w, req)

	if w.status != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.status)
	}
}
