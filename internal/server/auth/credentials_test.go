package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/session"
)

// mockCredentialStore implements CredentialStore for testing.
type mockCredentialStore struct {
	mockUserStore
	updateUsernameFunc func(ctx context.Context, username string) error
	updatePasswordFunc func(ctx context.Context, passwordHash string) error
	getUserFunc        func(ctx context.Context, username string) (*session.User, error)
}

func (m *mockCredentialStore) UpdateUsername(ctx context.Context, username string) error {
	if m.updateUsernameFunc != nil {
		return m.updateUsernameFunc(ctx, username)
	}
	return nil
}

func (m *mockCredentialStore) UpdatePassword(ctx context.Context, passwordHash string) error {
	if m.updatePasswordFunc != nil {
		return m.updatePasswordFunc(ctx, passwordHash)
	}
	return nil
}

func (m *mockCredentialStore) GetUser(ctx context.Context, username string) (*session.User, error) {
	if m.getUserFunc != nil {
		return m.getUserFunc(ctx, username)
	}
	return &session.User{Username: "admin", Password: "hashedpassword"}, nil
}

func TestService_UpdateCredentials_NoChanges(t *testing.T) {
	ctx := context.Background()
	store := &mockCredentialStore{}
	svc := NewService(store)

	opts := CredentialUpdateOptions{
		CurrentUsername: "admin",
		NewUsername:     "admin", // Same as current
		CurrentPassword: "",
		NewPassword:     "",
		ConfirmPassword: "",
	}

	result, err := svc.UpdateCredentials(ctx, opts, store)

	if err != nil {
		t.Errorf("UpdateCredentials() error = %v", err)
	}
	if result.ChangingUsername {
		t.Error("expected ChangingUsername to be false")
	}
	if result.ChangingPassword {
		t.Error("expected ChangingPassword to be false")
	}
	if len(result.ValidationErrors) > 0 {
		t.Errorf("expected no validation errors, got %v", result.ValidationErrors)
	}
}

func TestService_UpdateCredentials_ChangeUsername(t *testing.T) {
	ctx := context.Background()

	// Save and restore originals
	origVerifyPassword := verifyPassword
	defer func() {
		verifyPassword = origVerifyPassword
	}()
	verifyPassword = func(hashedPassword, plaintextPassword string) error { return nil }

	store := &mockCredentialStore{
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: "admin", Password: "hashedpassword"}, nil
		},
		updateUsernameFunc: func(ctx context.Context, username string) error {
			return nil
		},
	}
	svc := NewService(store)

	opts := CredentialUpdateOptions{
		CurrentUsername: "admin",
		NewUsername:     "newadmin88", // 10 chars
		CurrentPassword: "currentpass",
	}

	result, err := svc.UpdateCredentials(ctx, opts, store)

	if err != nil {
		t.Errorf("UpdateCredentials() error = %v", err)
	}
	if !result.ChangingUsername {
		t.Error("expected ChangingUsername to be true")
	}
	if len(result.ValidationErrors) > 0 {
		t.Errorf("expected no validation errors, got %v", result.ValidationErrors)
	}
}

func TestService_UpdateCredentials_MissingCurrentPassword(t *testing.T) {
	ctx := context.Background()
	store := &mockCredentialStore{}
	svc := NewService(store)

	opts := CredentialUpdateOptions{
		CurrentUsername: "admin",
		NewUsername:     "newadmin",
		CurrentPassword: "", // Missing
		NewPassword:     "",
		ConfirmPassword: "",
	}

	result, err := svc.UpdateCredentials(ctx, opts, store)

	if err != nil {
		t.Errorf("UpdateCredentials() error = %v", err)
	}
	if result.ValidationErrors["admin_current_password"] == "" {
		t.Error("expected validation error for missing current password")
	}
}

func TestService_UpdateCredentials_InvalidCurrentPassword(t *testing.T) {
	ctx := context.Background()

	// Save and restore original verifyPassword
	origVerify := verifyPassword
	defer func() { verifyPassword = origVerify }()
	verifyPassword = func(hashedPassword, plaintextPassword string) error {
		return errors.New("invalid password")
	}

	store := &mockCredentialStore{
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: "admin", Password: "hashedpassword"}, nil
		},
	}
	svc := NewService(store)

	opts := CredentialUpdateOptions{
		CurrentUsername: "admin",
		NewUsername:     "newadmin",
		CurrentPassword: "wrongpassword",
	}

	result, err := svc.UpdateCredentials(ctx, opts, store)

	if err != nil {
		t.Errorf("UpdateCredentials() error = %v", err)
	}
	if result.ValidationErrors["admin_current_password"] == "" {
		t.Error("expected validation error for incorrect current password")
	}
}

func TestService_UpdateCredentials_PasswordMismatch(t *testing.T) {
	ctx := context.Background()

	// Save and restore originals
	origVerify := verifyPassword
	defer func() {
		verifyPassword = origVerify
	}()
	verifyPassword = func(hashedPassword, plaintextPassword string) error { return nil }

	store := &mockCredentialStore{
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: "admin", Password: "hashedpassword"}, nil
		},
	}
	svc := NewService(store)

	opts := CredentialUpdateOptions{
		CurrentUsername: "admin",
		NewUsername:     "admin",
		CurrentPassword: "currentpass",
		NewPassword:     "NewPassword123!",
		ConfirmPassword: "differentpassword",
	}

	result, err := svc.UpdateCredentials(ctx, opts, store)

	if err != nil {
		t.Errorf("UpdateCredentials() error = %v", err)
	}
	if result.ValidationErrors["admin_confirm_password"] == "" {
		t.Error("expected validation error for password mismatch")
	}
}

func TestService_UpdateCredentials_UpdateUsernameError(t *testing.T) {
	ctx := context.Background()

	// Save and restore originals
	origVerify := verifyPassword
	defer func() {
		verifyPassword = origVerify
	}()
	verifyPassword = func(hashedPassword, plaintextPassword string) error { return nil }

	store := &mockCredentialStore{
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: "admin", Password: "hashedpassword"}, nil
		},
		updateUsernameFunc: func(ctx context.Context, username string) error {
			return errors.New("database error")
		},
	}
	svc := NewService(store)

	opts := CredentialUpdateOptions{
		CurrentUsername: "admin",
		NewUsername:     "newadmin",
		CurrentPassword: "currentpass",
	}

	_, err := svc.UpdateCredentials(ctx, opts, store)

	if err == nil {
		t.Error("expected error when UpdateUsername fails")
	}
}

func TestService_UpdateCredentials_UpdatePasswordError(t *testing.T) {
	ctx := context.Background()

	// Save and restore originals
	origVerify := verifyPassword
	defer func() {
		verifyPassword = origVerify
	}()
	verifyPassword = func(hashedPassword, plaintextPassword string) error { return nil }

	store := &mockCredentialStore{
		getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
			return &session.User{Username: "admin", Password: "hashedpassword"}, nil
		},
		updatePasswordFunc: func(ctx context.Context, passwordHash string) error {
			return errors.New("database error")
		},
	}
	svc := NewService(store)

	opts := CredentialUpdateOptions{
		CurrentUsername: "admin",
		NewUsername:     "admin",
		CurrentPassword: "currentpass",
		NewPassword:     "SecurePassword123!",
		ConfirmPassword: "SecurePassword123!",
	}

	_, err := svc.UpdateCredentials(ctx, opts, store)

	if err == nil {
		t.Error("expected error when UpdatePassword fails")
	}
}

func TestService_UpdateCredentials_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	// Save and restore package-level hooks
	origVerifyPassword := verifyPassword
	origGenerateFromPassword := generateFromPassword
	defer func() {
		verifyPassword = origVerifyPassword
		generateFromPassword = origGenerateFromPassword
	}()

	// Default verifyPassword to succeed for these tests
	verifyPassword = func(hashedPassword, plaintextPassword string) error { return nil }

	tests := []struct {
		name                     string
		opts                     CredentialUpdateOptions
		store                    *mockCredentialStore
		generateFromPassword     func(password []byte, cost int) ([]byte, error)
		wantErr                  bool
		wantErrContains          string
		wantValidationKey        string
		wantValidationContains   string
		wantChangingUsername     bool
		wantChangingPassword     bool
		wantUpdateUsernameCalled bool
		wantUpdatePasswordCalled bool
	}{
		{
			name: "empty CurrentUsername with GetUser admin returning error",
			opts: CredentialUpdateOptions{
				CurrentUsername: "",
				NewUsername:     "newadmin88",
				CurrentPassword: "currentpass",
			},
			store: &mockCredentialStore{
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return nil, errors.New("user lookup failed")
				},
			},
			wantValidationKey:      "admin_current_password",
			wantValidationContains: "incorrect",
			wantChangingUsername:   true,
		},
		{
			name: "empty CurrentUsername with GetUser admin returning user",
			opts: CredentialUpdateOptions{
				CurrentUsername: "",
				NewUsername:     "newadmin88",
				CurrentPassword: "currentpass",
			},
			store: &mockCredentialStore{
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return &session.User{Username: "admin", Password: "hashed"}, nil
				},
				updateUsernameFunc: func(ctx context.Context, username string) error {
					return nil
				},
			},
			wantChangingUsername:     true,
			wantUpdateUsernameCalled: true,
		},
		{
			name: "GetUser currentUsername error during current-password verification",
			opts: CredentialUpdateOptions{
				CurrentUsername: "admin",
				NewUsername:     "newadmin88",
				CurrentPassword: "currentpass",
			},
			store: &mockCredentialStore{
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return nil, errors.New("db read failed")
				},
			},
			wantValidationKey:      "admin_current_password",
			wantValidationContains: "incorrect",
			wantChangingUsername:   true,
		},
		{
			name: "invalid new username validation",
			opts: CredentialUpdateOptions{
				CurrentUsername: "admin",
				NewUsername:     "bad",
				CurrentPassword: "currentpass",
			},
			store: &mockCredentialStore{
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return &session.User{Username: "admin", Password: "hashed"}, nil
				},
			},
			wantValidationKey:    "admin_username",
			wantChangingUsername: true,
		},
		{
			name: "new password empty with confirm provided",
			opts: CredentialUpdateOptions{
				CurrentUsername: "admin",
				NewUsername:     "admin",
				CurrentPassword: "currentpass",
				NewPassword:     "",
				ConfirmPassword: "something",
			},
			store: &mockCredentialStore{
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return &session.User{Username: "admin", Password: "hashed"}, nil
				},
			},
			wantValidationKey:      "admin_new_password",
			wantValidationContains: "required",
			wantChangingPassword:   true,
		},
		{
			name: "confirm password empty with new password provided",
			opts: CredentialUpdateOptions{
				CurrentUsername: "admin",
				NewUsername:     "admin",
				CurrentPassword: "currentpass",
				NewPassword:     "NewPassword123!",
				ConfirmPassword: "",
			},
			store: &mockCredentialStore{
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return &session.User{Username: "admin", Password: "hashed"}, nil
				},
			},
			wantValidationKey:      "admin_confirm_password",
			wantValidationContains: "required",
			wantChangingPassword:   true,
		},
		{
			name: "new password fails validation",
			opts: CredentialUpdateOptions{
				CurrentUsername: "admin",
				NewUsername:     "admin",
				CurrentPassword: "currentpass",
				NewPassword:     "short1!",
				ConfirmPassword: "short1!",
			},
			store: &mockCredentialStore{
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return &session.User{Username: "admin", Password: "hashed"}, nil
				},
			},
			wantValidationKey:    "admin_new_password",
			wantChangingPassword: true,
		},
		{
			name: "generateFromPassword failure",
			opts: CredentialUpdateOptions{
				CurrentUsername: "admin",
				NewUsername:     "admin",
				CurrentPassword: "currentpass",
				NewPassword:     "SecurePassword123!",
				ConfirmPassword: "SecurePassword123!",
			},
			store: &mockCredentialStore{
				getUserFunc: func(ctx context.Context, username string) (*session.User, error) {
					return &session.User{Username: "admin", Password: "hashed"}, nil
				},
			},
			generateFromPassword: func(password []byte, cost int) ([]byte, error) {
				return nil, errors.New("hash failed")
			},
			wantErr:              true,
			wantErrContains:      "hash failed",
			wantValidationKey:    "_global",
			wantChangingPassword: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updateUsernameCalled := false
			updatePasswordCalled := false

			if tt.generateFromPassword != nil {
				generateFromPassword = tt.generateFromPassword
			} else {
				generateFromPassword = origGenerateFromPassword
			}

			store := tt.store
			if store.updateUsernameFunc != nil {
				originalUpdateUsername := store.updateUsernameFunc
				store.updateUsernameFunc = func(ctx context.Context, username string) error {
					updateUsernameCalled = true
					return originalUpdateUsername(ctx, username)
				}
			} else {
				store.updateUsernameFunc = func(ctx context.Context, username string) error {
					updateUsernameCalled = true
					return nil
				}
			}

			if store.updatePasswordFunc != nil {
				originalUpdatePassword := store.updatePasswordFunc
				store.updatePasswordFunc = func(ctx context.Context, passwordHash string) error {
					updatePasswordCalled = true
					return originalUpdatePassword(ctx, passwordHash)
				}
			} else {
				store.updatePasswordFunc = func(ctx context.Context, passwordHash string) error {
					updatePasswordCalled = true
					return nil
				}
			}

			svc := NewService(store)
			result, err := svc.UpdateCredentials(ctx, tt.opts, store)

			if tt.wantErr && err == nil {
				t.Errorf("UpdateCredentials() error = nil, want error")
			}
			if tt.wantErr && err != nil && tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
				t.Errorf("UpdateCredentials() error = %v, want error containing %q", err, tt.wantErrContains)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("UpdateCredentials() unexpected error = %v", err)
			}

			if tt.wantValidationKey != "" {
				got := result.ValidationErrors[tt.wantValidationKey]
				if got == "" {
					t.Errorf("expected validation error for key %q, got none", tt.wantValidationKey)
				}
				if tt.wantValidationContains != "" && !strings.Contains(got, tt.wantValidationContains) {
					t.Errorf("validation error for key %q = %q, want containing %q", tt.wantValidationKey, got, tt.wantValidationContains)
				}
			}

			if result.ChangingUsername != tt.wantChangingUsername {
				t.Errorf("ChangingUsername = %v, want %v", result.ChangingUsername, tt.wantChangingUsername)
			}
			if result.ChangingPassword != tt.wantChangingPassword {
				t.Errorf("ChangingPassword = %v, want %v", result.ChangingPassword, tt.wantChangingPassword)
			}
			if updateUsernameCalled != tt.wantUpdateUsernameCalled {
				t.Errorf("UpdateUsername called = %v, want %v", updateUsernameCalled, tt.wantUpdateUsernameCalled)
			}
			if updatePasswordCalled != tt.wantUpdatePasswordCalled {
				t.Errorf("UpdatePassword called = %v, want %v", updatePasswordCalled, tt.wantUpdatePasswordCalled)
			}
		})
	}
}
