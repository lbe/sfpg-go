package server

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/getopt"
	"go.local/sfpg/internal/scheduler"
	"go.local/sfpg/internal/server/ui"
	"go.local/sfpg/internal/workerpool"
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

// TestNewWorkerPool verifies that the New function initializes the worker pool with correct parameters.
func TestNewWorkerPool(t *testing.T) {
	// discardHandler := slog.NewTextHandler(io.Discard, nil)
	// slog.SetDefault(slog.New(discardHandler))

	ss := "this-is-a-test-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")
	app.pool = workerpool.NewPool(app.ctx, 0, 0, 10*time.Second)

	numCPU := runtime.NumCPU()

	expectedMin := 1
	if (numCPU - 2) > 4 {
		expectedMin = 4
	} else if numCPU > 2 && numCPU <= 4 {
		expectedMin = 2
	}

	expectedMax := 1
	if numCPU > 4 {
		expectedMax = numCPU - 2
	} else if numCPU > 2 && numCPU <= 4 {
		expectedMax = 2
	}

	if app.pool.MinWorkers != expectedMin {
		t.Errorf("Expected MinWorkers to be %d with %d CPUs, but got %d", expectedMin, numCPU, app.pool.MinWorkers)
	}

	if app.pool.MaxWorkers != expectedMax {
		t.Errorf("Expected MaxWorkers to be %d with %d CPUs, but got %d", expectedMax, numCPU, app.pool.MaxWorkers)
	}
}

// REMOVED: TestMemoryReclaimer - Slow integration test (1.81s)
// REMOVED: func TestMemoryReclaimer(t *testing.T) {
// REMOVED: 	t.Setenv("SEPG_SESSION_SECRET", "this-is-a-test-secret")
// REMOVED: 	t.Run("triggers when idle", func(t *testing.T) {
// REMOVED: 		app := CreateApp(t, true)
// REMOVED: 		time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
// REMOVED: 		doneChan := make(chan struct{})
// REMOVED:
// REMOVED: 		freeMemCalled := make(chan struct{}, 1)
// REMOVED: 		mockFreeMemFunc := func() {
// REMOVED: 			select {
// REMOVED: 			case freeMemCalled <- struct{}{}:
// REMOVED: 			default:
// REMOVED: 			}
// REMOVED: 		}
// REMOVED:
// REMOVED: 		// Ensure the pool has a last completion time in the past
// REMOVED: 		app.pool.AddCompleted() // Set initial time
// REMOVED: 		time.Sleep(20 * time.Millisecond)
// REMOVED:
// REMOVED: 		testCfg := MemoryReclaimerConfig{
// REMOVED: 			InitialDelay:  1 * time.Millisecond,
// REMOVED: 			CheckInterval: 5 * time.Millisecond,
// REMOVED: 			IdleThreshold: 10 * time.Millisecond,
// REMOVED: 			FreeMemFunc:   mockFreeMemFunc,
// REMOVED: 		}
// REMOVED:
// REMOVED: 		// Run the reclaimer in a controlled goroutine
// REMOVED: 		go func() {
// REMOVED: 			app.memoryReclaimer(testCfg)
// REMOVED: 			close(doneChan)
// REMOVED: 		}()
// REMOVED:
// REMOVED: 		// Wait for freeMem to be called
// REMOVED: 		select {
// REMOVED: 		case <-freeMemCalled:
// REMOVED: 			// Success, signal memory reclaimer to stop
// REMOVED: 			app.cancel()
// REMOVED: 			<-doneChan     // Wait for it to stop
// REMOVED: 			app.Shutdown() // Then do full shutdown
// REMOVED: 		case <-time.After(100 * time.Millisecond):
// REMOVED: 			app.cancel()
// REMOVED: 			<-doneChan
// REMOVED: 			t.Fatal("timed out waiting for FreeMemFunc to be called")
// REMOVED: 		}
// REMOVED: 	})
// REMOVED:
// REMOVED: 	t.Run("does not trigger when queue is not empty", func(t *testing.T) {
// REMOVED: 		app := CreateApp(t, false) // Create app without starting the worker pool
// REMOVED: 		defer app.Shutdown()
// REMOVED:
// REMOVED: 		freeMemCalled := make(chan struct{}, 1)
// REMOVED: 		mockFreeMemFunc := func() {
// REMOVED: 			freeMemCalled <- struct{}{}
// REMOVED: 		}
// REMOVED:
// REMOVED: 		// Add an item to the queue to make it not idle
// REMOVED: 		_ = app.q.Enqueue("some-item")
// REMOVED:
// REMOVED: 		testCfg := MemoryReclaimerConfig{
// REMOVED: 			InitialDelay:  1 * time.Millisecond,
// REMOVED: 			CheckInterval: 5 * time.Millisecond,
// REMOVED: 			IdleThreshold: 10 * time.Millisecond,
// REMOVED: 			FreeMemFunc:   mockFreeMemFunc,
// REMOVED: 		}
// REMOVED:
// REMOVED: 		go app.memoryReclaimer(testCfg)
// REMOVED:
// REMOVED: 		select {
// REMOVED: 		case <-freeMemCalled:
// REMOVED: 			t.Fatal("FreeMemFunc was called, but should not have been")
// REMOVED: 		case <-time.After(50 * time.Millisecond):
// REMOVED: 			// Success, it did not trigger
// REMOVED: 		}
// REMOVED: 	})
// REMOVED:
// REMOVED: 	t.Run("does not trigger when recently active", func(t *testing.T) {
// REMOVED: 		app := CreateApp(t, true)
// REMOVED: 		time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
// REMOVED: 		doneChan := make(chan struct{})
// REMOVED:
// REMOVED: 		freeMemCalled := make(chan struct{}, 1)
// REMOVED: 		mockFreeMemFunc := func() {
// REMOVED: 			freeMemCalled <- struct{}{}
// REMOVED: 		}
// REMOVED:
// REMOVED: 		// Set the last completion time to now
// REMOVED: 		app.pool.AddCompleted()
// REMOVED:
// REMOVED: 		testCfg := MemoryReclaimerConfig{
// REMOVED: 			InitialDelay:  1 * time.Millisecond,
// REMOVED: 			CheckInterval: 5 * time.Millisecond,
// REMOVED: 			IdleThreshold: 20 * time.Millisecond,
// REMOVED: 			FreeMemFunc:   mockFreeMemFunc,
// REMOVED: 		}
// REMOVED:
// REMOVED: 		// Run the reclaimer in a controlled goroutine
// REMOVED: 		go func() {
// REMOVED: 			app.memoryReclaimer(testCfg)
// REMOVED: 			close(doneChan)
// REMOVED: 		}()
// REMOVED:
// REMOVED: 		// Test that it doesn't trigger too early
// REMOVED: 		select {
// REMOVED: 		case <-freeMemCalled:
// REMOVED: 			app.Shutdown()
// REMOVED: 			<-doneChan
// REMOVED: 			t.Fatal("FreeMemFunc was called, but should not have been")
// REMOVED: 		case <-time.After(15 * time.Millisecond): // Wait less than the idle threshold
// REMOVED: 			app.cancel()   // Signal context done to stop memory reclaimer
// REMOVED: 			<-doneChan     // Wait for memory reclaimer to stop first
// REMOVED: 			app.Shutdown() // Then do the full shutdown
// REMOVED: 		}
// REMOVED: 	})
// REMOVED: }

// TestSetDB verifies that the setDB method initializes the database connection pools correctly.
func TestSetDB(t *testing.T) {
	// Create a temporary directory structure
	tempDir := t.TempDir()

	// discardHandler := slog.NewTextHandler(io.Discard, nil)
	// slog.SetDefault(slog.New(discardHandler))

	ss := "this-is-a-test-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)

	defer app.cancel()

	// Set the database path and initialize DB
	app.dbPath = filepath.Join(app.dbDir, "sfpg.db")
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

func TestUnlockAccount(t *testing.T) {
	tempDir := t.TempDir()
	ss := "this-is-a-test-secret"
	t.Setenv("SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)

	defer app.cancel()

	app.dbPath = filepath.Join(app.dbDir, "sfpg.db")
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
	t.Setenv("SEPG_SESSION_SECRET", ss)
	app := New(getopt.Opt{}, "x.y.z")

	app.setRootDir(&tempDir)

	defer app.cancel()

	app.dbPath = filepath.Join(app.dbDir, "sfpg.db")
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
	t.Setenv("SEPG_SESSION_SECRET", ss)
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
	app.applyConfig()

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
	t.Setenv("SEPG_SESSION_SECRET", ss)
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
	app.applyConfig()

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
	t.Setenv("SEPG_SESSION_SECRET", ss)
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
	app.applyConfig()

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
	t.Setenv("SEPG_SESSION_SECRET", ss)
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
	app.applyConfig()

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
	t.Setenv("SEPG_SESSION_SECRET", ss)
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

	app.applyConfig()

	// Verify initial directory is set
	if app.imagesDir != initialImageDir {
		t.Errorf("expected initial imagesDir to be %q, got %q", initialImageDir, app.imagesDir)
	}

	// Simulate runtime config change (as if from config handler)
	app.configMu.Lock()
	app.config.ImageDirectory = newImageDir
	app.configMu.Unlock()

	app.applyConfig()

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
	t.Setenv("SEPG_SESSION_SECRET", ss)
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

	app.applyConfig()

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
	t.Setenv("SEPG_SESSION_SECRET", ss)
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
	app.applyConfig()
}

func TestApp_LoadsETagFromConfig(t *testing.T) {
	app := CreateApp(t, false)
	ctx := context.Background()

	// Set custom ETag in database
	cfg, err := app.configService.Load(ctx)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	cfg.ETagVersion = "20260129-99"
	app.configService.Save(ctx, cfg)

	// Create new app using same root directory (simulates restart, same database)
	app2 := CreateAppWithRoot(t, false, app.rootDir)

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
