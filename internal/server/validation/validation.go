package validation

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	lowerRe   = regexp.MustCompile(`[a-z]`)
	upperRe   = regexp.MustCompile(`[A-Z]`)
	digitRe   = regexp.MustCompile(`\d`)
	specialRe = regexp.MustCompile(`[!@#$%^&*()\-_=+\[\]{};':"\\|,.<>/?]`)
)

// ValidateUsername validates an admin username according to the following rules:
// - Minimum length: 8 characters
// - Allowed characters: alphanumeric, underscore (_), hyphen (-)
// Returns an error with a descriptive message if validation fails.
func ValidateUsername(username string) error {
	if username == "" {
		return fmt.Errorf("username is required")
	}

	if len(username) < 8 {
		return fmt.Errorf("username must be at least 8 characters")
	}

	// Check for allowed characters: alphanumeric, underscore, hyphen
	validPattern := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !validPattern.MatchString(username) {
		return fmt.Errorf("username can only contain alphanumeric characters, underscores, and hyphens")
	}

	return nil
}

// ValidatePassword validates a password according to the following rules:
// - Minimum length: 8 characters
// - Must contain at least: 1 uppercase letter, 1 lowercase letter, 1 number, 1 special character
// - Special characters: !@#$%^&*()-_=+[]{};':"\\|,.<>/?
// Returns an error with a descriptive message if validation fails.
func ValidatePassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}

	if !isValidPassword(password) {
		// Check what's missing for better error messages
		var missing []string

		if !lowerRe.MatchString(password) {
			missing = append(missing, "lowercase letter")
		}
		if !upperRe.MatchString(password) {
			missing = append(missing, "uppercase letter")
		}
		if !digitRe.MatchString(password) {
			missing = append(missing, "number")
		}
		if !specialRe.MatchString(password) {
			missing = append(missing, "special character")
		}

		return fmt.Errorf("password must contain at least one %s", strings.Join(missing, ", "))
	}

	return nil
}

func isValidPassword(s string) bool {
	return len(s) >= 8 &&
		lowerRe.MatchString(s) &&
		upperRe.MatchString(s) &&
		digitRe.MatchString(s) &&
		specialRe.MatchString(s)
}
