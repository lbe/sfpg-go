package security

import (
	"context"
	"log/slog"
)

// UnlockAccountTask unlocks a user account when the scheduled lockout period expires.
// It calls the database to clear failed attempts and reset the locked_until timestamp.
type UnlockAccountTask struct {
	Username string
	UnlockFn func(ctx context.Context, username string) error
}

// Run implements scheduler.Task.
func (t *UnlockAccountTask) Run(ctx context.Context) error {
	slog.Info("executing scheduled account unlock", "username", t.Username)
	if err := t.UnlockFn(ctx, t.Username); err != nil {
		slog.Error("failed to unlock account", "username", t.Username, "error", err)
		return err
	}
	slog.Info("successfully unlocked account", "username", t.Username)
	return nil
}

// Ensure UnlockAccountTask implements scheduler.Task (compile-time check).
var _ interface {
	Run(ctx context.Context) error
} = (*UnlockAccountTask)(nil)
