package config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/migrations"
)

// setupTestDB creates a test database with migrations applied for config tests.
// Uses an in-memory database for faster test execution.
func setupTestDB(t *testing.T) (*sql.DB, *gallerydb.Queries, context.Context) {
	t.Helper()

	// Use in-memory database for faster tests
	db, err := sql.Open("sqlite3", ":memory:")
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

	ctx := context.Background()
	q, err := gallerydb.Prepare(ctx, db)
	if err != nil {
		db.Close()
		t.Fatalf("failed to prepare queries: %v", err)
	}

	return db, q, ctx
}

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

// TestFromMap_ErrorPath verifies FromMap returns an error for invalid known values.
func TestFromMap_ErrorPath(t *testing.T) {
	_, err := FromMap(map[string]string{"listener_port": "abc"})
	if err == nil {
		t.Fatal("FromMap expected error, got nil")
	}
}

// contains reports whether values includes target.
func contains(values []string, target string) bool {
	return slices.Contains(values, target)
}
