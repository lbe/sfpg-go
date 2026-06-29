package server

import (
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
)

// Minimal Thumb struct for testing purposes

// TestAuthMiddleware tests the authMiddleware to ensure it correctly protects
// routes, redirecting unauthenticated requests and allowing authenticated ones.
func TestGetSessionOptions(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	opts := app.getSessionOptions()
	if opts == nil {
		t.Fatal("Expected non-nil session options")
	}
	if opts.MaxAge <= 0 {
		t.Error("Expected positive MaxAge")
	}
}

// TestGetSessionOptionsConfig tests session options config
func TestGetSessionOptionsConfig(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Load config from database
	if err := app.loadConfig(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	cfg := app.getSessionOptionsConfig()
	if cfg == nil {
		t.Fatal("Expected non-nil session options config")
	}

	if cfg.SessionMaxAge <= 0 {
		t.Error("Expected positive session max age")
	}

	// Just check that values exist, don't test specific values since they come from defaults
	_ = cfg.SessionHttpOnly
	_ = cfg.SessionSecure

	if cfg.SessionSameSite == "" {
		t.Error("Expected non-empty SessionSameSite")
	}
}

// TestResponseWriterMethods tests responseWriter helper methods
func TestGetSessionOptions_EdgeCases(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	t.Run("with loaded config", func(t *testing.T) {
		if err := app.loadConfig(); err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}

		opts := app.getSessionOptions()
		if opts == nil {
			t.Error("Expected non-nil session options")
		}
	})

	t.Run("without session manager", func(t *testing.T) {
		app2 := CreateApp(t, false)
		defer app2.Shutdown()

		// Even without config, should return some options
		opts := app2.getSessionOptions()
		if opts == nil {
			t.Error("Expected non-nil fallback session options")
		}
	})
}

// TestEnsureSessionAndRestart tests session and restart initialization
func TestEnsureSessionAndRestart(t *testing.T) {
	// CreateApp already calls ensureSessionAndRestart, so test it's properly initialized
	app := CreateApp(t, false)
	defer app.Shutdown()

	if app.store == nil {
		t.Error("Expected store to be initialized")
	}
	if app.sessionManager == nil {
		t.Error("Expected sessionManager to be initialized")
	}
	if app.restartCh == nil {
		t.Error("Expected restartCh to be initialized")
	}
}

// TestRestartRequired tests restart flag checking
func TestRestartRequired(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Initially should not require restart
	if app.RestartRequired() {
		t.Error("Expected restart not required initially")
	}

	// Set restart required
	app.restartMu.Lock()
	app.restartRequired = true
	app.restartMu.Unlock()

	if !app.RestartRequired() {
		t.Error("Expected restart required after setting flag")
	}
}

// TestResponseWriter_AdditionalMethods tests more responseWriter methods
func TestRestartRequired_Coverage(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	app.restartRequired = false
	if app.RestartRequired() {
		t.Error("Expected RestartRequired to return false")
	}

	app.restartRequired = true
	if !app.RestartRequired() {
		t.Error("Expected RestartRequired to return true")
	}
}
