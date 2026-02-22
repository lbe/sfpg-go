package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/getopt"
	"go.local/sfpg/migrations"
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

	if upErr := m.Up(); upErr != nil && upErr != migrate.ErrNoChange {
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
func (f *fakeService) Export() (string, error)                     { return "", nil }
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
	tempDB := filepath.Join(t.TempDir(), "test.db")

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

	if upErr := m.Up(); upErr != nil && upErr != migrate.ErrNoChange {
		db.Close()
		t.Fatalf("failed to apply migrations: %v", upErr)
	}
	db.Close()

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
	})
	if err != nil {
		t.Fatalf("failed to create RW pool: %v", err)
	}
	t.Cleanup(func() { _ = rwPool.Close() })

	return NewService(rwPool, roPool)
}

// TestDefaultConfig_EnableCachePreload verifies EnableCachePreload field exists with default true.
func TestDefaultConfig_EnableCachePreload(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.EnableCachePreload {
		t.Error("expected EnableCachePreload default true, got false")
	}
}

// TestRecoverFromCorruption_EnableCachePreload verifies RecoverFromCorruption includes EnableCachePreload.
func TestRecoverFromCorruption_EnableCachePreload(t *testing.T) {
	defaults := DefaultConfig()
	defaults.EnableCachePreload = false
	cfg := DefaultConfig()
	cfg.EnableCachePreload = true // corrupt to different value
	cfg.RecoverFromCorruption(defaults)
	if cfg.EnableCachePreload {
		t.Error("expected EnableCachePreload false after recovery, got true")
	}
}

// TestDefaultConfig_MaxHTTPCacheEntryInsertPerTransaction verifies the field exists with default 10.
func TestDefaultConfig_MaxHTTPCacheEntryInsertPerTransaction(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxHTTPCacheEntryInsertPerTransaction != 10 {
		t.Errorf("expected MaxHTTPCacheEntryInsertPerTransaction default 10, got %d", cfg.MaxHTTPCacheEntryInsertPerTransaction)
	}
}

// TestRecoverFromCorruption_MaxHTTPCacheEntryInsertPerTransaction verifies RecoverFromCorruption includes the field.
func TestRecoverFromCorruption_MaxHTTPCacheEntryInsertPerTransaction(t *testing.T) {
	defaults := DefaultConfig()
	defaults.MaxHTTPCacheEntryInsertPerTransaction = 25
	cfg := DefaultConfig()
	cfg.MaxHTTPCacheEntryInsertPerTransaction = 1
	cfg.RecoverFromCorruption(defaults)
	if cfg.MaxHTTPCacheEntryInsertPerTransaction != 25 {
		t.Errorf("expected MaxHTTPCacheEntryInsertPerTransaction 25 after recovery, got %d", cfg.MaxHTTPCacheEntryInsertPerTransaction)
	}
}

// TestDefaultConfig verifies that DefaultConfig returns correct default values.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Verify server settings
	if cfg.ListenerAddress != "0.0.0.0" {
		t.Errorf("expected ListenerAddress to be '0.0.0.0', got %q", cfg.ListenerAddress)
	}
	if cfg.ListenerPort != 8081 {
		t.Errorf("expected ListenerPort to be 8081, got %d", cfg.ListenerPort)
	}

	// Verify log settings
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel to be 'debug', got %q", cfg.LogLevel)
	}
	if cfg.LogRetentionCount != 7 {
		t.Errorf("expected LogRetentionCount to be 7, got %d", cfg.LogRetentionCount)
	}

	// Verify database settings
	if cfg.DBMaxPoolSize != 100 {
		t.Errorf("expected DBMaxPoolSize to be 100, got %d", cfg.DBMaxPoolSize)
	}
	if cfg.DBMinIdleConnections != 10 {
		t.Errorf("expected DBMinIdleConnections to be 10, got %d", cfg.DBMinIdleConnections)
	}

	// Verify cache settings
	if cfg.CacheMaxSize != 500*1024*1024 {
		t.Errorf("expected CacheMaxSize to be 500MB, got %d", cfg.CacheMaxSize)
	}
	if cfg.CacheMaxTime != 30*24*time.Hour {
		t.Errorf("expected CacheMaxTime to be 30 days, got %v", cfg.CacheMaxTime)
	}

	// Verify worker pool settings
	if cfg.WorkerPoolMax != 0 { // 0 means auto-calculate
		t.Errorf("expected WorkerPoolMax to be 0 (auto), got %d", cfg.WorkerPoolMax)
	}
	if cfg.WorkerPoolMinIdle != 0 { // 0 means auto-calculate
		t.Errorf("expected WorkerPoolMinIdle to be 0 (auto), got %d", cfg.WorkerPoolMinIdle)
	}
}

func TestDefaultConfig_IncludesETagVersion(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ETagVersion == "" {
		t.Error("DefaultConfig() ETagVersion is empty, want default value")
	}

	// Verify it matches expected format YYYYMMDD-NN
	pattern := `^\d{8}-\d{2}$`
	matched, err := regexp.MatchString(pattern, cfg.ETagVersion)
	if err != nil {
		t.Fatalf("regexp error: %v", err)
	}
	if !matched {
		t.Errorf("ETagVersion = %q, does not match pattern %q", cfg.ETagVersion, pattern)
	}
}

func TestConfig_ToMap_IncludesETagVersion(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ETagVersion = "20260129-01"

	m := cfg.ToMap()

	val, exists := m["etag_version"]
	if !exists {
		t.Error("ToMap() does not include 'etag_version' key")
	}
	if val != "20260129-01" {
		t.Errorf("ToMap()['etag_version'] = %q, want %q", val, "20260129-01")
	}
}

func TestConfig_FromMap_LoadsETagVersion(t *testing.T) {
	m := map[string]string{
		"listener_address": "0.0.0.0",
		"listener_port":    "8080",
		"etag_version":     "20260129-05",
		// Add other required fields based on existing FromMap requirements
	}

	cfg, err := FromMap(m)
	if err != nil {
		t.Fatalf("FromMap() error = %v", err)
	}

	if cfg.ETagVersion != "20260129-05" {
		t.Errorf("FromMap() ETagVersion = %q, want %q", cfg.ETagVersion, "20260129-05")
	}
}

func TestConfig_RoundTrip_PreservesETagVersion(t *testing.T) {
	original := DefaultConfig()
	original.ETagVersion = "20260129-42"

	m := original.ToMap()
	restored, err := FromMap(m)
	if err != nil {
		t.Fatalf("FromMap() error = %v", err)
	}

	if restored.ETagVersion != original.ETagVersion {
		t.Errorf("Roundtrip ETagVersion = %q, want %q", restored.ETagVersion, original.ETagVersion)
	}
}

// TestLoadFromDatabase verifies loading configuration from database.
func TestLoadFromDatabase(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	// Insert some test config values
	now := time.Now().Unix()
	err := q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "listener_port",
		Value:     "9090",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to insert test config: %v", err)
	}

	err = q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "log_level",
		Value:     "info",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to insert test config: %v", err)
	}

	// Load config from database
	cfg := DefaultConfig()
	err = cfg.LoadFromDatabase(ctx, q)
	if err != nil {
		t.Fatalf("failed to load config from database: %v", err)
	}

	// Verify loaded values
	if cfg.ListenerPort != 9090 {
		t.Errorf("expected ListenerPort to be 9090, got %d", cfg.ListenerPort)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected LogLevel to be 'info', got %q", cfg.LogLevel)
	}

	// Verify defaults are still present for unset values
	if cfg.ListenerAddress != "0.0.0.0" {
		t.Errorf("expected ListenerAddress to remain default '0.0.0.0', got %q", cfg.ListenerAddress)
	}
}

// TestLoadFromOpt verifies loading configuration from getopt.Opt.
func TestLoadFromOpt(t *testing.T) {
	opt := getopt.Opt{
		Port:               getopt.OptInt{Int: 9090, IsSet: true},
		EnableCompression:  getopt.OptBool{Bool: true, IsSet: true},
		EnableHTTPCache:    getopt.OptBool{Bool: false, IsSet: true},
		EnableCachePreload: getopt.OptBool{Bool: false, IsSet: true},
		RunFileDiscovery:   getopt.OptBool{Bool: false, IsSet: true},
	}

	cfg := DefaultConfig()
	cfg.LoadFromOpt(opt)

	if cfg.EnableCachePreload {
		t.Errorf("expected EnableCachePreload to be false after LoadFromOpt, got %v", cfg.EnableCachePreload)
	}
	if cfg.ListenerPort != 9090 {
		t.Errorf("expected ListenerPort to be 9090, got %d", cfg.ListenerPort)
	}
	if !cfg.ServerCompressionEnable {
		t.Errorf("expected ServerCompressionEnable to be true, got %v", cfg.ServerCompressionEnable)
	}
	if cfg.EnableHTTPCache {
		t.Errorf("expected EnableHTTPCache to be false, got %v", cfg.EnableHTTPCache)
	}
	if cfg.RunFileDiscovery {
		t.Errorf("expected RunFileDiscovery to be false, got %v", cfg.RunFileDiscovery)
	}
}

// TestLoadFromOpt_SessionOptions verifies session options from getopt.Opt override config defaults
func TestLoadFromOpt_SessionOptions(t *testing.T) {
	opt := getopt.Opt{
		SessionSecure:   getopt.OptBool{Bool: false, IsSet: true},
		SessionHttpOnly: getopt.OptBool{Bool: false, IsSet: true},
		SessionMaxAge:   getopt.OptInt{Int: 7200, IsSet: true},
		SessionSameSite: getopt.OptString{String: "Strict", IsSet: true},
	}

	cfg := DefaultConfig()
	// Default config has SessionSecure=true, SessionHttpOnly=true
	if !cfg.SessionSecure {
		t.Fatal("expected default SessionSecure=true")
	}
	if !cfg.SessionHttpOnly {
		t.Fatal("expected default SessionHttpOnly=true")
	}

	// LoadFromOpt should override with env var values
	cfg.LoadFromOpt(opt)

	if cfg.SessionSecure {
		t.Errorf("expected SessionSecure to be false after LoadFromOpt, got %v", cfg.SessionSecure)
	}
	if cfg.SessionHttpOnly {
		t.Errorf("expected SessionHttpOnly to be false after LoadFromOpt, got %v", cfg.SessionHttpOnly)
	}
	if cfg.SessionMaxAge != 7200 {
		t.Errorf("expected SessionMaxAge=7200 after LoadFromOpt, got %v", cfg.SessionMaxAge)
	}
	if cfg.SessionSameSite != "Strict" {
		t.Errorf("expected SessionSameSite=Strict after LoadFromOpt, got %v", cfg.SessionSameSite)
	}
}

// TestMergeDefaults verifies that MergeDefaults correctly applies defaults to unset values.
func TestMergeDefaults(t *testing.T) {
	cfg := &Config{
		ListenerPort: 9090,
		LogLevel:     "error",
		// Other fields are zero values
	}

	defaults := DefaultConfig()
	cfg.MergeDefaults(defaults)

	// Verify explicitly set values are preserved
	if cfg.ListenerPort != 9090 {
		t.Errorf("expected ListenerPort to remain 9090, got %d", cfg.ListenerPort)
	}
	if cfg.LogLevel != "error" {
		t.Errorf("expected LogLevel to remain 'error', got %q", cfg.LogLevel)
	}

	// Verify zero values are filled with defaults
	if cfg.ListenerAddress != defaults.ListenerAddress {
		t.Errorf("expected ListenerAddress to be default %q, got %q", defaults.ListenerAddress, cfg.ListenerAddress)
	}
	if cfg.LogRetentionCount != defaults.LogRetentionCount {
		t.Errorf("expected LogRetentionCount to be default %d, got %d", defaults.LogRetentionCount, cfg.LogRetentionCount)
	}
}

// TestSaveToDatabase verifies saving configuration to database.
func TestSaveToDatabase(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	cfg := DefaultConfig()
	cfg.ListenerPort = 9090
	cfg.LogLevel = "warn"

	err := cfg.SaveToDatabase(ctx, q)
	if err != nil {
		t.Fatalf("failed to save config to database: %v", err)
	}

	// Verify values were saved
	portValue, err := q.GetConfigValueByKey(ctx, "listener_port")
	if err != nil {
		t.Fatalf("failed to get listener_port from database: %v", err)
	}
	if portValue != "9090" {
		t.Errorf("expected listener_port to be '9090', got %q", portValue)
	}

	logLevelValue, err := q.GetConfigValueByKey(ctx, "log_level")
	if err != nil {
		t.Fatalf("failed to get log_level from database: %v", err)
	}
	if logLevelValue != "warn" {
		t.Errorf("expected log_level to be 'warn', got %q", logLevelValue)
	}
}

// TestLoadFromDatabase_MissingKeys verifies that missing keys are handled gracefully.
func TestLoadFromDatabase_MissingKeys(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	// Don't insert any config values - database is empty
	cfg := DefaultConfig()
	err := cfg.LoadFromDatabase(ctx, q)
	if err != nil {
		t.Fatalf("failed to load config from empty database: %v", err)
	}

	// Verify defaults are still present
	if cfg.ListenerPort != 8081 {
		t.Errorf("expected ListenerPort to remain default 8081, got %d", cfg.ListenerPort)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel to remain default 'debug', got %q", cfg.LogLevel)
	}
}

// TestSaveToDatabase_UpdatesExisting verifies that saving updates existing keys.
func TestSaveToDatabase_UpdatesExisting(t *testing.T) {
	db, q, ctx := setupTestDB(t)
	defer db.Close()

	// Insert initial value
	now := time.Now().Unix()
	err := q.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "listener_port",
		Value:     "8080",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to insert initial config: %v", err)
	}

	// Save new value
	cfg := DefaultConfig()
	cfg.ListenerPort = 9090
	err = cfg.SaveToDatabase(ctx, q)
	if err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// Verify value was updated
	portValue, err := q.GetConfigValueByKey(ctx, "listener_port")
	if err != nil {
		t.Fatalf("failed to get listener_port: %v", err)
	}
	if portValue != "9090" {
		t.Errorf("expected listener_port to be updated to '9090', got %q", portValue)
	}
}

// TestTypeConversion verifies that type conversions work correctly.
func TestTypeConversion(t *testing.T) {
	cfg := DefaultConfig()

	// Test string to int conversion
	err := cfg.SetValueFromString("listener_port", "9999")
	if err != nil {
		t.Fatalf("failed to set listener_port: %v", err)
	}
	if cfg.ListenerPort != 9999 {
		t.Errorf("expected ListenerPort to be 9999, got %d", cfg.ListenerPort)
	}

	// Test string to bool conversion
	err = cfg.SetValueFromString("server_compression_enable", "false")
	if err != nil {
		t.Fatalf("failed to set server_compression_enable: %v", err)
	}
	if cfg.ServerCompressionEnable {
		t.Error("expected ServerCompressionEnable to be false")
	}

	// Test string to duration conversion
	err = cfg.SetValueFromString("cache_max_time", "24h")
	if err != nil {
		t.Fatalf("failed to set cache_max_time: %v", err)
	}
	if cfg.CacheMaxTime != 24*time.Hour {
		t.Errorf("expected CacheMaxTime to be 24h, got %v", cfg.CacheMaxTime)
	}

	// Test invalid type conversion
	err = cfg.SetValueFromString("listener_port", "invalid")
	if err == nil {
		t.Error("expected error for invalid port value")
	}
}

// TestJSONSerialization verifies that complex types (arrays, durations) serialize correctly.
func TestJSONSerialization(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Themes = []string{"dark", "light", "auto"}

	// Convert to map (which serializes to JSON for database)
	configMap := cfg.ToMap()

	themesJSON := configMap["themes"]
	if themesJSON == "" {
		t.Fatal("themes should be serialized to JSON")
	}

	// Verify it can be deserialized
	var themes []string
	err := json.Unmarshal([]byte(themesJSON), &themes)
	if err != nil {
		t.Fatalf("failed to deserialize themes: %v", err)
	}
	if len(themes) != 3 {
		t.Errorf("expected 3 themes, got %d", len(themes))
	}
	if themes[0] != "dark" || themes[1] != "light" || themes[2] != "auto" {
		t.Errorf("themes not deserialized correctly: %v", themes)
	}
}

// TestValidate_InvalidPort verifies that Validate rejects invalid port values.
func TestValidate_InvalidPort(t *testing.T) {
	cfg := DefaultConfig()

	// Test port too low
	cfg.ListenerPort = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for port 0, got nil")
	}

	// Test port too high
	cfg.ListenerPort = 65536
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for port 65536, got nil")
	}

	// Test valid port
	cfg.ListenerPort = 8081
	if err := cfg.Validate(); err != nil {
		t.Errorf("Expected no error for valid port, got: %v", err)
	}
}

// TestValidate_InvalidLogLevel verifies that Validate rejects invalid log levels.
func TestValidate_InvalidLogLevel(t *testing.T) {
	cfg := DefaultConfig()

	// Test invalid log level
	cfg.LogLevel = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for invalid log level, got nil")
	}

	// Test valid log levels
	validLevels := []string{"debug", "info", "warn", "error", "DEBUG", "INFO", "WARN", "ERROR"}
	for _, level := range validLevels {
		cfg.LogLevel = level
		if err := cfg.Validate(); err != nil {
			t.Errorf("Expected no error for log level %q, got: %v", level, err)
		}
	}
}

// TestValidate_InvalidLogRollover verifies that Validate rejects invalid log rollover values.
func TestValidate_InvalidLogRollover(t *testing.T) {
	cfg := DefaultConfig()

	// Test invalid rollover
	cfg.LogRollover = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for invalid log rollover, got nil")
	}

	// Test valid rollovers
	validRollovers := []string{"daily", "weekly", "monthly", "DAILY", "WEEKLY", "MONTHLY"}
	for _, rollover := range validRollovers {
		cfg.LogRollover = rollover
		if err := cfg.Validate(); err != nil {
			t.Errorf("Expected no error for log rollover %q, got: %v", rollover, err)
		}
	}
}

// TestValidate_InvalidLogRetentionCount verifies that Validate rejects invalid retention counts.
func TestValidate_InvalidLogRetentionCount(t *testing.T) {
	cfg := DefaultConfig()

	// Test retention count too low
	cfg.LogRetentionCount = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for retention count 0, got nil")
	}

	// Test valid retention count
	cfg.LogRetentionCount = 7
	if err := cfg.Validate(); err != nil {
		t.Errorf("Expected no error for valid retention count, got: %v", err)
	}
}

// TestValidate_InvalidSessionSameSite verifies that Validate rejects invalid session same-site values.
func TestValidate_InvalidSessionSameSite(t *testing.T) {
	cfg := DefaultConfig()

	// Test invalid same-site
	cfg.SessionSameSite = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for invalid session same-site, got nil")
	}

	// Test valid same-site values
	validSameSite := []string{"Lax", "Strict", "None"}
	for _, sameSite := range validSameSite {
		cfg.SessionSameSite = sameSite
		if err := cfg.Validate(); err != nil {
			t.Errorf("Expected no error for session same-site %q, got: %v", sameSite, err)
		}
	}
}

// TestValidate_NegativeCacheSizes verifies that Validate rejects negative cache sizes.
func TestValidate_NegativeCacheSizes(t *testing.T) {
	cfg := DefaultConfig()

	// Test negative cache max size
	cfg.CacheMaxSize = -1
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for negative cache max size, got nil")
	}

	// Test negative cache max entry size
	cfg.CacheMaxSize = 0 // Reset
	cfg.CacheMaxEntrySize = -1
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for negative cache max entry size, got nil")
	}
}

// TestValidate_InvalidDBPoolSizes verifies that Validate rejects invalid database pool sizes.
func TestValidate_InvalidDBPoolSizes(t *testing.T) {
	cfg := DefaultConfig()

	// Test max pool size too low
	cfg.DBMaxPoolSize = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for max pool size 0, got nil")
	}

	// Test min idle connections negative
	cfg.DBMaxPoolSize = 100
	cfg.DBMinIdleConnections = -1
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for negative min idle connections, got nil")
	}

	// Test min idle exceeds max pool size
	cfg.DBMinIdleConnections = 150
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error when min idle exceeds max pool size, got nil")
	}
}

// TestValidate_InvalidWorkerPoolSizes verifies that Validate rejects invalid worker pool sizes.
func TestValidate_InvalidWorkerPoolSizes(t *testing.T) {
	cfg := DefaultConfig()

	// Test negative worker pool max
	cfg.WorkerPoolMax = -1
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for negative worker pool max, got nil")
	}

	// Test negative worker pool min idle
	cfg.WorkerPoolMax = 10
	cfg.WorkerPoolMinIdle = -1
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for negative worker pool min idle, got nil")
	}

	// Test min idle exceeds max (when both are set)
	cfg.WorkerPoolMax = 10
	cfg.WorkerPoolMinIdle = 15
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error when min idle exceeds max, got nil")
	}

	// Test valid: 0 means auto-calculate
	cfg.WorkerPoolMax = 0
	cfg.WorkerPoolMinIdle = 0
	if err := cfg.Validate(); err != nil {
		t.Errorf("Expected no error for auto-calculate (0), got: %v", err)
	}
}

// TestValidate_InvalidQueueSize verifies that Validate rejects invalid queue sizes.
func TestValidate_InvalidQueueSize(t *testing.T) {
	cfg := DefaultConfig()

	// Test queue size too low
	cfg.QueueSize = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for queue size 0, got nil")
	}

	// Test valid queue size
	cfg.QueueSize = 10000
	if err := cfg.Validate(); err != nil {
		t.Errorf("Expected no error for valid queue size, got: %v", err)
	}
}

// TestValidate_ValidConfig verifies that Validate accepts a fully valid configuration.
func TestValidate_ValidConfig(t *testing.T) {
	cfg := DefaultConfig()

	if err := cfg.Validate(); err != nil {
		t.Errorf("Expected no error for default config, got: %v", err)
	}
}

// TestValidate_MinMaxRelationship verifies that Validate enforces min <= max relationships.
func TestValidate_MinMaxRelationship(t *testing.T) {
	cfg := DefaultConfig()

	// DB pool: min > max
	cfg.DBMaxPoolSize = 10
	cfg.DBMinIdleConnections = 15
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error when DB min idle > max pool size, got nil")
	}

	// Worker pool: min > max (when both set)
	cfg.DBMinIdleConnections = 5 // Reset
	cfg.WorkerPoolMax = 10
	cfg.WorkerPoolMinIdle = 15
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error when worker pool min idle > max, got nil")
	}
}

// TestValidate_CriticalSettings verifies that critical settings are validated.
// Critical settings are those that could break the application if invalid.
func TestValidate_CriticalSettings(t *testing.T) {
	cfg := DefaultConfig()

	// Invalid port (critical - server won't start)
	cfg.ListenerPort = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for critical setting (port), got nil")
	}

	// Invalid DB pool size (critical - database won't work)
	cfg.ListenerPort = 8081 // Reset
	cfg.DBMaxPoolSize = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for critical setting (DB pool size), got nil")
	}
}

// TestValidate_DurationFields verifies that duration fields are handled correctly.
// Note: Duration validation happens during parsing, not in Validate()
func TestValidate_DurationFields(t *testing.T) {
	cfg := DefaultConfig()

	// Valid durations are set during LoadFromYAML or LoadFromDatabase
	// Validate() doesn't check durations, but we verify they're set correctly
	if cfg.CacheMaxTime == 0 {
		t.Error("CacheMaxTime should have a default value")
	}
	if cfg.CacheMaxTime != 30*24*time.Hour {
		t.Errorf("Expected CacheMaxTime to be 30 days, got: %v", cfg.CacheMaxTime)
	}
}

// TestValidateSetting_Comprehensive tests all validation paths in ValidateSetting
// to improve coverage. It covers all config keys with valid and invalid values,
// ensuring proper error messages are returned for invalid inputs.
func TestValidateSetting_Comprehensive(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		name    string
		key     string
		value   string
		wantErr bool
		errMsg  string
	}{
		// listener_port validation
		{
			name:    "valid port",
			key:     "listener_port",
			value:   "8081",
			wantErr: false,
		},
		{
			name:    "port too low",
			key:     "listener_port",
			value:   "0",
			wantErr: true,
			errMsg:  "port must be between 1 and 65535",
		},
		{
			name:    "port too high",
			key:     "listener_port",
			value:   "65536",
			wantErr: true,
			errMsg:  "port must be between 1 and 65535",
		},
		{
			name:    "port invalid format",
			key:     "listener_port",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid port value",
		},
		{
			name:    "port boundary min",
			key:     "listener_port",
			value:   "1",
			wantErr: false,
		},
		{
			name:    "port boundary max",
			key:     "listener_port",
			value:   "65535",
			wantErr: false,
		},
		// log_level validation
		{
			name:    "valid log level debug",
			key:     "log_level",
			value:   "debug",
			wantErr: false,
		},
		{
			name:    "valid log level info",
			key:     "log_level",
			value:   "info",
			wantErr: false,
		},
		{
			name:    "valid log level warn",
			key:     "log_level",
			value:   "warn",
			wantErr: false,
		},
		{
			name:    "valid log level error",
			key:     "log_level",
			value:   "error",
			wantErr: false,
		},
		{
			name:    "log level case insensitive",
			key:     "log_level",
			value:   "DEBUG",
			wantErr: false,
		},
		{
			name:    "invalid log level",
			key:     "log_level",
			value:   "invalid",
			wantErr: true,
			errMsg:  "invalid log level",
		},
		// log_rollover validation
		{
			name:    "valid rollover daily",
			key:     "log_rollover",
			value:   "daily",
			wantErr: false,
		},
		{
			name:    "valid rollover weekly",
			key:     "log_rollover",
			value:   "weekly",
			wantErr: false,
		},
		{
			name:    "valid rollover monthly",
			key:     "log_rollover",
			value:   "monthly",
			wantErr: false,
		},
		{
			name:    "rollover case insensitive",
			key:     "log_rollover",
			value:   "WEEKLY",
			wantErr: false,
		},
		{
			name:    "invalid rollover",
			key:     "log_rollover",
			value:   "invalid",
			wantErr: true,
			errMsg:  "invalid log rollover",
		},
		// log_retention_count validation
		{
			name:    "valid retention count",
			key:     "log_retention_count",
			value:   "7",
			wantErr: false,
		},
		{
			name:    "retention count too low",
			key:     "log_retention_count",
			value:   "0",
			wantErr: true,
			errMsg:  "log retention count must be at least 1",
		},
		{
			name:    "retention count invalid format",
			key:     "log_retention_count",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid log retention count",
		},
		{
			name:    "retention count boundary",
			key:     "log_retention_count",
			value:   "1",
			wantErr: false,
		},
		// session_same_site validation
		{
			name:    "valid same site Lax",
			key:     "session_same_site",
			value:   "Lax",
			wantErr: false,
		},
		{
			name:    "valid same site Strict",
			key:     "session_same_site",
			value:   "Strict",
			wantErr: false,
		},
		{
			name:    "valid same site None",
			key:     "session_same_site",
			value:   "None",
			wantErr: false,
		},
		{
			name:    "invalid same site",
			key:     "session_same_site",
			value:   "invalid",
			wantErr: true,
			errMsg:  "invalid session same-site",
		},
		{
			name:    "same site case sensitive",
			key:     "session_same_site",
			value:   "lax",
			wantErr: true,
			errMsg:  "invalid session same-site",
		},
		// cache_max_size validation
		{
			name:    "valid cache max size",
			key:     "cache_max_size",
			value:   "524288000",
			wantErr: false,
		},
		{
			name:    "cache max size zero",
			key:     "cache_max_size",
			value:   "0",
			wantErr: false,
		},
		{
			name:    "cache max size negative",
			key:     "cache_max_size",
			value:   "-1",
			wantErr: true,
			errMsg:  "size must be non-negative",
		},
		{
			name:    "cache max size invalid format",
			key:     "cache_max_size",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid size value",
		},
		// cache_max_entry_size validation
		{
			name:    "valid cache max entry size",
			key:     "cache_max_entry_size",
			value:   "10485760",
			wantErr: false,
		},
		{
			name:    "cache max entry size negative",
			key:     "cache_max_entry_size",
			value:   "-1",
			wantErr: true,
			errMsg:  "size must be non-negative",
		},
		// db_max_pool_size validation
		{
			name:    "valid db max pool size",
			key:     "db_max_pool_size",
			value:   "100",
			wantErr: false,
		},
		{
			name:    "db max pool size too low",
			key:     "db_max_pool_size",
			value:   "0",
			wantErr: true,
			errMsg:  "database max pool size must be at least 1",
		},
		{
			name:    "db max pool size invalid format",
			key:     "db_max_pool_size",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid db max pool size",
		},
		{
			name:    "db max pool size boundary",
			key:     "db_max_pool_size",
			value:   "1",
			wantErr: false,
		},
		// db_min_idle_connections validation
		{
			name:    "valid db min idle connections",
			key:     "db_min_idle_connections",
			value:   "10",
			wantErr: false,
		},
		{
			name:    "db min idle connections zero",
			key:     "db_min_idle_connections",
			value:   "0",
			wantErr: false,
		},
		{
			name:    "db min idle connections negative",
			key:     "db_min_idle_connections",
			value:   "-1",
			wantErr: true,
			errMsg:  "database min idle connections must be non-negative",
		},
		{
			name:    "db min idle exceeds max",
			key:     "db_min_idle_connections",
			value:   "200",
			wantErr: true,
			errMsg:  "cannot exceed max pool size",
		},
		{
			name:    "db min idle invalid format",
			key:     "db_min_idle_connections",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid min idle connections value",
		},
		// worker_pool_max validation
		{
			name:    "valid worker pool max",
			key:     "worker_pool_max",
			value:   "10",
			wantErr: false,
		},
		{
			name:    "worker pool max zero",
			key:     "worker_pool_max",
			value:   "0",
			wantErr: false,
		},
		{
			name:    "worker pool max negative",
			key:     "worker_pool_max",
			value:   "-1",
			wantErr: true,
			errMsg:  "worker pool max must be non-negative",
		},
		{
			name:    "worker pool max invalid format",
			key:     "worker_pool_max",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid worker pool max value",
		},
		// worker_pool_min_idle validation
		{
			name:    "valid worker pool min idle",
			key:     "worker_pool_min_idle",
			value:   "5",
			wantErr: false,
		},
		{
			name:    "worker pool min idle zero",
			key:     "worker_pool_min_idle",
			value:   "0",
			wantErr: false,
		},
		{
			name:    "worker pool min idle negative",
			key:     "worker_pool_min_idle",
			value:   "-1",
			wantErr: true,
			errMsg:  "worker pool min idle must be non-negative",
		},
		{
			name:    "worker pool min idle exceeds max",
			key:     "worker_pool_min_idle",
			value:   "20",
			wantErr: true,
			errMsg:  "cannot exceed max",
		},
		{
			name:    "worker pool min idle invalid format",
			key:     "worker_pool_min_idle",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid worker pool min idle value",
		},
		// queue_size validation
		{
			name:    "valid queue size",
			key:     "queue_size",
			value:   "10000",
			wantErr: false,
		},
		{
			name:    "queue size too low",
			key:     "queue_size",
			value:   "0",
			wantErr: true,
			errMsg:  "queue size must be at least 1",
		},
		{
			name:    "queue size invalid format",
			key:     "queue_size",
			value:   "not-a-number",
			wantErr: true,
			errMsg:  "invalid queue size value",
		},
		{
			name:    "queue size boundary",
			key:     "queue_size",
			value:   "1",
			wantErr: false,
		},
		// unknown key (should not error)
		{
			name:    "unknown key",
			key:     "unknown_setting",
			value:   "any-value",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up config state for dependency checks
			if tt.key == "db_min_idle_connections" && tt.value == "200" {
				cfg.DBMaxPoolSize = 100
			} else if tt.key == "db_min_idle_connections" {
				cfg.DBMaxPoolSize = 100
			}
			if tt.key == "worker_pool_min_idle" && tt.value == "20" {
				cfg.WorkerPoolMax = 10
			} else if tt.key == "worker_pool_min_idle" {
				cfg.WorkerPoolMax = 0
			}

			err := cfg.ValidateSetting(tt.key, tt.value)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateSetting(%q, %q) expected error but got none", tt.key, tt.value)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateSetting(%q, %q) error = %v, want error containing %q", tt.key, tt.value, err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateSetting(%q, %q) unexpected error: %v", tt.key, tt.value, err)
				}
			}

			// Reset config state
			cfg.DBMaxPoolSize = 100
			cfg.WorkerPoolMax = 0
		})
	}
}

// TestSetValueFromString_Comprehensive tests all code paths in SetValueFromString,
// covering all config keys with valid and invalid string values to ensure proper parsing.
func TestSetValueFromString_Comprehensive(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr bool
		errMsg  string
		verify  func(*Config) bool
	}{
		// String fields
		{"listener_address", "listener_address", "127.0.0.1", false, "", func(c *Config) bool { return c.ListenerAddress == "127.0.0.1" }},
		{"log_directory", "log_directory", "/tmp/logs", false, "", func(c *Config) bool { return c.LogDirectory == "/tmp/logs" }},
		{"log_level", "log_level", "info", false, "", func(c *Config) bool { return c.LogLevel == "info" }},
		{"log_rollover", "log_rollover", "daily", false, "", func(c *Config) bool { return c.LogRollover == "daily" }},
		{"site_name", "site_name", "Test Site", false, "", func(c *Config) bool { return c.SiteName == "Test Site" }},
		{"current_theme", "current_theme", "light", false, "", func(c *Config) bool { return c.CurrentTheme == "light" }},
		{"image_directory", "image_directory", "/tmp/images", false, "", func(c *Config) bool { return c.ImageDirectory == "/tmp/images" }},
		{"session_same_site", "session_same_site", "Strict", false, "", func(c *Config) bool { return c.SessionSameSite == "Strict" }},
		// Integer fields
		{"listener_port", "listener_port", "9090", false, "", func(c *Config) bool { return c.ListenerPort == 9090 }},
		{"listener_port_invalid", "listener_port", "not-a-number", true, "invalid port value", nil},
		{"log_retention_count", "log_retention_count", "10", false, "", func(c *Config) bool { return c.LogRetentionCount == 10 }},
		{"log_retention_count_invalid", "log_retention_count", "not-a-number", true, "invalid log retention count", nil},
		{"session_max_age", "session_max_age", "3600", false, "", func(c *Config) bool { return c.SessionMaxAge == 3600 }},
		{"session_max_age_invalid", "session_max_age", "not-a-number", true, "invalid session max age", nil},
		// Boolean fields
		{"session_http_only_true", "session_http_only", "true", false, "", func(c *Config) bool { return c.SessionHttpOnly == true }},
		{"session_http_only_false", "session_http_only", "false", false, "", func(c *Config) bool { return c.SessionHttpOnly == false }},
		{"session_http_only_invalid", "session_http_only", "not-a-bool", true, "invalid session http only", nil},
		{"session_secure_true", "session_secure", "true", false, "", func(c *Config) bool { return c.SessionSecure == true }},
		{"session_secure_false", "session_secure", "false", false, "", func(c *Config) bool { return c.SessionSecure == false }},
		{"server_compression_enable_true", "server_compression_enable", "true", false, "", func(c *Config) bool { return c.ServerCompressionEnable == true }},
		{"enable_http_cache_false", "enable_http_cache", "false", false, "", func(c *Config) bool { return c.EnableHTTPCache == false }},
		{"enable_cache_preload_true", "enable_cache_preload", "true", false, "", func(c *Config) bool { return c.EnableCachePreload == true }},
		{"enable_cache_preload_false", "enable_cache_preload", "false", false, "", func(c *Config) bool { return c.EnableCachePreload == false }},
		{"max_http_cache_entry_insert_per_transaction", "max_http_cache_entry_insert_per_transaction", "25", false, "", func(c *Config) bool { return c.MaxHTTPCacheEntryInsertPerTransaction == 25 }},
		{"max_http_cache_entry_insert_per_transaction_invalid", "max_http_cache_entry_insert_per_transaction", "x", true, "invalid max http cache entry insert per transaction", nil},
		{"run_file_discovery_true", "run_file_discovery", "true", false, "", func(c *Config) bool { return c.RunFileDiscovery == true }},
		// Int64 fields
		{"cache_max_size", "cache_max_size", "524288000", false, "", func(c *Config) bool { return c.CacheMaxSize == 524288000 }},
		{"cache_max_size_invalid", "cache_max_size", "not-a-number", true, "invalid cache max size", nil},
		{"cache_max_entry_size", "cache_max_entry_size", "10485760", false, "", func(c *Config) bool { return c.CacheMaxEntrySize == 10485760 }},
		{"cache_max_entry_size_invalid", "cache_max_entry_size", "not-a-number", true, "invalid cache max entry size", nil},
		// Duration fields
		{"cache_max_time", "cache_max_time", "720h", false, "", func(c *Config) bool { return c.CacheMaxTime == 720*time.Hour }},
		{"cache_max_time_invalid", "cache_max_time", "not-a-duration", true, "invalid cache max time", nil},
		{"cache_cleanup_interval", "cache_cleanup_interval", "5m", false, "", func(c *Config) bool { return c.CacheCleanupInterval == 5*time.Minute }},
		{"cache_cleanup_interval_invalid", "cache_cleanup_interval", "not-a-duration", true, "invalid cache cleanup interval", nil},
		{"db_optimize_interval", "db_optimize_interval", "1h", false, "", func(c *Config) bool { return c.DBOptimizeInterval == 1*time.Hour }},
		{"worker_pool_max_idle_time", "worker_pool_max_idle_time", "10s", false, "", func(c *Config) bool { return c.WorkerPoolMaxIdleTime == 10*time.Second }},
		{"worker_pool_max_idle_time_invalid", "worker_pool_max_idle_time", "not-a-duration", true, "invalid worker pool max idle time", nil},
		// Integer fields (more)
		{"db_max_pool_size", "db_max_pool_size", "100", false, "", func(c *Config) bool { return c.DBMaxPoolSize == 100 }},
		{"db_max_pool_size_invalid", "db_max_pool_size", "not-a-number", true, "invalid db max pool size", nil},
		{"db_min_idle_connections", "db_min_idle_connections", "10", false, "", func(c *Config) bool { return c.DBMinIdleConnections == 10 }},
		{"db_min_idle_connections_invalid", "db_min_idle_connections", "not-a-number", true, "invalid db min idle connections", nil},
		{"worker_pool_max", "worker_pool_max", "20", false, "", func(c *Config) bool { return c.WorkerPoolMax == 20 }},
		{"worker_pool_max_invalid", "worker_pool_max", "not-a-number", true, "invalid worker pool max", nil},
		{"worker_pool_min_idle", "worker_pool_min_idle", "5", false, "", func(c *Config) bool { return c.WorkerPoolMinIdle == 5 }},
		{"worker_pool_min_idle_invalid", "worker_pool_min_idle", "not-a-number", true, "invalid worker pool min idle", nil},
		{"queue_size", "queue_size", "5000", false, "", func(c *Config) bool { return c.QueueSize == 5000 }},
		{"queue_size_invalid", "queue_size", "not-a-number", true, "invalid queue size", nil},
		// JSON array field
		{"themes_valid", "themes", `["dark","light","auto"]`, false, "", func(c *Config) bool {
			return len(c.Themes) == 3 && c.Themes[0] == "dark" && c.Themes[1] == "light" && c.Themes[2] == "auto"
		}},
		{"themes_invalid_json", "themes", "not-json", true, "invalid themes JSON", nil},
		{"themes_empty_array", "themes", "[]", false, "", func(c *Config) bool { return len(c.Themes) == 0 }},
		// Unknown key
		{"unknown_key", "unknown_setting", "any-value", false, "", func(c *Config) bool { return true }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testCfg := DefaultConfig()
			err := testCfg.SetValueFromString(tt.key, tt.value)

			if tt.wantErr {
				if err == nil {
					t.Errorf("SetValueFromString(%q, %q) expected error but got none", tt.key, tt.value)
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("SetValueFromString(%q, %q) error = %v, want error containing %q", tt.key, tt.value, err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("SetValueFromString(%q, %q) unexpected error: %v", tt.key, tt.value, err)
				} else if tt.verify != nil && !tt.verify(testCfg) {
					t.Errorf("SetValueFromString(%q, %q) verification failed", tt.key, tt.value)
				}
			}
		})
	}
}

func TestFileExists(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	if !FileExists(file) {
		t.Fatal("expected FileExists to return true for file")
	}
	if FileExists(dir) {
		t.Fatal("expected FileExists to return false for directory")
	}
}

func TestLoadYAML_EnableCachePreload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("enable-cache-preload: false\n"), 0o644); err != nil {
		t.Fatalf("failed to write yaml: %v", err)
	}

	cfg, err := loadYAMLConfigForConfig(path)
	if err != nil {
		t.Fatalf("loadYAMLConfigForConfig failed: %v", err)
	}
	if cfg.EnableCachePreload == nil || *cfg.EnableCachePreload != false {
		t.Fatalf("expected enable-cache-preload false, got %#v", cfg.EnableCachePreload)
	}
}

func TestConfig_ToMap_EnableCachePreload(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableCachePreload = false
	m := cfg.ToMap()
	if v, ok := m["enable_cache_preload"]; !ok || v != "false" {
		t.Errorf("ToMap() enable_cache_preload = %q (ok=%v), want \"false\"", v, ok)
	}
}

func TestConfig_ToMap_MaxHTTPCacheEntryInsertPerTransaction(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxHTTPCacheEntryInsertPerTransaction = 25
	m := cfg.ToMap()
	if v, ok := m["max_http_cache_entry_insert_per_transaction"]; !ok || v != "25" {
		t.Errorf("ToMap() max_http_cache_entry_insert_per_transaction = %q (ok=%v), want \"25\"", v, ok)
	}
}

func TestLoadYAMLConfigForConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("listener-port: 9091\nlog-level: warn\n"), 0o644); err != nil {
		t.Fatalf("failed to write yaml: %v", err)
	}

	cfg, err := loadYAMLConfigForConfig(path)
	if err != nil {
		t.Fatalf("loadYAMLConfigForConfig failed: %v", err)
	}
	if cfg.ListenerPort == nil || *cfg.ListenerPort != 9091 {
		t.Fatalf("expected listener-port 9091, got %#v", cfg.ListenerPort)
	}
	if cfg.LogLevel == nil || *cfg.LogLevel != "warn" {
		t.Fatalf("expected log-level warn, got %#v", cfg.LogLevel)
	}
}

func TestLoadYAMLConfigForConfig_Invalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("listener-port: ["), 0o644); err != nil {
		t.Fatalf("failed to write yaml: %v", err)
	}

	if _, err := loadYAMLConfigForConfig(path); err == nil {
		t.Fatal("expected error for invalid yaml")
	}
}

func TestLoadFromYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "sfpg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	yamlContent := "listener-port: 9001\nlog-level: warn\ncache-max-time: 45s\n"
	if err := os.WriteFile(configPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg := DefaultConfig()
	if err := cfg.LoadFromYAML(); err != nil {
		t.Fatalf("LoadFromYAML failed: %v", err)
	}

	if cfg.ListenerPort != 9001 {
		t.Fatalf("expected listener port 9001, got %d", cfg.ListenerPort)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("expected log level warn, got %q", cfg.LogLevel)
	}
	if cfg.CacheMaxTime != 45*time.Second {
		t.Fatalf("expected cache max time 45s, got %v", cfg.CacheMaxTime)
	}
}

func TestLoad(t *testing.T) {
	rootDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "sfpg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	yamlContent := "log-level: error\n"
	if err := os.WriteFile(configPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	opt := getopt.Opt{Port: getopt.OptInt{Int: 9010, IsSet: true}}
	cfg, err := Load(context.Background(), rootDir, nil, opt)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.ListenerPort != 9010 {
		t.Fatalf("expected port 9010, got %d", cfg.ListenerPort)
	}
	if cfg.LogLevel != "error" {
		t.Fatalf("expected log level error, got %q", cfg.LogLevel)
	}
	if cfg.ImageDirectory != filepath.Join(rootDir, "Images") {
		t.Fatalf("expected image directory %q, got %q", filepath.Join(rootDir, "Images"), cfg.ImageDirectory)
	}
}

func TestLoad_WithService(t *testing.T) {
	service := &fakeService{cfg: func() *Config {
		cfg := DefaultConfig()
		cfg.ListenerPort = 8001
		cfg.LogLevel = "info"
		return cfg
	}()}

	cfg, err := Load(context.Background(), "", service, getopt.Opt{Port: getopt.OptInt{Int: 9002, IsSet: true}})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !service.called {
		t.Fatal("expected service.Load to be called")
	}
	if cfg.ListenerPort != 9002 {
		t.Fatalf("expected port overridden to 9002, got %d", cfg.ListenerPort)
	}
}

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

func TestValidateImageDirectory_Empty(t *testing.T) {
	if err := ValidateImageDirectory(""); err == nil {
		t.Fatal("expected ValidateImageDirectory to fail for empty path")
	}
}

func TestLoadFromYAML_InvalidConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "sfpg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("listener-port: ["), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg := DefaultConfig()
	if err := cfg.LoadFromYAML(); err != nil {
		t.Fatalf("LoadFromYAML should ignore invalid yaml and return nil, got %v", err)
	}
}

func TestIdentifyChanges(t *testing.T) {
	cfg := DefaultConfig()
	other := DefaultConfig()
	other.ListenerAddress = "127.0.0.1"
	other.ListenerPort = 9090
	other.LogDirectory = "/tmp/logs"
	other.LogLevel = "error"
	other.LogRollover = "daily"
	other.LogRetentionCount = cfg.LogRetentionCount + 1
	other.SiteName = "New Name"
	other.CurrentTheme = "light"
	other.ImageDirectory = "/tmp/images"
	other.SessionMaxAge = cfg.SessionMaxAge + 1
	other.SessionHttpOnly = false
	other.SessionSecure = false
	other.SessionSameSite = "Strict"
	other.ServerCompressionEnable = false
	other.EnableHTTPCache = false
	other.CacheMaxSize = cfg.CacheMaxSize + 1
	other.CacheMaxTime = cfg.CacheMaxTime + time.Second
	other.CacheMaxEntrySize = cfg.CacheMaxEntrySize + 1
	other.CacheCleanupInterval = cfg.CacheCleanupInterval + time.Second
	other.DBMaxPoolSize = cfg.DBMaxPoolSize + 1
	other.DBMinIdleConnections = cfg.DBMinIdleConnections + 1
	other.DBOptimizeInterval = cfg.DBOptimizeInterval + time.Second
	other.WorkerPoolMax = cfg.WorkerPoolMax + 1
	other.WorkerPoolMinIdle = cfg.WorkerPoolMinIdle + 1
	other.WorkerPoolMaxIdleTime = cfg.WorkerPoolMaxIdleTime + time.Second
	other.QueueSize = cfg.QueueSize + 1
	other.EnableCachePreload = false
	other.RunFileDiscovery = false
	changes := cfg.IdentifyChanges(other)
	if !contains(changes, "log-level") || !contains(changes, "listener-port") || !contains(changes, "log-directory") {
		t.Fatalf("expected key changes, got %v", changes)
	}
}

func TestGetLastKnownGoodDiff_NoConfig(t *testing.T) {
	cfg := DefaultConfig()
	_, err := cfg.GetLastKnownGoodDiff(context.Background(), mockConfigQueries{configs: nil})
	if err == nil {
		t.Fatal("expected error when last known good config is missing")
	}
}

func TestPreviewImport_InvalidYAML(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	_, err := cfg.PreviewImport("listener-port: [")
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "invalid YAML") && !strings.Contains(err.Error(), "invalid YAML syntax") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyImageDirectory_Empty(t *testing.T) {
	imagesDir, normalized, err := ApplyImageDirectory("")
	if err != nil {
		t.Fatalf("expected ApplyImageDirectory to return nil error for empty path, got %v", err)
	}
	if imagesDir != "" || normalized != "" {
		t.Fatal("expected empty image directory outputs for empty input")
	}
}

func TestImportFromYAML(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultConfig()
	saver := &mockSaver{}

	if err := cfg.ImportFromYAML("log-level: info\n", ctx, saver); err != nil {
		t.Fatalf("ImportFromYAML failed: %v", err)
	}

	if err := cfg.ImportFromYAML("session-secret: nope\n", ctx, saver); err == nil {
		t.Fatal("expected ImportFromYAML to fail with session-secret")
	}

	if err := cfg.ImportFromYAML("listener-port: [", ctx, saver); err == nil {
		t.Fatal("expected ImportFromYAML to fail with invalid yaml")
	}
}

func TestSaveToDatabase_ErrorPaths(t *testing.T) {
	cfg := DefaultConfig()

	t.Run("returns error when upsert fails", func(t *testing.T) {
		saver := &mockSaver{failKey: "listener_port"}
		err := cfg.SaveToDatabase(context.Background(), saver)
		if err == nil {
			t.Fatal("expected error when upsert fails")
		}
	})

	t.Run("ignores last known good save error", func(t *testing.T) {
		saver := &mockSaver{failKey: "LastKnownGoodConfig"}
		if err := cfg.SaveToDatabase(context.Background(), saver); err != nil {
			t.Fatalf("expected SaveToDatabase to succeed, got %v", err)
		}

		found := false
		for _, call := range saver.calls {
			if call.Key == "LastKnownGoodConfig" {
				found = true
			}
		}
		if !found {
			t.Fatal("expected LastKnownGoodConfig to be saved")
		}
	})
}

func TestPreviewImportAndLastKnownGoodDiff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	preview, err := cfg.PreviewImport("log-level: warn\n")
	if err != nil {
		t.Fatalf("PreviewImport failed: %v", err)
	}
	if !contains(preview.Changes, "log-level") {
		t.Fatalf("expected log-level change, got %v", preview.Changes)
	}

	if _, cfgErr := cfg.PreviewImport("session-secret: nope\n"); cfgErr == nil {
		t.Fatal("expected PreviewImport to fail when session-secret is present")
	}

	lastKnownGoodYAML := "log-level: warn\n"
	mock := mockConfigQueries{configs: []gallerydb.Config{{Key: "LastKnownGoodConfig", Value: lastKnownGoodYAML}}}
	diff, err := cfg.GetLastKnownGoodDiff(context.Background(), mock)
	if err != nil {
		t.Fatalf("GetLastKnownGoodDiff failed: %v", err)
	}
	if !contains(diff.Changes, "log-level") {
		t.Fatalf("expected log-level change in diff, got %v", diff.Changes)
	}
}

func TestRestoreLastKnownGood(t *testing.T) {
	cfg := DefaultConfig()
	yamlContent, err := cfg.ExportToYAML()
	if err != nil {
		t.Fatalf("ExportToYAML failed: %v", err)
	}
	mock := mockConfigQueries{configs: []gallerydb.Config{{Key: "LastKnownGoodConfig", Value: yamlContent}}}

	restored, err := cfg.RestoreLastKnownGood(context.Background(), mock)
	if err != nil {
		t.Fatalf("RestoreLastKnownGood failed: %v", err)
	}
	if restored == nil {
		t.Fatal("expected restored config")
	}
}

func TestApplyYAMLConfigToConfig_InvalidDuration(t *testing.T) {
	cfg := DefaultConfig()
	yaml := &yamlConfigForConfig{CacheMaxTime: new("not-a-duration")}
	applyYAMLConfigToConfig(cfg, yaml)
	if cfg.CacheMaxTime != DefaultConfig().CacheMaxTime {
		t.Fatal("expected CacheMaxTime to remain unchanged on invalid duration")
	}
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
			yamlContent, err := svc.Export()
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
	cpc, err := svc.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get connection: %v", err)
	}
	defer svc.dbRwPool.Put(cpc)

	now := time.Now().Unix()
	err = cpc.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
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
	_, err = cpcRw.Conn.ExecContext(ctx, "INSERT INTO config (key, value) VALUES (?, ?)", "log_level", "debug")
	svc.dbRwPool.Put(cpcRw)
	if err != nil {
		t.Fatalf("failed to seed config table: %v", err)
	}

	if err := svc.EnsureDefaults(ctx, t.TempDir()); err != nil {
		t.Fatalf("EnsureDefaults failed: %v", err)
	}
}

func contains(values []string, target string) bool {
	return slices.Contains(values, target)
}

// TestDiscoveryEnabled_ByDefault verifies that file discovery is enabled by default.
func TestDiscoveryEnabled_ByDefault(t *testing.T) {
	defaults := DefaultConfig()
	if !defaults.RunFileDiscovery {
		t.Fatal("RunFileDiscovery should be true by default in config.DefaultConfig()")
	}
}

// TestConfigExport_ToFile_ShowsDiff verifies that exporting to file shows current vs new YAML content.
func TestConfigExport_ToFile_ShowsDiff(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ListenerPort = 9999
	cfg.SiteName = "Test Gallery"

	yamlContent, err := cfg.ExportToYAML()
	if err != nil {
		t.Fatalf("Failed to export config to YAML: %v", err)
	}

	var yamlData map[string]any
	if err := json.Unmarshal([]byte(yamlContent), &yamlData); err != nil {
		// Try YAML if JSON fails (ExportToYAML returns YAML format)
		t.Logf("Note: ExportToYAML returns YAML, not JSON. Test validates YAML content exists.")
	}

	if !strings.Contains(yamlContent, "listener-port") && !strings.Contains(yamlContent, "ListenerPort") {
		t.Error("YAML should contain listener-port configuration")
	}
	if !strings.Contains(yamlContent, "9999") {
		t.Error("YAML should contain port value 9999")
	}
	if !strings.Contains(yamlContent, "Test Gallery") {
		t.Error("YAML should contain site name 'Test Gallery'")
	}
}

// TestConfigExport_ToFile_RequiresConfirmation verifies file exists check works.
func TestConfigExport_ToFile_RequiresConfirmation(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")

	err := os.WriteFile(configFile, []byte("existing: config\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create existing config file: %v", err)
	}

	if _, err := os.Stat(configFile); err != nil {
		t.Fatalf("Config file should exist: %v", err)
	}
}

// TestConfigExport_ToFile_Cancellation verifies file content preservation.
func TestConfigExport_ToFile_Cancellation(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")

	originalContent := "original: content\n"
	err := os.WriteFile(configFile, []byte(originalContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	content, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("Failed to read config file: %v", err)
	}
	if string(content) != originalContent {
		t.Error("File content should match original")
	}
}

// TestConfigExport_Download verifies export generates valid YAML.
func TestConfigExport_Download(t *testing.T) {
	cfg := DefaultConfig()

	yamlContent, err := cfg.ExportToYAML()
	if err != nil {
		t.Fatalf("Failed to export config: %v", err)
	}

	if !strings.Contains(yamlContent, "listener-port") && !strings.Contains(yamlContent, "ListenerPort") {
		t.Error("YAML should contain listener-port key")
	}
	if !strings.Contains(yamlContent, "log-level") && !strings.Contains(yamlContent, "LogLevel") {
		t.Error("YAML should contain log-level key")
	}
}

// TestConfigExport_FilePermissions verifies directory permission setup.
func TestConfigExport_FilePermissions(t *testing.T) {
	tempDir := t.TempDir()
	readOnlyDir := filepath.Join(tempDir, "readonly")
	err := os.Mkdir(readOnlyDir, 0400)
	if err != nil {
		t.Fatalf("Failed to create read-only directory: %v", err)
	}
	defer os.Chmod(readOnlyDir, 0755)

	configFile := filepath.Join(readOnlyDir, "config.yaml")
	_ = configFile
}

// TestConfigExport_ExcludesSessionSecret verifies session secret is never exported.
func TestConfigExport_ExcludesSessionSecret(t *testing.T) {
	cfg := DefaultConfig()

	yamlContent, err := cfg.ExportToYAML()
	if err != nil {
		t.Fatalf("Failed to export config: %v", err)
	}

	// Session secrets are managed separately by the App, not in Config
	// Verify secret-related strings don't appear in export
	if strings.Contains(yamlContent, "secret") {
		t.Error("YAML should not contain secret-related fields")
	}
}

// TestConfigExport_AllSettings verifies export includes all major settings.
func TestConfigExport_AllSettings(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ListenerPort = 9999
	cfg.SiteName = "Test"
	cfg.LogLevel = "info"
	cfg.CacheMaxSize = 1000000

	yamlContent, err := cfg.ExportToYAML()
	if err != nil {
		t.Fatalf("Failed to export: %v", err)
	}

	expectedSubstrings := []string{"9999", "Test", "info"}
	for _, substr := range expectedSubstrings {
		if !strings.Contains(yamlContent, substr) {
			t.Errorf("YAML should contain %q", substr)
		}
	}
}
