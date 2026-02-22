package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/gallerydb"
	"golang.org/x/crypto/bcrypt"
)

// ErrNilConfig is returned when a nil config is passed to Save or Validate.
var ErrNilConfig = errors.New("config cannot be nil")

// ConfigService provides a high-level interface for configuration management.
// It abstracts away the details of loading, saving, validating, and exporting configuration.
type ConfigService interface {
	// Load loads the current configuration from the database.
	Load(ctx context.Context) (*Config, error)

	// Save saves the configuration to the database.
	Save(ctx context.Context, cfg *Config) error

	// Validate validates the configuration and returns an error if invalid.
	Validate(cfg *Config) error

	// Export exports the configuration as a YAML string.
	Export() (string, error)

	// Import imports configuration from a YAML string and saves it to the database.
	Import(yamlContent string, ctx context.Context) error

	// RestoreLastKnownGood restores the last known good configuration from the database.
	RestoreLastKnownGood(ctx context.Context) (*Config, error)

	// EnsureDefaults ensures default config (admin creds, default keys) exists in the database.
	// rootDir is used for default paths (e.g. log_directory). Call when configService is available.
	EnsureDefaults(ctx context.Context, rootDir string) error

	// GetConfigValue returns the value for key from the config table, or error if not found.
	GetConfigValue(ctx context.Context, key string) (string, error)

	// IncrementETag increments the ETag version in the database and returns the new value.
	IncrementETag(ctx context.Context) (string, error)
}

// configService is the default implementation of ConfigService.
type configService struct {
	dbRwPool *dbconnpool.DbSQLConnPool
	dbRoPool *dbconnpool.DbSQLConnPool
}

// NewService creates a new ConfigService instance.
// It accepts database connection pools for read-write and read-only operations.
func NewService(dbRwPool, dbRoPool *dbconnpool.DbSQLConnPool) ConfigService {
	return &configService{
		dbRwPool: dbRwPool,
		dbRoPool: dbRoPool,
	}
}

// Load loads the current configuration from the database.
func (s *configService) Load(ctx context.Context) (*Config, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cpcRo, err := s.dbRoPool.Get()
	if err != nil {
		return nil, err
	}
	defer s.dbRoPool.Put(cpcRo)

	cfg := DefaultConfig()
	if err := cfg.LoadFromDatabase(ctx, cpcRo.Queries); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Save saves the configuration to the database.
func (s *configService) Save(ctx context.Context, cfg *Config) error {
	if cfg == nil {
		return ErrNilConfig
	}
	cpcRw, err := s.dbRwPool.Get()
	if err != nil {
		return err
	}
	defer s.dbRwPool.Put(cpcRw)

	return cfg.SaveToDatabase(ctx, cpcRw.Queries)
}

// Validate validates the configuration and returns an error if invalid.
func (s *configService) Validate(cfg *Config) error {
	if cfg == nil {
		return ErrNilConfig
	}
	return cfg.Validate()
}

// Export exports the configuration as a YAML string.
func (s *configService) Export() (string, error) {
	// Export requires a config instance, but we don't have one in the service.
	// We need to load the current config first, then export it.
	ctx := context.Background()
	cfg, err := s.Load(ctx)
	if err != nil {
		return "", err
	}
	return cfg.ExportToYAML()
}

// Import imports configuration from a YAML string and saves it to the database.
func (s *configService) Import(yamlContent string, ctx context.Context) error {
	cpcRw, err := s.dbRwPool.Get()
	if err != nil {
		return err
	}
	defer s.dbRwPool.Put(cpcRw)

	// Load current config as base
	cfg, err := s.Load(ctx)
	if err != nil {
		return err
	}

	// Import the YAML (this validates and saves)
	return cfg.ImportFromYAML(yamlContent, ctx, cpcRw.Queries)
}

// RestoreLastKnownGood restores the last known good configuration from the database.
func (s *configService) RestoreLastKnownGood(ctx context.Context) (*Config, error) {
	cpcRo, err := s.dbRoPool.Get()
	if err != nil {
		return nil, err
	}
	defer s.dbRoPool.Put(cpcRo)

	// Create a temporary config to call RestoreLastKnownGood
	cfg := DefaultConfig()
	return cfg.RestoreLastKnownGood(ctx, cpcRo.Queries)
}

const bootstrapLogDir = "logs"

// EnsureDefaults ensures default config (admin creds, default keys) exists in the database.
func (s *configService) EnsureDefaults(ctx context.Context, rootDir string) error {
	cpcRw, err := s.dbRwPool.Get()
	if err != nil {
		return fmt.Errorf("ensure defaults: get rw connection: %w", err)
	}
	defer s.dbRwPool.Put(cpcRw)

	var userExists bool
	if err := cpcRw.Conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM config WHERE key = 'user')").Scan(&userExists); err != nil {
		return fmt.Errorf("ensure defaults: check user: %w", err)
	}
	if !userExists {
		hashed, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("ensure defaults: bcrypt: %w", err)
		}
		defaults := map[string]string{
			"user":                "admin",
			"password":            string(hashed),
			"log_directory":       filepath.Join(rootDir, bootstrapLogDir),
			"log_level":           "debug",
			"log_rollover":        "weekly",
			"log_retention_count": "7",
		}
		for k, v := range defaults {
			if _, err := cpcRw.Conn.ExecContext(ctx, "INSERT OR IGNORE INTO config (key, value) VALUES (?, ?)", k, v); err != nil {
				return fmt.Errorf("ensure defaults: insert %q: %w", k, err)
			}
		}
	}

	cfg := DefaultConfig()
	if cfg.ImageDirectory == "" && rootDir != "" {
		cfg.ImageDirectory = filepath.Join(rootDir, "Images")
	}
	configMap := cfg.ToMap()
	now := time.Now().Unix()
	for key, value := range configMap {
		if key == "user" || key == "password" {
			continue
		}
		var exists bool
		if err := cpcRw.Conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM config WHERE key = ?)", key).Scan(&exists); err != nil {
			slog.Warn("ensure defaults: check exists", "key", key, "err", err)
			continue
		}
		if !exists {
			if err := cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
				Key: key, Value: value, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				slog.Warn("ensure defaults: upsert", "key", key, "err", err)
			}
		} else {
			// If key exists, check if it's empty and update critical defaults
			var currentValue string
			if err := cpcRw.Conn.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", key).Scan(&currentValue); err != nil {
				slog.Warn("ensure defaults: get current value", "key", key, "err", err)
				continue
			}
			// Update empty critical values (image_directory, log_directory)
			if currentValue == "" && (key == "image_directory" || key == "log_directory") {
				if err := cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
					Key: key, Value: value, CreatedAt: now, UpdatedAt: now,
				}); err != nil {
					slog.Warn("ensure defaults: update empty critical value", "key", key, "err", err)
				}
			}
		}
	}
	return nil
}

// GetConfigValue returns the value for key from the config table.
func (s *configService) GetConfigValue(ctx context.Context, key string) (string, error) {
	cpcRo, err := s.dbRoPool.Get()
	if err != nil {
		return "", err
	}
	defer s.dbRoPool.Put(cpcRo)
	v, err := cpcRo.Queries.GetConfigValueByKey(ctx, key)
	if err != nil {
		return "", fmt.Errorf("get config value %q: %w", key, err)
	}
	return v, nil
}

// IncrementETag increments the ETag version in the database and returns the new value.
func (s *configService) IncrementETag(ctx context.Context) (string, error) {
	cfg, err := s.Load(ctx)
	if err != nil {
		return "", err
	}

	newETag := IncrementETagVersion(cfg.ETagVersion)
	cfg.ETagVersion = newETag

	if err := s.Save(ctx, cfg); err != nil {
		return "", err
	}

	return newETag, nil
}
