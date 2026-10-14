package config

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

func resetConfigTable(t *testing.T, svc *configService) {
	t.Helper()
	ctx := context.Background()
	cpc, err := svc.dbRwPool.Get()
	if err != nil {
		t.Fatalf("reset config table: get conn: %v", err)
	}
	defer svc.dbRwPool.Put(cpc)
	if _, err := cpc.Conn.ExecContext(ctx, "DELETE FROM config"); err != nil {
		t.Fatalf("reset config table: %v", err)
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestConfigService(t *testing.T) {
	svc := sharedConfigService()
	cs := svc.(*configService)

	t.Run("Load", func(t *testing.T) {
		resetConfigTable(t, cs)
		for _, tt := range []struct {
			name    string
			ctx     context.Context
			wantErr bool
		}{
			{"valid context", context.Background(), false},
			{"cancelled context", cancelledContext(), true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				cfg, err := svc.Load(tt.ctx)
				if (err != nil) != tt.wantErr {
					t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if !tt.wantErr && cfg == nil {
					t.Error("Load() returned nil config without error")
				}
			})
		}
	})

	t.Run("Save", func(t *testing.T) {
		resetConfigTable(t, cs)
		for _, tt := range []struct {
			name    string
			ctx     context.Context
			cfg     *Config
			wantErr bool
		}{
			{"valid config", context.Background(), DefaultConfig(), false},
			{"nil config", context.Background(), nil, true},
			{"cancelled context", cancelledContext(), DefaultConfig(), true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				err := svc.Save(tt.ctx, tt.cfg)
				if (err != nil) != tt.wantErr {
					t.Errorf("Save() error = %v, wantErr %v", err, tt.wantErr)
				}
			})
		}
		if err := svc.Save(context.Background(), nil); !errors.Is(err, ErrNilConfig) {
			t.Errorf("Save(nil) = %v, want ErrNilConfig", err)
		}
	})

	t.Run("Validate", func(t *testing.T) {
		for _, tt := range []struct {
			name    string
			cfg     *Config
			wantErr bool
		}{
			{"valid config", DefaultConfig(), false},
			{"nil config", nil, true},
			{"invalid listener port", func() *Config {
				c := DefaultConfig()
				c.ListenerPort = -1
				return c
			}(), true},
			{"invalid session same-site", func() *Config {
				c := DefaultConfig()
				c.SessionSameSite = "Invalid"
				return c
			}(), true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				err := svc.Validate(tt.cfg)
				if (err != nil) != tt.wantErr {
					t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				}
			})
		}
		if err := svc.Validate(nil); !errors.Is(err, ErrNilConfig) {
			t.Errorf("Validate(nil) = %v, want ErrNilConfig", err)
		}
	})

	t.Run("Export", func(t *testing.T) {
		resetConfigTable(t, cs)
		yamlContent, err := svc.Export(context.Background())
		if err != nil {
			t.Fatalf("Export() error = %v", err)
		}
		if yamlContent == "" {
			t.Error("Export() returned empty YAML")
		}
	})

	t.Run("Import", func(t *testing.T) {
		resetConfigTable(t, cs)
		for _, tt := range []struct {
			name        string
			yamlContent string
			ctx         context.Context
			wantErr     bool
		}{
			{"valid YAML", "listener_port: 8080\nsite_name: Test Site", context.Background(), false},
			{"invalid YAML", "invalid: yaml: content: [", context.Background(), true},
			{"empty YAML", "", context.Background(), false},
			{"cancelled context", "listener_port: 8080", cancelledContext(), true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				err := svc.Import(tt.yamlContent, tt.ctx)
				if (err != nil) != tt.wantErr {
					t.Errorf("Import() error = %v, wantErr %v", err, tt.wantErr)
				}
			})
		}
	})

	t.Run("RestoreLastKnownGood", func(t *testing.T) {
		resetConfigTable(t, cs)
		for _, tt := range []struct {
			name    string
			ctx     context.Context
			wantErr bool
		}{
			{"valid context", context.Background(), false},
			{"cancelled context", cancelledContext(), true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				if !tt.wantErr {
					if err := svc.Save(context.Background(), DefaultConfig()); err != nil {
						t.Fatalf("save for restore test: %v", err)
					}
				}
				cfg, err := svc.RestoreLastKnownGood(tt.ctx)
				if (err != nil) != tt.wantErr {
					t.Errorf("RestoreLastKnownGood() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if !tt.wantErr && cfg == nil {
					t.Error("RestoreLastKnownGood() returned nil config")
				}
			})
		}
	})

	t.Run("EnsureDefaults", func(t *testing.T) {
		resetConfigTable(t, cs)
		rootDir := t.TempDir()

		if err := svc.EnsureDefaults(context.Background(), rootDir); err != nil {
			t.Fatalf("EnsureDefaults: %v", err)
		}
		val, err := svc.GetConfigValue(context.Background(), "enable_cache_preload")
		if err != nil {
			t.Fatalf("GetConfigValue(enable_cache_preload): %v", err)
		}
		if val != "true" {
			t.Errorf("enable_cache_preload = %q, want true", val)
		}
		logDir, err := svc.GetConfigValue(context.Background(), "log_directory")
		if err != nil {
			t.Fatalf("GetConfigValue(log_directory): %v", err)
		}
		if want := filepath.Join(rootDir, "logs"); logDir != want {
			t.Fatalf("log_directory = %q, want %q", logDir, want)
		}
		imageDir, err := svc.GetConfigValue(context.Background(), "image_directory")
		if err != nil {
			t.Fatalf("GetConfigValue(image_directory): %v", err)
		}
		if want := filepath.Join(rootDir, "Images"); imageDir != want {
			t.Fatalf("image_directory = %q, want %q", imageDir, want)
		}

		t.Run("updates empty image_directory", func(t *testing.T) {
			resetConfigTable(t, cs)
			root := t.TempDir()
			ctx := context.Background()
			cpcRw, err := cs.dbRwPool.Get()
			if err != nil {
				t.Fatalf("get conn: %v", err)
			}
			defer cs.dbRwPool.Put(cpcRw)
			now := time.Now().Unix()
			if err = cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
				Key: "image_directory", Value: "", CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatalf("seed empty image_directory: %v", err)
			}
			if err = svc.EnsureDefaults(ctx, root); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			got, err := svc.GetConfigValue(ctx, "image_directory")
			if err != nil {
				t.Fatalf("GetConfigValue: %v", err)
			}
			if want := filepath.Join(root, "Images"); got != want {
				t.Fatalf("image_directory = %q, want %q", got, want)
			}
		})

		t.Run("when config already exists", func(t *testing.T) {
			resetConfigTable(t, cs)
			ctx := context.Background()
			cpcRw, err := cs.dbRwPool.Get()
			if err != nil {
				t.Fatalf("get conn: %v", err)
			}
			now := time.Now().Unix()
			err = cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
				Key: "log_level", Value: "debug", CreatedAt: now, UpdatedAt: now,
			})
			cs.dbRwPool.Put(cpcRw)
			if err != nil {
				t.Fatalf("seed log_level: %v", err)
			}
			if err = svc.EnsureDefaults(ctx, t.TempDir()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
		})
	})

	t.Run("GetConfigValue", func(t *testing.T) {
		resetConfigTable(t, cs)
		if _, err := svc.GetConfigValue(context.Background(), "missing_key"); err == nil {
			t.Fatal("expected error for missing config key")
		}
	})

	t.Run("IncrementETag", func(t *testing.T) {
		resetConfigTable(t, cs)
		ctx := context.Background()
		cfg, err := svc.Load(ctx)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		originalETag := cfg.ETagVersion
		cfg.DBMaxPoolSize = 5
		cfg.DBMinIdleConnections = 10
		if err = svc.Save(ctx, cfg); err != nil {
			t.Fatalf("Save corrupted config: %v", err)
		}
		_, err = svc.IncrementETag(ctx)
		if err == nil {
			t.Fatal("IncrementETag expected validation error")
		}
		if !strings.Contains(err.Error(), "invalid config after ETag increment") {
			t.Fatalf("IncrementETag error = %v", err)
		}
		reloaded, err := svc.Load(ctx)
		if err != nil {
			t.Fatalf("Reload: %v", err)
		}
		if reloaded.ETagVersion != originalETag {
			t.Errorf("ETag changed to %q after failed increment, want %q", reloaded.ETagVersion, originalETag)
		}
	})
}

func TestConfigService_closedPoolErrors(t *testing.T) {
	t.Run("Export after RO pool closed", func(t *testing.T) {
		cs := newIsolatedConfigService(t)
		if err := cs.dbRoPool.Close(); err != nil {
			t.Fatalf("close RO: %v", err)
		}
		_, err := cs.Export(context.Background())
		if err == nil {
			t.Fatal("Export expected error after closing RO pool")
		}
	})

	t.Run("IncrementETag after RO pool closed", func(t *testing.T) {
		cs := newIsolatedConfigService(t)
		if err := cs.dbRoPool.Close(); err != nil {
			t.Fatalf("close RO: %v", err)
		}
		_, err := cs.IncrementETag(context.Background())
		if err == nil {
			t.Fatal("IncrementETag expected Load error after closing RO pool")
		}
	})

	t.Run("IncrementETag after RW pool closed", func(t *testing.T) {
		cs := newIsolatedConfigService(t)
		if err := cs.dbRwPool.Close(); err != nil {
			t.Fatalf("close RW: %v", err)
		}
		_, err := cs.IncrementETag(context.Background())
		if err == nil {
			t.Fatal("IncrementETag expected Save error after closing RW pool")
		}
	})
}
