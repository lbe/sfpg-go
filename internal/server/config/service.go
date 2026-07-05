package config

import (
	"context"
	"errors"
	"fmt"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

// ErrNilConfig is returned when a nil config is passed to Save or Validate.
var ErrNilConfig = errors.New("config cannot be nil")

// ConfigStore provides loading, saving, and validating configuration data.
type ConfigStore interface {
	// Load loads the current configuration from the database.
	Load(ctx context.Context) (*Config, error)

	// Save saves the configuration to the database.
	Save(ctx context.Context, cfg *Config) error

	// Validate validates the configuration and returns an error if invalid.
	Validate(cfg *Config) error
}

// ConfigAdmin provides admin-level configuration operations.
type ConfigAdmin interface {
	// Export exports the configuration as a YAML string.
	Export(ctx context.Context) (string, error)

	// Import imports configuration from a YAML string and saves it to the database.
	Import(yamlContent string, ctx context.Context) error

	// RestoreLastKnownGood restores the last known good configuration from the database.
	RestoreLastKnownGood(ctx context.Context) (*Config, error)

	// EnsureDefaults ensures default config (admin creds, default keys) exists in the database.
	// rootDir is used for default paths (e.g. log_directory).
	EnsureDefaults(ctx context.Context, rootDir string) error

	// GetConfigValue returns the value for key from the config table, or error if not found.
	GetConfigValue(ctx context.Context, key string) (string, error)

	// IncrementETag increments the ETag version in the database and returns the new value.
	IncrementETag(ctx context.Context) (string, error)
}

// ConfigService is the union of ConfigStore and ConfigAdmin for backward compatibility.
type ConfigService interface {
	ConfigStore
	ConfigAdmin
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
func (s *configService) Export(ctx context.Context) (string, error) {
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

// EnsureDefaults ensures default config (admin creds, default keys) exists in the database.
func (s *configService) EnsureDefaults(ctx context.Context, rootDir string) error {
	cpcRw, err := s.dbRwPool.Get()
	if err != nil {
		return fmt.Errorf("ensure defaults: get rw connection: %w", err)
	}
	defer s.dbRwPool.Put(cpcRw)

	return EnsureBootstrapDefaults(ctx, rootDir, cpcRw.Queries)
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

	if err := s.Validate(cfg); err != nil {
		return "", fmt.Errorf("invalid config after ETag increment: %w", err)
	}

	if err := s.Save(ctx, cfg); err != nil {
		return "", err
	}

	return newETag, nil
}
