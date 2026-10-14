package config

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

type mockConfigQueries struct {
	configs []gallerydb.Config
	err     error
}

func (m mockConfigQueries) GetConfigs(ctx context.Context) ([]gallerydb.Config, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.configs, nil
}

type mockSaver struct {
	calls   []gallerydb.UpsertConfigValueOnlyParams
	failKey string
}

func (m *mockSaver) UpsertConfigValueOnly(ctx context.Context, arg gallerydb.UpsertConfigValueOnlyParams) error {
	m.calls = append(m.calls, arg)
	if arg.Key == m.failKey {
		return fmt.Errorf("boom")
	}
	return nil
}

type fakeService struct {
	cfg         *Config
	called      bool
	ensureRoot  string
	ensureErr   error
	validateErr error
	saveErr     error
}

func (f *fakeService) Load(ctx context.Context) (*Config, error) {
	f.called = true
	return f.cfg, nil
}

func (f *fakeService) Save(ctx context.Context, cfg *Config) error { return f.saveErr }
func (f *fakeService) Validate(cfg *Config) error                  { return f.validateErr }
func (f *fakeService) Export(ctx context.Context) (string, error)  { return "", nil }
func (f *fakeService) Import(yamlContent string, ctx context.Context) error {
	return nil
}
func (f *fakeService) RestoreLastKnownGood(ctx context.Context) (*Config, error) {
	return DefaultConfig(), nil
}
func (f *fakeService) EnsureDefaults(ctx context.Context, rootDir string) error {
	f.ensureRoot = rootDir
	return f.ensureErr
}
func (f *fakeService) GetConfigValue(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (f *fakeService) IncrementETag(ctx context.Context) (string, error) {
	return "20260129-01", nil
}

// TestLoadFromDatabase_ErrorPath verifies LoadFromDatabase propagates GetConfigs errors.
func TestLoadFromDatabase_ErrorPath(t *testing.T) {
	sentinel := errors.New("db unavailable")
	q := mockConfigQueries{err: sentinel}
	cfg := DefaultConfig()

	err := cfg.LoadFromDatabase(context.Background(), q)
	if err == nil {
		t.Fatal("LoadFromDatabase expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want wrapping %v", err, sentinel)
	}
}

// TestConfigFromStringMap_ErrorPath verifies invalid known values return an error.
func TestConfigFromStringMap_ErrorPath(t *testing.T) {
	_, err := configFromStringMap(map[string]string{"listener_port": "abc"})
	if err == nil {
		t.Fatal("configFromStringMap expected error, got nil")
	}
}

func configFromStringMap(m map[string]string) (*Config, error) {
	cfg := DefaultConfig()
	for k, v := range m {
		if err := cfg.SetValueFromString(k, v); err != nil {
			return nil, err
		}
	}
	if cfg.ETagVersion == "" {
		cfg.ETagVersion = DefaultConfig().ETagVersion
	}
	return cfg, nil
}

// contains reports whether values includes target.
func contains(values []string, target string) bool {
	return slices.Contains(values, target)
}
