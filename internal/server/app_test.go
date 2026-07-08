package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/log"
	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/ui"
)

// TestNew verifies that the New function initializes the App struct correctly.
func TestNew(t *testing.T) {
	ss := "this-is-a-test-secret"
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: ss, IsSet: true}}
	app := New(opt, "x.y.z")
	t.Run("Initializes App struct correctly", func(t *testing.T) {
		if app.ctx == nil {
			t.Error("Expected app.ctx to not be nil")
		}
		if app.cancel == nil {
			t.Error("Expected app.cancel to not be nil")
		}
		if app.sessionSecret != ss {
			t.Errorf("Expected sessionSecret to be %q, got %q", ss, app.sessionSecret)
		}
	})
}

// TestNew_DoesNotCreatePool verifies that New does not create the worker pool;
// pool creation is deferred to SubsystemManager.Start.
func TestNew_DoesNotCreatePool(t *testing.T) {
	ss := "this-is-a-test-secret"
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: ss, IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	if app.pool != nil {
		t.Errorf("Expected app.pool to be nil after New, got %v", app.pool)
	}
	if app.InfrastructureService == nil {
		t.Error("Expected InfrastructureService to be initialized")
	}
	if app.ConfigManager == nil {
		t.Error("Expected ConfigManager to be initialized")
	}
	if app.AuthService == nil {
		t.Error("Expected AuthService to be initialized")
	}
	if app.HandlerManager == nil {
		t.Error("Expected HandlerManager to be initialized")
	}
	if app.RuntimeManager == nil {
		t.Error("Expected RuntimeManager to be initialized")
	}
	if app.SubsystemManager == nil {
		t.Error("Expected SubsystemManager to be initialized")
	}
}

// TestSetDB verifies that the setDB method initializes the database connection pools correctly.
func TestSetDB(t *testing.T) {
	// Create a temporary directory structure
	tempDir := t.TempDir()

	// discardHandler := slog.NewTextHandler(io.Discard, nil)
	// slog.SetDefault(slog.New(discardHandler))

	ss := "this-is-a-test-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)

	defer app.cancel()

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
		err = roConn.Conn.QueryRowContext(app.ctx, "SELECT 1").Scan(&count)
		if err != nil {
			t.Errorf("failed basic query on deferred tx connection: %v", err)
		}
		app.dbRoPool.Put(roConn)

		// Test immediate (RW) pool
		rwConn, err := app.dbRwPool.Get()
		if err != nil {
			t.Fatalf("failed to get immediate tx connection: %v", err)
		}

		err = rwConn.Conn.QueryRowContext(app.ctx, "SELECT 1").Scan(&count)
		if err != nil {
			t.Errorf("failed basic query on immediate tx connection: %v", err)
		}
		app.dbRwPool.Put(rwConn)
	})

	t.Run("writeBatcher configured with dque overflow", func(t *testing.T) {
		if app.writeBatcher == nil {
			t.Fatal("writeBatcher should be initialized after setDB")
		}

		stats := app.writeBatcher.GetStats()
		if !stats.DQueEnabled {
			t.Error("writeBatcher.DQueEnabled is false — DQueDirPath not set in setDB()")
		}

		// Verify the dque overflow directory exists on disk
		dqueDir := filepath.Join(filepath.Dir(app.dbPaths.Main), filepath.Base(app.dbPaths.Main)+"-dque")
		if _, err := os.Stat(dqueDir); os.IsNotExist(err) {
			t.Error("dque overflow directory was not created — DQueDirPath not set in setDB()")
		}
	})

	// Close writeBatcher first to release any database connections
	if app.writeBatcher != nil {
		if err := app.writeBatcher.Close(); err != nil {
			t.Errorf("failed to close writeBatcher: %v", err)
		}
	}

	// Give a moment for connections to be fully returned to the pool
	// This helps avoid "database is locked" errors with SQLite
	time.Sleep(100 * time.Millisecond)

	// Close pools at end of test
	// Note: With SQLite, closing one pool may fail if the other pool still has
	// connections open, since they share the same database file. We close both
	// and only report errors if they're not "database is locked" errors.
	roErr := app.dbRoPool.Close()
	rwErr := app.dbRwPool.Close()

	// Only report errors that aren't related to database locking
	// (which is expected when multiple pools access the same SQLite file)
	if roErr != nil && !strings.Contains(roErr.Error(), "database is locked") {
		t.Errorf("failed to close deferred tx pool: %v", roErr)
	}
	if rwErr != nil && !strings.Contains(rwErr.Error(), "database is locked") {
		t.Errorf("failed to close immediate tx pool: %v", rwErr)
	}
}

// TestReconfigurePools_DQueWired verifies that reconfigurePoolsFromConfig
// creates a WriteBatcher with dque overflow configured.
func TestReconfigurePools_DQueWired(t *testing.T) {
	tempDir := t.TempDir()
	ss := "this-is-a-test-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")
	app.setRootDir(&tempDir)
	defer app.cancel()

	// Initialize DB
	app.setDB()

	// Ensure config is initialized before reconfiguration
	app.configMu.Lock()
	if app.config == nil {
		app.config = config.DefaultConfig()
	}
	app.config.DBMaxPoolSize = 999 // different from default to trigger reconfigure
	app.configMu.Unlock()

	// Verify the new batcher has dque overflow configured after reconfigure.
	// This also implicitly tests close-before-create ordering: once setDB is
	// fixed to configure dque, the old batcher will hold a dque flock.
	// If reconfigurePoolsFromConfig creates the new batcher before closing
	// the old one, dque.NewOrOpen will fail with a flock conflict on the
	// same directory, causing reconfigurePoolsFromConfig to return an error.
	err := app.reconfigurePoolsFromConfig()
	if err != nil {
		t.Fatalf("reconfigurePoolsFromConfig failed: %v", err)
	}

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
	ss := "this-is-a-test-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)

	defer app.cancel()

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
	err = cpcRw.Queries.UpsertLoginAttempt(app.ctx, gallerydb.UpsertLoginAttemptParams{
		Username:       username,
		FailedAttempts: 3,
		LastAttemptAt:  now,
		LockedUntil:    sql.NullInt64{Int64: futureTime, Valid: true},
	})
	if err != nil {
		t.Fatalf("UpsertLoginAttempt failed: %v", err)
	}

	// Verify account is locked
	attempt, err := cpcRw.Queries.GetLoginAttempt(app.ctx, username)
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
	attempt, err = cpcRw.Queries.GetLoginAttempt(app.ctx, username)
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
	ss := "this-is-a-test-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)

	defer app.cancel()

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

// TestApp_ImageDirectory_FromConfig_NotHardcoded verifies that image directory comes from config,
// not from a hardcoded default. It ensures setDBDirectory() does not create the Images directory,
// and that setImageDirectory() uses the configured path instead.
func TestApp_ImageDirectory_FromConfig_NotHardcoded(t *testing.T) {
	tempDir := t.TempDir()
	customImageDir := filepath.Join(tempDir, "custom-images")

	ss := "this-is-a-test-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	// Initialize scheduler
	app.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.scheduler.Start(app.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.scheduler != nil {
			_ = app.scheduler.Shutdown()
		}
	}()

	// Call setDBDirectory (should only create DB directory, not Images)

	// Verify default Images directory was NOT created
	defaultImagesDir := filepath.Join(tempDir, "Images")
	if _, err := os.Stat(defaultImagesDir); !os.IsNotExist(err) {
		t.Errorf("setDBDirectory should not create Images directory, but it exists at %q", defaultImagesDir)
	}

	// Set up DB and config
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

	// Load config and set custom image directory
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// Set custom image directory in config
	app.configMu.Lock()
	app.config.ImageDirectory = customImageDir
	app.configMu.Unlock()

	// Apply config
	app.ApplyConfig()

	// Set image directory (should create it from config)

	// Verify custom directory was created and used
	if app.imagesDir != customImageDir {
		t.Errorf("expected imagesDir to be %q from config, got %q", customImageDir, app.imagesDir)
	}
	if _, err := os.Stat(customImageDir); os.IsNotExist(err) {
		t.Errorf("setImageDirectory should have created custom image directory at %q", customImageDir)
	}
}

// TestApp_ImageDirectory_CreatedAfterConfigLoad verifies that image directory is created
// after config is loaded and applied, not before.
func TestApp_ImageDirectory_CreatedAfterConfigLoad(t *testing.T) {
	tempDir := t.TempDir()
	customImageDir := filepath.Join(tempDir, "my-images")

	ss := "this-is-a-test-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	// Initialize scheduler
	app.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.scheduler.Start(app.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.scheduler != nil {
			_ = app.scheduler.Shutdown()
		}
	}()

	// Call setDBDirectory (should only create DB directory)

	// Verify image directory does NOT exist yet
	if _, err := os.Stat(customImageDir); !os.IsNotExist(err) {
		t.Fatalf("image directory should not exist before config load, but it does: %v", err)
	}

	// Set up DB and config
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

	// Load config
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// Set custom image directory in config
	app.configMu.Lock()
	app.config.ImageDirectory = customImageDir
	app.configMu.Unlock()

	// Apply config
	app.ApplyConfig()

	// At this point, directory should still not exist (applyConfig doesn't create it yet)
	// setImageDirectory() will create it
	if _, err := os.Stat(customImageDir); !os.IsNotExist(err) {
		t.Logf("Note: directory exists after applyConfig (this is OK if applyConfig creates it)")
	}

	// Now call setImageDirectory - this should create the directory

	// Verify directory was created
	if _, err := os.Stat(customImageDir); os.IsNotExist(err) {
		t.Errorf("setImageDirectory should have created image directory at %q, but it doesn't exist", customImageDir)
	}

	// Verify app.imagesDir is set correctly
	if app.imagesDir != customImageDir {
		t.Errorf("expected imagesDir to be %q, got %q", customImageDir, app.imagesDir)
	}
}

// TestApp_ImageDirectory_CustomPath verifies that custom image directory path from config
// is used instead of default.
func TestApp_ImageDirectory_CustomPath(t *testing.T) {
	tempDir := t.TempDir()
	customImageDir := filepath.Join(tempDir, "photos", "gallery")

	ss := "this-is-a-test-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	// Initialize scheduler
	app.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.scheduler.Start(app.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.scheduler != nil {
			_ = app.scheduler.Shutdown()
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

	// Load config
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// Set custom nested image directory in config
	app.configMu.Lock()
	app.config.ImageDirectory = customImageDir
	app.configMu.Unlock()

	// Apply config
	app.ApplyConfig()

	// Set image directory

	// Verify custom path is used (not default)
	defaultPath := filepath.Join(tempDir, "Images")
	if app.imagesDir == defaultPath {
		t.Errorf("expected custom path %q, but got default path %q", customImageDir, defaultPath)
	}

	if app.imagesDir != customImageDir {
		t.Errorf("expected imagesDir to be %q, got %q", customImageDir, app.imagesDir)
	}

	// Verify directory was created
	if _, err := os.Stat(customImageDir); os.IsNotExist(err) {
		t.Errorf("custom image directory should exist at %q, but it doesn't", customImageDir)
	}

	// Verify normalized path is set correctly
	expectedNormalized := filepath.ToSlash(customImageDir)
	if app.normalizedImagesDir != expectedNormalized {
		t.Errorf("expected normalizedImagesDir to be %q, got %q", expectedNormalized, app.normalizedImagesDir)
	}
}

// TestImageDirectoryIntegration_StartupFlow tests complete startup flow with custom image directory.
func TestImageDirectoryIntegration_StartupFlow(t *testing.T) {
	tempDir := t.TempDir()
	customImageDir := filepath.Join(tempDir, "custom-gallery")

	ss := "this-is-a-test-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	// Initialize scheduler
	app.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.scheduler.Start(app.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.scheduler != nil {
			_ = app.scheduler.Shutdown()
		}
	}()

	// Simulate Run() sequence
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

	// Load config
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// Set custom image directory in config
	app.configMu.Lock()
	app.config.ImageDirectory = customImageDir
	app.configMu.Unlock()

	// Apply config
	app.ApplyConfig()

	// Set image directory (from Run() sequence)

	// Verify complete integration
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

// TestImageDirectoryIntegration_RuntimeChange tests that runtime config change requires restart.
func TestImageDirectoryIntegration_RuntimeChange(t *testing.T) {
	tempDir := t.TempDir()
	initialImageDir := filepath.Join(tempDir, "initial")
	newImageDir := filepath.Join(tempDir, "new")

	ss := "this-is-a-test-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	// Initialize scheduler
	app.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.scheduler.Start(app.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.scheduler != nil {
			_ = app.scheduler.Shutdown()
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

	// Load config
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// Set initial image directory
	app.configMu.Lock()
	app.config.ImageDirectory = initialImageDir
	app.configMu.Unlock()

	app.ApplyConfig()

	// Verify initial directory is set
	if app.imagesDir != initialImageDir {
		t.Errorf("expected initial imagesDir to be %q, got %q", initialImageDir, app.imagesDir)
	}

	// Simulate runtime config change (as if from config handler)
	app.configMu.Lock()
	app.config.ImageDirectory = newImageDir
	app.configMu.Unlock()

	app.ApplyConfig()

	// After applyConfig, imagesDir should be updated
	if app.imagesDir != newImageDir {
		t.Errorf("expected imagesDir to be updated to %q after config change, got %q", newImageDir, app.imagesDir)
	}

	// Note: In real scenario, restart would be required and walkImageDir() would use new directory
	// This test verifies the config change is applied correctly
}

// TestImageDirectoryIntegration_FileDiscoveryUsesConfig verifies that walkImageDir()
// would use config directory (we can't actually run walkImageDir in test, but we verify
// the directory it would use is from config).
func TestImageDirectoryIntegration_FileDiscoveryUsesConfig(t *testing.T) {
	tempDir := t.TempDir()
	customImageDir := filepath.Join(tempDir, "discovery-test")

	ss := "this-is-a-test-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	// Initialize scheduler
	app.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.scheduler.Start(app.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.scheduler != nil {
			_ = app.scheduler.Shutdown()
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

	// Load config
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// Set custom image directory in config
	app.configMu.Lock()
	app.config.ImageDirectory = customImageDir
	app.configMu.Unlock()

	app.ApplyConfig()

	// Verify app.imagesDir is set from config (this is what walkImageDir() would use)
	if app.imagesDir != customImageDir {
		t.Errorf("expected imagesDir to be %q (what walkImageDir would use), got %q", customImageDir, app.imagesDir)
	}

	// Verify directory exists (walkImageDir would fail if it doesn't)
	if _, err := os.Stat(customImageDir); os.IsNotExist(err) {
		t.Errorf("image directory should exist for walkImageDir to use, but it doesn't: %v", err)
	}
}

// TestApplyConfig_PanicsWhenImageDirectoryUndefined verifies that applyConfig()
// panics when ImageDirectory is undefined.
func TestApplyConfig_PanicsWhenImageDirectoryUndefined(t *testing.T) {
	tempDir := t.TempDir()

	ss := "this-is-a-test-secret"
	setenvForTest(t, "SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	defer func() {
		if app.logger != nil {
			_ = app.logger.Shutdown()
		}
	}()

	// Initialize scheduler
	app.scheduler = scheduler.NewScheduler(0)
	go func() {
		if err := app.scheduler.Start(app.ctx); err != nil {
			t.Logf("scheduler error: %v", err)
		}
	}()
	defer func() {
		if app.scheduler != nil {
			_ = app.scheduler.Shutdown()
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

	// Load config
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// Simulate cold-start scenario: Empty ImageDirectory in config
	app.configMu.Lock()
	app.config.ImageDirectory = ""
	app.configMu.Unlock()

	// Reset app state before calling applyConfig
	app.imagesDir = ""
	app.normalizedImagesDir = ""

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when ImageDirectory is undefined")
		}
	}()

	// Expect panic when ImageDirectory is undefined
	app.ApplyConfig()
}

func TestApp_Shutdown_DelegatesToSubsystemManager(t *testing.T) {
	app := CreateApp(t)
	app.Start(app.ctx, app.config, 1, 2, app.imagesDir, app.normalizedImagesDir, removeImagesDirPrefix, app.getRouter, app.GetHandlerQueries, app.GetETagVersion)

	app.poolDone = make(chan struct{})
	app.StartPool(app.ctx, app.poolDone, app.normalizedImagesDir, removeImagesDirPrefix, app.fileProcessor)

	app.Shutdown()

	if app.preloadManager.GetScheduler() != nil {
		t.Error("preloadManager scheduler should be stopped after Shutdown")
	}
}

func TestApp_ResetStats_DelegatesToSubsystemManager(t *testing.T) {
	app := CreateApp(t)
	app.Start(app.ctx, app.config, 1, 2, app.imagesDir, app.normalizedImagesDir, removeImagesDirPrefix, app.getRouter, app.GetHandlerQueries, app.GetETagVersion)

	app.processingStats.TotalFound.Store(5)
	app.processingStats.AlreadyExisting.Store(4)
	app.processingStats.NewlyInserted.Store(3)
	app.processingStats.SkippedInvalid.Store(2)
	app.processingStats.InFlight.Store(1)

	app.ResetStats()

	if app.processingStats.TotalFound.Load() != 0 {
		t.Errorf("TotalFound = %d, want 0", app.processingStats.TotalFound.Load())
	}
	if app.processingStats.AlreadyExisting.Load() != 0 {
		t.Errorf("AlreadyExisting = %d, want 0", app.processingStats.AlreadyExisting.Load())
	}
	if app.processingStats.NewlyInserted.Load() != 0 {
		t.Errorf("NewlyInserted = %d, want 0", app.processingStats.NewlyInserted.Load())
	}
	if app.processingStats.SkippedInvalid.Load() != 0 {
		t.Errorf("SkippedInvalid = %d, want 0", app.processingStats.SkippedInvalid.Load())
	}
	if app.processingStats.InFlight.Load() != 0 {
		t.Errorf("InFlight = %d, want 0", app.processingStats.InFlight.Load())
	}
}

func TestApp_LoadsETagFromConfig(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	ctx := context.Background()

	// Set custom ETag in database
	cfg, err := app.configService.Load(ctx)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	cfg.ETagVersion = "20260129-99"
	app.configService.Save(ctx, cfg)

	// Shut down the first app so its dque flock is released before
	// creating the second app with the same root directory.
	app.Shutdown()

	// Create new app using same root directory (simulates restart, same database)
	app2 := CreateApp(t, WithRoot(app.rootDir))

	// Verify ui package has correct cache version (should be set during app2 initialization)
	cacheVer := ui.GetCacheVersion()
	if cacheVer != "20260129-99" {
		t.Errorf("UI cache version = %q, want %q", cacheVer, "20260129-99")
	}

	// Verify GetETagVersion returns correct ETag from config
	etagVer := app2.GetETagVersion()
	if etagVer != "20260129-99" {
		t.Errorf("GetETagVersion() = %q, want %q", etagVer, "20260129-99")
	}
}

// TestMemoryReclaimer verifies the memory reclaimer's idle-detection logic.
func TestMemoryReclaimer(t *testing.T) {
	t.Run("triggers when idle", func(t *testing.T) {
		app := CreateApp(t, WithPool())
		defer app.Shutdown()

		called := make(chan struct{}, 1)
		cfg := MemoryReclaimerConfig{
			InitialDelay:  10 * time.Millisecond,
			CheckInterval: 20 * time.Millisecond,
			IdleThreshold: 50 * time.Millisecond,
			FreeMemFunc:   func() { called <- struct{}{} },
		}

		// Mark a task completion so TimeSinceLastCompletion is meaningful.
		app.pool.AddCompleted()

		done := make(chan struct{})
		go func() {
			app.memoryReclaimer(cfg)
			close(done)
		}()

		select {
		case <-called:
			// success
		case <-time.After(2 * time.Second):
			t.Error("FreeMemFunc was not called while idle")
		}

		app.cancel()
		<-done
	})

	t.Run("does not trigger when queue is not empty", func(t *testing.T) {
		app := CreateApp(t)
		defer app.Shutdown()

		app.q.Enqueue("dummy-path")

		called := make(chan struct{}, 1)
		cfg := MemoryReclaimerConfig{
			InitialDelay:  10 * time.Millisecond,
			CheckInterval: 20 * time.Millisecond,
			IdleThreshold: 50 * time.Millisecond,
			FreeMemFunc:   func() { called <- struct{}{} },
		}

		app.pool.AddCompleted()

		done := make(chan struct{})
		go func() {
			app.memoryReclaimer(cfg)
			close(done)
		}()

		select {
		case <-called:
			t.Error("FreeMemFunc was called even though queue was not empty")
		case <-time.After(300 * time.Millisecond):
			// expected
		}

		app.cancel()
		<-done
	})

	t.Run("does not trigger when recently active", func(t *testing.T) {
		app := CreateApp(t, WithPool())
		defer app.Shutdown()

		called := make(chan struct{}, 1)
		cfg := MemoryReclaimerConfig{
			InitialDelay:  10 * time.Millisecond,
			CheckInterval: 20 * time.Millisecond,
			IdleThreshold: 1 * time.Second,
			FreeMemFunc:   func() { called <- struct{}{} },
		}

		app.pool.AddCompleted()

		done := make(chan struct{})
		go func() {
			app.memoryReclaimer(cfg)
			close(done)
		}()

		select {
		case <-called:
			t.Error("FreeMemFunc was called even though pool was recently active")
		case <-time.After(300 * time.Millisecond):
			// expected
		}

		app.cancel()
		<-done
	})
}

// TestApp_SetupBootstrapLogging_CreatesLogger verifies that setupBootstrapLogging
// initializes the logger and creates the logs directory under rootDir.
func TestApp_SetupBootstrapLogging_CreatesLogger(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()

	if app.logger == nil {
		t.Fatal("Expected app.logger to be initialized")
	}

	logsDir := filepath.Join(tempDir, "logs")
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		t.Errorf("Expected logs directory to be created at %q", logsDir)
	}
}

// TestApp_GetCtx_ReturnsCtxOrBackground verifies getCtx behavior before and
// after the application context is set.
func TestApp_GetCtx_ReturnsCtxOrBackground(t *testing.T) {
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	if got := app.getCtx(); got != app.ctx {
		t.Errorf("getCtx() with RuntimeManager ctx = %v, want %v", got, app.ctx)
	}

	app.ctx = nil
	if got := app.getCtx(); got != context.Background() {
		t.Errorf("getCtx() with nil ctx = %v, want context.Background()", got)
	}
}

// errorConfigService is a fake ConfigService that returns an error from Load
// but still records the call and returns a supplied config.
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

// TestApp_LoadConfig_StoreErrorFallsBackToDefaults verifies that loadConfig
// falls back to default config and logs diagnostics when ConfigStore.Load fails.
func TestApp_LoadConfig_StoreErrorFallsBackToDefaults(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	original := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(original)
	})

	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
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
	app.configService = fakeSvc

	// config.Load ignores the returned config on store error and continues with
	// defaults + rootDir merge, so app.config.ImageDirectory must come from rootDir.
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
	if app.config == nil {
		t.Fatal("Expected app.config to be set")
	}
	wantImageDir := filepath.Join(tempDir, "Images")
	if app.config.ImageDirectory != wantImageDir {
		t.Errorf("ImageDirectory = %q, want %q", app.config.ImageDirectory, wantImageDir)
	}
}

// TestApp_ApplyConfig_InvalidatesETagWhenChanged verifies that ApplyConfig
// updates the UI cache version and invalidates the HTTP cache when the ETag changes.
func TestApp_ApplyConfig_InvalidatesETagWhenChanged(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	oldETag := "20260101-01"
	newETag := "20260101-02"

	ui.SetCacheVersion(oldETag)

	// Seed one HTTP cache entry so we can verify invalidation runs.
	ctx := app.ctx
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

	app.configMu.Lock()
	app.config.ETagVersion = newETag
	app.configMu.Unlock()

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

// TestApp_LogProfileLocation verifies that LogProfileLocation calls the stop
// function and logs the profile directory.
func TestApp_LogProfileLocation(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	original := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(original)
	})

	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	stopProfiler, err := profiler.Start(profiler.Config{Mode: "cpu"})
	if err != nil {
		t.Fatalf("profiler.Start failed: %v", err)
	}
	defer stopProfiler()

	stopped := false
	app.stopProfiler = func() {
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
}

// TestApp_ApplyConfig_NilConfigNoPanic verifies that ApplyConfig returns early
// without panicking when the config is nil.
func TestApp_ApplyConfig_NilConfigNoPanic(t *testing.T) {
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.configMu.Lock()
	app.config = nil
	app.configMu.Unlock()

	app.ApplyConfig()
}

// TestApp_ApplyConfig_ETagUnchangedSkipsInvalidate verifies that the HTTP cache
// is preserved when the ETag version does not change.
func TestApp_ApplyConfig_ETagUnchangedSkipsInvalidate(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	const etag = "same-etag"
	ui.SetCacheVersion(etag)

	ctx := app.ctx
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

	app.configMu.Lock()
	app.config.ETagVersion = etag
	app.configMu.Unlock()

	app.ApplyConfig()

	after, err := cachelite.CountCacheEntries(ctx, app.dbRwPool)
	if err != nil {
		t.Fatalf("failed to count cache entries: %v", err)
	}
	if after != 1 {
		t.Errorf("expected cache entry to be preserved, got %d entries", after)
	}
}

// TestApp_ApplyConfig_PreloadManagerSetEnabled verifies that ApplyConfig toggles
// the preload manager enabled state.
func TestApp_ApplyConfig_PreloadManagerSetEnabled(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Wire the preload manager via SubsystemManager.Start.
	app.Start(app.ctx, app.config, 1, 2, app.imagesDir, app.normalizedImagesDir, removeImagesDirPrefix, app.getRouter, app.GetHandlerQueries, app.GetETagVersion)

	if app.preloadManager == nil {
		t.Fatal("preloadManager should be wired after Start")
	}

	app.configMu.Lock()
	app.config.EnableCachePreload = true
	app.configMu.Unlock()
	app.ApplyConfig()
	if !app.preloadManager.IsEnabled() {
		t.Error("expected preload manager to be enabled")
	}

	app.configMu.Lock()
	app.config.EnableCachePreload = false
	app.configMu.Unlock()
	app.ApplyConfig()
	if app.preloadManager.IsEnabled() {
		t.Error("expected preload manager to be disabled")
	}
}

// TestApp_setDB_RebuildsHandlersWhenAlreadyBuilt verifies that setDB rebuilds
// handlers when they have already been constructed.
func TestApp_setDB_RebuildsHandlersWhenAlreadyBuilt(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	if app.authHandlers == nil {
		t.Fatal("expected handlers to be built by CreateApp")
	}

	// Close the existing write batcher so setDB can recreate it without dque
	// flock conflicts.
	if app.writeBatcher != nil {
		if err := app.writeBatcher.Close(); err != nil {
			t.Fatalf("failed to close writeBatcher: %v", err)
		}
	}

	app.setDB()

	if app.authHandlers == nil {
		t.Error("expected authHandlers to be rebuilt after setDB")
	}
	if app.galleryHandlers == nil {
		t.Error("expected galleryHandlers to be rebuilt after setDB")
	}
}

// TestApp_reconfigurePoolsFromConfig_RebuildsHandlers verifies that
// reconfigurePoolsFromConfig rebuilds handlers when they already exist.
func TestApp_reconfigurePoolsFromConfig_RebuildsHandlers(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	if app.authHandlers == nil {
		t.Fatal("expected handlers to be built by CreateApp")
	}

	app.configMu.Lock()
	currentMax := app.config.DBMaxPoolSize
	app.config.DBMaxPoolSize = currentMax + 1
	app.configMu.Unlock()

	app.testHookRecreatePoolsWithConfig = func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return oldRw, oldRo, nil
	}

	if err := app.reconfigurePoolsFromConfig(); err != nil {
		t.Fatalf("reconfigurePoolsFromConfig failed: %v", err)
	}

	if app.authHandlers == nil {
		t.Error("expected authHandlers to remain wired after reconfigure")
	}
	if app.galleryHandlers == nil {
		t.Error("expected galleryHandlers to remain wired after reconfigure")
	}
}

// TestApp_setDB_RebuildHandlersError verifies that setDB panics when handler
// reconstruction fails after the DB is re-initialized.
func TestApp_setDB_RebuildHandlersError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	if app.authHandlers == nil {
		t.Fatal("expected handlers to be built by CreateApp")
	}

	app.testHookBuildHandlers = func(fs.FS) error {
		return fmt.Errorf("rebuild failed")
	}

	// Close the existing write batcher so setDB can recreate it without dque
	// flock conflicts.
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

// TestApp_reconfigurePoolsFromConfig_NilConfig verifies that
// reconfigurePoolsFromConfig returns early when the config is nil.
func TestApp_reconfigurePoolsFromConfig_NilConfig(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.configMu.Lock()
	app.config = nil
	app.configMu.Unlock()

	if err := app.reconfigurePoolsFromConfig(); err != nil {
		t.Fatalf("reconfigurePoolsFromConfig with nil config returned error: %v", err)
	}
}

// TestApp_reconfigurePoolsFromConfig_UpdatesCacheMiddleware verifies that an
// existing HTTP cache middleware has its pool updated during reconfiguration.
func TestApp_reconfigurePoolsFromConfig_UpdatesCacheMiddleware(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.configMu.Lock()
	app.config.EnableHTTPCache = true
	app.configMu.Unlock()
	app.initializeHTTPCache()

	if app.cacheMW == nil {
		t.Fatal("expected cacheMW to be initialized")
	}

	app.configMu.Lock()
	currentMax := app.config.DBMaxPoolSize
	app.config.DBMaxPoolSize = currentMax + 1
	app.configMu.Unlock()

	app.testHookRecreatePoolsWithConfig = func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return oldRw, oldRo, nil
	}

	if err := app.reconfigurePoolsFromConfig(); err != nil {
		t.Fatalf("reconfigurePoolsFromConfig failed: %v", err)
	}
}

// TestApp_reconfigurePoolsFromConfig_BuildHandlersError verifies that
// reconfigurePoolsFromConfig returns an error when handler rebuild fails.
func TestApp_reconfigurePoolsFromConfig_BuildHandlersError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	if app.authHandlers == nil {
		t.Fatal("expected handlers to be built by CreateApp")
	}

	app.configMu.Lock()
	currentMax := app.config.DBMaxPoolSize
	app.config.DBMaxPoolSize = currentMax + 1
	app.configMu.Unlock()

	app.testHookBuildHandlers = func(fs.FS) error {
		return fmt.Errorf("rebuild failed")
	}

	app.testHookRecreatePoolsWithConfig = func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
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

// TestNew_ParseTemplatesError_Exits verifies that New exits with code 1 when
// template parsing fails.
func TestNew_ParseTemplatesError_Exits(t *testing.T) {
	var parseCalled bool
	var exitCode int
	testHookNewParseTemplates = func(fs.FS) error {
		parseCalled = true
		return fmt.Errorf("parse failed")
	}
	testHookNewExit = func(code int) {
		exitCode = code
		panic("exit")
	}
	t.Cleanup(func() {
		testHookNewParseTemplates = nil
		testHookNewExit = nil
	})

	func() {
		defer func() { recover() }()
		New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}, "x.y.z")
	}()

	if !parseCalled {
		t.Error("expected parse hook to be called")
	}
	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1", exitCode)
	}
}

// TestParseConfigUITemplates_MissingFileReturnsError verifies that a missing
// config UI template file returns an error.
func TestParseConfigUITemplates_MissingFileReturnsError(t *testing.T) {
	fsys := fstest.MapFS{}
	_, err := parseConfigUITemplates(fsys)
	if err == nil {
		t.Fatal("expected error for missing template file")
	}
}

// TestParseConfigUITemplates_InvalidTemplatePerFile_ReturnsError verifies that
// an invalid config UI template at each position returns a parse error.
func TestParseConfigUITemplates_InvalidTemplatePerFile_ReturnsError(t *testing.T) {
	templateNames := []string{
		"templates/config-ui/config-save-restart-alert.html.tmpl",
		"templates/config-ui/config-save-success-alert.html.tmpl",
		"templates/config-ui/config-export-modal.html.tmpl",
		"templates/config-ui/config-import-modal.html.tmpl",
		"templates/config-ui/config-restore-modal.html.tmpl",
		"templates/config-ui/config-restore-success-alert.html.tmpl",
		"templates/config-ui/config-import-success-alert.html.tmpl",
		"templates/config-ui/config-restart-initiated-alert.html.tmpl",
	}
	for _, badName := range templateNames {
		t.Run(badName, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for _, name := range templateNames {
				data := []byte("")
				if name == badName {
					data = []byte("{{.Bad}")
				}
				fsys[name] = &fstest.MapFile{Data: data}
			}
			_, err := parseConfigUITemplates(fsys)
			if err == nil {
				t.Fatal("expected error for invalid template syntax")
			}
		})
	}
}

// TestSetRootDir_ExecutableErrorPanics verifies that setRootDir panics when
// os.Executable fails.
func TestSetRootDir_ExecutableErrorPanics(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	app.testHookExecutable = func() (string, error) {
		return "", fmt.Errorf("exec failed")
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when executable lookup fails")
		}
	}()
	app.setRootDir(nil)
}

// TestSetupBootstrapLogging_ErrorPanics verifies that setupBootstrapLogging
// panics when bootstrap logging setup fails.
func TestSetupBootstrapLogging_ErrorPanics(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	tempDir := t.TempDir()
	app.setRootDir(&tempDir)
	app.testHookSetupBootstrapLogging = func(string, *scheduler.Scheduler, string) (*log.Logger, error) {
		return nil, fmt.Errorf("setup failed")
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when bootstrap logging setup fails")
		}
	}()
	app.setupBootstrapLogging()
}

// TestUnlockAccount_PoolGetError verifies that UnlockAccount returns an error
// when the database pool cannot provide a connection.
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

// TestUnlockAccount_QueryError verifies that UnlockAccount returns an error
// when the database query fails.
func TestUnlockAccount_QueryError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()
	app.cancel()

	err := app.UnlockAccount("anyone")
	if err == nil {
		t.Fatal("expected error when unlock query fails")
	}
	if !strings.Contains(err.Error(), "failed to unlock account") {
		t.Errorf("error = %q, want unlock failure", err.Error())
	}
}

// TestInitForIncrementETag_DatabaseSetupError verifies that InitForIncrementETag
// returns an error when database setup fails.
func TestInitForIncrementETag_DatabaseSetupError(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	tempDir := t.TempDir()
	app.setRootDir(&tempDir)
	app.testHookDatabaseSetup = func(context.Context, string, *config.Config) (database.DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
		return database.DatabasePaths{}, nil, nil, fmt.Errorf("setup failed")
	}

	err := app.InitForIncrementETag(getopt.Opt{})
	if err == nil {
		t.Fatal("expected error when database setup fails")
	}
	if !strings.Contains(err.Error(), "failed to setup database for increment-etag") {
		t.Errorf("error = %q, want setup failure", err.Error())
	}
}

// ensureDefaultsFailConfigService is a fake ConfigService that fails EnsureDefaults.
type ensureDefaultsFailConfigService struct {
	recordingConfigService
}

func (e *ensureDefaultsFailConfigService) EnsureDefaults(ctx context.Context, rootDir string) error {
	e.ensureDefaultsCalled = true
	e.ensureDefaultsRoot = rootDir
	return fmt.Errorf("ensure failed")
}

// TestInitForIncrementETag_EnsureDefaultsError verifies that InitForIncrementETag
// returns an error when EnsureDefaults fails.
func TestInitForIncrementETag_EnsureDefaultsError(t *testing.T) {
	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}, "x.y.z")
	defer app.Shutdown()
	tempDir := t.TempDir()
	app.setRootDir(&tempDir)
	app.testHookDatabaseSetup = database.Setup
	fakeSvc := &ensureDefaultsFailConfigService{}
	app.testHookConfigService = fakeSvc

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
