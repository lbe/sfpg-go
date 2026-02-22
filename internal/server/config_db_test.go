package server

import (
	"context"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// setupTestDBForSQLCQueries creates a test database with migrations for SQLC query tests.
func setupTestDBForSQLCQueries(t *testing.T) (*gallerydb.Queries, context.Context) {
	t.Helper()
	_, q, ctx := setupTestDBForConfig(t)
	return q, ctx
}

// TestGetAllSettings verifies that GetAllSettings retrieves all configuration settings.
func TestGetAllSettings(t *testing.T) {
	q, ctx := setupTestDBForSQLCQueries(t)

	// Insert some test settings
	now := time.Now().Unix()
	testSettings := map[string]string{
		"listener_port": "9090",
		"log_level":     "info",
		"site_name":     "Test Site",
	}

	for key, value := range testSettings {
		err := q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
			Key:       key,
			Value:     value,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("failed to insert %s: %v", key, err)
		}
	}

	// Get all settings
	configs, err := q.GetConfigs(ctx)
	if err != nil {
		t.Fatalf("failed to get all configs: %v", err)
	}

	// Verify we got at least our test settings
	found := make(map[string]bool)
	for _, cfg := range configs {
		found[cfg.Key] = true
		if expectedValue, ok := testSettings[cfg.Key]; ok {
			if cfg.Value != expectedValue {
				t.Errorf("expected %s to be %q, got %q", cfg.Key, expectedValue, cfg.Value)
			}
		}
	}

	for key := range testSettings {
		if !found[key] {
			t.Errorf("expected to find setting %s", key)
		}
	}
}

// TestGetSettingByKey verifies that GetSettingByKey retrieves a single setting.
func TestGetSettingByKey(t *testing.T) {
	q, ctx := setupTestDBForSQLCQueries(t)

	// Insert a test setting
	now := time.Now().Unix()
	err := q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "listener_port",
		Value:     "9090",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to insert config: %v", err)
	}

	// Get the setting
	value, err := q.GetConfigValueByKey(ctx, "listener_port")
	if err != nil {
		t.Fatalf("failed to get config value: %v", err)
	}

	if value != "9090" {
		t.Errorf("expected value to be '9090', got %q", value)
	}
}

// TestGetSettingByKey_NotFound verifies that GetSettingByKey handles missing keys.
func TestGetSettingByKey_NotFound(t *testing.T) {
	q, ctx := setupTestDBForSQLCQueries(t)

	// Try to get a non-existent key
	_, err := q.GetConfigValueByKey(ctx, "non_existent_key")
	if err == nil {
		t.Error("expected error for non-existent key")
	}
	// sql.ErrNoRows is expected
}

// TestUpsertSetting verifies that UpsertSetting inserts new settings.
func TestUpsertSetting(t *testing.T) {
	q, ctx := setupTestDBForSQLCQueries(t)

	now := time.Now().Unix()
	err := q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "test_key",
		Value:     "test_value",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to upsert config: %v", err)
	}

	// Verify it was inserted
	value, err := q.GetConfigValueByKey(ctx, "test_key")
	if err != nil {
		t.Fatalf("failed to get inserted value: %v", err)
	}
	if value != "test_value" {
		t.Errorf("expected value to be 'test_value', got %q", value)
	}
}

// TestUpsertSetting_UpdateExisting verifies that UpsertSetting updates existing settings.
func TestUpsertSetting_UpdateExisting(t *testing.T) {
	q, ctx := setupTestDBForSQLCQueries(t)

	now := time.Now().Unix()
	// Insert initial value
	err := q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "test_key",
		Value:     "initial_value",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to insert initial value: %v", err)
	}

	// Update the value
	err = q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "test_key",
		Value:     "updated_value",
		CreatedAt: now,
		UpdatedAt: now + 1, // Different timestamp
	})
	if err != nil {
		t.Fatalf("failed to update value: %v", err)
	}

	// Verify it was updated
	value, err := q.GetConfigValueByKey(ctx, "test_key")
	if err != nil {
		t.Fatalf("failed to get updated value: %v", err)
	}
	if value != "updated_value" {
		t.Errorf("expected value to be 'updated_value', got %q", value)
	}
}
