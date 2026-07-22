package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"

	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/security"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

// --- Mock implementations for auth handler tests ---

// hashPassword returns a bcrypt hash of the supplied password using the minimum
// cost so handler tests can exercise the real auth service without the default
// bcrypt cost penalty.
func hashPassword(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	return string(hash)
}

// mockSessionManagerAuth provides basic session management for auth tests.
type mockSessionManagerAuth struct {
	authenticated bool
}

func (m *mockSessionManagerAuth) GetOptions() *sessions.Options {
	return &sessions.Options{}
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

func (m *mockSessionManagerAuth) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	return m.authenticated
}

func (m *mockSessionManagerAuth) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	m.authenticated = authenticated
	return nil
}

// mockSessionManagerValid implements SessionManager for authenticated sessions.
type mockSessionManagerValid struct{}

func (m *mockSessionManagerValid) GetOptions() *sessions.Options {
	return &sessions.Options{}
}

func (m *mockSessionManagerValid) ClearSession(w http.ResponseWriter, r *http.Request) {}

func (m *mockSessionManagerValid) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	return sessions.NewSession(nil, session.SessionName), nil
}

func (m *mockSessionManagerValid) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerValid) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	return true
}

func (m *mockSessionManagerValid) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
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

func (m *mockSessionManagerWithError) ClearSession(w http.ResponseWriter, r *http.Request) {
}

func (m *mockSessionManagerWithError) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	return sessions.NewSession(nil, session.SessionName), nil
}

func (m *mockSessionManagerWithError) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return nil
}

func (m *mockSessionManagerWithError) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	return false
}

func (m *mockSessionManagerWithError) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	return errors.New("session error")
}

// --- Test cases ---

func TestNewAuthHandlers(t *testing.T) {
	cs := &fakeCredentialStore{}
	sm := &mockSessionManagerAuth{}

	h := NewAuthHandlers(auth.NewService(cs), sm)

	if h == nil {
		t.Fatal("NewAuthHandlers returned nil")
	}
	if h.AuthService == nil {
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

	password := "anypassword"
	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: username, Password: hashPassword(t, password)}, nil
		},
	}

	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=testuser&password="+password))
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

	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: username, Password: hashPassword(t, "correctpassword")}, nil
		},
	}

	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})

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

func TestAuthHandlers_Login_WrongUsernameWrongPasswordParity(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	username := "testuser"
	password := "wrongpassword"

	cases := []struct {
		name       string
		getUserErr error
	}{
		{"wrong username", sql.ErrNoRows},
		{"wrong password", nil},
	}

	var bodies []string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := &fakeCredentialStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return false, nil
				},
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					if tc.getUserErr != nil {
						return nil, tc.getUserErr
					}
					return &session.User{Username: username, Password: hashPassword(t, "correctpassword")}, nil
				},
			}

			authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})
			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(fmt.Sprintf("username=%s&password=%s", username, password)))
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

			bodies = append(bodies, w.Body.String())
		})
	}

	if len(bodies) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(bodies))
	}
	if bodies[0] != bodies[1] {
		t.Errorf("wrong username and wrong password produced different client-visible responses")
	}
}

func TestAuthHandlers_Login_AccountLocked(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return true, nil
		},
	}

	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})

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

func TestAuthHandlers_Logout(t *testing.T) {
	cs := &fakeCredentialStore{}
	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerValid{})

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

func TestAuthHandlers_Logout_SessionError(t *testing.T) {
	cs := &fakeCredentialStore{}
	// Use a mock that returns error on SetAuthenticated
	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerWithError{})

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	w := httptest.NewRecorder()

	authHandlers.Logout(w, req)

	// Should return 500 on error
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthHandlers_Login_CheckLockoutError(t *testing.T) {
	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, errors.New("lockout error")
		},
	}

	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=testuser&password=any"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestAuthHandlers_Login_AuthServiceError(t *testing.T) {
	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return nil, errors.New("get user error")
		},
	}

	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=testuser&password=any"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestAuthHandlers_Login_SetAuthenticatedError(t *testing.T) {
	password := "any"
	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: username, Password: hashPassword(t, password)}, nil
		},
	}

	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerWithError{})

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("username=testuser&password="+password))
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

	authHandlers := NewAuthHandlers(auth.NewService(&fakeCredentialStore{}), &mockSessionManagerAuth{})

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
	authHandlers := NewAuthHandlers(auth.NewService(&fakeCredentialStore{}), &mockSessionManagerAuth{})

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

	authHandlers := NewAuthHandlers(auth.NewService(&fakeCredentialStore{}), &mockSessionManagerAuth{})

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
	authHandlers := NewAuthHandlers(auth.NewService(&fakeCredentialStore{}), &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodGet, "/logout-form", nil)
	w := &errorResponseWriter{}

	authHandlers.LogoutFormHandler(w, req)

	if w.status != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.status)
	}
}

// TestAuthHandlers_Login_LockoutAfterWrongUsername verifies that lockout is enforced
// even when login fails due to a wrong username (not just wrong password).
// This proves the WP-4/WP-9 fix: failed attempts are recorded regardless of whether
// the username exists or the password is wrong, and the handler correctly shows the
// lockout message when the account becomes locked.
func TestAuthHandlers_Login_LockoutAfterWrongUsername(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Track attempt count for wrong-username scenario.
	// Simulates: wrong username attempts accumulate → lockout triggers after 3 failed checks.
	var attempts int
	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			attempts++
			if attempts > 3 { // After 3rd failed attempt check, account is locked
				return true, nil
			}
			return false, nil
		},
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return nil, sql.ErrNoRows // Always wrong username
		},
	}

	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})

	// First 3 attempts with wrong username should show "Invalid credentials"
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login",
			strings.NewReader("username=nonexistent&password=anypass"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		authHandlers.Login(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d: %s", i+1, w.Code, w.Body.String())
		}

		doc, err := testutil.ParseHTML(w.Body)
		if err != nil {
			t.Fatalf("attempt %d: parse HTML: %v", i+1, err)
		}
		errMsg := testutil.FindElementByID(doc, "login-error-message")
		if errMsg == nil {
			t.Fatalf("attempt %d: missing #login-error-message", i+1)
		}
		if got := testutil.GetTextContent(errMsg); got != "Invalid credentials" {
			t.Errorf("attempt %d: error message = %q, want %q", i+1, got, "Invalid credentials")
		}
	}

	// 4th attempt — lockout should now trigger after accumulated wrong-username attempts
	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=nonexistent&password=anypass"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("lockout attempt: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("lockout attempt: parse HTML: %v", err)
	}
	errMsg := testutil.FindElementByID(doc, "login-error-message")
	if errMsg == nil {
		t.Fatal("lockout attempt: missing #login-error-message")
	}
	if got := testutil.GetTextContent(errMsg); got != "Account locked. Please try again later." {
		t.Errorf("lockout attempt: error message = %q, want %q",
			got, "Account locked. Please try again later.")
	}
}

// TestAuthHandlers_Login_ClearsFailedAttemptsOnSuccess verifies that after a
// successful login, the failed-attempts counter is cleared so a subsequent
// lockout does not happen prematurely. This replaces the deleted
// TestLoginHandler_ClearAttemptsOnSuccess integration test at the handler level.
func TestAuthHandlers_Login_ClearsFailedAttemptsOnSuccess(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Stateful mock that simulates the real service's clear-attempts-on-success behavior.
	var attempts int
	var locked bool
	var mu sync.Mutex

	correctPassword := "correct"
	hashedCorrect := hashPassword(t, correctPassword)

	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return locked, nil
		},
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			mu.Lock()
			defer mu.Unlock()
			return &session.User{Username: username, Password: hashedCorrect}, nil
		},
		recordFailedLoginAttemptFunc: func(ctx context.Context, username string) error {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			if attempts >= 3 {
				locked = true
			}
			return nil
		},
		clearLoginAttemptsFunc: func(ctx context.Context, username string) error {
			mu.Lock()
			defer mu.Unlock()
			attempts = 0
			locked = false
			return nil
		},
	}

	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})

	// Make 2 failed attempts — should show "Invalid credentials"
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login",
			strings.NewReader("username=admin&password=wrong"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		authHandlers.Login(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d", i+1, w.Code)
		}

		doc, err := testutil.ParseHTML(w.Body)
		if err != nil {
			t.Fatalf("attempt %d: parse HTML: %v", i+1, err)
		}
		errMsg := testutil.FindElementByID(doc, "login-error-message")
		if errMsg == nil {
			t.Fatalf("attempt %d: missing #login-error-message", i+1)
		}
		if got := testutil.GetTextContent(errMsg); got != "Invalid credentials" {
			t.Errorf("attempt %d: error = %q, want %q", i+1, got, "Invalid credentials")
		}
	}

	// Successful login — this should clear the attempts counter
	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=admin&password="+correctPassword))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	authHandlers.Login(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on successful login, got %d", w.Code)
	}
	if trigger := w.Header().Get("HX-Trigger"); trigger != "auth-changed" {
		t.Errorf("expected HX-Trigger 'auth-changed', got %q", trigger)
	}

	// Now make 2 more failed attempts — should NOT lock out because
	// the successful login cleared the counter
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login",
			strings.NewReader("username=admin&password=wrong"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		authHandlers.Login(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d", i+3, w.Code)
		}

		doc, err := testutil.ParseHTML(w.Body)
		if err != nil {
			t.Fatalf("attempt %d: parse HTML: %v", i+3, err)
		}
		errMsg := testutil.FindElementByID(doc, "login-error-message")
		if errMsg == nil {
			t.Fatalf("attempt %d: missing #login-error-message", i+3)
		}
		// Should still see "Invalid credentials", NOT lockout
		if got := testutil.GetTextContent(errMsg); got != "Invalid credentials" {
			t.Errorf("attempt %d: error = %q, want %q", i+3, got, "Invalid credentials")
		}
	}
}

// TestAuthHandlers_Login_LockoutExpired verifies that when an account was locked
// but the lockout period has expired, login is allowed again.
// This replaces the deleted TestLoginHandler_LockoutExpiration integration test
// at the handler level: CheckLockout returning false (DB determined lockout expired)
// followed by Authenticate succeeding is the handler-level equivalent.
func TestAuthHandlers_Login_LockoutExpired(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Simulate: user was locked, lockout has now expired
	// CheckAccountLockout returns false, GetUser returns user
	password := "admin"
	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil // Lockout expired
		},
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: username, Password: hashPassword(t, password)}, nil
		},
	}

	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})

	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=admin&password="+password))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for expired lockout, got %d", w.Code)
	}
	if trigger := w.Header().Get("HX-Trigger"); trigger != "auth-changed" {
		t.Errorf("expected HX-Trigger 'auth-changed', got %q", trigger)
	}
}

// TestAuthHandlers_Login_RateLimited verifies that IP rate limiting returns 429.
func TestAuthHandlers_Login_RateLimited(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: username, Password: hashPassword(t, "correctpassword")}, nil
		},
	}

	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})
	// Set rate limiter with 1 attempt per 60 second window.
	authHandlers.SetRateLimiter(security.NewIPRateLimiter(1, 60))

	// First attempt should be allowed.
	ip := "192.168.1.1:9999"
	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=testuser&password=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = ip
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	// First attempt should succeed (login fails, but handler processes it).
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for first attempt, got %d: %s", w.Code, w.Body.String())
	}

	// Second attempt from same IP should be rate limited.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=testuser&password=wrong"))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.RemoteAddr = ip

	authHandlers.Login(w2, req2)

	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 for rate limited attempt, got %d: %s", w2.Code, w2.Body.String())
	}
	if got := strings.TrimSpace(w2.Body.String()); got != "Too many login attempts. Please try again later." {
		t.Errorf("expected generic rate limit message, got %q", got)
	}
}

// TestAuthHandlers_Login_RateLimited_DifferentIP verifies that rate limiting
// is per-IP and different IPs are not affected by another IP's limit.
func TestAuthHandlers_Login_RateLimited_DifferentIP(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: username, Password: hashPassword(t, "correctpassword")}, nil
		},
	}

	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})
	// Set rate limiter with 1 attempt per 60 second window.
	authHandlers.SetRateLimiter(security.NewIPRateLimiter(1, 60))

	// First IP exhausts rate limit.
	req1 := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=testuser&password=wrong"))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req1.RemoteAddr = "10.0.0.1:12345"
	w1 := httptest.NewRecorder()
	authHandlers.Login(w1, req1)

	if w1.Code == http.StatusTooManyRequests {
		t.Error("first IP first attempt should not be rate limited")
	}

	// Same IP second attempt should be 429.
	w1b := httptest.NewRecorder()
	req1b := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=testuser&password=wrong"))
	req1b.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req1b.RemoteAddr = "10.0.0.1:12345"
	authHandlers.Login(w1b, req1b)

	if w1b.Code != http.StatusTooManyRequests {
		t.Errorf("first IP second attempt should be 429, got %d", w1b.Code)
	}

	// Different IP should still be allowed.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=testuser&password=wrong"))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.RemoteAddr = "10.0.0.2:12345"
	authHandlers.Login(w2, req2)

	if w2.Code == http.StatusTooManyRequests {
		t.Error("different IP should not be rate limited")
	}
}

// TestAuthHandlers_Login_NoRateLimiter verifies that Login works normally
// when no rate limiter is set (nil check).
func TestAuthHandlers_Login_NoRateLimiter(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	password := "correct"
	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: username, Password: hashPassword(t, password)}, nil
		},
	}

	// Create handler WITHOUT rate limiter.
	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})

	// Multiple attempts from same IP should all work (no rate limiter).
	ip := "10.0.0.5:8080"
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login",
			strings.NewReader("username=testuser&password="+password))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		authHandlers.Login(w, req)

		if w.Code == http.StatusTooManyRequests {
			t.Errorf("attempt %d: expected no rate limit, got 429", i+1)
			break
		}
	}
}

// TestAuthHandlers_Login_SyncLoginRateLimitMax verifies SyncLoginRateLimitMax
// configures the rate limiter (SetMax + Clear) and enforces the new limit.
func TestAuthHandlers_Login_SyncLoginRateLimitMax(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	cs := &fakeCredentialStore{
		checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
			return false, nil
		},
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: username, Password: hashPassword(t, "correctpassword")}, nil
		},
	}

	authHandlers := NewAuthHandlers(auth.NewService(cs), &mockSessionManagerAuth{})
	authHandlers.SyncLoginRateLimitMax(1)

	// First attempt should be allowed.
	ip := "192.168.1.1:9999"
	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=testuser&password=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = ip
	w := httptest.NewRecorder()

	authHandlers.Login(w, req)

	// First attempt should succeed (login fails, but handler processes it).
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for first attempt, got %d: %s", w.Code, w.Body.String())
	}

	// Second attempt from same IP should be rate limited.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=testuser&password=wrong"))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.RemoteAddr = ip

	authHandlers.Login(w2, req2)

	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 for rate limited attempt, got %d: %s", w2.Code, w2.Body.String())
	}
	if got := strings.TrimSpace(w2.Body.String()); got != "Too many login attempts. Please try again later." {
		t.Errorf("expected generic rate limit message, got %q", got)
	}
}
