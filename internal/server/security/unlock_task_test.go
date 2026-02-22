package security

import (
	"context"
	"errors"
	"testing"
)

func TestUnlockAccountTask_Run(t *testing.T) {
	tests := []struct {
		name     string
		username string
		unlockFn func(ctx context.Context, username string) error
		wantErr  bool
		errCheck func(error) bool
	}{
		{
			name:     "successful unlock",
			username: "testuser",
			unlockFn: func(ctx context.Context, username string) error {
				if username != "testuser" {
					t.Errorf("expected username testuser, got %s", username)
				}
				return nil
			},
			wantErr: false,
		},
		{
			name:     "unlock function returns error",
			username: "erroruser",
			unlockFn: func(ctx context.Context, username string) error {
				return errors.New("database connection failed")
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return err != nil && err.Error() == "database connection failed"
			},
		},
		{
			name:     "unlock function returns context error",
			username: "ctxuser",
			unlockFn: func(ctx context.Context, username string) error {
				return context.Canceled
			},
			wantErr: true,
			errCheck: func(err error) bool {
				return errors.Is(err, context.Canceled)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &UnlockAccountTask{
				Username: tt.username,
				UnlockFn: tt.unlockFn,
			}

			ctx := context.Background()
			err := task.Run(ctx)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
					return
				}
				if tt.errCheck != nil && !tt.errCheck(err) {
					t.Errorf("error check failed: %v", err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestUnlockAccountTask_ImplementsSchedulerTask(t *testing.T) {
	// Compile-time check that UnlockAccountTask implements the scheduler.Task interface
	var _ interface {
		Run(ctx context.Context) error
	} = (*UnlockAccountTask)(nil)
}
