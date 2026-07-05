package config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

const bootstrapLogDir = "logs"

// EnsureBootstrapDefaults ensures the database contains the minimum default
// configuration: admin credentials, logging settings, and any missing default
// keys. It operates purely against the sqlc-generated Querier interface so it
// can be tested without a full ConfigService.
func EnsureBootstrapDefaults(ctx context.Context, rootDir string, queries gallerydb.Querier) error {
	now := time.Now().Unix()

	userExists, err := queries.ConfigKeyExists(ctx, "user")
	if err != nil {
		return fmt.Errorf("ensure defaults: check user exists: %w", err)
	}

	if !userExists {
		hashed, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("ensure defaults: bcrypt: %w", err)
		}

		adminDefaults := map[string]string{
			"user":                "admin",
			"password":            string(hashed),
			"log_directory":       filepath.Join(rootDir, bootstrapLogDir),
			"log_level":           "debug",
			"log_rollover":        "weekly",
			"log_retention_count": "7",
		}
		for k, v := range adminDefaults {
			if err := queries.InsertConfigIfNotExists(ctx, gallerydb.InsertConfigIfNotExistsParams{
				Key: k, Value: v, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return fmt.Errorf("ensure defaults: insert admin default %q: %w", k, err)
			}
		}
	}

	cfg := DefaultConfig()
	if rootDir != "" {
		cfg.LogDirectory = filepath.Join(rootDir, bootstrapLogDir)
		if cfg.ImageDirectory == "" {
			cfg.ImageDirectory = filepath.Join(rootDir, "Images")
		}
	}
	defaults := cfg.ToMap()

	for key, value := range defaults {
		if key == "user" || key == "password" {
			continue
		}

		currentValue, err := queries.GetConfigValueByKey(ctx, key)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("ensure defaults: get current value %q: %w", key, err)
		}

		missing := errors.Is(err, sql.ErrNoRows)
		emptyCritical := currentValue == "" && (key == "image_directory" || key == "log_directory")
		if !missing && !emptyCritical {
			continue
		}

		if err := queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
			Key: key, Value: value, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("ensure defaults: upsert default %q: %w", key, err)
		}
	}

	return nil
}
