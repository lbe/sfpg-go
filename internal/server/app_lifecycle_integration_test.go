//go:build integration

package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

// createStartedApp returns a fully-started app with worker pool running.
func createStartedApp(t testing.TB) *App {
	t.Helper()
	app := CreateApp(t)
	app.Start(app.RuntimeManager.ctx, app.ConfigManager.Config, 1, 2, app.imagesDir, app.normalizedImagesDir, removeImagesDirPrefix, app.getRouter, app.GetHandlerQueries, app.GetETagVersion)
	app.RuntimeManager.poolDone = make(chan struct{})
	app.StartPool(app.RuntimeManager.ctx, app.RuntimeManager.poolDone, app.normalizedImagesDir, removeImagesDirPrefix, app.SubsystemManager.fileProcessor, nil)
	return app
}

// createAppWithPool returns a full app with the worker pool enabled.
func createAppWithPool(t testing.TB) *App {
	t.Helper()
	return CreateApp(t, WithPool())
}

// createAppWithHandlers returns a full app whose handlers are already built.
func createAppWithHandlers(t testing.TB) *App {
	t.Helper()
	app := CreateApp(t)
	if app.HandlerManager.authHandlers == nil {
		t.Fatal("expected handlers to be built by CreateApp")
	}
	return app
}

// --- merged from app_integration_test.go ---
type recordingConfigService struct {
	ensureDefaultsCalled bool
	ensureDefaultsRoot   string
	getConfigValueKeys   []string
	getConfigValueVal    string
	getConfigValueErr    error
}

func (r *recordingConfigService) Load(ctx context.Context) (*config.Config, error) {
	return config.DefaultConfig(), nil
}

func (r *recordingConfigService) Save(ctx context.Context, cfg *config.Config) error {
	return nil
}

func (r *recordingConfigService) Validate(cfg *config.Config) error {
	return nil
}

func (r *recordingConfigService) Export(ctx context.Context) (string, error) {
	return "", nil
}

func (r *recordingConfigService) Import(yamlContent string, ctx context.Context) error {
	return nil
}

func (r *recordingConfigService) RestoreLastKnownGood(ctx context.Context) (*config.Config, error) {
	return config.DefaultConfig(), nil
}

func (r *recordingConfigService) EnsureDefaults(ctx context.Context, rootDir string) error {
	r.ensureDefaultsCalled = true
	r.ensureDefaultsRoot = rootDir
	return nil
}

func (r *recordingConfigService) GetConfigValue(ctx context.Context, key string) (string, error) {
	r.getConfigValueKeys = append(r.getConfigValueKeys, key)
	if r.getConfigValueErr != nil {
		return "", r.getConfigValueErr
	}
	return r.getConfigValueVal, nil
}

func (r *recordingConfigService) IncrementETag(ctx context.Context) (string, error) {
	return "20260129-01", nil
}

type errorConfigService struct {
	recordingConfigService
	loadCfg    *config.Config
	loadErr    error
	loadCalled bool
}

func (e *errorConfigService) Load(ctx context.Context) (*config.Config, error) {
	e.loadCalled = true
	return e.loadCfg, e.loadErr
}

type ensureDefaultsFailConfigService struct {
	recordingConfigService
}

func (e *ensureDefaultsFailConfigService) EnsureDefaults(ctx context.Context, rootDir string) error {
	e.ensureDefaultsCalled = true
	e.ensureDefaultsRoot = rootDir
	return fmt.Errorf("ensure failed")
}

func TestSetDB(t *testing.T) {
	// Create a temporary directory structure
	tempDir := t.TempDir()

	ss := "this-is-a-test-secret-with-min-32-bytes"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)

	defer app.RuntimeManager.cancel()

	// Initialize DB (setDB will set app.dbPaths)
	app.setDB()

	// Test that both dbConnPools are operational
	t.Run("verify pools operational", func(t *testing.T) {
		// Test deferred (RO) pool
		roConn, err := app.dbRoPool.Get()
		if err != nil {
			t.Fatalf("failed to get deferred tx connection: %v", err)
		}

		// Basic query should work
		var count int
		err = roConn.Conn.QueryRowContext(app.RuntimeManager.ctx, "SELECT 1").Scan(&count)
		if err != nil {
			t.Errorf("failed basic query on deferred tx connection: %v", err)
		}
		app.dbRoPool.Put(roConn)

		// Test immediate (RW) pool
		rwConn, err := app.dbRwPool.Get()
		if err != nil {
			t.Fatalf("failed to get immediate tx connection: %v", err)
		}

		err = rwConn.Conn.QueryRowContext(app.RuntimeManager.ctx, "SELECT 1").Scan(&count)
		if err != nil {
			t.Errorf("failed basic query on immediate tx connection: %v", err)
		}
		app.dbRwPool.Put(rwConn)
	})

	t.Run("dque path configured after setDB", func(t *testing.T) {
		if app.writeBatcher != nil {
			t.Fatal("writeBatcher should not be initialized by setDB alone")
		}
		dqueDir := filepath.Join(filepath.Dir(app.dbPaths.Main), filepath.Base(app.dbPaths.Main)+"-dque")
		if app.dqueDirPath != dqueDir {
			t.Fatalf("dqueDirPath = %q, want %q", app.dqueDirPath, dqueDir)
		}
	})

	t.Run("writeBatcher configured with dque overflow", func(t *testing.T) {
		app.StartWriteBatcher(app.RuntimeManager.ctx, true, config.DefaultDQueMaxDiskBytes)

		if app.writeBatcher == nil {
			t.Fatal("writeBatcher should be initialized after StartWriteBatcher")
		}

		stats := app.writeBatcher.GetStats()
		if !stats.DQueEnabled {
			t.Error("writeBatcher.DQueEnabled is false — DQueDirPath not set")
		}
		if stats.MaxDiskBytes != config.DefaultDQueMaxDiskBytes {
			t.Errorf("writeBatcher.MaxDiskBytes = %d, want %d (config default)", stats.MaxDiskBytes, config.DefaultDQueMaxDiskBytes)
		}

		// Verify the dque overflow directory exists on disk
		dqueDir := filepath.Join(filepath.Dir(app.dbPaths.Main), filepath.Base(app.dbPaths.Main)+"-dque")
		if _, err := os.Stat(dqueDir); os.IsNotExist(err) {
			t.Error("dque overflow directory was not created — DQueDirPath not set in setDB()")
		}
	})

	// Close writeBatcher first to release any database connections. Close is
	// synchronous: it waits for the worker to flush and exit, so connections
	// are back in the pool before the pools are closed below.
	if app.writeBatcher != nil {
		if err := app.writeBatcher.Close(); err != nil {
			t.Errorf("failed to close writeBatcher: %v", err)
		}
	}

	// Close pools at end of test
	roErr := app.dbRoPool.Close()
	rwErr := app.dbRwPool.Close()

	if roErr != nil && !strings.Contains(roErr.Error(), "database is locked") {
		t.Errorf("failed to close deferred tx pool: %v", roErr)
	}
	if rwErr != nil && !strings.Contains(rwErr.Error(), "database is locked") {
		t.Errorf("failed to close immediate tx pool: %v", rwErr)
	}
}

func TestReconfigurePools_DQueWired(t *testing.T) {
	tempDir := t.TempDir()
	ss := "this-is-a-test-secret-with-min-32-bytes"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	defer app.RuntimeManager.cancel()

	// Initialize DB
	app.setDB()

	// Ensure config is initialized before reconfiguration
	app.ConfigManager.ConfigMu.Lock()
	if app.ConfigManager.Config == nil {
		app.ConfigManager.Config = config.DefaultConfig()
	}
	app.ConfigManager.Config.DBMaxPoolSize = 999 // different from default to trigger reconfigure
	app.ConfigManager.ConfigMu.Unlock()

	// Verify the new batcher has dque overflow configured after reconfigure.
	err := app.reconfigurePoolsFromConfig()
	if err != nil {
		t.Fatalf("reconfigurePoolsFromConfig failed: %v", err)
	}

	app.StartWriteBatcher(app.RuntimeManager.ctx, true, config.DefaultDQueMaxDiskBytes)

	if app.writeBatcher == nil {
		t.Fatal("writeBatcher is nil after reconfigure")
	}

	stats := app.writeBatcher.GetStats()
	if !stats.DQueEnabled {
		t.Error("writeBatcher.DQueEnabled is false after reconfigure — DQueDirPath not set in reconfigurePoolsFromConfig()")
	}

	dqueDir := filepath.Join(filepath.Dir(app.dbPaths.Main), filepath.Base(app.dbPaths.Main)+"-dque")
	if _, err := os.Stat(dqueDir); os.IsNotExist(err) {
		t.Error("dque overflow directory was not created after reconfigure — DQueDirPath not set in reconfigurePoolsFromConfig()")
	}

	// Cleanup
	if app.writeBatcher != nil {
		app.writeBatcher.Close()
	}
	app.dbRoPool.Close()
	app.dbRwPool.Close()
}

func TestUnlockAccount(t *testing.T) {
	tempDir := t.TempDir()
	ss := "this-is-a-test-secret-with-min-32-bytes"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)

	defer app.RuntimeManager.cancel()

	app.setDB()
	defer func() {
		_ = app.dbRoPool.Close()
		_ = app.dbRwPool.Close()
	}()

	username := "testuser"

	// Create a locked account
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	now := time.Now().Unix()
	futureTime := now + 3600
	err = cpcRw.Queries.UpsertLoginAttempt(app.RuntimeManager.ctx, gallerydb.UpsertLoginAttemptParams{
		Username:       username,
		FailedAttempts: 3,
		LastAttemptAt:  now,
		LockedUntil:    sql.NullInt64{Int64: futureTime, Valid: true},
	})
	if err != nil {
		t.Fatalf("UpsertLoginAttempt failed: %v", err)
	}

	// Verify account is locked
	attempt, err := cpcRw.Queries.GetLoginAttempt(app.RuntimeManager.ctx, username)
	if err != nil {
		t.Fatalf("GetLoginAttempt failed: %v", err)
	}
	if attempt.FailedAttempts != 3 {
		t.Errorf("expected failed_attempts 3, got %d", attempt.FailedAttempts)
	}
	if !attempt.LockedUntil.Valid {
		t.Error("expected locked_until to be set, but it was NULL")
	}

	// Unlock the account
	err = app.UnlockAccount(username)
	if err != nil {
		t.Fatalf("UnlockAccount failed: %v", err)
	}

	// Verify account is unlocked
	attempt, err = cpcRw.Queries.GetLoginAttempt(app.RuntimeManager.ctx, username)
	if err != nil {
		t.Fatalf("GetLoginAttempt failed: %v", err)
	}
	if attempt.FailedAttempts != 0 {
		t.Errorf("expected failed_attempts 0 after unlock, got %d", attempt.FailedAttempts)
	}
	if attempt.LockedUntil.Valid {
		t.Error("expected locked_until to be NULL after unlock, but it was set")
	}
}

func TestUnlockAccount_NonExistent(t *testing.T) {
	tempDir := t.TempDir()
	ss := "this-is-a-test-secret-with-min-32-bytes"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)

	defer app.RuntimeManager.cancel()

	app.setDB()
	defer func() {
		_ = app.dbRoPool.Close()
		_ = app.dbRwPool.Close()
	}()

	// Unlocking non-existent account should succeed (no rows affected is not an error)
	err := app.UnlockAccount("nonexistent")
	if err != nil {
		t.Fatalf("UnlockAccount on non-existent account should not error, got: %v", err)
	}
}

func TestApp_ImageDirectory_FromConfig_NotHardcoded(t *testing.T) {
	tempDir := t.TempDir()
	customImageDir := filepath.Join(tempDir, "custom-images")

	ss := "this-is-a-test-secret-with-min-32-bytes"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	app.SubsystemManager.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.SubsystemManager.scheduler.Start(app.RuntimeManager.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.SubsystemManager.scheduler != nil {
			_ = app.SubsystemManager.scheduler.Shutdown()
		}
	}()

	defaultImagesDir := filepath.Join(tempDir, "Images")
	if _, err := os.Stat(defaultImagesDir); !os.IsNotExist(err) {
		t.Errorf("setDBDirectory should not create Images directory, but it exists at %q", defaultImagesDir)
	}

	app.setDB()
	defer func() {
		if app.dbRoPool != nil {
			_ = app.dbRoPool.Close()
		}
		if app.dbRwPool != nil {
			_ = app.dbRwPool.Close()
		}
	}()

	app.setConfigDefaults()

	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ImageDirectory = customImageDir
	app.ConfigManager.ConfigMu.Unlock()

	app.ApplyConfig()

	if app.imagesDir != customImageDir {
		t.Errorf("expected imagesDir to be %q from config, got %q", customImageDir, app.imagesDir)
	}
	if _, err := os.Stat(customImageDir); os.IsNotExist(err) {
		t.Errorf("setImageDirectory should have created custom image directory at %q", customImageDir)
	}
}

func TestApp_ImageDirectory_CreatedAfterConfigLoad(t *testing.T) {
	tempDir := t.TempDir()
	customImageDir := filepath.Join(tempDir, "my-images")

	ss := "this-is-a-test-secret-with-min-32-bytes"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	app.SubsystemManager.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.SubsystemManager.scheduler.Start(app.RuntimeManager.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.SubsystemManager.scheduler != nil {
			_ = app.SubsystemManager.scheduler.Shutdown()
		}
	}()

	if _, err := os.Stat(customImageDir); !os.IsNotExist(err) {
		t.Fatalf("image directory should not exist before config load, but it does: %v", err)
	}

	app.setDB()
	defer func() {
		if app.dbRoPool != nil {
			_ = app.dbRoPool.Close()
		}
		if app.dbRwPool != nil {
			_ = app.dbRwPool.Close()
		}
	}()

	app.setConfigDefaults()

	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ImageDirectory = customImageDir
	app.ConfigManager.ConfigMu.Unlock()

	app.ApplyConfig()

	if _, err := os.Stat(customImageDir); os.IsNotExist(err) {
		t.Errorf("setImageDirectory should have created image directory at %q, but it doesn't exist", customImageDir)
	}

	if app.imagesDir != customImageDir {
		t.Errorf("expected imagesDir to be %q, got %q", customImageDir, app.imagesDir)
	}
}

func TestApp_ImageDirectory_CustomPath(t *testing.T) {
	tempDir := t.TempDir()
	customImageDir := filepath.Join(tempDir, "photos", "gallery")

	ss := "this-is-a-test-secret-with-min-32-bytes"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	app.SubsystemManager.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.SubsystemManager.scheduler.Start(app.RuntimeManager.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.SubsystemManager.scheduler != nil {
			_ = app.SubsystemManager.scheduler.Shutdown()
		}
	}()

	app.setDB()
	defer func() {
		if app.dbRoPool != nil {
			_ = app.dbRoPool.Close()
		}
		if app.dbRwPool != nil {
			_ = app.dbRwPool.Close()
		}
	}()

	app.setConfigDefaults()

	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ImageDirectory = customImageDir
	app.ConfigManager.ConfigMu.Unlock()

	app.ApplyConfig()

	defaultPath := filepath.Join(tempDir, "Images")
	if app.imagesDir == defaultPath {
		t.Errorf("expected custom path %q, but got default path %q", customImageDir, defaultPath)
	}

	if app.imagesDir != customImageDir {
		t.Errorf("expected imagesDir to be %q, got %q", customImageDir, app.imagesDir)
	}

	if _, err := os.Stat(customImageDir); os.IsNotExist(err) {
		t.Errorf("custom image directory should exist at %q, but it doesn't", customImageDir)
	}

	expectedNormalized := filepath.ToSlash(customImageDir)
	if app.normalizedImagesDir != expectedNormalized {
		t.Errorf("expected normalizedImagesDir to be %q, got %q", expectedNormalized, app.normalizedImagesDir)
	}
}

func TestImageDirectoryIntegration_StartupFlow(t *testing.T) {
	tempDir := t.TempDir()
	customImageDir := filepath.Join(tempDir, "custom-gallery")

	ss := "this-is-a-test-secret-with-min-32-bytes"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	app.SubsystemManager.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.SubsystemManager.scheduler.Start(app.RuntimeManager.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.SubsystemManager.scheduler != nil {
			_ = app.SubsystemManager.scheduler.Shutdown()
		}
	}()

	app.setDB()
	defer func() {
		if app.dbRoPool != nil {
			_ = app.dbRoPool.Close()
		}
		if app.dbRwPool != nil {
			_ = app.dbRwPool.Close()
		}
	}()

	app.setConfigDefaults()

	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ImageDirectory = customImageDir
	app.ConfigManager.ConfigMu.Unlock()

	app.ApplyConfig()

	if app.imagesDir != customImageDir {
		t.Errorf("expected imagesDir to be %q after startup flow, got %q", customImageDir, app.imagesDir)
	}

	if _, err := os.Stat(customImageDir); os.IsNotExist(err) {
		t.Errorf("custom image directory should exist after startup flow, but it doesn't: %v", err)
	}

	expectedNormalized := filepath.ToSlash(customImageDir)
	if app.normalizedImagesDir != expectedNormalized {
		t.Errorf("expected normalizedImagesDir to be %q, got %q", expectedNormalized, app.normalizedImagesDir)
	}
}

func TestImageDirectoryIntegration_RuntimeChange(t *testing.T) {
	tempDir := t.TempDir()
	initialImageDir := filepath.Join(tempDir, "initial")
	newImageDir := filepath.Join(tempDir, "new")

	ss := "this-is-a-test-secret-with-min-32-bytes"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	app.SubsystemManager.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.SubsystemManager.scheduler.Start(app.RuntimeManager.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.SubsystemManager.scheduler != nil {
			_ = app.SubsystemManager.scheduler.Shutdown()
		}
	}()

	app.setDB()
	defer func() {
		if app.dbRoPool != nil {
			_ = app.dbRoPool.Close()
		}
		if app.dbRwPool != nil {
			_ = app.dbRwPool.Close()
		}
	}()

	app.setConfigDefaults()

	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ImageDirectory = initialImageDir
	app.ConfigManager.ConfigMu.Unlock()

	app.ApplyConfig()

	if app.imagesDir != initialImageDir {
		t.Errorf("expected initial imagesDir to be %q, got %q", initialImageDir, app.imagesDir)
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ImageDirectory = newImageDir
	app.ConfigManager.ConfigMu.Unlock()

	app.ApplyConfig()

	if app.imagesDir != newImageDir {
		t.Errorf("expected imagesDir to be updated to %q after config change, got %q", newImageDir, app.imagesDir)
	}
}

func TestImageDirectoryIntegration_FileDiscoveryUsesConfig(t *testing.T) {
	tempDir := t.TempDir()
	customImageDir := filepath.Join(tempDir, "discovery-test")

	ss := "this-is-a-test-secret-with-min-32-bytes"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	app.SubsystemManager.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.SubsystemManager.scheduler.Start(app.RuntimeManager.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.SubsystemManager.scheduler != nil {
			_ = app.SubsystemManager.scheduler.Shutdown()
		}
	}()

	app.setDB()
	defer func() {
		if app.dbRoPool != nil {
			_ = app.dbRoPool.Close()
		}
		if app.dbRwPool != nil {
			_ = app.dbRwPool.Close()
		}
	}()

	app.setConfigDefaults()

	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ImageDirectory = customImageDir
	app.ConfigManager.ConfigMu.Unlock()

	app.ApplyConfig()

	if app.imagesDir != customImageDir {
		t.Errorf("expected imagesDir to be %q (what walkImageDir would use), got %q", customImageDir, app.imagesDir)
	}

	if _, err := os.Stat(customImageDir); os.IsNotExist(err) {
		t.Errorf("image directory should exist for walkImageDir to use, but it doesn't: %v", err)
	}
}

func TestApplyConfig_PanicsWhenImageDirectoryUndefined(t *testing.T) {
	tempDir := t.TempDir()

	ss := "this-is-a-test-secret-with-min-32-bytes"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	app.SubsystemManager.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.SubsystemManager.scheduler.Start(app.RuntimeManager.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.SubsystemManager.scheduler != nil {
			_ = app.SubsystemManager.scheduler.Shutdown()
		}
	}()

	app.setDB()
	defer func() {
		if app.dbRoPool != nil {
			_ = app.dbRoPool.Close()
		}
		if app.dbRwPool != nil {
			_ = app.dbRwPool.Close()
		}
	}()

	app.setConfigDefaults()

	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ImageDirectory = ""
	app.ConfigManager.ConfigMu.Unlock()

	app.imagesDir = ""
	app.normalizedImagesDir = ""

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when ImageDirectory is undefined")
		}
	}()

	app.ApplyConfig()
}

// TestApplyConfig_SyncDQueMaxDiskBytes verifies ApplyConfig hot-reloads the
// write batcher dque disk quota: mutating the in-memory config and applying it
// must update the batcher's stats without a restart.
func TestApplyConfig_SyncDQueMaxDiskBytes(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	if app.writeBatcher == nil {
		t.Fatal("writeBatcher not initialized by CreateApp")
	}

	const quota int64 = 123456789
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.DQueMaxDiskBytes = quota
	app.ConfigManager.ConfigMu.Unlock()

	app.ApplyConfig()

	if got := app.writeBatcher.GetStats().MaxDiskBytes; got != quota {
		t.Errorf("writeBatcher.MaxDiskBytes = %d, want %d after ApplyConfig", got, quota)
	}
}

func TestApp_Shutdown_DelegatesToSubsystemManager(t *testing.T) {
	app := createStartedApp(t)
	defer app.Shutdown()

	app.Shutdown()

	if app.SubsystemManager.preloadManager.GetScheduler() != nil {
		t.Error("preloadManager scheduler should be stopped after Shutdown")
	}
}

func TestApp_ResetStats_DelegatesToSubsystemManager(t *testing.T) {
	app := createStartedApp(t)
	defer app.Shutdown()

	app.SubsystemManager.processingStats.TotalFound.Store(5)
	app.SubsystemManager.processingStats.AlreadyExisting.Store(4)
	app.SubsystemManager.processingStats.NewlyInserted.Store(3)
	app.SubsystemManager.processingStats.SkippedInvalid.Store(2)
	app.SubsystemManager.processingStats.InFlight.Store(1)

	app.ResetStats()

	if app.SubsystemManager.processingStats.TotalFound.Load() != 0 {
		t.Errorf("TotalFound = %d, want 0", app.SubsystemManager.processingStats.TotalFound.Load())
	}
	if app.SubsystemManager.processingStats.AlreadyExisting.Load() != 0 {
		t.Errorf("AlreadyExisting = %d, want 0", app.SubsystemManager.processingStats.AlreadyExisting.Load())
	}
	if app.SubsystemManager.processingStats.NewlyInserted.Load() != 0 {
		t.Errorf("NewlyInserted = %d, want 0", app.SubsystemManager.processingStats.NewlyInserted.Load())
	}
	if app.SubsystemManager.processingStats.SkippedInvalid.Load() != 0 {
		t.Errorf("SkippedInvalid = %d, want 0", app.SubsystemManager.processingStats.SkippedInvalid.Load())
	}
	if app.SubsystemManager.processingStats.InFlight.Load() != 0 {
		t.Errorf("InFlight = %d, want 0", app.SubsystemManager.processingStats.InFlight.Load())
	}
}

func TestApp_LoadsETagFromConfig(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	ctx := context.Background()

	cfg, err := app.ConfigManager.ConfigService.Load(ctx)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	cfg.ETagVersion = "20260129-99"
	app.ConfigManager.ConfigService.Save(ctx, cfg)

	app.Shutdown()

	app2 := CreateApp(t, WithRoot(app.rootDir))

	cacheVer := ui.GetCacheVersion()
	if cacheVer != "20260129-99" {
		t.Errorf("UI cache version = %q, want %q", cacheVer, "20260129-99")
	}

	etagVer := app2.GetETagVersion()
	if etagVer != "20260129-99" {
		t.Errorf("GetETagVersion() = %q, want %q", etagVer, "20260129-99")
	}
}

func TestMemoryReclaimer(t *testing.T) {
	t.Run("triggers when idle", func(t *testing.T) {
		app := createAppWithPool(t)
		defer app.Shutdown()

		called := make(chan struct{}, 1)
		cfg := MemoryReclaimerConfig{
			InitialDelay:  10 * time.Millisecond,
			CheckInterval: 20 * time.Millisecond,
			IdleThreshold: 50 * time.Millisecond,
			FreeMemFunc:   func() { called <- struct{}{} },
		}

		app.SubsystemManager.pool.AddCompleted()

		done := make(chan struct{})
		go func() {
			app.memoryReclaimer(cfg)
			close(done)
		}()

		select {
		case <-called:
		case <-time.After(2 * time.Second):
			t.Error("FreeMemFunc was not called while idle")
		}

		app.RuntimeManager.cancel()
		<-done
	})

	t.Run("does not trigger when queue is not empty", func(t *testing.T) {
		app := CreateApp(t)
		defer app.Shutdown()

		app.SubsystemManager.q.Enqueue("dummy-path")

		called := make(chan struct{}, 1)
		cfg := MemoryReclaimerConfig{
			InitialDelay:  10 * time.Millisecond,
			CheckInterval: 20 * time.Millisecond,
			IdleThreshold: 50 * time.Millisecond,
			FreeMemFunc:   func() { called <- struct{}{} },
		}

		app.SubsystemManager.pool.AddCompleted()

		done := make(chan struct{})
		go func() {
			app.memoryReclaimer(cfg)
			close(done)
		}()

		select {
		case <-called:
			t.Error("FreeMemFunc was called even though queue was not empty")
		case <-time.After(300 * time.Millisecond):
		}

		app.RuntimeManager.cancel()
		<-done
	})

	t.Run("does not trigger when recently active", func(t *testing.T) {
		app := createAppWithPool(t)
		defer app.Shutdown()

		called := make(chan struct{}, 1)
		cfg := MemoryReclaimerConfig{
			InitialDelay:  10 * time.Millisecond,
			CheckInterval: 20 * time.Millisecond,
			IdleThreshold: 1 * time.Second,
			FreeMemFunc:   func() { called <- struct{}{} },
		}

		app.SubsystemManager.pool.AddCompleted()

		done := make(chan struct{})
		go func() {
			app.memoryReclaimer(cfg)
			close(done)
		}()

		select {
		case <-called:
			t.Error("FreeMemFunc was called even though pool was recently active")
		case <-time.After(300 * time.Millisecond):
		}

		app.RuntimeManager.cancel()
		<-done
	})
}

func TestApp_ApplyConfig_InvalidatesETagWhenChanged(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	oldETag := "20260101-01"
	newETag := "20260101-02"

	ui.SetCacheVersion(oldETag)

	ctx := app.RuntimeManager.ctx
	entry := &cachelite.HTTPCacheEntry{
		Key:           "etag-test-key",
		Method:        "GET",
		Path:          "/gallery/1",
		Status:        200,
		Body:          []byte("test"),
		ContentLength: sql.NullInt64{Int64: 4, Valid: true},
		CreatedAt:     time.Now().Unix(),
	}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("failed to seed cache entry: %v", err)
	}
	before, err := cachelite.CountCacheEntries(ctx, app.dbRwPool)
	if err != nil {
		t.Fatalf("failed to count cache entries before: %v", err)
	}
	if before != 1 {
		t.Fatalf("expected 1 cache entry before ETag change, got %d", before)
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ETagVersion = newETag
	app.ConfigManager.ConfigMu.Unlock()

	app.ApplyConfig()

	if got := ui.GetCacheVersion(); got != newETag {
		t.Errorf("ui.GetCacheVersion() = %q, want %q", got, newETag)
	}

	after, err := cachelite.CountCacheEntries(ctx, app.dbRwPool)
	if err != nil {
		t.Fatalf("failed to count cache entries after: %v", err)
	}
	if after != 0 {
		t.Errorf("expected cache to be invalidated after ETag change, got %d entries", after)
	}
}

func TestApp_ApplyConfig_ETagUnchangedSkipsInvalidate(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	const etag = "same-etag"
	ui.SetCacheVersion(etag)

	ctx := app.RuntimeManager.ctx
	entry := &cachelite.HTTPCacheEntry{
		Key:           "etag-unchanged-key",
		Method:        "GET",
		Path:          "/gallery/1",
		Status:        200,
		Body:          []byte("test"),
		ContentLength: sql.NullInt64{Int64: 4, Valid: true},
		CreatedAt:     time.Now().Unix(),
	}
	if err := cachelite.StoreCacheEntry(ctx, app.dbRwPool, entry); err != nil {
		t.Fatalf("failed to seed cache entry: %v", err)
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ETagVersion = etag
	app.ConfigManager.ConfigMu.Unlock()

	app.ApplyConfig()

	after, err := cachelite.CountCacheEntries(ctx, app.dbRwPool)
	if err != nil {
		t.Fatalf("failed to count cache entries: %v", err)
	}
	if after != 1 {
		t.Errorf("expected cache entry to be preserved, got %d entries", after)
	}
}

func TestApp_ApplyConfig_PreloadManagerSetEnabled(t *testing.T) {
	app := createStartedApp(t)
	defer app.Shutdown()

	if app.SubsystemManager.preloadManager == nil {
		t.Fatal("preloadManager should be wired after Start")
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.EnableCachePreload = true
	app.ConfigManager.ConfigMu.Unlock()
	app.ApplyConfig()
	if !app.SubsystemManager.preloadManager.IsEnabled() {
		t.Error("expected preload manager to be enabled")
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.EnableCachePreload = false
	app.ConfigManager.ConfigMu.Unlock()
	app.ApplyConfig()
	if app.SubsystemManager.preloadManager.IsEnabled() {
		t.Error("expected preload manager to be disabled")
	}
}

func TestApp_setDB_RebuildsHandlersWhenAlreadyBuilt(t *testing.T) {
	app := createAppWithHandlers(t)
	defer app.Shutdown()

	if app.writeBatcher != nil {
		if err := app.writeBatcher.Close(); err != nil {
			t.Fatalf("failed to close writeBatcher: %v", err)
		}
	}

	app.setDB()

	if app.HandlerManager.authHandlers == nil {
		t.Error("expected authHandlers to be rebuilt after setDB")
	}
	if app.HandlerManager.galleryHandlers == nil {
		t.Error("expected galleryHandlers to be rebuilt after setDB")
	}
}

func TestApp_reconfigurePoolsFromConfig_RebuildsHandlers(t *testing.T) {
	app := createAppWithHandlers(t)
	defer app.Shutdown()

	app.ConfigManager.ConfigMu.Lock()
	currentMax := app.ConfigManager.Config.DBMaxPoolSize
	app.ConfigManager.Config.DBMaxPoolSize = currentMax + 1
	app.ConfigManager.ConfigMu.Unlock()

	app.InfrastructureService.testSeams.RecreatePoolsWithConfig = func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return oldRw, oldRo, nil
	}

	if err := app.reconfigurePoolsFromConfig(); err != nil {
		t.Fatalf("reconfigurePoolsFromConfig failed: %v", err)
	}

	if app.HandlerManager.authHandlers == nil {
		t.Error("expected authHandlers to remain wired after reconfigure")
	}
	if app.HandlerManager.galleryHandlers == nil {
		t.Error("expected galleryHandlers to remain wired after reconfigure")
	}
}

func TestApp_setDB_RebuildHandlersError(t *testing.T) {
	app := createAppWithHandlers(t)
	defer app.Shutdown()

	app.HandlerManager.testSeams.BuildHandlers = func(fs.FS) error {
		return fmt.Errorf("rebuild failed")
	}

	if app.writeBatcher != nil {
		if err := app.writeBatcher.Close(); err != nil {
			t.Fatalf("failed to close writeBatcher: %v", err)
		}
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when handler rebuild fails")
		}
	}()

	app.setDB()
}

func TestApp_reconfigurePoolsFromConfig_NilConfig(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = nil
	app.ConfigManager.ConfigMu.Unlock()

	if err := app.reconfigurePoolsFromConfig(); err != nil {
		t.Fatalf("reconfigurePoolsFromConfig with nil config returned error: %v", err)
	}
}

func TestApp_reconfigurePoolsFromConfig_UpdatesCacheMiddleware(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.EnableHTTPCache = true
	app.ConfigManager.ConfigMu.Unlock()
	app.StartWriteBatcher(app.RuntimeManager.ctx, true, config.DefaultDQueMaxDiskBytes)
	app.initializeHTTPCache()

	if app.cacheMW == nil {
		t.Fatal("expected cacheMW to be initialized")
	}

	app.ConfigManager.ConfigMu.Lock()
	currentMax := app.ConfigManager.Config.DBMaxPoolSize
	app.ConfigManager.Config.DBMaxPoolSize = currentMax + 1
	app.ConfigManager.ConfigMu.Unlock()

	app.InfrastructureService.testSeams.RecreatePoolsWithConfig = func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return oldRw, oldRo, nil
	}

	if err := app.reconfigurePoolsFromConfig(); err != nil {
		t.Fatalf("reconfigurePoolsFromConfig failed: %v", err)
	}
}

func TestApp_reconfigurePoolsFromConfig_BuildHandlersError(t *testing.T) {
	app := createAppWithHandlers(t)
	defer app.Shutdown()

	app.ConfigManager.ConfigMu.Lock()
	currentMax := app.ConfigManager.Config.DBMaxPoolSize
	app.ConfigManager.Config.DBMaxPoolSize = currentMax + 1
	app.ConfigManager.ConfigMu.Unlock()

	app.HandlerManager.testSeams.BuildHandlers = func(fs.FS) error {
		return fmt.Errorf("rebuild failed")
	}

	app.InfrastructureService.testSeams.RecreatePoolsWithConfig = func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return oldRw, oldRo, nil
	}

	err := app.reconfigurePoolsFromConfig()
	if err == nil {
		t.Fatal("expected reconfigurePoolsFromConfig to return error")
	}
	if !strings.Contains(err.Error(), "rebuild failed") {
		t.Errorf("error = %q, want rebuild failed", err.Error())
	}
}

func TestUnlockAccount_PoolGetError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()
	_ = app.dbRwPool.Close()

	err := app.UnlockAccount("anyone")
	if err == nil {
		t.Fatal("expected error when pool Get fails")
	}
	if !strings.Contains(err.Error(), "failed to get database connection") {
		t.Errorf("error = %q, want connection failure", err.Error())
	}
}

func TestUnlockAccount_QueryError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()
	app.RuntimeManager.cancel()

	err := app.UnlockAccount("anyone")
	if err == nil {
		t.Fatal("expected error when unlock query fails")
	}
	if !strings.Contains(err.Error(), "failed to unlock account") {
		t.Errorf("error = %q, want unlock failure", err.Error())
	}
}

func TestApp_LoadConfig_StoreErrorFallsBackToDefaults(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	original := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(original)
	})

	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()
	app.setRootDir(&tempDir)
	app.setDB()

	validCfg := config.DefaultConfig()
	validCfg.ImageDirectory = filepath.Join(tempDir, "from-fake-service")
	fakeSvc := &errorConfigService{
		loadCfg: validCfg,
		loadErr: errors.New("load failed"),
	}
	app.ConfigManager.ConfigService = fakeSvc

	err := app.loadConfig()
	if err != nil {
		t.Fatalf("Expected loadConfig to swallow store error, got: %v", err)
	}
	if !fakeSvc.loadCalled {
		t.Error("Expected ConfigService.Load to be called")
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "failed to load config from database") {
		t.Errorf("Expected database load warning log, got: %s", logs)
	}
	if app.ConfigManager.Config == nil {
		t.Fatal("Expected app.ConfigManager.Config to be set")
	}
	wantImageDir := filepath.Join(tempDir, "Images")
	if app.ConfigManager.Config.ImageDirectory != wantImageDir {
		t.Errorf("ImageDirectory = %q, want %q", app.ConfigManager.Config.ImageDirectory, wantImageDir)
	}
}

func TestInitForIncrementETag_EnsureDefaultsError(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	tempDir := t.TempDir()
	app.setRootDir(&tempDir)
	app.testSeams.DatabaseSetup = database.Setup
	fakeSvc := &ensureDefaultsFailConfigService{}
	app.testSeams.ConfigService = fakeSvc

	err := app.InitForIncrementETag(getopt.Opt{})
	if err == nil {
		t.Fatal("expected error when EnsureDefaults fails")
	}
	if !strings.Contains(err.Error(), "failed to ensure config defaults") {
		t.Errorf("error = %q, want ensure defaults failure", err.Error())
	}
	if !fakeSvc.ensureDefaultsCalled {
		t.Error("expected EnsureDefaults to be called")
	}
	if fakeSvc.ensureDefaultsRoot != app.rootDir {
		t.Errorf("EnsureDefaults rootDir = %q, want %q", fakeSvc.ensureDefaultsRoot, app.rootDir)
	}
}

// --- merged from app_profile_integration_test.go ---
func TestApp_LogProfileLocation(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	original := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(original)
	})

	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	stopProfiler, err := profiler.Start(profiler.Config{Mode: "cpu"})
	if err != nil {
		t.Fatalf("profiler.Start failed: %v", err)
	}
	defer stopProfiler()

	stopped := false
	app.RuntimeManager.stopProfiler = func() {
		stopped = true
		stopProfiler()
	}

	app.LogProfileLocation()

	if !stopped {
		t.Error("Expected stopProfiler to be called")
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "Profile artifacts written") {
		t.Errorf("Expected profile log, got: %s", logs)
	}

	app.LogProfileLocation() // second call must be no-op
	if app.RuntimeManager.stopProfiler != nil {
		t.Error("stopProfiler should remain nil after second LogProfileLocation")
	}
}

func TestApp_Shutdown_FlushesCPUProfile(t *testing.T) {
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
	}
	app := New(opt, "x.y.z")

	stopProfiler, err := profiler.Start(profiler.Config{Mode: "cpu"})
	if err != nil {
		t.Fatalf("profiler.Start failed: %v", err)
	}
	app.RuntimeManager.stopProfiler = stopProfiler
	// pkg/profile uses a package-level started flag; if Start succeeds but the test
	// fails before Stop, the next profiler.Start in the suite fatals the binary.
	t.Cleanup(func() {
		if app.RuntimeManager.stopProfiler != nil {
			app.RuntimeManager.stopProfiler()
		}
	})
	defer app.Shutdown()

	profileDir := profiler.Dir()
	if profileDir == "" {
		t.Fatal("expected profiler.Dir() to be set")
	}
	profilePath := filepath.Join(profileDir, "cpu.pprof")

	app.Shutdown()

	if _, err := os.Stat(profilePath); err != nil {
		t.Fatalf("expected %s after Shutdown: %v", profilePath, err)
	}
	info, err := os.Stat(profilePath)
	if err != nil {
		t.Fatalf("stat profile: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected non-empty %s", profilePath)
	}
	if app.RuntimeManager.stopProfiler != nil {
		t.Error("stopProfiler should be nil after Shutdown")
	}
}

// --- merged from app_serve_test.go ---
func TestApp_Serve_DelegatesToRuntimeManager(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ListenerPort = port
	app.ConfigManager.ConfigMu.Unlock()

	app.RuntimeManager.testSeams.BeforeListen = func() { app.RuntimeManager.cancel() }

	if err := app.Serve(); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	if app.testSeams.Serve != nil {
		t.Error("testSeams.Serve should not be set in this test")
	}
}

func TestApp_Serve_NilConfig_LoadsDefaults(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = nil
	app.ConfigManager.ConfigMu.Unlock()

	app.RuntimeManager.testSeams.BeforeListen = func() { app.RuntimeManager.cancel() }

	if err := app.Serve(); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	if app.GetConfig() == nil {
		t.Fatal("expected config to be loaded after Serve")
	}
}

func TestApp_Serve_NilConfig_LoadConfigErrorFallsBack(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)
	app.setDB()

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = nil
	app.ConfigManager.ConfigMu.Unlock()

	app.testSeams.LoadConfig = func() (*config.Config, error) {
		return nil, fmt.Errorf("load failed")
	}
	app.RuntimeManager.testSeams.BeforeListen = func() { app.RuntimeManager.cancel() }

	if err := app.Serve(); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	if app.GetConfig() == nil {
		t.Fatal("expected fallback config to be set after Serve")
	}
}

func TestApp_Serve_BuildsHandlersWhenNil(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.HandlerManager.authHandlers = nil
	app.HandlerManager.galleryHandlers = nil
	app.HandlerManager.configHandlers = nil

	app.RuntimeManager.testSeams.BeforeListen = func() { app.RuntimeManager.cancel() }

	if err := app.Serve(); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	if app.HandlerManager.authHandlers == nil {
		t.Error("expected authHandlers to be built by Serve")
	}
	if app.HandlerManager.galleryHandlers == nil {
		t.Error("expected galleryHandlers to be built by Serve")
	}
}

func TestApp_Serve_BuildHandlersError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.HandlerManager.authHandlers = nil
	app.HandlerManager.testSeams.BuildHandlers = func(fs.FS) error {
		return fmt.Errorf("build failed")
	}

	if err := app.Serve(); err == nil {
		t.Fatal("expected Serve to return build error")
	} else if err.Error() != "build failed" {
		t.Errorf("Serve error = %q, want %q", err.Error(), "build failed")
	}
}

// --- merged from app_startup_integration_test.go ---
type recordingMemoryReclaimerIntegration struct {
	once    sync.Once
	started chan struct{}
	cfg     MemoryReclaimerConfig
}

func (r *recordingMemoryReclaimerIntegration) Reclaim(cfg MemoryReclaimerConfig) {
	r.once.Do(func() {
		if r.started != nil {
			close(r.started)
		}
	})
	r.cfg = cfg
}

// waitForGoroutinesToSettle polls until the goroutine count drops back to the
// baseline (allowing a small margin for runtime background threads) or the
// deadline expires. This replaces a fixed Sleep before goroutine-leak asserts.
func waitForGoroutinesToSettle(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		remaining := runtime.NumGoroutine()
		if remaining <= baseline+2 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("goroutine leak: baseline=%d remaining=%d", baseline, remaining)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRun_Integration_FullStartupAndShutdown(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	reclaimer := &recordingMemoryReclaimerIntegration{started: make(chan struct{})}
	app.testSeams.MemoryReclaimer = reclaimer.Reclaim

	baseline := runtime.NumGoroutine()

	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run(1, 1)
		close(done)
	}()

	// Wait for Run to complete (Serve returns immediately via the hook).
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not complete within timeout")
	}

	if runErr != nil {
		t.Fatalf("Run failed: %v", runErr)
	}
	if !serveHook.called {
		t.Error("Serve was not called")
	}
	// The memory reclaimer is started in a goroutine right before Serve.
	select {
	case <-reclaimer.started:
	case <-time.After(500 * time.Millisecond):
		t.Error("memory reclaimer was not started")
	}

	app.Shutdown()

	// Wait for spawned goroutines to exit before counting; allow a small
	// margin for runtime background threads that we do not control.
	waitForGoroutinesToSettle(t, baseline)
}

func TestRun_Integration_HTTPCacheCleanupGoroutineStarts(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	reclaimer := &recordingMemoryReclaimerIntegration{}
	app.testSeams.MemoryReclaimer = reclaimer.Reclaim

	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run(1, 1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not complete within timeout")
	}

	if runErr != nil {
		t.Fatalf("Run failed: %v", runErr)
	}
	if app.cacheMW == nil {
		t.Fatal("HTTP cache middleware was not initialized")
	}

	app.Shutdown()
}

func TestRun_Integration_HTTPCacheCleanupGoroutineExits(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:    getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		EnableHTTPCache:  getopt.OptBool{Bool: true, IsSet: true},
		RunFileDiscovery: getopt.OptBool{Bool: false, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	reclaimer := &recordingMemoryReclaimerIntegration{}
	app.testSeams.MemoryReclaimer = reclaimer.Reclaim

	baseline := runtime.NumGoroutine()

	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run(1, 1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not complete within timeout")
	}

	if runErr != nil {
		t.Fatalf("Run failed: %v", runErr)
	}
	if app.cacheMW == nil {
		t.Fatal("HTTP cache middleware was not initialized")
	}

	app.Shutdown()

	// Wait for spawned goroutines to exit before counting; allow a small
	// margin for runtime background threads that we do not control.
	waitForGoroutinesToSettle(t, baseline)
}

func TestRun_Integration_BatchLoadManagerCreatedWhenCacheEnabled(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:    getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		EnableHTTPCache:  getopt.OptBool{Bool: true, IsSet: true},
		RunFileDiscovery: getopt.OptBool{Bool: false, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	reclaimer := &recordingMemoryReclaimerIntegration{}
	app.testSeams.MemoryReclaimer = reclaimer.Reclaim

	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run(1, 1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not complete within timeout")
	}

	if runErr != nil {
		t.Fatalf("Run failed: %v", runErr)
	}
	if app.SubsystemManager.batchLoadManager == nil {
		t.Fatal("batchLoadManager was not created when HTTP cache is enabled")
	}
	if app.RuntimeManager.metricsCollector == nil {
		t.Fatal("metricsCollector was not initialized")
	}
}

func TestApp_Run_ProfilerUsesRealStart(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		Profile:       getopt.OptString{String: "cpu", IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	reclaimer := &recordingMemoryReclaimerIntegration{}
	app.testSeams.MemoryReclaimer = reclaimer.Reclaim

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if app.RuntimeManager.stopProfiler == nil {
		t.Fatal("expected stopProfiler to be set when real profiler starts")
	}

	app.RuntimeManager.stopProfiler()

	if profiler.Dir() == "" {
		t.Error("expected profiler.Dir() to be non-empty after real start")
	}
}

func TestStartupLogging_BootstrapThenReload_CapturesEarlyLogs(t *testing.T) {
	tmpDir := t.TempDir()
	bootstrapLogDir := filepath.Join(tmpDir, "logs")
	configuredLogDir := filepath.Join(tmpDir, "configured-logs")

	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	app.rootDir = tmpDir
	app.SubsystemManager.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.SubsystemManager.scheduler.Start(app.RuntimeManager.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() { _ = app.SubsystemManager.scheduler.Shutdown() }()

	app.setupBootstrapLogging()

	if info, err := os.Stat(bootstrapLogDir); err != nil || !info.IsDir() {
		t.Fatalf("bootstrap logs directory should exist: %v", err)
	}
	bootstrapLogFile := app.logger.FilePath()
	if info, err := os.Stat(bootstrapLogFile); err != nil || info.IsDir() {
		t.Fatalf("bootstrap log file should exist: %v", err)
	}

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = &config.Config{
		LogDirectory: configuredLogDir,
		LogLevel:     "debug",
	}
	app.ConfigManager.ConfigMu.Unlock()

	if err := app.reloadLoggingFromConfig(); err != nil {
		t.Fatalf("reloadLoggingFromConfig should not fail: %v", err)
	}

	if info, err := os.Stat(configuredLogDir); err != nil || !info.IsDir() {
		t.Fatalf("configured logs directory should exist: %v", err)
	}

	newLogFile := app.logger.FilePath()
	if newLogFile == bootstrapLogFile {
		t.Fatal("log file path should have changed to configured directory")
	}
	if info, err := os.Stat(newLogFile); err != nil || info.IsDir() {
		t.Fatalf("new log file should exist: %v", err)
	}
	if filepath.Dir(newLogFile) != configuredLogDir {
		t.Fatalf("new log file should be in configured directory: got %s, want %s", filepath.Dir(newLogFile), configuredLogDir)
	}
	if filepath.Base(newLogFile) != filepath.Base(bootstrapLogFile) {
		t.Fatalf("log file name should be preserved: got %s, want %s", filepath.Base(newLogFile), filepath.Base(bootstrapLogFile))
	}

	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

func TestStartupLogging_BootstrapThenReload_SameDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	app.rootDir = tmpDir
	app.SubsystemManager.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.SubsystemManager.scheduler.Start(app.RuntimeManager.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() { _ = app.SubsystemManager.scheduler.Shutdown() }()

	app.setupBootstrapLogging()

	originalLogger := app.logger
	originalLogFile := app.logger.File()

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = &config.Config{
		LogDirectory: "logs",
		LogLevel:     "debug",
	}
	app.ConfigManager.ConfigMu.Unlock()

	if err := app.reloadLoggingFromConfig(); err != nil {
		t.Fatalf("reloadLoggingFromConfig should not fail: %v", err)
	}

	if app.logger != originalLogger {
		t.Fatal("logger should remain unchanged when directory is same")
	}
	if app.logger.File() != originalLogFile {
		t.Fatal("log file should remain unchanged when directory is same")
	}

	if app.logger != nil {
		_ = app.logger.Shutdown()
	}
}

func TestLogStartupConfigSummary_EmitsConfiguredVsEffective(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	logBuf := captureLogs(t)

	app.ConfigManager.ConfigMu.RLock()
	cfg := app.ConfigManager.Config
	app.ConfigManager.ConfigMu.RUnlock()

	// Ensure non-default configured values.
	cfg.DBMaxPoolSize = 50
	cfg.DBMinIdleConnections = 5
	cfg.WorkerPoolMax = 20
	cfg.WorkerPoolMinIdle = 3
	cfg.QueueSize = 5000
	cfg.EnableHTTPCache = true
	cfg.EnableCachePreload = true
	cfg.RunFileDiscovery = false

	// Wire effective values.
	app.SubsystemManager.pool = workerpool.NewPool(app.RuntimeManager.ctx, 3, 20, 10*time.Second)

	app.logStartupConfigSummary(5000, false)

	logs := logBuf.String()
	keys := []string{
		"db_configured_max",
		"db_rw_effective_max",
		"worker_configured_max",
		"worker_effective_max",
		"queue_configured_size",
		"queue_effective_size",
		"cache_configured_enabled",
		"preload_configured_enabled",
		"discovery_configured_enabled",
	}
	for _, key := range keys {
		if !strings.Contains(logs, key) {
			t.Errorf("expected log key %q missing, got: %s", key, logs)
		}
	}
}

func TestLogStartupConfigSummary_NilConfig(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	logBuf := captureLogs(t)

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config = nil
	app.ConfigManager.ConfigMu.Unlock()

	app.logStartupConfigSummary(1000, true)

	if logBuf.Len() != 0 {
		t.Errorf("expected no log output, got: %s", logBuf.String())
	}
}

func TestStartup_OrderingConstraint(t *testing.T) {
	tempDir := t.TempDir()

	opt := getopt.Opt{
		SessionSecret: getopt.OptString{
			String: "test-secret-ordering",
			IsSet:  true,
		},
	}

	// Track the order of operations
	var mu sync.Mutex
	operationOrder := []string{}
	recordOp := func(op string) {
		mu.Lock()
		operationOrder = append(operationOrder, op)
		mu.Unlock()
	}

	app := New(opt, "x.y.z")
	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()

	// Wrap critical operations to track ordering
	recordOp("start_setDB")
	app.setDB()
	recordOp("end_setDB")

	recordOp("start_loadConfig")
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	recordOp("end_loadConfig")

	// Verify ordering: setDB should complete before loadConfig
	mu.Lock()
	order := make([]string, len(operationOrder))
	copy(order, operationOrder)
	mu.Unlock()

	// Check that operations happened in the correct order
	expectedOrder := []string{
		"start_setDB",
		"end_setDB",
		"start_loadConfig",
		"end_loadConfig",
	}

	if len(order) != len(expectedOrder) {
		t.Errorf("unexpected number of operations: got %d, want %d", len(order), len(expectedOrder))
	}

	for i := range expectedOrder {
		if i >= len(order) {
			t.Errorf("missing operation at index %d: expected %s", i, expectedOrder[i])
			continue
		}
		if order[i] != expectedOrder[i] {
			t.Errorf("operation order mismatch at index %d: got %s, want %s", i, order[i], expectedOrder[i])
		}
	}

	// ASSERTION: Verify that pools were created with config values, not defaults
	// This checks that loadConfig() preceded pool creation
	app.ConfigManager.ConfigMu.RLock()
	configMaxPool := app.ConfigManager.Config.DBMaxPoolSize
	app.ConfigManager.ConfigMu.RUnlock()

	actualMaxPool := app.dbRwPool.Config.MaxConnections

	// If pools were created before config was loaded, they would have default values (100)
	// After loadConfig, they should have config values
	if actualMaxPool != int64(configMaxPool) {
		t.Errorf("DEFECT: Pool was created before config was applied. "+
			"Pool has MaxConnections=%d, but config has DBMaxPoolSize=%d. "+
			"This indicates setDB created pools before loadConfig was called.",
			actualMaxPool, configMaxPool)
	}

	app.Shutdown()

	// EXPECTED: This test SHOULD FAIL if pools are created before configuration
	// is loaded, indicating improper initialization ordering.
}

func TestApp_Run_DefaultStartup(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	reclaimer := &recordingMemoryReclaimerIntegration{started: make(chan struct{})}
	app.testSeams.MemoryReclaimer = reclaimer.Reclaim

	if err := app.Run(1, 2); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if app.SubsystemManager.scheduler == nil {
		t.Error("scheduler not initialized")
	}
	if app.dbRwPool == nil || app.dbRoPool == nil {
		t.Error("database pools not initialized")
	}
	if app.ConfigManager.Config == nil {
		t.Error("config not loaded")
	}
	if app.SubsystemManager.pool == nil {
		t.Fatal("worker pool not created")
	}
	if app.SubsystemManager.pool.MinWorkers != 1 || app.SubsystemManager.pool.MaxWorkers != 2 {
		t.Errorf("pool bounds = (%d, %d), want (1, 2)", app.SubsystemManager.pool.MinWorkers, app.SubsystemManager.pool.MaxWorkers)
	}
	if app.SubsystemManager.fileProcessor == nil {
		t.Error("fileProcessor not initialized")
	}
	if app.SubsystemManager.preloadManager == nil {
		t.Error("preloadManager not initialized")
	}
	if app.RuntimeManager.metricsCollector == nil {
		t.Error("metricsCollector not initialized")
	}
	if !serveHook.called {
		t.Error("App.Serve was not called")
	}
	if serveHook.handler == nil {
		t.Error("Serve handler is nil")
	}
	if !strings.Contains(serveHook.addr, ":") {
		t.Errorf("Serve addr = %q, expected listener address", serveHook.addr)
	}
	select {
	case <-reclaimer.started:
	case <-time.After(500 * time.Millisecond):
		t.Error("memory reclaimer was not started")
	}

	// Explicit shutdown (defer also runs, but shutdownOnce makes it safe).
	app.Shutdown()

	if app.logger == nil {
		t.Fatal("expected app.logger to be set")
	}
	logsBytes, err := os.ReadFile(app.logger.FilePath())
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	logs := string(logsBytes)
	if !strings.Contains(logs, "startup config summary") {
		t.Errorf("startup summary not logged, got: %s", logs)
	}
	for _, key := range []string{"worker_configured_max", "worker_effective_max", "db_rw_effective_max", "queue_configured_size", "cache_configured_enabled"} {
		if !strings.Contains(logs, key) {
			t.Errorf("expected log key %q missing, got: %s", key, logs)
		}
	}
}

func TestApp_Run_LoadConfigFails_FallsBackToDefaults(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	fallbackCalled := false
	fallbackCfg := validConfigWithImageDir(tempDir)
	fallbackCfg.WorkerPoolMax = 7
	app.testSeams.FallbackConfig = func() *config.Config {
		fallbackCalled = true
		return fallbackCfg
	}

	app.testSeams.LoadConfig = func() (*config.Config, error) {
		return nil, fmt.Errorf("database load failed")
	}

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !fallbackCalled {
		t.Error("fallback config was not used")
	}
	if app.ConfigManager.Config.WorkerPoolMax != 7 {
		t.Errorf("WorkerPoolMax = %d, want 7", app.ConfigManager.Config.WorkerPoolMax)
	}
	if !serveHook.called {
		t.Error("Serve was not called")
	}
}

func TestApp_Run_RestartRequested(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	app.TriggerRestart()

	execCalled := false
	app.RuntimeManager.testSeams.Executable = func() (string, error) {
		return "/tmp/test-exe", nil
	}
	app.RuntimeManager.testSeams.ExecCommand = func(path string, args []string, env []string) error {
		execCalled = true
		return nil
	}
	app.RuntimeManager.testSeams.Exit = func(code int) {}

	serveHook := &recordingServeHook{}
	app.testSeams.Serve = serveHook.Serve

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !app.IsRestartRequested() {
		t.Error("restart request flag should still be set")
	}
	if !execCalled {
		t.Error("ExecRestart was not invoked")
	}
}

func TestApp_Run_DiscoveryMonitor_CompletionLog(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{
		SessionSecret:    getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true},
		RunFileDiscovery: getopt.OptBool{Bool: true, IsSet: true},
	}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)

	// Stub out real directory walking: the completion monitor only needs
	// the fake sender/stats below, and real discovery would race this test.
	app.testSeams.TriggerDiscovery = func() {}

	// Pretend a discovery sender is active so the monitor enters the end-loop.
	app.SubsystemManager.qSendersActive.Store(1)

	app.testSeams.Serve = func(h http.Handler, addr string) error {
		// processingStats is initialized by SubsystemManager.Start, which runs
		// before Serve. Prime the found counter so the monitor's phase-1 gate
		// is satisfied even if the sender was cleared before its first poll.
		app.SubsystemManager.processingStats.TotalFound.Store(1)

		// Clear the active sender; the monitor observes completion on its next
		// poll and logs the summary. Wait for that log before canceling.
		app.SubsystemManager.qSendersActive.Store(0)

		deadline := time.Now().Add(5 * time.Second)
		for {
			data, err := os.ReadFile(app.logger.FilePath())
			if err == nil && strings.Contains(string(data), "File processing completed") {
				app.RuntimeManager.cancel()
				return nil
			}
			if time.Now().After(deadline) {
				app.RuntimeManager.cancel()
				t.Fatal("monitor did not log 'File processing completed'")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	app.testSeams.MemoryReclaimer = (&recordingMemoryReclaimerIntegration{}).Reclaim

	if err := app.Run(1, 1); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	logs := readStartupLogs(t, app)
	if !strings.Contains(logs, "File processing completed") {
		t.Errorf("expected 'File processing completed' log, got: %s", logs)
	}
}

// --- merged from restart_cli_test.go ---
func TestCase1_CLIOverridesUnchangedField(t *testing.T) {
	// Create app with CLI port=8083
	opt := getopt.Opt{
		Port: getopt.OptInt{Int: 8083, IsSet: true},
	}
	app := CreateApp(t, WithGetoptOpt(opt))
	defer app.Shutdown()

	// Simulate DB having port=8081 (different from CLI)
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.ListenerPort = 8081
	app.ConfigManager.ConfigMu.Unlock()

	// Simulate user changing a field (not port) via web UI
	// changedFields does NOT include "listener_port"
	newConfig := config.DefaultConfig()
	newConfig.ListenerPort = 8081  // DB value (unchanged)
	newConfig.SiteName = "Updated" // User changed site name

	// Call UpdateConfig with site_name in changedFields
	app.UpdateConfigWithPrecedence(newConfig, []string{"site_name"})

	// After UpdateConfig, port should be 8083 (CLI value) because it wasn't changed
	app.ConfigManager.ConfigMu.RLock()
	port := app.ConfigManager.Config.ListenerPort
	app.ConfigManager.ConfigMu.RUnlock()

	if port != 8083 {
		t.Errorf("Case 1 FAILED: Expected port 8083 (CLI value), got %d. "+
			"CLI values should override unchanged fields.", port)
	} else {
		t.Logf("Case 1 PASSED: Port is 8083 (CLI value) as expected")
	}
}

func TestCase2_UserChangeOverridesCLI(t *testing.T) {
	// Create app with CLI port=8083
	opt := getopt.Opt{
		Port: getopt.OptInt{Int: 8083, IsSet: true},
	}
	app := CreateApp(t, WithGetoptOpt(opt))
	defer app.Shutdown()

	// Simulate user changing port to 8084 via web UI
	newConfig := config.DefaultConfig()
	newConfig.ListenerPort = 8084 // User changed port

	// Call UpdateConfig with listener_port in changedFields
	app.UpdateConfigWithPrecedence(newConfig, []string{"listener_port"})

	// After UpdateConfig, port should be 8084 (user change)
	app.ConfigManager.ConfigMu.RLock()
	port := app.ConfigManager.Config.ListenerPort
	app.ConfigManager.ConfigMu.RUnlock()

	if port != 8084 {
		t.Errorf("Case 2 FAILED: Expected port 8084 (user change), got %d. "+
			"User changes should override CLI values.", port)
	} else {
		t.Logf("Case 2 PASSED: Port is 8084 (user change) as expected")
	}
}

// --- merged from server_restart_test.go ---
func TestProcessRestart_RequestsRestartAndExecs(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	var execCalled bool
	var gotPath string
	var gotArgs []string
	app.RuntimeManager.execCommand = func(path string, args []string, env []string) error {
		execCalled = true
		gotPath = path
		gotArgs = args
		return nil
	}

	app.TriggerRestart()

	if !app.IsRestartRequested() {
		t.Fatal("expected restartRequested to be true after requestRestart")
	}

	// execCommand should not be called by requestRestart; that happens in execRestart.
	if execCalled {
		t.Fatal("execCommand should not be called by requestRestart")
	}

	app.ExecRestart()

	if !execCalled {
		t.Fatal("expected execCommand to be called by execRestart")
	}
	if gotPath == "" {
		t.Error("expected non-empty executable path")
	}
	if len(gotArgs) == 0 {
		t.Error("expected non-empty argument list")
	}
}

// TestApp_ApplyConfig_SyncsLoginRateLimitMax verifies ApplyConfig pushes the
// configured per-IP login limit into the live auth handlers (hot reload).
func TestApp_ApplyConfig_SyncsLoginRateLimitMax(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")
	app := createStartedApp(t)
	defer app.Shutdown()

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.LoginRateLimitPerIP = 1
	app.ConfigManager.ConfigMu.Unlock()
	app.ApplyConfig()

	router := app.getRouter()
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "http://example.com/login",
			strings.NewReader("username=foo&password=bar"))
		req.Host = "example.com"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://example.com") // required: Origin middleware
		req.RemoteAddr = "198.51.100.1:12345"          // honored only via ServeHTTP
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if i == 0 && w.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d: unexpected 429", i+1)
		}
		if i == 1 && w.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: got %d, want 429", i+1, w.Code)
		}
	}
}

func readStartupLogs(t *testing.T, app *App) string {
	t.Helper()
	if app.logger == nil {
		t.Fatal("app.logger is nil")
	}
	data, err := os.ReadFile(app.logger.FilePath())
	if err != nil {
		t.Fatalf("failed to read startup log file: %v", err)
	}
	return string(data)
}
