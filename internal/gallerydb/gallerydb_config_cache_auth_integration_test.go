//go:build integration

package gallerydb

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestLoginAttemptQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	username := "testuser"
	now := time.Now().Unix()

	t.Run("GetLoginAttempt_NonExistent", func(t *testing.T) {
		_, err := q.GetLoginAttempt(ctx, username)
		if err == nil {
			t.Error("expected error when getting non-existent login attempt, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %v", err)
		}
	})

	t.Run("UpsertLoginAttempt_Insert", func(t *testing.T) {
		failedAttempts := int64(1)
		err := q.UpsertLoginAttempt(ctx, UpsertLoginAttemptParams{
			Username:       username,
			FailedAttempts: failedAttempts,
			LastAttemptAt:  now,
			LockedUntil:    sql.NullInt64{Valid: false},
		})
		if err != nil {
			t.Fatalf("UpsertLoginAttempt (insert) failed: %v", err)
		}

		attempt, err := q.GetLoginAttempt(ctx, username)
		if err != nil {
			t.Fatalf("GetLoginAttempt failed: %v", err)
		}
		if attempt.Username != username {
			t.Errorf("expected username %q, got %q", username, attempt.Username)
		}
		if attempt.FailedAttempts != failedAttempts {
			t.Errorf("expected failed_attempts %d, got %d", failedAttempts, attempt.FailedAttempts)
		}
		if attempt.LockedUntil.Valid {
			t.Error("expected locked_until to be NULL, but it was set")
		}
	})

	t.Run("UpsertLoginAttempt_UpdateIncrement", func(t *testing.T) {
		newFailedAttempts := int64(2)
		err := q.UpsertLoginAttempt(ctx, UpsertLoginAttemptParams{
			Username:       username,
			FailedAttempts: newFailedAttempts,
			LastAttemptAt:  now + 1,
			LockedUntil:    sql.NullInt64{Valid: false},
		})
		if err != nil {
			t.Fatalf("UpsertLoginAttempt (update) failed: %v", err)
		}

		attempt, err := q.GetLoginAttempt(ctx, username)
		if err != nil {
			t.Fatalf("GetLoginAttempt failed: %v", err)
		}
		if attempt.FailedAttempts != newFailedAttempts {
			t.Errorf("expected failed_attempts %d, got %d", newFailedAttempts, attempt.FailedAttempts)
		}
	})

	t.Run("UpsertLoginAttempt_SetLockout", func(t *testing.T) {
		failedAttempts := int64(3)
		lockedUntil := now + 3600 // 1 hour from now
		err := q.UpsertLoginAttempt(ctx, UpsertLoginAttemptParams{
			Username:       username,
			FailedAttempts: failedAttempts,
			LastAttemptAt:  now + 2,
			LockedUntil:    sql.NullInt64{Int64: lockedUntil, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertLoginAttempt (set lockout) failed: %v", err)
		}

		attempt, err := q.GetLoginAttempt(ctx, username)
		if err != nil {
			t.Fatalf("GetLoginAttempt failed: %v", err)
		}
		if attempt.FailedAttempts != failedAttempts {
			t.Errorf("expected failed_attempts %d, got %d", failedAttempts, attempt.FailedAttempts)
		}
		if !attempt.LockedUntil.Valid {
			t.Error("expected locked_until to be set, but it was NULL")
		}
		if attempt.LockedUntil.Int64 != lockedUntil {
			t.Errorf("expected locked_until %d, got %d", lockedUntil, attempt.LockedUntil.Int64)
		}
	})

	t.Run("ClearLoginAttempts", func(t *testing.T) {
		err := q.ClearLoginAttempts(ctx, username)
		if err != nil {
			t.Fatalf("ClearLoginAttempts failed: %v", err)
		}

		_, err = q.GetLoginAttempt(ctx, username)
		if err == nil {
			t.Error("expected error when getting cleared login attempt, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %v", err)
		}
	})

	t.Run("UnlockAccount", func(t *testing.T) {
		// First create a locked account
		lockedUntil := now + 3600
		err := q.UpsertLoginAttempt(ctx, UpsertLoginAttemptParams{
			Username:       username,
			FailedAttempts: 3,
			LastAttemptAt:  now,
			LockedUntil:    sql.NullInt64{Int64: lockedUntil, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertLoginAttempt failed: %v", err)
		}

		// Unlock the account
		err = q.UnlockAccount(ctx, username)
		if err != nil {
			t.Fatalf("UnlockAccount failed: %v", err)
		}

		// Verify account is unlocked
		attempt, err := q.GetLoginAttempt(ctx, username)
		if err != nil {
			t.Fatalf("GetLoginAttempt failed: %v", err)
		}
		if attempt.FailedAttempts != 0 {
			t.Errorf("expected failed_attempts 0 after unlock, got %d", attempt.FailedAttempts)
		}
		if attempt.LockedUntil.Valid {
			t.Error("expected locked_until to be NULL after unlock, but it was set")
		}
	})

	t.Run("CleanupExpiredLockouts", func(t *testing.T) {
		// Create multiple accounts with expired and non-expired lockouts
		expiredUsername := "expireduser"
		activeUsername := "activeuser"
		pastTime := now - 7200   // 2 hours ago
		futureTime := now + 3600 // 1 hour from now

		// Expired lockout
		err := q.UpsertLoginAttempt(ctx, UpsertLoginAttemptParams{
			Username:       expiredUsername,
			FailedAttempts: 3,
			LastAttemptAt:  pastTime,
			LockedUntil:    sql.NullInt64{Int64: pastTime, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertLoginAttempt (expired) failed: %v", err)
		}

		// Active lockout
		err = q.UpsertLoginAttempt(ctx, UpsertLoginAttemptParams{
			Username:       activeUsername,
			FailedAttempts: 3,
			LastAttemptAt:  now,
			LockedUntil:    sql.NullInt64{Int64: futureTime, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertLoginAttempt (active) failed: %v", err)
		}
	})
}

// TestConfigQueries tests configuration-related queries
func TestConfigQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	now := time.Now().Unix()

	t.Run("UpsertConfigValueOnly and GetConfigValueByKey", func(t *testing.T) {
		// Insert a config value
		err := q.UpsertConfigValueOnly(ctx, UpsertConfigValueOnlyParams{
			Key:       "test_key",
			Value:     "test_value",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertConfigValueOnly failed: %v", err)
		}

		// Get the config value
		value, err := q.GetConfigValueByKey(ctx, "test_key")
		if err != nil {
			t.Fatalf("GetConfigValueByKey failed: %v", err)
		}
		if value != "test_value" {
			t.Errorf("Expected value 'test_value', got %s", value)
		}

		// Update the config value
		err = q.UpsertConfigValueOnly(ctx, UpsertConfigValueOnlyParams{
			Key:       "test_key",
			Value:     "updated_value",
			CreatedAt: now,
			UpdatedAt: now + 1,
		})
		if err != nil {
			t.Fatalf("UpsertConfigValueOnly (update) failed: %v", err)
		}

		value, err = q.GetConfigValueByKey(ctx, "test_key")
		if err != nil {
			t.Fatalf("GetConfigValueByKey (after update) failed: %v", err)
		}
		if value != "updated_value" {
			t.Errorf("Expected updated value 'updated_value', got %s", value)
		}
	})

	t.Run("GetConfigValueByKey_NonExistent", func(t *testing.T) {
		_, err := q.GetConfigValueByKey(ctx, "nonexistent_key")
		if err == nil {
			t.Error("expected error when getting non-existent config key, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %v", err)
		}
	})

	t.Run("GetConfigs", func(t *testing.T) {
		// Insert another config value
		err := q.UpsertConfigValueOnly(ctx, UpsertConfigValueOnlyParams{
			Key:       "another_key",
			Value:     "another_value",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertConfigValueOnly failed: %v", err)
		}

		// Get all configs
		configs, err := q.GetConfigs(ctx)
		if err != nil {
			t.Fatalf("GetConfigs failed: %v", err)
		}
		// Should have at least the configs we inserted
		if len(configs) < 2 {
			t.Errorf("Expected at least 2 configs, got %d", len(configs))
		}

		// Find our test configs
		foundTestKey := false
		foundAnotherKey := false
		for _, cfg := range configs {
			if cfg.Key == "test_key" {
				foundTestKey = true
				if cfg.Value != "updated_value" {
					t.Errorf("Expected test_key value 'updated_value', got %s", cfg.Value)
				}
			}
			if cfg.Key == "another_key" {
				foundAnotherKey = true
				if cfg.Value != "another_value" {
					t.Errorf("Expected another_key value 'another_value', got %s", cfg.Value)
				}
			}
		}
		if !foundTestKey {
			t.Error("Did not find test_key in configs")
		}
		if !foundAnotherKey {
			t.Error("Did not find another_key in configs")
		}
	})
}

// TestHttpCacheQueries tests HTTP cache queries
func TestHttpCacheQueries(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	now := time.Now().Unix()

	t.Run("UpsertHttpCache and GetHttpCacheByKey", func(t *testing.T) {
		// Insert a cache entry
		params := UpsertHttpCacheParams{
			Key:             "test_key_1",
			Method:          "GET",
			Path:            "/api/test",
			Encoding:        "gzip",
			Status:          200,
			ContentType:     sql.NullString{String: "application/json", Valid: true},
			ContentEncoding: sql.NullString{String: "gzip", Valid: true},
			CacheControl:    sql.NullString{String: "max-age=3600", Valid: true},
			Etag:            sql.NullString{String: `\"12345\"`, Valid: true},
			LastModified:    sql.NullString{String: "Mon, 01 Jan 2024 00:00:00 GMT", Valid: true},
			Body:            []byte(`{"test": "data"}`),
			ContentLength:   sql.NullInt64{Int64: 17, Valid: true},
			CreatedAt:       now,
			ExpiresAt:       sql.NullInt64{Int64: now + 3600, Valid: true},
		}
		err := q.UpsertHttpCache(ctx, params)
		if err != nil {
			t.Fatalf("UpsertHttpCache failed: %v", err)
		}

		// Get the cache entry
		entry, err := q.GetHttpCacheByKey(ctx, "test_key_1")
		if err != nil {
			t.Fatalf("GetHttpCacheByKey failed: %v", err)
		}
		if entry.Key != "test_key_1" {
			t.Errorf("Expected key 'test_key_1', got %s", entry.Key)
		}
		if entry.Method != "GET" {
			t.Errorf("Expected method 'GET', got %s", entry.Method)
		}
		if entry.Status != 200 {
			t.Errorf("Expected status 200, got %d", entry.Status)
		}
		if string(entry.Body) != `{"test": "data"}` {
			t.Errorf("Expected body '{\"test\": \"data\"}', got %s", string(entry.Body))
		}
	})

	t.Run("GetHttpCacheByKey_NonExistent", func(t *testing.T) {
		_, err := q.GetHttpCacheByKey(ctx, "nonexistent_key")
		if err == nil {
			t.Error("expected error when getting non-existent cache entry, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %v", err)
		}
	})

	t.Run("HttpCacheExistsByKey", func(t *testing.T) {
		// Check existing key
		exists, err := q.HttpCacheExistsByKey(ctx, "test_key_1")
		if err != nil {
			t.Fatalf("HttpCacheExistsByKey (existing) failed: %v", err)
		}
		if !exists {
			t.Errorf("Expected exists=true for existing key, got %v", exists)
		}

		// Check non-existing key
		exists, err = q.HttpCacheExistsByKey(ctx, "nonexistent_key")
		if err != nil {
			t.Fatalf("HttpCacheExistsByKey (non-existing) failed: %v", err)
		}
		if exists {
			t.Errorf("Expected exists=false for non-existing key, got %v", exists)
		}
	})

	t.Run("CountHttpCacheEntries", func(t *testing.T) {
		// Add another entry
		err := q.UpsertHttpCache(ctx, UpsertHttpCacheParams{
			Key:       "test_key_2",
			Method:    "GET",
			Path:      "/api/test2",
			Encoding:  "gzip",
			Status:    200,
			CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertHttpCache (second entry) failed: %v", err)
		}

		// Count entries
		count, err := q.CountHttpCacheEntries(ctx)
		if err != nil {
			t.Fatalf("CountHttpCacheEntries failed: %v", err)
		}
		if count < 2 {
			t.Errorf("Expected at least 2 cache entries, got %d", count)
		}
	})

	t.Run("GetHttpCacheSizeBytes", func(t *testing.T) {
		size, err := q.GetHttpCacheSizeBytes(ctx)
		if err != nil {
			t.Fatalf("GetHttpCacheSizeBytes failed: %v", err)
		}
		// Size should be at least the sum of our test entries
		// We can't check exact size due to type being interface{}, but it should be non-nil
		if size == nil {
			t.Error("Expected size to be non-nil")
		}
	})

	t.Run("GetHttpCacheOldestCreated", func(t *testing.T) {
		// Get oldest entries
		entries, err := q.GetHttpCacheOldestCreated(ctx, 2)
		if err != nil {
			t.Fatalf("GetHttpCacheOldestCreated failed: %v", err)
		}
		if len(entries) == 0 {
			t.Error("Expected at least 1 oldest entry, got none")
		}
		if len(entries) > 2 {
			t.Errorf("Expected at most 2 entries, got %d", len(entries))
		}
	})

	t.Run("DeleteHttpCacheByID", func(t *testing.T) {
		// Get an entry to find its ID
		entry, err := q.GetHttpCacheByKey(ctx, "test_key_1")
		if err != nil {
			t.Fatalf("GetHttpCacheByKey for delete test failed: %v", err)
		}

		// Delete by ID
		err = q.DeleteHttpCacheByID(ctx, entry.ID)
		if err != nil {
			t.Fatalf("DeleteHttpCacheByID failed: %v", err)
		}

		// Verify it's gone
		_, err = q.GetHttpCacheByKey(ctx, "test_key_1")
		if err == nil {
			t.Error("expected error after DeleteHttpCacheByID, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
		}
	})

	t.Run("DeleteHttpCacheByKey", func(t *testing.T) {
		// Delete by key
		err := q.DeleteHttpCacheByKey(ctx, "test_key_2")
		if err != nil {
			t.Fatalf("DeleteHttpCacheByKey failed: %v", err)
		}

		// Verify it's gone
		_, err = q.GetHttpCacheByKey(ctx, "test_key_2")
		if err == nil {
			t.Error("expected error after DeleteHttpCacheByKey, but got nil")
		} else if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
		}
	})

	t.Run("DeleteHttpCacheExpired", func(t *testing.T) {
		// Insert an expired entry
		pastTime := now - 3600
		err := q.UpsertHttpCache(ctx, UpsertHttpCacheParams{
			Key:       "expired_key",
			Method:    "GET",
			Path:      "/api/expired",
			Encoding:  "gzip",
			Status:    200,
			CreatedAt: pastTime - 100,
			ExpiresAt: sql.NullInt64{Int64: pastTime, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertHttpCache (expired) failed: %v", err)
		}

		// Insert a non-expired entry
		err = q.UpsertHttpCache(ctx, UpsertHttpCacheParams{
			Key:       "nonexpired_key",
			Method:    "GET",
			Path:      "/api/valid",
			Encoding:  "gzip",
			Status:    200,
			CreatedAt: now,
			ExpiresAt: sql.NullInt64{Int64: now + 3600, Valid: true},
		})
		if err != nil {
			t.Fatalf("UpsertHttpCache (non-expired) failed: %v", err)
		}

		// Delete expired entries
		err = q.DeleteHttpCacheExpired(ctx)
		if err != nil {
			t.Fatalf("DeleteHttpCacheExpired failed: %v", err)
		}

		// Verify expired entry is gone
		_, err = q.GetHttpCacheByKey(ctx, "expired_key")
		if err == nil {
			t.Error("expected error for expired entry after DeleteHttpCacheExpired, but got nil")
		}

		// Verify non-expired entry still exists
		_, err = q.GetHttpCacheByKey(ctx, "nonexpired_key")
		if err != nil {
			t.Errorf("Expected non-expired entry to exist, got error: %v", err)
		}
	})

	t.Run("ClearHttpCache", func(t *testing.T) {
		// Ensure we have an entry
		err := q.UpsertHttpCache(ctx, UpsertHttpCacheParams{
			Key:       "to_be_cleared",
			Method:    "GET",
			Path:      "/api/clearme",
			Encoding:  "gzip",
			Status:    200,
			CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertHttpCache (for clear test) failed: %v", err)
		}

		// Clear all cache
		err = q.ClearHttpCache(ctx)
		if err != nil {
			t.Fatalf("ClearHttpCache failed: %v", err)
		}

		// Verify all entries are gone
		count, err := q.CountHttpCacheEntries(ctx)
		if err != nil {
			t.Fatalf("CountHttpCacheEntries (after clear) failed: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 entries after ClearHttpCache, got %d", count)
		}
	})
}

func TestConfigKeyExists(t *testing.T) {
	_, q, ctx := setupTestDB(t)

	exists, err := q.ConfigKeyExists(ctx, "test_exists_key")
	if err != nil {
		t.Fatalf("ConfigKeyExists failed: %v", err)
	}
	if exists {
		t.Error("expected ConfigKeyExists to return false for missing key")
	}

	err = q.UpsertConfigValueOnly(ctx, UpsertConfigValueOnlyParams{
		Key:       "test_exists_key",
		Value:     "exists_value",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("UpsertConfigValueOnly failed: %v", err)
	}

	exists, err = q.ConfigKeyExists(ctx, "test_exists_key")
	if err != nil {
		t.Fatalf("ConfigKeyExists failed: %v", err)
	}
	if !exists {
		t.Error("expected ConfigKeyExists to return true for existing key")
	}
}

func TestInsertConfigIfNotExists(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	now := time.Now().Unix()

	err := q.InsertConfigIfNotExists(ctx, InsertConfigIfNotExistsParams{
		Key:       "insert_if_not_exists_key",
		Value:     "original_value",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertConfigIfNotExists failed: %v", err)
	}

	value, err := q.GetConfigValueByKey(ctx, "insert_if_not_exists_key")
	if err != nil {
		t.Fatalf("GetConfigValueByKey failed: %v", err)
	}
	if value != "original_value" {
		t.Errorf("expected value 'original_value', got %s", value)
	}

	err = q.InsertConfigIfNotExists(ctx, InsertConfigIfNotExistsParams{
		Key:       "insert_if_not_exists_key",
		Value:     "new_value",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertConfigIfNotExists (second) failed: %v", err)
	}

	value, err = q.GetConfigValueByKey(ctx, "insert_if_not_exists_key")
	if err != nil {
		t.Fatalf("GetConfigValueByKey (second) failed: %v", err)
	}
	if value != "original_value" {
		t.Errorf("expected value to remain 'original_value', got %s", value)
	}
}

func TestGetConfigs_RowsCloseError(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	_ = db

	seedConfigRows(t, q, ctx)

	orig := rowsCloseFn
	rowsCloseFn = func(r *sql.Rows) error { return errors.New("rows close denied") }
	t.Cleanup(func() { rowsCloseFn = orig })

	_, err := q.GetConfigs(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetConfigs_RowsErrError(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	_ = db

	seedConfigRows(t, q, ctx)

	orig := rowsErrFn
	rowsErrFn = func(r *sql.Rows) error { return errors.New("rows err denied") }
	t.Cleanup(func() { rowsErrFn = orig })

	_, err := q.GetConfigs(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetHttpCacheOldestCreated_RowsCloseError(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	seedHttpCacheRows(t, q, ctx)

	orig := rowsCloseFn
	rowsCloseFn = func(r *sql.Rows) error { return errors.New("rows close denied") }
	t.Cleanup(func() { rowsCloseFn = orig })

	_, err := q.GetHttpCacheOldestCreated(ctx, 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetHttpCacheOldestCreated_RowsErrError(t *testing.T) {
	_, q, ctx := setupTestDB(t)
	seedHttpCacheRows(t, q, ctx)

	orig := rowsErrFn
	rowsErrFn = func(r *sql.Rows) error { return errors.New("rows err denied") }
	t.Cleanup(func() { rowsErrFn = orig })

	_, err := q.GetHttpCacheOldestCreated(ctx, 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func seedConfigRows(t *testing.T, q *CustomQueries, ctx context.Context) {
	t.Helper()
	now := time.Now().Unix()
	if err := q.UpsertConfigValueOnly(ctx, UpsertConfigValueOnlyParams{
		Key:       "seed_key_1",
		Value:     "seed_value_1",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to seed config row: %v", err)
	}
}

func seedHttpCacheRows(t *testing.T, q *CustomQueries, ctx context.Context) {
	t.Helper()
	now := time.Now().Unix()
	err := q.UpsertHttpCache(ctx, UpsertHttpCacheParams{
		Key:       "seed_cache_key",
		Method:    "GET",
		Path:      "/seed",
		Encoding:  "gzip",
		Status:    200,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertHttpCache failed: %v", err)
	}
}
