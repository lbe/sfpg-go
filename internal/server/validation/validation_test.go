package validation

import (
	"strings"
	"testing"
)

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name      string
		username  string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "valid username - alphanumeric",
			username:  "admin123",
			wantError: false,
		},
		{
			name:      "valid username - with underscore",
			username:  "admin_user",
			wantError: false,
		},
		{
			name:      "valid username - with hyphen",
			username:  "admin-user",
			wantError: false,
		},
		{
			name:      "valid username - minimum length",
			username:  "abcdefgh",
			wantError: false,
		},
		{
			name:      "empty username",
			username:  "",
			wantError: true,
			errorMsg:  "username is required",
		},
		{
			name:      "too short - 7 characters",
			username:  "abcdefg",
			wantError: true,
			errorMsg:  "username must be at least 8 characters",
		},
		{
			name:      "contains space",
			username:  "admin user",
			wantError: true,
			errorMsg:  "username can only contain alphanumeric characters, underscores, and hyphens",
		},
		{
			name:      "contains special character",
			username:  "admin@user",
			wantError: true,
			errorMsg:  "username can only contain alphanumeric characters, underscores, and hyphens",
		},
		{
			name:      "starts with number - 8 chars",
			username:  "123admin",
			wantError: false, // Numbers are allowed
		},
		{
			name:      "only numbers - 8 chars",
			username:  "12345678",
			wantError: false, // Numbers are allowed
		},
		{
			name:      "only letters - 8 chars",
			username:  "adminuser",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUsername(tt.username)
			if tt.wantError {
				if err == nil {
					t.Errorf("ValidateUsername(%q) expected error, got nil", tt.username)
					return
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("ValidateUsername(%q) error = %q, want error containing %q", tt.username, err.Error(), tt.errorMsg)
				}
			} else if err != nil {
				t.Errorf("ValidateUsername(%q) unexpected error: %v", tt.username, err)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name      string
		password  string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "valid password - all requirements",
			password:  "Password123!",
			wantError: false,
		},
		{
			name:      "valid password - with special chars",
			password:  "MyP@ssw0rd#",
			wantError: false,
		},
		{
			name:      "valid password - minimum length",
			password:  "Pass1!ab",
			wantError: false,
		},
		{
			name:      "empty password",
			password:  "",
			wantError: true,
			errorMsg:  "password must be at least 8 characters",
		},
		{
			name:      "too short - 7 characters",
			password:  "Pass1!a",
			wantError: true,
			errorMsg:  "password must be at least 8 characters",
		},
		{
			name:      "missing uppercase",
			password:  "password123!",
			wantError: true,
			errorMsg:  "password must contain at least one uppercase letter",
		},
		{
			name:      "missing lowercase",
			password:  "PASSWORD123!",
			wantError: true,
			errorMsg:  "password must contain at least one lowercase letter",
		},
		{
			name:      "missing number",
			password:  "Password!",
			wantError: true,
			errorMsg:  "password must contain at least one number",
		},
		{
			name:      "missing special character",
			password:  "Password123",
			wantError: true,
			errorMsg:  "password must contain at least one special character",
		},
		{
			name:      "only lowercase",
			password:  "password",
			wantError: true,
		},
		{
			name:      "only uppercase",
			password:  "PASSWORD",
			wantError: true,
		},
		{
			name:      "only numbers",
			password:  "12345678",
			wantError: true,
		},
		{
			name:      "only special chars",
			password:  "!@#$%^&*",
			wantError: true,
		},
		{
			name:      "has uppercase, lowercase, number but no special",
			password:  "Password123",
			wantError: true,
			errorMsg:  "password must contain at least one special character",
		},
		{
			name:      "has uppercase, lowercase, special but no number",
			password:  "Password!",
			wantError: true,
			errorMsg:  "password must contain at least one number",
		},
		{
			name:      "has uppercase, number, special but no lowercase",
			password:  "PASSWORD123!",
			wantError: true,
			errorMsg:  "password must contain at least one lowercase letter",
		},
		{
			name:      "has lowercase, number, special but no uppercase",
			password:  "password123!",
			wantError: true,
			errorMsg:  "password must contain at least one uppercase letter",
		},
		{
			name:      "valid - complex password",
			password:  "MyStr0ng!P@ssw0rd#2024",
			wantError: false,
		},
		{
			name:      "valid - with underscore",
			password:  "Pass_123!",
			wantError: false,
		},
		{
			name:      "valid - with hyphen",
			password:  "Pass-123!",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if tt.wantError {
				if err == nil {
					t.Errorf("ValidatePassword(%q) expected error, got nil", tt.password)
					return
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("ValidatePassword(%q) error = %q, want error containing %q", tt.password, err.Error(), tt.errorMsg)
				}
			} else if err != nil {
				t.Errorf("ValidatePassword(%q) unexpected error: %v", tt.password, err)
			}
		})
	}
}
