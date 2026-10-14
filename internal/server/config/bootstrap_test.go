package config

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

func TestEnsureBootstrapDefaults_CreatesAdminDefaults(t *testing.T) {
	queries, cleanup := createBootstrapQueries(t)
	defer cleanup()

	ctx := context.Background()
	rootDir := t.TempDir()

	if err := EnsureBootstrapDefaults(ctx, rootDir, queries); err != nil {
		t.Fatalf("EnsureBootstrapDefaults failed: %v", err)
	}

	user, err := queries.GetConfigValueByKey(ctx, "user")
	if err != nil {
		t.Fatalf("GetConfigValueByKey(user) failed: %v", err)
	}
	if user != "admin" {
		t.Errorf("user = %q, want admin", user)
	}

	logDir, err := queries.GetConfigValueByKey(ctx, "log_directory")
	if err != nil {
		t.Fatalf("GetConfigValueByKey(log_directory) failed: %v", err)
	}
	if want := filepath.Join(rootDir, "logs"); logDir != want {
		t.Errorf("log_directory = %q, want %q", logDir, want)
	}
}

func TestEnsureBootstrapDefaults_PreservesExistingUser(t *testing.T) {
	queries, cleanup := createBootstrapQueries(t)
	defer cleanup()

	ctx := context.Background()
	now := int64(1)

	if err := queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key: "user", Value: "existing", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed user failed: %v", err)
	}
	if err := queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key: "password", Value: "hash", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed password failed: %v", err)
	}

	if err := EnsureBootstrapDefaults(ctx, t.TempDir(), queries); err != nil {
		t.Fatalf("EnsureBootstrapDefaults failed: %v", err)
	}

	user, err := queries.GetConfigValueByKey(ctx, "user")
	if err != nil {
		t.Fatalf("GetConfigValueByKey(user) failed: %v", err)
	}
	if user != "existing" {
		t.Errorf("user = %q, want existing", user)
	}
}

func TestEnsureBootstrapDefaults_InsertsMissingDefaults(t *testing.T) {
	queries, cleanup := createBootstrapQueries(t)
	defer cleanup()

	ctx := context.Background()
	rootDir := t.TempDir()

	if err := EnsureBootstrapDefaults(ctx, rootDir, queries); err != nil {
		t.Fatalf("EnsureBootstrapDefaults failed: %v", err)
	}

	val, err := queries.GetConfigValueByKey(ctx, "enable_cache_preload")
	if err != nil {
		t.Fatalf("GetConfigValueByKey(enable_cache_preload) failed: %v", err)
	}
	if val != "true" {
		t.Errorf("enable_cache_preload = %q, want true", val)
	}

	imageDir, err := queries.GetConfigValueByKey(ctx, "image_directory")
	if err != nil {
		t.Fatalf("GetConfigValueByKey(image_directory) failed: %v", err)
	}
	if want := filepath.Join(rootDir, "Images"); imageDir != want {
		t.Errorf("image_directory = %q, want %q", imageDir, want)
	}
}

func TestEnsureBootstrapDefaults_RepairsEmptyCriticalValues(t *testing.T) {
	queries, cleanup := createBootstrapQueries(t)
	defer cleanup()

	ctx := context.Background()
	rootDir := t.TempDir()
	now := int64(1)

	if err := queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key: "image_directory", Value: "", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed empty image_directory failed: %v", err)
	}

	if err := EnsureBootstrapDefaults(ctx, rootDir, queries); err != nil {
		t.Fatalf("EnsureBootstrapDefaults failed: %v", err)
	}

	imageDir, err := queries.GetConfigValueByKey(ctx, "image_directory")
	if err != nil {
		t.Fatalf("GetConfigValueByKey(image_directory) failed: %v", err)
	}
	if want := filepath.Join(rootDir, "Images"); imageDir != want {
		t.Errorf("image_directory = %q, want %q", imageDir, want)
	}
}

func TestEnsureBootstrapDefaults_SetsListenerPortAndLogLevel(t *testing.T) {
	queries, cleanup := createBootstrapQueries(t)
	defer cleanup()

	ctx := context.Background()
	rootDir := t.TempDir()

	if err := EnsureBootstrapDefaults(ctx, rootDir, queries); err != nil {
		t.Fatalf("EnsureBootstrapDefaults failed: %v", err)
	}

	portValue, err := queries.GetConfigValueByKey(ctx, "listener_port")
	if err != nil {
		t.Fatalf("listener_port should exist after initialization: %v", err)
	}
	if portValue != "8081" {
		t.Errorf("expected listener_port to be '8081', got %q", portValue)
	}

	logLevelValue, err := queries.GetConfigValueByKey(ctx, "log_level")
	if err != nil {
		t.Fatalf("log_level should exist after initialization: %v", err)
	}
	if logLevelValue != "info" {
		t.Errorf("expected log_level to be 'info', got %q", logLevelValue)
	}
}

func TestEnsureBootstrapDefaults_PreservesExistingValue(t *testing.T) {
	queries, cleanup := createBootstrapQueries(t)
	defer cleanup()

	ctx := context.Background()
	rootDir := t.TempDir()
	now := int64(1)

	err := queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key: "listener_port", Value: "9999", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set existing config: %v", err)
	}

	if err = EnsureBootstrapDefaults(ctx, rootDir, queries); err != nil {
		t.Fatalf("EnsureBootstrapDefaults failed: %v", err)
	}

	portValue, err := queries.GetConfigValueByKey(ctx, "listener_port")
	if err != nil {
		t.Fatalf("failed to get listener_port: %v", err)
	}
	if portValue != "9999" {
		t.Errorf("expected listener_port to be preserved as '9999', got %q", portValue)
	}
}

func TestEnsureBootstrapDefaults_OnlyMissingKeysAdded(t *testing.T) {
	queries, cleanup := createBootstrapQueries(t)
	defer cleanup()

	ctx := context.Background()
	rootDir := t.TempDir()
	now := int64(1)

	err := queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key: "log_level", Value: "info", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to set existing config: %v", err)
	}

	if err = EnsureBootstrapDefaults(ctx, rootDir, queries); err != nil {
		t.Fatalf("EnsureBootstrapDefaults failed: %v", err)
	}

	logLevelValue, err := queries.GetConfigValueByKey(ctx, "log_level")
	if err != nil {
		t.Fatalf("failed to get log_level: %v", err)
	}
	if logLevelValue != "info" {
		t.Errorf("expected log_level to be preserved as 'info', got %q", logLevelValue)
	}

	portValue, err := queries.GetConfigValueByKey(ctx, "listener_port")
	if err != nil {
		t.Fatalf("listener_port should have been added: %v", err)
	}
	if portValue == "" {
		t.Error("listener_port should have a default value")
	}
}

func TestEnsureBootstrapDefaults_LeavesExistingNonCriticalValues(t *testing.T) {
	queries, cleanup := createBootstrapQueries(t)
	defer cleanup()

	ctx := context.Background()
	rootDir := t.TempDir()
	now := int64(1)

	if err := queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key: "site_name", Value: "Custom", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed site_name failed: %v", err)
	}

	if err := EnsureBootstrapDefaults(ctx, rootDir, queries); err != nil {
		t.Fatalf("EnsureBootstrapDefaults failed: %v", err)
	}

	siteName, err := queries.GetConfigValueByKey(ctx, "site_name")
	if err != nil {
		t.Fatalf("GetConfigValueByKey(site_name) failed: %v", err)
	}
	if siteName != "Custom" {
		t.Errorf("site_name = %q, want Custom", siteName)
	}
}
