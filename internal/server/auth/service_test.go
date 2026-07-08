package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/lbe/sfpg-go/internal/server/session"
)

// mockUserStore implements UserStore for testing.
type mockUserStore struct {
	checkAccountLockoutFunc func(ctx context.Context, username string) (bool, error)
	getUserFunc             func(ctx context.Context, username string) (*session.User, error)
	recordFailedAttemptFunc func(ctx context.Context, username string) error
	clearLoginAttemptsFunc  func(ctx context.Context, username string) error
}

func (m *mockUserStore) CheckAccountLockout(ctx context.Context, username string) (bool, error) {
	if m.checkAccountLockoutFunc != nil {
		return m.checkAccountLockoutFunc(ctx, username)
	}
	return false, nil
}

func (m *mockUserStore) GetUser(ctx context.Context, username string) (*session.User, error) {
	if m.getUserFunc != nil {
		return m.getUserFunc(ctx, username)
	}
	return nil, ErrUserNotFound
}

func (m *mockUserStore) RecordFailedLoginAttempt(ctx context.Context, username string) error {
	if m.recordFailedAttemptFunc != nil {
		return m.recordFailedAttemptFunc(ctx, username)
	}
	return nil
}

func (m *mockUserStore) ClearLoginAttempts(ctx context.Context, username string) error {
	if m.clearLoginAttemptsFunc != nil {
		return m.clearLoginAttemptsFunc(ctx, username)
	}
	return nil
}

func TestNewService(t *testing.T) {
	store := &mockUserStore{}
	svc := NewService(store)

	if svc == nil {
		t.Fatal("NewService returned nil")
	}

	// Verify it implements AuthService
	//nolint:staticcheck // Intentional compile-time type assertion
	var _ AuthService = svc
}

func TestService_Authenticate(t *testing.T) {
	ctx := context.Background()
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("correctpassword"), bcrypt.DefaultCost)

	tests := []struct {
		name       string
		store      *mockUserStore
		username   string
		password   string
		wantErr    error
		wantUser   bool
		verifyMock func(t *testing.T, store *mockUserStore)
	}{
		{
			name: "successful authentication",
			store: &mockUserStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return false, nil
				},
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return &session.User{Username: "testuser", Password: string(hashedPassword)}, nil
				},
				clearLoginAttemptsFunc: func(ctx context.Context, username string) error {
					return nil
				},
			},
			username: "testuser",
			password: "correctpassword",
			wantErr:  nil,
			wantUser: true,
		},
		{
			name: "account locked",
			store: &mockUserStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return true, nil
				},
			},
			username: "lockeduser",
			password: "anypassword",
			wantErr:  ErrAccountLocked,
			wantUser: false,
		},
		{
			name: "user not found",
			store: &mockUserStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return false, nil
				},
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return nil, ErrUserNotFound
				},
				recordFailedAttemptFunc: func(ctx context.Context, username string) error {
					return nil
				},
			},
			username: "nonexistent",
			password: "anypassword",
			wantErr:  ErrInvalidCredentials,
			wantUser: false,
		},
	}

	// Save original verifyPassword and restore after tests
	originalVerify := verifyPassword
	defer func() { verifyPassword = originalVerify }()

	// Set up verifyPassword to use bcrypt for these tests
	verifyPassword = func(hashedPassword, plaintextPassword string) error {
		return bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(plaintextPassword))
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(tt.store)
			user, err := svc.Authenticate(ctx, tt.username, tt.password)

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Authenticate() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantUser && user == nil {
				t.Error("Authenticate() returned nil user, expected non-nil")
			}
			if !tt.wantUser && user != nil {
				t.Error("Authenticate() returned non-nil user, expected nil")
			}
		})
	}
}

func TestService_CheckLockout(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		store    *mockUserStore
		username string
		want     bool
		wantErr  error
	}{
		{
			name: "account not locked",
			store: &mockUserStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return false, nil
				},
			},
			username: "testuser",
			want:     false,
			wantErr:  nil,
		},
		{
			name: "account locked",
			store: &mockUserStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return true, nil
				},
			},
			username: "lockeduser",
			want:     true,
			wantErr:  nil,
		},
		{
			name: "store error",
			store: &mockUserStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return false, errors.New("database error")
				},
			},
			username: "testuser",
			want:     false,
			wantErr:  errors.New("database error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(tt.store)
			got, err := svc.CheckLockout(ctx, tt.username)

			if (err != nil) != (tt.wantErr != nil) {
				t.Errorf("CheckLockout() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("CheckLockout() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestService_RecordFailedAttempt(t *testing.T) {
	ctx := context.Background()
	called := false

	store := &mockUserStore{
		recordFailedAttemptFunc: func(ctx context.Context, username string) error {
			called = true
			return nil
		},
	}

	svc := NewService(store)
	err := svc.RecordFailedAttempt(ctx, "testuser")

	if err != nil {
		t.Errorf("RecordFailedAttempt() error = %v", err)
	}
	if !called {
		t.Error("RecordFailedAttempt() did not call store.RecordFailedLoginAttempt")
	}
}

func TestService_ClearAttempts(t *testing.T) {
	ctx := context.Background()
	called := false

	store := &mockUserStore{
		clearLoginAttemptsFunc: func(ctx context.Context, username string) error {
			called = true
			return nil
		},
	}

	svc := NewService(store)
	err := svc.ClearAttempts(ctx, "testuser")

	if err != nil {
		t.Errorf("ClearAttempts() error = %v", err)
	}
	if !called {
		t.Error("ClearAttempts() did not call store.ClearLoginAttempts")
	}
}

func TestService_Authenticate_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("correctpassword"), bcrypt.DefaultCost)

	// Save and restore package-level hooks
	originalVerify := verifyPassword
	originalGenerate := generateFromPassword
	defer func() {
		verifyPassword = originalVerify
		generateFromPassword = originalGenerate
	}()

	tests := []struct {
		name                   string
		store                  *mockUserStore
		username               string
		password               string
		verifyPassword         func(hashedPassword, plaintextPassword string) error
		wantErr                error
		wantErrContains        string
		wantUser               bool
		recordFailedCalled     *bool
		wantRecordFailedCalled bool
	}{
		{
			name: "CheckAccountLockout generic error",
			store: &mockUserStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return false, errors.New("lockout check failed")
				},
			},
			username:        "testuser",
			password:        "anypassword",
			wantErrContains: "lockout check failed",
			wantUser:        false,
		},
		{
			name: "GetUser generic error",
			store: &mockUserStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return false, nil
				},
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return nil, errors.New("db read failed")
				},
			},
			username:        "testuser",
			password:        "anypassword",
			wantErrContains: "db read failed",
			wantUser:        false,
		},
		{
			name: "user not found with failing RecordFailedLoginAttempt",
			store: &mockUserStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return false, nil
				},
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return nil, ErrUserNotFound
				},
				recordFailedAttemptFunc: func(ctx context.Context, username string) error {
					return errors.New("record failed")
				},
			},
			username:        "nonexistent",
			password:        "anypassword",
			wantErr:         ErrInvalidCredentials,
			wantErrContains: "record failed",
			wantUser:        false,
		},
		{
			name: "password mismatch with successful RecordFailedLoginAttempt",
			store: &mockUserStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return false, nil
				},
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return &session.User{Username: "testuser", Password: string(hashedPassword)}, nil
				},
				recordFailedAttemptFunc: func(ctx context.Context, username string) error {
					return nil
				},
			},
			username:               "testuser",
			password:               "wrongpassword",
			recordFailedCalled:     new(bool),
			wantRecordFailedCalled: true,
			verifyPassword: func(hashedPassword, plaintextPassword string) error {
				return errors.New("invalid password")
			},
			wantErr:  ErrInvalidCredentials,
			wantUser: false,
		},
		{
			name: "password mismatch with failing RecordFailedLoginAttempt",
			store: &mockUserStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return false, nil
				},
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return &session.User{Username: "testuser", Password: string(hashedPassword)}, nil
				},
				recordFailedAttemptFunc: func(ctx context.Context, username string) error {
					return errors.New("record failed")
				},
			},
			username: "testuser",
			password: "wrongpassword",
			verifyPassword: func(hashedPassword, plaintextPassword string) error {
				return errors.New("invalid password")
			},
			wantErr:         ErrInvalidCredentials,
			wantErrContains: "record failed",
			wantUser:        false,
		},
		{
			name: "ClearLoginAttempts failure on successful auth",
			store: &mockUserStore{
				checkAccountLockoutFunc: func(ctx context.Context, username string) (bool, error) {
					return false, nil
				},
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return &session.User{Username: "testuser", Password: string(hashedPassword)}, nil
				},
				clearLoginAttemptsFunc: func(ctx context.Context, username string) error {
					return errors.New("clear failed")
				},
			},
			username: "testuser",
			password: "correctpassword",
			verifyPassword: func(hashedPassword, plaintextPassword string) error {
				return nil
			},
			wantErrContains: "clear failed",
			wantUser:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.verifyPassword != nil {
				verifyPassword = tt.verifyPassword
			} else {
				verifyPassword = func(hashedPassword, plaintextPassword string) error {
					return bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(plaintextPassword))
				}
			}

			if tt.recordFailedCalled != nil {
				*tt.recordFailedCalled = false
				originalRecordFailed := tt.store.recordFailedAttemptFunc
				tt.store.recordFailedAttemptFunc = func(ctx context.Context, username string) error {
					*tt.recordFailedCalled = true
					return originalRecordFailed(ctx, username)
				}
			}

			svc := NewService(tt.store)
			user, err := svc.Authenticate(ctx, tt.username, tt.password)

			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("Authenticate() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErrContains != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrContains)) {
				t.Errorf("Authenticate() error = %v, want error containing %q", err, tt.wantErrContains)
			}

			if tt.wantUser && user == nil {
				t.Error("Authenticate() returned nil user, expected non-nil")
			}
			if !tt.wantUser && user != nil {
				t.Error("Authenticate() returned non-nil user, expected nil")
			}

			if tt.recordFailedCalled != nil && *tt.recordFailedCalled != tt.wantRecordFailedCalled {
				t.Errorf("RecordFailedLoginAttempt() called = %v, want %v", *tt.recordFailedCalled, tt.wantRecordFailedCalled)
			}
		})
	}
}
