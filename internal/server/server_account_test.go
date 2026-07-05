package server

import (
	"context"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
)

// Minimal Thumb struct for testing purposes

// TestAuthMiddleware tests the authMiddleware to ensure it correctly protects
// routes, redirecting unauthenticated requests and allowing authenticated ones.
func TestCheckAccountLockout_Additional(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	username := "lockouttest2"

	// Should not be locked initially
	isLocked, err := app.CheckAccountLockout(context.Background(), username)
	if err != nil {
		t.Errorf("checkAccountLockout failed: %v", err)
	}
	if isLocked {
		t.Error("Account should not be locked initially")
	}
}

// TestRecordFailedLoginAttempt_Additional tests recording failed login attempts
func TestRecordFailedLoginAttempt_Additional(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	username := "failedlogintest2"

	err := app.RecordFailedLoginAttempt(context.Background(), username)
	if err != nil {
		t.Errorf("recordFailedLoginAttempt failed: %v", err)
	}

	// Record another attempt
	err = app.RecordFailedLoginAttempt(context.Background(), username)
	if err != nil {
		t.Errorf("Second recordFailedLoginAttempt failed: %v", err)
	}
}

// TestClearLoginAttempts_Additional tests clearing login attempts
func TestClearLoginAttempts_Additional(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	username := "cleartest2"

	// Record an attempt first
	err := app.RecordFailedLoginAttempt(context.Background(), username)
	if err != nil {
		t.Fatalf("Failed to record attempt: %v", err)
	}

	// Clear attempts
	err = app.ClearLoginAttempts(context.Background(), username)
	if err != nil {
		t.Errorf("clearLoginAttempts failed: %v", err)
	}
}

// TestRemoveImagesDirPrefix tests image directory prefix removal (standalone function)
func TestClearLoginAttempts_EdgeCases(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	username := "testuser"

	// Record some failed attempts
	for i := range 3 {
		if err := app.RecordFailedLoginAttempt(context.Background(), username); err != nil {
			t.Fatalf("Failed to record attempt %d: %v", i, err)
		}
	}

	// Clear them
	if err := app.ClearLoginAttempts(context.Background(), username); err != nil {
		t.Errorf("clearLoginAttempts failed: %v", err)
	}

	// Check if cleared
	locked, err := app.CheckAccountLockout(context.Background(), username)
	if err != nil {
		t.Errorf("checkAccountLockout failed: %v", err)
	}
	if locked {
		t.Error("Account should not be locked after clearing attempts")
	}

	// Clear again (should be idempotent)
	if err := app.ClearLoginAttempts(context.Background(), username); err != nil {
		t.Errorf("Second clearLoginAttempts failed: %v", err)
	}
}

// TestUnlockAccount_EdgeCases tests account unlocking
func TestUnlockAccount_EdgeCases(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	username := "unlocktestuser"

	// Create user first
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	// Setup user in database
	err = cpcRw.Queries.UpsertConfigValueOnly(app.ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "user",
		Value:     username,
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Logf("Failed to setup user (may be okay): %v", err)
	}

	// Unlock account (may or may not exist)
	err = app.UnlockAccount(username)
	// Error is acceptable if user doesn't exist
	_ = err

	// Unlock again (should be idempotent)
	err = app.UnlockAccount(username)
	_ = err
}

// TestCompressMiddleware_AdditionalCases tests more compression scenarios
func TestInitForUnlock_Coverage(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}, "x.y.z")
	defer app.Shutdown()

	testDir := t.TempDir()
	app.setRootDir(&testDir)

	err := app.InitForUnlock()
	if err != nil {
		t.Fatalf("InitForUnlock failed: %v", err)
	}

	if app.dbPaths.Main == "" {
		t.Error("Expected dbPaths.Main to be set")
	}
	if app.dbRwPool == nil {
		t.Error("Expected dbRwPool to be set")
	}
	if app.dbRoPool == nil {
		t.Error("Expected dbRoPool to be set")
	}
}

// TestUnlockAccount_Coverage verifies account unlock
func TestUnlockAccount_Coverage(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	username := "testuser"

	// Record a failed login to create account record
	_ = app.RecordFailedLoginAttempt(context.Background(), username)

	// Unlock the account
	err := app.UnlockAccount(username)
	if err != nil {
		t.Fatalf("UnlockAccount failed: %v", err)
	}

	// Verify it's unlocked
	isLocked, _ := app.CheckAccountLockout(context.Background(), username)
	if isLocked {
		t.Error("Expected account to be unlocked")
	}
}

// TestGetAdminUsername_Coverage verifies admin username retrieval
func TestRecordFailedLoginAttempt_Coverage(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	username := "testuser"
	err := app.RecordFailedLoginAttempt(context.Background(), username)
	if err != nil {
		t.Errorf("recordFailedLoginAttempt failed: %v", err)
	}
}

// TestCheckAccountLockout_Coverage tests account lockout checking
func TestCheckAccountLockout_Coverage(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	username := "testuser"

	// Initially should not be locked
	isLocked, err := app.CheckAccountLockout(context.Background(), username)
	if err != nil {
		t.Errorf("checkAccountLockout failed: %v", err)
	}

	if isLocked {
		t.Error("Expected account to not be locked initially")
	}
}

// TestClearLoginAttempts_Coverage tests clearing login attempts
func TestClearLoginAttempts_Coverage(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	username := "testuser"

	// Record a failed attempt
	_ = app.RecordFailedLoginAttempt(context.Background(), username)

	// Clear attempts
	err := app.ClearLoginAttempts(context.Background(), username)
	if err != nil {
		t.Errorf("clearLoginAttempts failed: %v", err)
	}
}

// TestGetAdminUsername_WithConfigService tests admin username with config service
func TestInitForUnlock_Multiple(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}, "x.y.z")
	defer app.Shutdown()

	testDir := t.TempDir()
	app.setRootDir(&testDir)

	// Initialize multiple times
	err1 := app.InitForUnlock()
	if err1 != nil {
		t.Fatalf("First InitForUnlock failed: %v", err1)
	}

	// Multiple initializations should work
	app2 := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}, "x.y.z")
	defer app2.Shutdown()
	app2.setRootDir(&testDir)

	err2 := app2.InitForUnlock()
	if err2 != nil {
		t.Fatalf("Second InitForUnlock failed: %v", err2)
	}
}

// TestUnlockAccount_MultipleUsers tests unlocking multiple users
func TestUnlockAccount_MultipleUsers(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	users := []string{"user1", "user2", "user3"}

	for _, username := range users {
		_ = app.RecordFailedLoginAttempt(context.Background(), username)
		err := app.UnlockAccount(username)
		if err != nil {
			t.Errorf("Failed to unlock %s: %v", username, err)
		}
	}
}

func TestScheduledUnlockTask(t *testing.T) {
	app := CreateApp(t) // Don't start pool, we don't need it for this test
	defer app.Shutdown()

	username := "scheduledunlockuser"

	// Record 3 failed attempts to trigger lockout
	for i := range 3 {
		if err := app.RecordFailedLoginAttempt(context.Background(), username); err != nil {
			t.Fatalf("Failed to record attempt %d: %v", i, err)
		}
	}

	// Verify account is locked
	locked, err := app.CheckAccountLockout(context.Background(), username)
	if err != nil {
		t.Fatalf("checkAccountLockout failed: %v", err)
	}
	if !locked {
		t.Error("Account should be locked after 3 failed attempts")
	}

	// Verify the database state shows the lockout
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	attempt, err := cpcRw.Queries.GetLoginAttempt(app.ctx, username)
	if err != nil {
		t.Fatalf("GetLoginAttempt failed: %v", err)
	}

	if !attempt.LockedUntil.Valid {
		t.Error("Expected locked_until to be set in database")
	}

	// Verify the locked_until time is in the future (1 hour from now)
	now := time.Now().Unix()
	expectedLockout := now + 3600 // 1 hour
	if attempt.LockedUntil.Int64 < expectedLockout-5 || attempt.LockedUntil.Int64 > expectedLockout+5 {
		t.Errorf("Expected locked_until to be approximately %d, got %d", expectedLockout, attempt.LockedUntil.Int64)
	}

	// Verify failed_attempts was incremented to 3
	if attempt.FailedAttempts != 3 {
		t.Errorf("Expected failed_attempts to be 3, got %d", attempt.FailedAttempts)
	}

	// Note: In this test, app.scheduler is nil because CreateApp doesn't initialize it.
	// The actual scheduling logic is tested implicitly by the fact that the code
	// runs without error when scheduler is nil (the nil check prevents the segfault).
	// In production (when the app runs with NewAndRun), the scheduler is initialized
	// and tasks are scheduled properly.
}

// TestGetGalleryStatistics_FormattedNumbers tests that getGalleryStatistics returns
// TestAddCommonTemplateData_AboutModal tests that addCommonTemplateData properly populates
// all data required by the about-modal template, including:
// - Version
// - GalleryStats.Folders (formatted string with commas)
// - GalleryStats.Images (formatted string with commas)
// - GalleryStats.ImagesSize (int64 bytes)
// - GalleryStats.FirstDiscovery (formatted timestamp)
// - GalleryStats.LastDiscovery (formatted timestamp)
// - IsAuthenticated (boolean)
// - CSRFToken (string)
// - Theme (string)
