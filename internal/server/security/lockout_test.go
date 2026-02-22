package security

import (
	"database/sql"
	"testing"
	"time"
)

func TestCalculateLockout(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name           string
		failedAttempts int64
		wantValid      bool
		wantDuration   int64
	}{
		{
			name:           "no failures",
			failedAttempts: 0,
			wantValid:      false,
		},
		{
			name:           "one failure",
			failedAttempts: 1,
			wantValid:      false,
		},
		{
			name:           "two failures",
			failedAttempts: 2,
			wantValid:      false,
		},
		{
			name:           "three failures - threshold reached",
			failedAttempts: 3,
			wantValid:      true,
			wantDuration:   LockoutDuration,
		},
		{
			name:           "five failures",
			failedAttempts: 5,
			wantValid:      true,
			wantDuration:   LockoutDuration,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateLockout(tt.failedAttempts, now)
			if got.Valid != tt.wantValid {
				t.Errorf("CalculateLockout() Valid = %v, want %v", got.Valid, tt.wantValid)
			}
			if tt.wantValid && got.Int64 != now+tt.wantDuration {
				t.Errorf("CalculateLockout() Int64 = %v, want %v", got.Int64, now+tt.wantDuration)
			}
		})
	}
}

func TestIsLocked(t *testing.T) {
	now := time.Now().Unix()
	future := now + 3600
	past := now - 3600

	tests := []struct {
		name        string
		lockedUntil sql.NullInt64
		now         int64
		want        bool
	}{
		{
			name:        "not locked (invalid)",
			lockedUntil: sql.NullInt64{Valid: false},
			now:         now,
			want:        false,
		},
		{
			name:        "locked in future",
			lockedUntil: sql.NullInt64{Int64: future, Valid: true},
			now:         now,
			want:        true,
		},
		{
			name:        "lockout expired",
			lockedUntil: sql.NullInt64{Int64: past, Valid: true},
			now:         now,
			want:        false,
		},
		{
			name:        "lockout exactly now",
			lockedUntil: sql.NullInt64{Int64: now, Valid: true},
			now:         now,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsLocked(tt.lockedUntil, tt.now)
			if got != tt.want {
				t.Errorf("IsLocked() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldClearLockout(t *testing.T) {
	now := time.Now().Unix()
	future := now + 3600
	past := now - 3600

	tests := []struct {
		name        string
		lockedUntil sql.NullInt64
		now         int64
		want        bool
	}{
		{
			name:        "not locked (invalid)",
			lockedUntil: sql.NullInt64{Valid: false},
			now:         now,
			want:        false,
		},
		{
			name:        "locked in future - don't clear",
			lockedUntil: sql.NullInt64{Int64: future, Valid: true},
			now:         now,
			want:        false,
		},
		{
			name:        "lockout expired - should clear",
			lockedUntil: sql.NullInt64{Int64: past, Valid: true},
			now:         now,
			want:        true,
		},
		{
			name:        "lockout exactly now - should clear",
			lockedUntil: sql.NullInt64{Int64: now, Valid: true},
			now:         now,
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldClearLockout(tt.lockedUntil, tt.now)
			if got != tt.want {
				t.Errorf("ShouldClearLockout() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIncrementFailedAttempts(t *testing.T) {
	tests := []struct {
		name    string
		current int64
		want    int64
	}{
		{"zero", 0, 1},
		{"one", 1, 2},
		{"two", 2, 3},
		{"ten", 10, 11},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IncrementFailedAttempts(tt.current)
			if got != tt.want {
				t.Errorf("IncrementFailedAttempts() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatLockoutDuration(t *testing.T) {
	now := int64(1640000000)

	tests := []struct {
		name        string
		lockedUntil int64
		now         int64
		want        string
	}{
		{
			name:        "expired",
			lockedUntil: now - 100,
			now:         now,
			want:        "0 minutes",
		},
		{
			name:        "one minute",
			lockedUntil: now + 60,
			now:         now,
			want:        "1 minute",
		},
		{
			name:        "30 minutes",
			lockedUntil: now + 1800,
			now:         now,
			want:        "30 minutes",
		},
		{
			name:        "one hour",
			lockedUntil: now + 3600,
			now:         now,
			want:        "1 hour",
		},
		{
			name:        "two hours",
			lockedUntil: now + 7200,
			now:         now,
			want:        "2 hours",
		},
		{
			name:        "90 minutes (1.5 hours, rounds to hours)",
			lockedUntil: now + 5400,
			now:         now,
			want:        "1 hour",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatLockoutDuration(tt.lockedUntil, tt.now)
			if got != tt.want {
				t.Errorf("FormatLockoutDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLockoutConstants(t *testing.T) {
	if LockoutThreshold != 3 {
		t.Errorf("LockoutThreshold = %v, want 3", LockoutThreshold)
	}
	if LockoutDuration != 3600 {
		t.Errorf("LockoutDuration = %v, want 3600", LockoutDuration)
	}
}
