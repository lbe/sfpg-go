package auth

import (
	"context"
	"errors"
	"testing"

	"go.local/sfpg/internal/server/session"
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
