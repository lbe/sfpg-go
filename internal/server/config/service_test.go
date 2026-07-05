package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

func TestEnsureDefaults_Delegates(t *testing.T) {
	service := &fakeService{}
	EnsureDefaults(context.Background(), "/tmp", service, nil)
	if service.ensureRoot != "/tmp" {
		t.Fatalf("expected EnsureDefaults to be called with rootDir /tmp, got %q", service.ensureRoot)
	}
}

func TestEnsureDefaults_NoService(t *testing.T) {
	EnsureDefaults(context.Background(), "/tmp", nil, nil)
}

func TestEnsureDefaults_PanicsOnError(t *testing.T) {
	service := &fakeService{ensureErr: fmt.Errorf("boom")}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when EnsureDefaults returns error")
		}
	}()
	EnsureDefaults(context.Background(), "/tmp", service, nil)
}

func TestConfigService_Load(t *testing.T) {
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

func TestConfigService_Save(t *testing.T) {
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

func TestConfigService_Validate(t *testing.T) {
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

func TestConfigService_Export(t *testing.T) {
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
}

func TestConfigService_Import(t *testing.T) {
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

func TestConfigService_RestoreLastKnownGood(t *testing.T) {
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

func TestConfigService_Save_NilConfig_ReturnsErrNilConfig(t *testing.T) {
	svc := createTestService(t)
	err := svc.Save(context.Background(), nil)

	if !errors.Is(err, ErrNilConfig) {
		t.Errorf("ConfigService.Save(nil) returned %v, expected ErrNilConfig", err)
	}
}

func TestConfigService_Validate_NilConfig_ReturnsErrNilConfig(t *testing.T) {
	svc := createTestService(t)
	err := svc.Validate(nil)

	if !errors.Is(err, ErrNilConfig) {
		t.Errorf("ConfigService.Validate(nil) returned %v, expected ErrNilConfig", err)
	}
}

func TestConfigService_EnsureDefaults_CreatesEnableCachePreload(t *testing.T) {
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

func TestConfigService_EnsureDefaultsAndGetConfigValue(t *testing.T) {
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

func TestConfigService_EnsureDefaults_UpdatesEmptyImageDirectory(t *testing.T) {
	svc := createTestService(t).(*configService)
	rootDir := t.TempDir()
	ctx := context.Background()

	// First, insert an empty image_directory into the database (simulating cold start with empty value)
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

	// Verify it's empty
	emptyVal, err := svc.GetConfigValue(ctx, "image_directory")
	if err != nil {
		t.Fatalf("GetConfigValue failed: %v", err)
	}
	if emptyVal != "" {
		t.Fatalf("expected empty image_directory, got %q", emptyVal)
	}

	// Now call EnsureDefaults - it should update the empty value
	if cfgErr := svc.EnsureDefaults(ctx, rootDir); cfgErr != nil {
		t.Fatalf("EnsureDefaults failed: %v", err)
	}

	// Verify it's now set to the default
	imageDir, err := svc.GetConfigValue(ctx, "image_directory")
	if err != nil {
		t.Fatalf("GetConfigValue failed after EnsureDefaults: %v", err)
	}

	expectedImages := filepath.Join(rootDir, "Images")
	if imageDir != expectedImages {
		t.Fatalf("expected image_directory %q after EnsureDefaults, got %q", expectedImages, imageDir)
	}
}

func TestConfigService_GetConfigValue_Missing(t *testing.T) {
	svc := createTestService(t)
	if _, err := svc.GetConfigValue(context.Background(), "missing_key"); err == nil {
		t.Fatal("expected error for missing config key")
	}
}

func TestConfigService_EnsureDefaults_WhenConfigExists(t *testing.T) {
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
