package config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
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
	cfg        *Config
	called     bool
	ensureRoot string
	ensureErr  error
}

func (f *fakeService) Load(ctx context.Context) (*Config, error) {
	f.called = true
	return f.cfg, nil
}

func (f *fakeService) Save(ctx context.Context, cfg *Config) error { return nil }
func (f *fakeService) Validate(cfg *Config) error                  { return nil }
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

// createTestService creates a ConfigService with temporary file-based database pools for testing.
func createTestService(t *testing.T) ConfigService {
	t.Helper()
	ctx := context.Background()

	// Use a temporary file-based database so both pools share the same database
	tempDir := t.TempDir()
	tempDB := filepath.Join(tempDir, "test.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")

	// Run migrations on the database before creating pools
	// Use simple DSN for migrations - no pragmas needed here
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

	// Create database pools using file-backed database
	// WAL mode is persistent, so it's already set from previous connections
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

// TestDefaultConfig_EnableCachePreload verifies EnableCachePreload field exists with default true.
func contains(values []string, target string) bool {
	return slices.Contains(values, target)
}

// TestDiscoveryEnabled_ByDefault verifies that file discovery is enabled by default.
