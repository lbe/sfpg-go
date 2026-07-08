//go:build integration

package config

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/migrations"
)

// createTestService creates a ConfigService with temporary file-based database pools for testing.
func createTestService(t *testing.T) ConfigService {
	t.Helper()
	ctx := context.Background()

	// Use a temporary file-based database so both pools share the same database
	tempDir := t.TempDir()
	tempDB := filepath.Join(tempDir, "test.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")

	// Run migrations on the database before creating pools
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(tempDB))
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		db.Close()
		t.Fatalf("failed to create sqlite driver instance: %v", err)
	}

	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		db.Close()
		t.Fatalf("failed to create iofs source driver: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create migrate instance: %v", err)
	}

	if upErr := m.Up(); upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		db.Close()
		t.Fatalf("failed to apply migrations: %v", upErr)
	}
	db.Close()

	m2, err := migrations.NewThumbsMigrator(thumbsDBPath)
	if err != nil {
		t.Fatalf("failed to create thumbs migrator: %v", err)
	}
	if thumbsErr := m2.Up(); thumbsErr != nil && !errors.Is(thumbsErr, migrate.ErrNoChange) {
		m2.Close()
		t.Fatalf("failed to run thumbs migrations: %v", thumbsErr)
	}
	m2.Close()

	roDSN := "file:" + filepath.ToSlash(tempDB) + "?mode=ro"
	rwDSN := "file:" + filepath.ToSlash(tempDB) + "?_txlock=immediate&mode=rwc"

	roPool, err := dbconnpool.NewDbSQLConnPool(ctx, roDSN, dbconnpool.Config{
		DriverName:         "sqlite3",
		MaxConnections:     10,
		MinIdleConnections: 1,
		ReadOnly:           true,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsDBPath,
	})
	if err != nil {
		t.Fatalf("failed to create RO pool: %v", err)
	}
	t.Cleanup(func() { _ = roPool.Close() })

	rwPool, err := dbconnpool.NewDbSQLConnPool(ctx, rwDSN, dbconnpool.Config{
		DriverName:         "sqlite3",
		MaxConnections:     10,
		MinIdleConnections: 1,
		ReadOnly:           false,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsDBPath,
	})
	if err != nil {
		t.Fatalf("failed to create RW pool: %v", err)
	}
	t.Cleanup(func() { _ = rwPool.Close() })

	return NewService(rwPool, roPool)
}

func TestConfigService_Load_Integration(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		wantErr bool
	}{
		{
			name:    "valid context",
			ctx:     context.Background(),
			wantErr: false,
		},
		{
			name:    "cancelled context",
			ctx:     func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }(),
			wantErr: true,
		},
	}

	svc := createTestService(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := svc.Load(tt.ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConfigService.Load() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && cfg == nil {
				t.Error("ConfigService.Load() returned nil config without error")
			}
		})
	}
}

func TestConfigService_Save_Integration(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		cfg     *Config
		wantErr bool
	}{
		{
			name:    "valid config",
			ctx:     context.Background(),
			cfg:     DefaultConfig(),
			wantErr: false,
		},
		{
			name:    "nil config",
			ctx:     context.Background(),
			cfg:     nil,
			wantErr: true,
		},
		{
			name:    "cancelled context",
			ctx:     func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }(),
			cfg:     DefaultConfig(),
			wantErr: true,
		},
	}

	svc := createTestService(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.Save(tt.ctx, tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConfigService.Save() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigService_Save_NilConfig_ReturnsErrNilConfig_Integration(t *testing.T) {
	svc := createTestService(t)
	err := svc.Save(context.Background(), nil)

	if !errors.Is(err, ErrNilConfig) {
		t.Errorf("ConfigService.Save(nil) returned %v, expected ErrNilConfig", err)
	}
}

func TestConfigService_Validate_Integration(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name:    "valid config",
			cfg:     DefaultConfig(),
			wantErr: false,
		},
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: true,
		},
		{
			name: "invalid listener port",
			cfg: func() *Config {
				cfg := DefaultConfig()
				cfg.ListenerPort = -1
				return cfg
			}(),
			wantErr: true,
		},
		{
			name: "invalid session same-site",
			cfg: func() *Config {
				cfg := DefaultConfig()
				cfg.SessionSameSite = "Invalid"
				return cfg
			}(),
			wantErr: true,
		},
	}

	svc := createTestService(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.Validate(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConfigService.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigService_Validate_NilConfig_ReturnsErrNilConfig_Integration(t *testing.T) {
	svc := createTestService(t)
	err := svc.Validate(nil)

	if !errors.Is(err, ErrNilConfig) {
		t.Errorf("ConfigService.Validate(nil) returned %v, expected ErrNilConfig", err)
	}
}

func TestConfigService_Export_Integration(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "export valid config",
			wantErr: false,
		},
	}

	svc := createTestService(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yamlContent, err := svc.Export(context.Background())
			if (err != nil) != tt.wantErr {
				t.Errorf("ConfigService.Export() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && yamlContent == "" {
				t.Error("ConfigService.Export() returned empty YAML without error")
			}
		})
	}

	t.Run("Load error", func(t *testing.T) {
		svc := createTestService(t).(*configService)
		if err := svc.dbRoPool.Close(); err != nil {
			t.Fatalf("failed to close RO pool: %v", err)
		}

		_, err := svc.Export(context.Background())
		if err == nil {
			t.Fatal("Export expected error after closing RO pool, got nil")
		}
	})
}

func TestConfigService_Import_Integration(t *testing.T) {
	tests := []struct {
		name        string
		yamlContent string
		ctx         context.Context
		wantErr     bool
	}{
		{
			name:        "valid YAML",
			yamlContent: "listener_port: 8080\nsite_name: Test Site",
			ctx:         context.Background(),
			wantErr:     false,
		},
		{
			name:        "invalid YAML",
			yamlContent: "invalid: yaml: content: [",
			ctx:         context.Background(),
			wantErr:     true,
		},
		{
			name:        "empty YAML",
			yamlContent: "",
			ctx:         context.Background(),
			wantErr:     false,
		},
		{
			name:        "cancelled context",
			yamlContent: "listener_port: 8080",
			ctx:         func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }(),
			wantErr:     true,
		},
	}

	svc := createTestService(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.Import(tt.yamlContent, tt.ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConfigService.Import() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigService_RestoreLastKnownGood_Integration(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		wantErr bool
	}{
		{
			name:    "valid context",
			ctx:     context.Background(),
			wantErr: false,
		},
		{
			name:    "cancelled context",
			ctx:     func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }(),
			wantErr: true,
		},
	}

	svc := createTestService(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.wantErr {
				if err := svc.Save(context.Background(), DefaultConfig()); err != nil {
					t.Fatalf("failed to save initial config for RestoreLastKnownGood test: %v", err)
				}
			}

			cfg, err := svc.RestoreLastKnownGood(tt.ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConfigService.RestoreLastKnownGood() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && cfg == nil {
				t.Error("ConfigService.RestoreLastKnownGood() returned nil config without error")
			}
		})
	}
}

func TestConfigService_EnsureDefaults_CreatesEnableCachePreload_Integration(t *testing.T) {
	svc := createTestService(t)
	rootDir := t.TempDir()

	if err := svc.EnsureDefaults(context.Background(), rootDir); err != nil {
		t.Fatalf("EnsureDefaults failed: %v", err)
	}

	val, err := svc.GetConfigValue(context.Background(), "enable_cache_preload")
	if err != nil {
		t.Fatalf("GetConfigValue(enable_cache_preload) failed: %v", err)
	}
	if val != "true" {
		t.Errorf("expected enable_cache_preload 'true' after EnsureDefaults, got %q", val)
	}
}

func TestConfigService_EnsureDefaultsAndGetConfigValue_Integration(t *testing.T) {
	svc := createTestService(t)
	rootDir := t.TempDir()

	if err := svc.EnsureDefaults(context.Background(), rootDir); err != nil {
		t.Fatalf("EnsureDefaults failed: %v", err)
	}

	logDir, err := svc.GetConfigValue(context.Background(), "log_directory")
	if err != nil {
		t.Fatalf("GetConfigValue failed: %v", err)
	}

	expected := filepath.Join(rootDir, "logs")
	if logDir != expected {
		t.Fatalf("expected log_directory %q, got %q", expected, logDir)
	}

	imageDir, err := svc.GetConfigValue(context.Background(), "image_directory")
	if err != nil {
		t.Fatalf("GetConfigValue failed: %v", err)
	}

	expectedImages := filepath.Join(rootDir, "Images")
	if imageDir != expectedImages {
		t.Fatalf("expected image_directory %q, got %q", expectedImages, imageDir)
	}
}

func TestConfigService_EnsureDefaults_UpdatesEmptyImageDirectory_Integration(t *testing.T) {
	svc := createTestService(t).(*configService)
	rootDir := t.TempDir()
	ctx := context.Background()

	cpcRw, err := svc.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get connection: %v", err)
	}
	defer svc.dbRwPool.Put(cpcRw)

	now := time.Now().Unix()
	err = cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "image_directory",
		Value:     "", // empty value
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to insert empty image_directory: %v", err)
	}

	emptyVal, err := svc.GetConfigValue(ctx, "image_directory")
	if err != nil {
		t.Fatalf("GetConfigValue failed: %v", err)
	}
	if emptyVal != "" {
		t.Fatalf("expected empty image_directory, got %q", emptyVal)
	}

	if cfgErr := svc.EnsureDefaults(ctx, rootDir); cfgErr != nil {
		t.Fatalf("EnsureDefaults failed: %v", cfgErr)
	}

	imageDir, err := svc.GetConfigValue(ctx, "image_directory")
	if err != nil {
		t.Fatalf("GetConfigValue failed after EnsureDefaults: %v", err)
	}

	expectedImages := filepath.Join(rootDir, "Images")
	if imageDir != expectedImages {
		t.Fatalf("expected image_directory %q after EnsureDefaults, got %q", expectedImages, imageDir)
	}
}

func TestConfigService_EnsureDefaults_WhenConfigExists_Integration(t *testing.T) {
	svc := createTestService(t).(*configService)
	ctx := context.Background()

	cpcRw, err := svc.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW connection: %v", err)
	}
	now := time.Now().Unix()
	err = cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key: "log_level", Value: "debug", CreatedAt: now, UpdatedAt: now,
	})
	svc.dbRwPool.Put(cpcRw)
	if err != nil {
		t.Fatalf("failed to seed config table: %v", err)
	}

	if err := svc.EnsureDefaults(ctx, t.TempDir()); err != nil {
		t.Fatalf("EnsureDefaults failed: %v", err)
	}
}

func TestConfigService_GetConfigValue_Missing_Integration(t *testing.T) {
	svc := createTestService(t)
	if _, err := svc.GetConfigValue(context.Background(), "missing_key"); err == nil {
		t.Fatal("expected error for missing config key")
	}
}

func TestConfigService_IncrementETag_ValidationFailure_Integration(t *testing.T) {
	svc := createTestService(t)
	ctx := context.Background()

	cfg, err := svc.Load(ctx)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	originalETag := cfg.ETagVersion

	// Corrupt the config with a cross-field violation that LoadFromDatabase
	// accepts but Validate rejects.
	cfg.DBMaxPoolSize = 5
	cfg.DBMinIdleConnections = 10
	if err = svc.Save(ctx, cfg); err != nil {
		t.Fatalf("Save corrupted config: %v", err)
	}

	_, err = svc.IncrementETag(ctx)
	if err == nil {
		t.Fatal("IncrementETag expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid config after ETag increment") {
		t.Fatalf("IncrementETag error = %v, want wrapped validation error", err)
	}

	// Verify the ETag was not changed in the database.
	reloaded, err := svc.Load(ctx)
	if err != nil {
		t.Fatalf("Reload config: %v", err)
	}
	if reloaded.ETagVersion != originalETag {
		t.Errorf("ETag changed to %q after failed IncrementETag, want %q", reloaded.ETagVersion, originalETag)
	}
}

func TestConfigService_IncrementETag_LoadError_Integration(t *testing.T) {
	svc := createTestService(t).(*configService)
	if err := svc.dbRoPool.Close(); err != nil {
		t.Fatalf("failed to close RO pool: %v", err)
	}

	_, err := svc.IncrementETag(context.Background())
	if err == nil {
		t.Fatal("IncrementETag expected Load error after closing RO pool, got nil")
	}
}

func TestConfigService_IncrementETag_SaveError_Integration(t *testing.T) {
	svc := createTestService(t).(*configService)
	if err := svc.dbRwPool.Close(); err != nil {
		t.Fatalf("failed to close RW pool: %v", err)
	}

	_, err := svc.IncrementETag(context.Background())
	if err == nil {
		t.Fatal("IncrementETag expected Save error after closing RW pool, got nil")
	}
}
