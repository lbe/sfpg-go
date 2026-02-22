package server

import (
	"context"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
)

// TestInitializeDefaults_FirstRun verifies that defaults are created on first run.
func TestInitializeDefaults_FirstRun(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	// Initialize defaults
	app.setConfigDefaults()

	// Verify some defaults were set in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	// Check a few key defaults
	portValue, err := cpcRw.Queries.GetConfigValueByKey(context.Background(), "listener_port")
	if err != nil {
		t.Fatalf("listener_port should exist after initialization: %v", err)
	}
	if portValue != "8081" {
		t.Errorf("expected listener_port to be '8081', got %q", portValue)
	}

	logLevelValue, err := cpcRw.Queries.GetConfigValueByKey(context.Background(), "log_level")
	if err != nil {
		t.Fatalf("log_level should exist after initialization: %v", err)
	}
	if logLevelValue != "debug" {
		t.Errorf("expected log_level to be 'debug', got %q", logLevelValue)
	}
}

// TestInitializeDefaults_ExistingConfig verifies that existing config values are preserved.
func TestInitializeDefaults_ExistingConfig(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	// Set an existing value before initialization
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	now := time.Now().Unix()
	originalValue := "9999"
	err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
		Key:       "listener_port",
		Value:     originalValue,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set existing config: %v", err)
	}

	// Initialize defaults (should not overwrite existing)
	app.setConfigDefaults()

	// Verify existing value is preserved
	portValue, err := cpcRw.Queries.GetConfigValueByKey(context.Background(), "listener_port")
	if err != nil {
		t.Fatalf("failed to get listener_port: %v", err)
	}
	if portValue != originalValue {
		t.Errorf("expected listener_port to be preserved as %q, got %q", originalValue, portValue)
	}
}

// TestInitializeDefaults_PreservesUserPassword verifies that user/password are never overwritten.
func TestInitializeDefaults_PreservesUserPassword(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	// Set existing user/password
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	now := time.Now().Unix()
	originalUser := "existing_user"
	originalPassword := "hashed_password_12345"

	err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
		Key:       "user",
		Value:     originalUser,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set user: %v", err)
	}

	err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
		Key:       "password",
		Value:     originalPassword,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set password: %v", err)
	}

	// Initialize defaults (should skip user/password)
	app.setConfigDefaults()

	// Verify user/password are preserved
	userValue, err := cpcRw.Queries.GetConfigValueByKey(context.Background(), "user")
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}
	if userValue != originalUser {
		t.Errorf("expected user to be preserved as %q, got %q", originalUser, userValue)
	}

	passwordValue, err := cpcRw.Queries.GetConfigValueByKey(context.Background(), "password")
	if err != nil {
		t.Fatalf("failed to get password: %v", err)
	}
	if passwordValue != originalPassword {
		t.Errorf("expected password to be preserved, got %q", passwordValue)
	}
}

// TestInitializeDefaults_OnlyMissingKeys verifies that only missing keys are added.
func TestInitializeDefaults_OnlyMissingKeys(t *testing.T) {
	tempDir := t.TempDir()
	ss := "test-session-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)

	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	app.setDB()

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	// Set one existing value
	now := time.Now().Unix()
	err = cpcRw.Queries.UpsertConfigValueOnly(context.Background(), gallerydb.UpsertConfigValueOnlyParams{
		Key:       "log_level",
		Value:     "info",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set existing config: %v", err)
	}

	// Initialize defaults
	app.setConfigDefaults()

	// Verify existing value is preserved
	logLevelValue, err := cpcRw.Queries.GetConfigValueByKey(context.Background(), "log_level")
	if err != nil {
		t.Fatalf("failed to get log_level: %v", err)
	}
	if logLevelValue != "info" {
		t.Errorf("expected log_level to be preserved as 'info', got %q", logLevelValue)
	}

	// Verify a missing key was added
	portValue, err := cpcRw.Queries.GetConfigValueByKey(context.Background(), "listener_port")
	if err != nil {
		t.Fatalf("listener_port should have been added: %v", err)
	}
	if portValue == "" {
		t.Error("listener_port should have a default value")
	}
}
