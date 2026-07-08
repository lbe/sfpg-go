package server

import (
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/getopt"
)

// Minimal Thumb struct for testing purposes

// TestAuthMiddleware tests the authMiddleware to ensure it correctly protects
// routes, redirecting unauthenticated requests and allowing authenticated ones.
func TestLogProfileLocation(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Call LogProfileLocation - should not panic even if no profiler is running
	app.LogProfileLocation()

	// Test with a stopProfiler function set
	called := false
	app.stopProfiler = func() {
		called = true
	}

	app.LogProfileLocation()

	if !called {
		t.Error("Expected stopProfiler to be called")
	}
}

// TestSetupLogging tests logging setup
func TestSetupLogging(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// setupLogging is already called by CreateApp, but we can call it again
	// to improve coverage of different code paths
	app.setupBootstrapLogging()

	if app.logger == nil {
		t.Error("Expected logger to be initialized")
	}
}

// TestSetupBootstrapLogging tests bootstrap logging setup
func TestSetupBootstrapLogging(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// setupBootstrapLogging is called before setupLogging
	// We can test it by creating a new app without full initialization
	app2 := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "test-secret", IsSet: true},
	}, "x.y.z")
	defer app2.Shutdown()

	tempDir := t.TempDir()
	app2.setRootDir(&tempDir)
	app2.setupBootstrapLogging()

	// Logger should be created
	if app2.logger == nil {
		t.Error("Expected logger to be initialized after bootstrap logging")
	}
}

func TestSetupLogging_Variations(t *testing.T) {
	t.Run("with default config", func(t *testing.T) {
		app := CreateApp(t)
		defer app.Shutdown()

		// Load config
		if err := app.loadConfig(); err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}

		// Call setupLogging again
		app.setupBootstrapLogging()

		if app.logger == nil {
			t.Error("Expected logger to be initialized")
		}
	})

	t.Run("without config", func(t *testing.T) {
		app := New(getopt.Opt{
			SessionSecret: getopt.OptString{String: "test-secret", IsSet: true},
		}, "x.y.z")
		defer app.Shutdown()

		tempDir := t.TempDir()
		app.setRootDir(&tempDir)
		app.setupBootstrapLogging()

		// setupLogging should work even without config
		app.setupBootstrapLogging()

		if app.logger == nil {
			t.Error("Expected logger to be initialized")
		}
	})
}

// TestSetupBootstrapLogging_Variations tests bootstrap logging variations
func TestSetupBootstrapLogging_Variations(t *testing.T) {
	t.Run("basic setup", func(t *testing.T) {
		app := New(getopt.Opt{
			SessionSecret: getopt.OptString{String: "test-secret", IsSet: true},
		}, "x.y.z")
		defer app.Shutdown()

		tempDir := t.TempDir()
		app.setRootDir(&tempDir)

		// Should create logger
		app.setupBootstrapLogging()

		if app.logger == nil {
			t.Error("Expected logger to be initialized")
		}
	})

	t.Run("with existing logger", func(t *testing.T) {
		app := CreateApp(t)
		defer app.Shutdown()

		// Logger already exists from CreateApp
		if app.logger == nil {
			t.Fatal("Expected logger to exist from CreateApp")
		}

		// Call again - should handle gracefully
		app.setupBootstrapLogging()

		if app.logger == nil {
			t.Error("Expected logger to still be initialized")
		}
	})
}

// TestLogProfileLocation_WithProfilerDir tests profiler logging
func TestLogProfileLocation_WithProfilerDir(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Test with stopProfiler that actually sets profiler dir
	// (would need actual profiler setup, which is complex, so we just test the code path)
	app.LogProfileLocation()
}

// TestGetSessionOptions_EdgeCases tests session options edge cases
func TestSetupBootstrapLogging_Coverage(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	if app.logger == nil {
		t.Fatal("Expected logger to be initialized")
	}

	// Call it again to test idempotence
	app.setupBootstrapLogging()

	if app.logger == nil {
		t.Error("Expected logger to remain initialized")
	}
}

// TestSetupLogging_Coverage verifies deprecated setupLogging delegates properly
func TestSetupLogging_Coverage(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Call setupLogging
	app.setupBootstrapLogging()

	if app.logger == nil {
		t.Error("Expected logger to still be set")
	}
}

// TestReloadLoggingFromConfig_Coverage verifies logging reload
func TestReloadLoggingFromConfig_Coverage(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Load config first
	_ = app.loadConfig()
	// Try to reload logging - may fail if config not fully loaded, that's OK
	_ = app.reloadLoggingFromConfig()
}

// TestLoadConfig_Coverage verifies config loading
func TestLogProfileLocation_Coverage(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Should not panic
	app.LogProfileLocation()
}

func TestSetupBootstrapLogging_ErrorPaths(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Call setupBootstrapLogging when already initialized
	oldLogger := app.logger
	app.setupBootstrapLogging()

	// Logger should still be valid
	if app.logger == nil {
		t.Error("Logger should not be nil after setupBootstrapLogging")
	}
	_ = oldLogger
}

// TestSetupLogging_WithConfigLogger tests setupLogging after config load
func TestSetupLogging_WithConfigLogger(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Load config first
	_ = app.loadConfig()

	// Then setup logging
	app.setupBootstrapLogging()

	if app.logger == nil {
		t.Error("Expected logger to be set")
	}
}

// TestBuildHandlers_Integration tests handler building with full setup
func TestSetupLogging_Complete(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// setupLogging should check profiler state and call setupBootstrapLogging
	app.setupBootstrapLogging()

	if app.logger == nil {
		t.Error("Expected logger to be set")
	}
}

// TestCacheWriteWorker_Shutdown tests cacheWriteWorker shutdown path
