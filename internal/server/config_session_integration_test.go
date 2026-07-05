//go:build integration || e2e

package server

import (
	"testing"

	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/server/config"
)

func TestSessionConfigIntegration_MaxAge(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Initialize config if not already loaded
	t.Parallel()
	app.configMu.Lock()
	if app.config == nil {
		app.config = config.DefaultConfig()
	}
	app.config.SessionMaxAge = 3600 // 1 hour
	app.configMu.Unlock()

	// Initialize session store (simulating what Serve() does)
	app.store = sessions.NewCookieStore([]byte(app.sessionSecret))
	app.store.Options = app.getSessionOptions()

	// Verify MaxAge matches config
	if app.store.Options.MaxAge != 3600 {
		t.Errorf("Expected MaxAge to be 3600 (from config), got %d", app.store.Options.MaxAge)
	}
}

// TestSessionConfigIntegration_HttpOnly verifies that SessionHttpOnly from config
// is correctly applied to the session cookie HttpOnly option.

func TestSessionConfigIntegration_HttpOnly(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Initialize config if not already loaded
	t.Parallel()
	app.configMu.Lock()
	if app.config == nil {
		app.config = config.DefaultConfig()
	}
	app.config.SessionHttpOnly = false
	app.configMu.Unlock()

	app.store = sessions.NewCookieStore([]byte(app.sessionSecret))
	app.store.Options = app.getSessionOptions()

	if app.store.Options.HttpOnly != false {
		t.Errorf("Expected HttpOnly to be false (from config), got %v", app.store.Options.HttpOnly)
	}
}

// TestSessionConfigIntegration_Secure verifies that SessionSecure from config
// is correctly applied to the session cookie Secure option.

func TestSessionConfigIntegration_Secure(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Initialize config if not already loaded
	t.Parallel()
	app.configMu.Lock()
	if app.config == nil {
		app.config = config.DefaultConfig()
	}
	app.config.SessionSecure = false
	app.configMu.Unlock()

	app.store = sessions.NewCookieStore([]byte(app.sessionSecret))
	app.store.Options = app.getSessionOptions()

	if app.store.Options.Secure != false {
		t.Errorf("Expected Secure to be false (from config), got %v", app.store.Options.Secure)
	}
}

// TestSessionConfigIntegration_SameSite verifies that SessionSameSite from config
// is correctly converted and applied to the session cookie SameSite option.
// Tests all three valid values: "Lax", "Strict", and "None".

// TestLoadConfig_CompleteStateAfterFreshDatabase verifies that after fresh database
// initialization and loadConfig(), the complete app.config matches config.DefaultConfig().
