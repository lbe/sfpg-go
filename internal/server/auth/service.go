// Package auth provides authentication services for the application.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/validation"
)

// Common authentication errors.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountLocked      = errors.New("account locked")
	ErrUserNotFound       = errors.New("user not found")
)

// UserStore abstracts user persistence operations needed for authentication.
type UserStore interface {
	// CheckAccountLockout returns true if the account is locked.
	CheckAccountLockout(ctx context.Context, username string) (locked bool, err error)
	// GetUser retrieves a user by username.
	GetUser(ctx context.Context, username string) (*session.User, error)
	// RecordFailedLoginAttempt records a failed login attempt.
	RecordFailedLoginAttempt(ctx context.Context, username string) error
	// ClearLoginAttempts clears failed login attempts for a user.
	ClearLoginAttempts(ctx context.Context, username string) error
}

// AuthService provides authentication operations.
type AuthService interface {
	// Authenticate validates credentials and returns the user if successful.
	// Returns ErrInvalidCredentials, ErrAccountLocked, or ErrUserNotFound on failure.
	Authenticate(ctx context.Context, username, password string) (*session.User, error)
	// CheckLockout returns true if the account is locked.
	CheckLockout(ctx context.Context, username string) (bool, error)
	// RecordFailedAttempt records a failed login attempt.
	RecordFailedAttempt(ctx context.Context, username string) error
	// ClearAttempts clears failed login attempts.
	ClearAttempts(ctx context.Context, username string) error
	// UpdateCredentials validates and updates admin credentials.
	UpdateCredentials(ctx context.Context, opts CredentialUpdateOptions, store CredentialStore) (*CredentialUpdateResult, error)
}

// authService implements AuthService.
type authService struct {
	store UserStore
}

// NewService creates a new AuthService with the given UserStore.
func NewService(store UserStore) AuthService {
	return &authService{store: store}
}

// Authenticate validates credentials and returns the user if successful.
func (s *authService) Authenticate(ctx context.Context, username, password string) (*session.User, error) {
	// Check lockout first
	locked, err := s.store.CheckAccountLockout(ctx, username)
	if err != nil {
		return nil, err
	}
	if locked {
		return nil, ErrAccountLocked
	}

	// Get user
	user, err := s.store.GetUser(ctx, username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) || errors.Is(err, sql.ErrNoRows) {
			// Unknown usernames are not persisted — lockout applies only to existing accounts.
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	// Validate password
	if err := verifyPassword(user.Password, password); err != nil {
		if recErr := s.store.RecordFailedLoginAttempt(ctx, username); recErr != nil {
			return nil, errors.Join(ErrInvalidCredentials, recErr)
		}
		return nil, ErrInvalidCredentials
	}

	// Success - clear failed attempts
	if clrErr := s.store.ClearLoginAttempts(ctx, username); clrErr != nil {
		return nil, clrErr
	}

	return user, nil
}

// CheckLockout returns true if the account is locked.
func (s *authService) CheckLockout(ctx context.Context, username string) (bool, error) {
	return s.store.CheckAccountLockout(ctx, username)
}

// RecordFailedAttempt records a failed login attempt.
func (s *authService) RecordFailedAttempt(ctx context.Context, username string) error {
	return s.store.RecordFailedLoginAttempt(ctx, username)
}

// ClearAttempts clears failed login attempts.
func (s *authService) ClearAttempts(ctx context.Context, username string) error {
	return s.store.ClearLoginAttempts(ctx, username)
}

var (
	// verifyPassword compares a hashed password with a plaintext password.
	// This is a variable to allow mocking in tests.
	verifyPassword = func(hashedPassword, plaintextPassword string) error {
		return bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(plaintextPassword))
	}

	// generateFromPassword is a testable hook for bcrypt.GenerateFromPassword.
	generateFromPassword = bcrypt.GenerateFromPassword
)

// CredentialStore extends UserStore with credential update operations.
type CredentialStore interface {
	UserStore
	// UpdateUsername updates the admin username.
	UpdateUsername(ctx context.Context, username string) error
	// UpdatePassword updates the admin password hash.
	UpdatePassword(ctx context.Context, passwordHash string) error
}

// CredentialUpdateOptions contains options for updating credentials.
type CredentialUpdateOptions struct {
	CurrentUsername string
	NewUsername     string
	CurrentPassword string
	NewPassword     string
	ConfirmPassword string
}

// CredentialUpdateResult contains the result of a credential update.
type CredentialUpdateResult struct {
	// ChangingUsername is true if the username is being changed.
	ChangingUsername bool
	// ChangingPassword is true if the password is being changed.
	ChangingPassword bool
	// ValidationErrors contains validation errors keyed by field name.
	ValidationErrors map[string]string
}

// UpdateCredentials validates and updates admin credentials.
// It returns a result containing what changes were made and any validation errors.
// Returns true if credentials were updated, false if only validation was performed.
func (s *authService) UpdateCredentials(ctx context.Context, opts CredentialUpdateOptions, store CredentialStore) (*CredentialUpdateResult, error) {
	result := &CredentialUpdateResult{
		ValidationErrors: make(map[string]string),
	}

	// Trim whitespace
	newUsername := strings.TrimSpace(opts.NewUsername)
	currentPassword := strings.TrimSpace(opts.CurrentPassword)
	newPassword := strings.TrimSpace(opts.NewPassword)
	confirmPassword := strings.TrimSpace(opts.ConfirmPassword)

	currentUsername := opts.CurrentUsername
	if currentUsername == "" {
		// Try to get current username from store
		user, err := store.GetUser(ctx, "admin") // Default admin username
		if err == nil && user != nil {
			currentUsername = user.Username
		}
	}

	result.ChangingUsername = newUsername != "" && newUsername != currentUsername
	result.ChangingPassword = newPassword != "" || confirmPassword != ""

	// If nothing is changing, return early
	if !result.ChangingUsername && !result.ChangingPassword {
		return result, nil
	}

	// Current password is required for any credential change
	if currentPassword == "" {
		result.ValidationErrors["admin_current_password"] = "Current password is required to change credentials"
		return result, nil
	}

	// Verify current password
	if currentPassword != "" {
		user, err := store.GetUser(ctx, currentUsername)
		if err != nil {
			result.ValidationErrors["admin_current_password"] = "Current password is incorrect"
		} else if err := verifyPassword(user.Password, currentPassword); err != nil {
			result.ValidationErrors["admin_current_password"] = "Current password is incorrect"
		}
	}

	// Validate new username
	if result.ChangingUsername && len(result.ValidationErrors) == 0 {
		if validateErr := validation.ValidateUsername(newUsername); validateErr != nil {
			result.ValidationErrors["admin_username"] = validateErr.Error()
		}
	}

	// Validate new password
	if result.ChangingPassword && len(result.ValidationErrors) == 0 {
		switch {
		case newPassword == "":
			result.ValidationErrors["admin_new_password"] = "New password is required when confirm password is provided"
		case confirmPassword == "":
			result.ValidationErrors["admin_confirm_password"] = "Password confirmation is required when new password is provided"
		case newPassword != confirmPassword:
			result.ValidationErrors["admin_confirm_password"] = "Passwords do not match"
		default:
			if pwErr := validation.ValidatePassword(newPassword); pwErr != nil {
				result.ValidationErrors["admin_new_password"] = pwErr.Error()
			}
		}
	}

	// If there are validation errors, don't proceed
	if len(result.ValidationErrors) > 0 {
		return result, nil
	}

	// Perform updates
	if result.ChangingUsername {
		if err := store.UpdateUsername(ctx, newUsername); err != nil {
			result.ValidationErrors["_global"] = "Failed to update admin username"
			return result, err
		}
	}

	if result.ChangingPassword {
		hashedPassword, err := generateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if err != nil {
			result.ValidationErrors["_global"] = "Failed to hash password"
			return result, err
		}
		if err := store.UpdatePassword(ctx, string(hashedPassword)); err != nil {
			result.ValidationErrors["_global"] = "Failed to update admin password"
			return result, err
		}
	}

	return result, nil
}
