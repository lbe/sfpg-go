//go:build integration

package server

import (
	"context"
	"database/sql"
	"testing"

	"go.local/sfpg/internal/gallerydb"
)

// TestApp_UnlockAccountFromTask verifies unlockAccountFromTask behavior.
func TestApp_UnlockAccountFromTask(t *testing.T) {
	t.Run("successfully unlocks an account", func(t *testing.T) {
		app := CreateApp(t, false)
		defer app.Shutdown()

		username := "testuser"
		ctx := context.Background()

		// First lock the account by setting locked_until
		cpcRw, err := app.dbRwPool.Get()
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer app.dbRwPool.Put(cpcRw)

		// Create a locked account entry in login_attempts
		lockedUntil := int64(99999999999) // Far future
		err = cpcRw.Queries.UpsertLoginAttempt(ctx, gallerydb.UpsertLoginAttemptParams{
			Username:       username,
			FailedAttempts: 3,
			LastAttemptAt:  1234567890,
			LockedUntil:    sql.NullInt64{Int64: lockedUntil, Valid: true},
		})
		if err != nil {
			t.Fatalf("failed to create locked account: %v", err)
		}

		// Now unlock it using unlockAccountFromTask
		err = app.unlockAccountFromTask(ctx, username)
		if err != nil {
			t.Fatalf("unlockAccountFromTask failed: %v", err)
		}

		// Verify account is unlocked
		attempt, err := cpcRw.Queries.GetLoginAttempt(ctx, username)
		if err != nil {
			t.Fatalf("failed to get login attempt: %v", err)
		}

		if attempt.LockedUntil.Valid {
			t.Error("expected account to be unlocked (LockedUntil should be NULL)")
		}
		if attempt.FailedAttempts != 0 {
			t.Errorf("expected failed_attempts 0 after unlock, got %d", attempt.FailedAttempts)
		}
	})

	t.Run("returns error when database connection fails", func(t *testing.T) {
		// Create app and then close the pool to simulate connection failure
		app := CreateApp(t, false)
		app.dbRwPool.Close()
		defer app.Shutdown()

		username := "testuser"
		ctx := context.Background()

		err := app.unlockAccountFromTask(ctx, username)
		if err == nil {
			t.Error("expected error when database connection fails")
		}
	})

	t.Run("handles non-existent username gracefully", func(t *testing.T) {
		app := CreateApp(t, false)
		defer app.Shutdown()

		username := "nonexistentuser"
		ctx := context.Background()

		// Should not panic, may return error depending on implementation
		// UnlockAccount operates on login_attempts table, if username doesn't exist
		// it's a no-op (no error, no rows affected)
		err := app.unlockAccountFromTask(ctx, username)
		// Don't check for error as it's valid for it to succeed with no rows affected
		_ = err
	})
}
