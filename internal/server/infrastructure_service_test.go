package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"

	_ "github.com/ncruces/go-sqlite3/driver"
)

type syncLogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncLogBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncLogBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// logCaptureMu serializes slog.SetDefault swaps; parallel server tests otherwise race under -race.
var logCaptureMu sync.Mutex

func withLogCapture(t *testing.T, level slog.Level, fn func()) string {
	t.Helper()
	logCaptureMu.Lock()
	defer logCaptureMu.Unlock()
	var buf syncLogBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level}))
	old := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(old)
	fn()
	return buf.String()
}

func newFakePool(maxConns, minIdle int64) *dbconnpool.DbSQLConnPool {
	return &dbconnpool.DbSQLConnPool{Config: dbconnpool.Config{MaxConnections: maxConns, MinIdleConnections: minIdle}}
}

func TestInfrastructureService_GetConfigQueries(t *testing.T) {
	infra := NewInfrastructureService()
	queries := gallerydb.NewCustomQueries(nil)
	cpc := &dbconnpool.CpConn{Queries: queries}

	got := infra.GetConfigQueries(cpc)
	if got != queries {
		t.Error("GetConfigQueries did not return cpc.Queries")
	}
}

func TestInfrastructureService_SetupDB_NilConfig(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    newFakePool(100, 10),
		setupRo:    newFakePool(100, 10),
	}

	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.SetupDB(context.Background(), nil)
	})

	wantDiscovery := "/tmp/fake/discovery-dque"
	if infra.discoveryDQueDirPath != wantDiscovery {
		t.Fatalf("discoveryDQueDirPath = %q, want %q", infra.discoveryDQueDirPath, wantDiscovery)
	}
	if infra.writer != nil {
		t.Fatal("writer should not be created in SetupDB")
	}
	if !strings.Contains(logs, "rw_configured_max=100") {
		t.Errorf("expected default configured max in logs, got: %s", logs)
	}
	if !strings.Contains(logs, "rw_configured_min_idle=1") {
		t.Errorf("expected default configured min idle in logs, got: %s", logs)
	}
}

func TestInfrastructureService_CalibrateCacheSizeNow_Error(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    newFakePool(100, 10),
		setupRo:    newFakePool(100, 10),
	}
	infra.SetupDB(context.Background(), config.DefaultConfig())
	infra.testSeams.GetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		return 0, errors.New("size error")
	}

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.CalibrateCacheSizeNow(context.Background())
	})
	if !strings.Contains(logs, "cache size calibration failed") {
		t.Errorf("expected cache size calibration warning, got: %s", logs)
	}
}

func TestInfrastructureService_SetupDB_PanicOnSetupError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{setupErr: errors.New("boom")}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if s, ok := r.(string); !ok || s != "main" {
			t.Fatalf("panic value = %v, want string 'main'", r)
		}
	}()

	logs := withLogCapture(t, slog.LevelError, func() {
		infra.SetupDB(context.Background(), config.DefaultConfig())
	})
	if !strings.Contains(logs, "failed to setup database") {
		t.Errorf("expected setup error log, got: %s", logs)
	}
}

func TestInfrastructureService_StartWriteBatcher_PanicOnError(t *testing.T) {
	// A regular file in place of the database directory makes the overflow
	// queue directory impossible to create, so NewWriter fails.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: filepath.Join(blocker, "sfpg.db")},
		setupRw:    newFakePool(100, 10),
		setupRo:    newFakePool(100, 10),
	}
	infra.SetupDB(context.Background(), config.DefaultConfig())

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if s, ok := r.(string); !ok || !strings.Contains(s, "failed to create unified WriteBatcher") {
			t.Fatalf("panic value = %v, want failure message", r)
		}
	}()

	withLogCapture(t, slog.LevelError, func() {
		infra.StartWriteBatcher(context.Background(), true, config.DefaultDQueMaxDiskBytes)
	})
}

func TestInfrastructureService_ReconfigurePools_NilConfig(t *testing.T) {
	infra := NewInfrastructureService()
	initializer := &fakeDatabaseInitializer{}
	infra.dbInitializer = initializer
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)

	if err := infra.ReconfigurePools(context.Background(), nil); err != nil {
		t.Fatalf("ReconfigurePools(nil) error = %v", err)
	}
	if initializer.recreateCalled {
		t.Error("RecreatePoolsWithConfig should not be called for nil config")
	}
}

func TestInfrastructureService_ReconfigurePools_NoOpWhenUnchanged(t *testing.T) {
	infra := NewInfrastructureService()
	initializer := &fakeDatabaseInitializer{}
	infra.dbInitializer = initializer
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)
	infra.dbRwPool.Config.MonitorInterval = config.DefaultConfig().DBPoolMonitorInterval
	infra.dbRoPool.Config.MonitorInterval = config.DefaultConfig().DBPoolMonitorInterval

	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 10
	cfg.DBMinIdleConnections = 2

	if err := infra.ReconfigurePools(context.Background(), cfg); err != nil {
		t.Fatalf("ReconfigurePools error = %v", err)
	}
	if initializer.recreateCalled {
		t.Error("RecreatePoolsWithConfig should not be called when unchanged")
	}
}

func TestInfrastructureService_ReconfigurePools_RecreatesWhenOnlyIntervalDiffers(t *testing.T) {
	infra := NewInfrastructureService()
	initializer := &fakeDatabaseInitializer{
		recreateRw: newFakePool(10, 2),
		recreateRo: newFakePool(10, 2),
	}
	infra.dbInitializer = initializer
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)
	infra.dbRwPool.Config.MonitorInterval = config.DefaultConfig().DBPoolMonitorInterval
	infra.dbRoPool.Config.MonitorInterval = config.DefaultConfig().DBPoolMonitorInterval

	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 10
	cfg.DBMinIdleConnections = 2
	cfg.DBPoolMonitorInterval = 30 * time.Second

	if err := infra.ReconfigurePools(context.Background(), cfg); err != nil {
		t.Fatalf("ReconfigurePools error = %v", err)
	}
	if !initializer.recreateCalled {
		t.Error("RecreatePoolsWithConfig should be called when only monitor interval differs")
	}
}

func TestInfrastructureService_ReconfigurePools_NoOpWhenConfiguredIntervalZeroMatchesClampedLive(t *testing.T) {
	infra := NewInfrastructureService()
	initializer := &fakeDatabaseInitializer{}
	infra.dbInitializer = initializer
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)
	infra.dbRwPool.Config.MonitorInterval = config.DefaultConfig().DBPoolMonitorInterval
	infra.dbRoPool.Config.MonitorInterval = config.DefaultConfig().DBPoolMonitorInterval

	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 10
	cfg.DBMinIdleConnections = 2
	cfg.DBPoolMonitorInterval = 0

	if err := infra.ReconfigurePools(context.Background(), cfg); err != nil {
		t.Fatalf("ReconfigurePools error = %v", err)
	}
	if initializer.recreateCalled {
		t.Error("RecreatePoolsWithConfig should not be called when configured interval 0 matches clamped live 1m")
	}
}

func TestInfrastructureService_ReconfigurePools_PropagatesError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{recreateErr: errors.New("recreate failed")}
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)

	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 20
	cfg.DBMinIdleConnections = 4

	err := infra.ReconfigurePools(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "recreate failed") {
		t.Fatalf("ReconfigurePools error = %v, want recreate failed", err)
	}
}

func TestInfrastructureService_ReconfigurePools_SkipsWriterWhenNil(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		recreateRw: newFakePool(20, 4),
		recreateRo: newFakePool(20, 4),
	}
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)

	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 20
	cfg.DBMinIdleConnections = 4

	if err := infra.ReconfigurePools(context.Background(), cfg); err != nil {
		t.Fatalf("ReconfigurePools error = %v", err)
	}
	if infra.writer != nil {
		t.Fatal("ReconfigurePools must not create a writer before StartWriteBatcher")
	}
}

func TestInfrastructureService_ReconfigurePools_UpdatesCacheMW(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		recreateRw: newFakePool(20, 4),
		recreateRo: newFakePool(20, 4),
	}
	infra.dbRwPool = newFakePool(10, 2)
	infra.dbRoPool = newFakePool(10, 2)
	infra.cacheMW = cachelite.NewHTTPCacheMiddleware(infra.dbRwPool, cachelite.CacheConfig{},
		&cachelite.HTTPCacheCounterState{
			SizeBytes:       &infra.cacheSizeBytes,
			EntryCount:      &infra.cacheEntryCount,
			BaselineRunning: &infra.cacheBaselineRunning,
		}, func(*cachelite.HTTPCacheEntry) {})

	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 20
	cfg.DBMinIdleConnections = 4

	if err := infra.ReconfigurePools(context.Background(), cfg); err != nil {
		t.Fatalf("ReconfigurePools error = %v", err)
	}
	if infra.cacheMW == nil {
		t.Fatal("cacheMW should not be nil")
	}
}

func TestInfrastructureService_Shutdown_NilWriter(t *testing.T) {
	infra := NewInfrastructureService()
	infra.writer = nil
	// Should not panic.
	infra.Shutdown()
}

func TestInfrastructureService_CacheStored_EarlyReturns(t *testing.T) {
	tests := []struct {
		name string
		cmw  cacheMiddlewareForEvict
	}{
		{name: "nil cache middleware"},
		{name: "max total size zero", cmw: &fakeCacheMiddlewareForEvict{cfg: cachelite.CacheConfig{MaxTotalSize: 0}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			infra := NewInfrastructureService()
			infra.cacheMWForEvict = tt.cmw
			evictCalled := false
			infra.testSeams.EvictLRU = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool, targetFree int64) (int64, int64, error) {
				evictCalled = true
				return 0, 0, nil
			}

			infra.cacheStored(100, 1)
			if evictCalled {
				t.Error("EvictLRU should not be called")
			}
		})
	}
}

func TestInfrastructureService_CacheStored_UnderLimit(t *testing.T) {
	infra := NewInfrastructureService()
	infra.cacheMWForEvict = &fakeCacheMiddlewareForEvict{cfg: cachelite.CacheConfig{MaxTotalSize: 1000}}
	infra.cacheSizeBytes.Store(500)
	evictCalled := false
	infra.testSeams.EvictLRU = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool, targetFree int64) (int64, int64, error) {
		evictCalled = true
		return 0, 0, nil
	}

	infra.cacheStored(100, 1)
	if evictCalled {
		t.Error("EvictLRU should not be called when under limit")
	}
}

func TestInfrastructureService_CacheStored_EvictionError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.cacheMWForEvict = &fakeCacheMiddlewareForEvict{cfg: cachelite.CacheConfig{MaxTotalSize: 1000}}
	infra.cacheSizeBytes.Store(1400)
	infra.testSeams.EvictLRU = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool, targetFree int64) (int64, int64, error) {
		return 0, 0, errors.New("eviction failed")
	}

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.cacheStored(200, 1)
	})
	if !strings.Contains(logs, "cache eviction failed") {
		t.Errorf("expected eviction error log, got: %s", logs)
	}
}

func TestInfrastructureService_CacheStored_OverLimit(t *testing.T) {
	infra := NewInfrastructureService()
	infra.cacheMWForEvict = &fakeCacheMiddlewareForEvict{cfg: cachelite.CacheConfig{MaxTotalSize: 1000}}
	var evictTarget int64
	infra.testSeams.EvictLRU = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool, targetFree int64) (int64, int64, error) {
		evictTarget = targetFree
		return 600, 2, nil
	}

	infra.cacheStored(1500, 10)

	wantTarget := int64(1500 - 1000 + 1000/10) // 600
	if evictTarget != wantTarget {
		t.Fatalf("EvictLRU target = %d, want %d", evictTarget, wantTarget)
	}
	if got := infra.cacheSizeBytes.Load(); got != 900 {
		t.Fatalf("cacheSizeBytes = %d, want 900", got)
	}
	if got := infra.cacheEntryCount.Load(); got != 8 {
		t.Fatalf("cacheEntryCount = %d, want 8", got)
	}
}

func TestInfrastructureService_CacheStored_UsesCounterNotQuery(t *testing.T) {
	infra := NewInfrastructureService()
	infra.cacheMWForEvict = &fakeCacheMiddlewareForEvict{cfg: cachelite.CacheConfig{MaxTotalSize: 1000}}
	infra.cacheSizeBytes.Store(1500)
	infra.testSeams.GetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		t.Fatal("GetCacheSizeBytes must not be called; eviction uses cacheSizeBytes counter")
		return 0, nil
	}
	infra.testSeams.EvictLRU = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool, targetFree int64) (int64, int64, error) {
		return 0, 0, nil
	}

	infra.cacheStored(100, 1)
}

func TestInfrastructureService_CacheMetrics_GettersDoNotQueryDB(t *testing.T) {
	infra := NewInfrastructureService()
	infra.cacheSizeBytes.Store(42)
	infra.cacheEntryCount.Store(7)

	// If the getters touch the DB, these t.Fatal seams will fire.

	infra.testSeams.GetCacheSizeBytes = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		t.Fatal("GetCacheSizeBytes must not be called by metrics getters")
		return 0, nil
	}
	infra.testSeams.GetCacheEntryCount = func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error) {
		t.Fatal("GetCacheEntryCount must not be called by metrics getters")
		return 0, nil
	}

	// Create a minimal cache middleware wired to the infra's counters.
	infra.cacheMW = cachelite.NewHTTPCacheMiddleware(nil, cachelite.CacheConfig{
		Enabled:      true,
		MaxEntrySize: 100000,
		MaxTotalSize: 1000000,
		DefaultTTL:   60,
	}, &cachelite.HTTPCacheCounterState{
		SizeBytes:       &infra.cacheSizeBytes,
		EntryCount:      &infra.cacheEntryCount,
		BaselineRunning: &infra.cacheBaselineRunning,
	}, func(*cachelite.HTTPCacheEntry) {})
	infra.cacheMWForEvict = infra.cacheMW

	if got := infra.cacheMW.GetSizeBytes(); got != 42 {
		t.Fatalf("GetSizeBytes = %d, want 42", got)
	}
	if got := infra.cacheMW.GetEntryCount(); got != 7 {
		t.Fatalf("GetEntryCount = %d, want 7", got)
	}
}

func TestInfrastructureService_InitializeHTTPCache_EarlyReturns(t *testing.T) {
	tests := []struct {
		name   string
		config *config.Config
	}{
		{"nil config", nil},
		{"cache disabled", func() *config.Config { c := config.DefaultConfig(); c.EnableHTTPCache = false; return c }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			infra := NewInfrastructureService()
			infra.InitializeHTTPCache(tt.config)
			if infra.cacheMW != nil {
				t.Error("cacheMW should remain nil")
			}
		})
	}
}

func TestInfrastructureService_InvalidateHTTPCache_NilPool(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = nil
	rotator := &fakeCacheRotator{}
	infra.cacheRotator = rotator

	infra.InvalidateHTTPCache()
	if rotator.rotateCalled {
		t.Error("RotateCacheTable should not be called when dbRwPool is nil")
	}
}

func TestInfrastructureService_InvalidateHTTPCache_RotateError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = newFakePool(10, 2)
	infra.cacheRotator = &fakeCacheRotator{rotateErr: errors.New("rotate failed")}

	logs := withLogCapture(t, slog.LevelError, func() {
		infra.InvalidateHTTPCache()
	})
	if !strings.Contains(logs, "failed to invalidate HTTP cache") {
		t.Errorf("expected invalidate error log, got: %s", logs)
	}
}

func TestInfrastructureService_IncrementETag_LoadError(t *testing.T) {
	infra := NewInfrastructureService()
	mockSvc := &mockConfigServiceForInfraETag{loadErr: errors.New("load failed")}

	_, err := infra.IncrementETag(context.Background(), mockSvc)
	if err == nil || !strings.Contains(err.Error(), "failed to load config") {
		t.Fatalf("error = %v, want failed to load config", err)
	}
}

func TestInfrastructureService_IncrementETag_SaveError(t *testing.T) {
	infra := NewInfrastructureService()
	cfg := config.DefaultConfig()
	cfg.ETagVersion = "20260701-01"
	mockSvc := &mockConfigServiceForInfraETag{
		loadReturn: cfg,
		saveErr:    errors.New("save failed"),
	}

	_, err := infra.IncrementETag(context.Background(), mockSvc)
	if err == nil || !strings.Contains(err.Error(), "failed to save config") {
		t.Fatalf("error = %v, want failed to save config", err)
	}
}

func TestInfrastructureService_IncrementETag_RotateError(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = newFakePool(10, 2)
	cfg := config.DefaultConfig()
	cfg.ETagVersion = "20260701-01"
	wantETag := config.IncrementETagVersion(cfg.ETagVersion)
	mockSvc := &mockConfigServiceForInfraETag{loadReturn: cfg}
	infra.cacheRotator = &fakeCacheRotator{rotateErr: errors.New("rotate failed")}

	logs := withLogCapture(t, slog.LevelWarn, func() {
		newETag, err := infra.IncrementETag(context.Background(), mockSvc)
		if err != nil {
			t.Fatalf("IncrementETag error = %v", err)
		}
		if newETag != wantETag {
			t.Fatalf("newETag = %q, want %q", newETag, wantETag)
		}
	})
	if !strings.Contains(logs, "failed to rotate HTTP cache after ETag increment") {
		t.Errorf("expected rotate error log, got: %s", logs)
	}
}

func TestInfrastructureService_IncrementETag_Success(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = newFakePool(10, 2)
	cfg := config.DefaultConfig()
	cfg.ETagVersion = "20260701-01"
	wantETag := config.IncrementETagVersion(cfg.ETagVersion)
	mockSvc := &mockConfigServiceForInfraETag{loadReturn: cfg}
	rotator := &fakeCacheRotator{}
	infra.cacheRotator = rotator
	infra.cacheSizeBytes.Store(999)

	newETag, err := infra.IncrementETag(context.Background(), mockSvc)
	if err != nil {
		t.Fatalf("IncrementETag error = %v", err)
	}
	if newETag != wantETag {
		t.Fatalf("newETag = %q, want %q", newETag, wantETag)
	}
	if !mockSvc.saveCalled {
		t.Error("Save should be called")
	}
	if mockSvc.savedConfig == nil || mockSvc.savedConfig.ETagVersion != wantETag {
		t.Errorf("saved ETag = %v, want %q", mockSvc.savedConfig, wantETag)
	}
	if !rotator.rotateCalled {
		t.Error("RotateCacheTable should be called")
	}
	if infra.cacheSizeBytes.Load() != 0 {
		t.Fatalf("cacheSizeBytes = %d, want 0", infra.cacheSizeBytes.Load())
	}
}

func TestInfrastructureService_LogDBPoolConfiguredVsEffective_NilPool(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = nil
	infra.dbRoPool = newFakePool(10, 2)

	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.logDBPoolConfiguredVsEffective("test", 10, 2, time.Minute)
	})
	if logs != "" {
		t.Errorf("expected no logs, got: %s", logs)
	}
}

func TestInfrastructureService_LogDBPoolConfiguredVsEffective_InvalidMinGreaterThanMax(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = newFakePool(5, 2)
	infra.dbRoPool = newFakePool(5, 2)

	logs := withLogCapture(t, slog.LevelWarn, func() {
		infra.logDBPoolConfiguredVsEffective("test", 5, 20, time.Minute)
	})
	if !strings.Contains(logs, "invalid DB pool combination") {
		t.Errorf("expected invalid combination warning, got: %s", logs)
	}
}

func TestInfrastructureService_LogDBPoolConfiguredVsEffective_NormalPath(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbRwPool = newFakePool(7, 3)
	infra.dbRoPool = newFakePool(7, 3)

	logs := withLogCapture(t, slog.LevelInfo, func() {
		infra.logDBPoolConfiguredVsEffective("test", 10, 2, time.Minute)
	})
	for _, want := range []string{
		"rw_configured_max=10",
		"rw_effective_max=7",
		"rw_configured_min_idle=2",
		"rw_effective_min_idle=3",
		"ro_configured_max=10",
		"ro_effective_max=7",
		"ro_configured_min_idle=2",
		"ro_effective_min_idle=3",
		"rw_configured_monitor_interval=",
		"rw_effective_monitor_interval=",
		"ro_configured_monitor_interval=",
		"ro_effective_monitor_interval=",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("expected %q in logs, got: %s", want, logs)
		}
	}
}

// fakeDatabaseInitializer is a test double for databaseInitializer.
type fakeDatabaseInitializer struct {
	setupPaths      database.DatabasePaths
	setupRw         *dbconnpool.DbSQLConnPool
	setupRo         *dbconnpool.DbSQLConnPool
	setupErr        error
	recreateRw      *dbconnpool.DbSQLConnPool
	recreateRo      *dbconnpool.DbSQLConnPool
	recreateErr     error
	setupCalled     bool
	recreateCalled  bool
	lastRecreateCfg *config.Config
}

func (f *fakeDatabaseInitializer) Setup(ctx context.Context, rootDir string, cfg *config.Config) (database.DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	f.setupCalled = true
	return f.setupPaths, f.setupRw, f.setupRo, f.setupErr
}

func (f *fakeDatabaseInitializer) RecreatePoolsWithConfig(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	f.recreateCalled = true
	f.lastRecreateCfg = cfg
	return f.recreateRw, f.recreateRo, f.recreateErr
}

// fakeCacheMiddlewareForEvict is a test double for cacheMiddlewareForEvict.
type fakeCacheMiddlewareForEvict struct {
	cfg cachelite.CacheConfig
}

func (f *fakeCacheMiddlewareForEvict) Config() cachelite.CacheConfig {
	return f.cfg
}

// fakeCacheRotator is a test double for cacheRotator.
type fakeCacheRotator struct {
	rotateErr    error
	rotateCalled bool
	rotatePool   *dbconnpool.DbSQLConnPool
}

func (f *fakeCacheRotator) RotateCacheTable(ctx context.Context, pool *dbconnpool.DbSQLConnPool) error {
	f.rotateCalled = true
	f.rotatePool = pool
	return f.rotateErr
}

// mockConfigServiceForInfraETag is a minimal config.ConfigService double for
// InfrastructureService ETag tests.
type mockConfigServiceForInfraETag struct {
	loadReturn  *config.Config
	loadErr     error
	validateErr error
	saveErr     error
	saveCalled  bool
	savedConfig *config.Config
}

func (m *mockConfigServiceForInfraETag) Load(ctx context.Context) (*config.Config, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.loadReturn, nil
}

func (m *mockConfigServiceForInfraETag) Save(ctx context.Context, cfg *config.Config) error {
	m.saveCalled = true
	m.savedConfig = cfg
	return m.saveErr
}

func (m *mockConfigServiceForInfraETag) Validate(cfg *config.Config) error {
	return m.validateErr
}

func (m *mockConfigServiceForInfraETag) Export(ctx context.Context) (string, error) {
	return "", nil
}

func (m *mockConfigServiceForInfraETag) Import(yamlContent string, ctx context.Context) error {
	return nil
}

func (m *mockConfigServiceForInfraETag) RestoreLastKnownGood(ctx context.Context) (*config.Config, error) {
	return nil, nil
}

func (m *mockConfigServiceForInfraETag) EnsureDefaults(ctx context.Context, rootDir string) error {
	return nil
}

func (m *mockConfigServiceForInfraETag) GetConfigValue(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (m *mockConfigServiceForInfraETag) IncrementETag(ctx context.Context) (string, error) {
	return "", nil
}

func TestInfrastructureService_IncrementETag_ValidationFailure(t *testing.T) {
	ctx := context.Background()
	infra := NewInfrastructureService()

	cfg := config.DefaultConfig()
	cfg.ETagVersion = "20260701-01"

	mockSvc := &mockConfigServiceForInfraETag{
		loadReturn:  cfg,
		validateErr: errors.New("listener port out of range"),
	}

	_, err := infra.IncrementETag(ctx, mockSvc)
	if err == nil {
		t.Fatal("IncrementETag expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid config after ETag increment") {
		t.Fatalf("IncrementETag error = %v, want wrapped validation error", err)
	}
	if mockSvc.saveCalled {
		t.Error("Save should not be called when validation fails")
	}
}

// TestInfrastructureService_RealDBLifecycle exercises the production database
// initializer, WAL checkpoint, metadata queries, cache rotation, and pool
// reconfiguration paths against a real temporary database.
func TestInfrastructureService_RealDBLifecycle(t *testing.T) {
	infra := NewInfrastructureService()
	infra.rootDir = t.TempDir()
	cfg := config.DefaultConfig()

	infra.SetupDB(context.Background(), cfg)
	infra.StartWriteBatcher(context.Background(), true, config.DefaultDQueMaxDiskBytes)
	t.Cleanup(func() { infra.Shutdown() })

	cpc, err := infra.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Get RW connection: %v", err)
	}

	if infra.GetMetadataQueries(cpc) == nil {
		t.Error("GetMetadataQueries returned nil")
	}
	infra.dbRwPool.Put(cpc)

	infra.InvalidateHTTPCache()

	// Pools are reconfigured only before the writer starts, even with a nil config.
	if err := infra.ReconfigurePools(context.Background(), nil); err == nil {
		t.Fatal("ReconfigurePools(nil) after StartWriteBatcher must fail")
	}
	cfg2 := config.DefaultConfig()
	cfg2.DBMaxPoolSize = 50
	if err := infra.ReconfigurePools(context.Background(), cfg2); err == nil {
		t.Fatal("ReconfigurePools after StartWriteBatcher must fail")
	}
	if got := infra.dbRwPool.Config.MaxConnections; got == 50 {
		t.Errorf("RW max connections = %d after refused reconfigure, want unchanged", got)
	}
}
