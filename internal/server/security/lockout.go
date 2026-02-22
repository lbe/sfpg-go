// Package security provides pure functions for security and lockout calculations.
package security

import (
	"database/sql"
	"fmt"
)

// LockoutThreshold is the number of failed attempts before account lockout.
const LockoutThreshold = 3

// LockoutDuration is the duration of account lockout in seconds.
const LockoutDuration = 3600 // 1 hour

// CalculateLockout calculates the lockout expiration timestamp based on failed attempts.
// Returns nil (invalid) if failed attempts < LockoutThreshold.
// Returns the expiration timestamp if failed attempts >= LockoutThreshold.
func CalculateLockout(failedAttempts int64, now int64) sql.NullInt64 {
	if failedAttempts >= LockoutThreshold {
		return sql.NullInt64{Int64: now + LockoutDuration, Valid: true}
	}
	return sql.NullInt64{Valid: false}
}

// IsLocked checks if an account is currently locked based on lockedUntil timestamp.
// Returns true if locked (lockedUntil is valid and in the future).
// Returns false if not locked or lockout has expired.
func IsLocked(lockedUntil sql.NullInt64, now int64) bool {
	if !lockedUntil.Valid {
		return false
	}
	return lockedUntil.Int64 > now
}

// ShouldClearLockout determines if a lockout should be cleared based on expiration.
// Returns true if the lockout has expired (lockedUntil <= now).
func ShouldClearLockout(lockedUntil sql.NullInt64, now int64) bool {
	if !lockedUntil.Valid {
		return false
	}
	return lockedUntil.Int64 <= now
}

// IncrementFailedAttempts increments the failed attempt counter.
func IncrementFailedAttempts(current int64) int64 {
	return current + 1
}

// FormatLockoutDuration converts the lockout duration to a human-readable string.
func FormatLockoutDuration(lockedUntil int64, now int64) string {
	duration := lockedUntil - now
	if duration <= 0 {
		return "0 minutes"
	}

	minutes := duration / 60
	if minutes < 60 {
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}

	hours := minutes / 60
	if hours == 1 {
		return "1 hour"
	}
	return fmt.Sprintf("%d hours", hours)
}
