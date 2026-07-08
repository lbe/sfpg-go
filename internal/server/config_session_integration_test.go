//go:build integration || e2e

package server

import (
	"net/http"
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

func TestSessionConfigIntegration_SameSite(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	cases := []struct {
		name     string
		value    string
		expected http.SameSite
	}{
		{"Lax", "Lax", http.SameSiteLaxMode},
		{"Strict", "Strict", http.SameSiteStrictMode},
		{"None", "None", http.SameSiteNoneMode},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app.configMu.Lock()
			app.config.SessionSameSite = tc.value
			app.configMu.Unlock()

			store := sessions.NewCookieStore([]byte(app.sessionSecret))
			store.Options = app.getSessionOptions()

			if store.Options.SameSite != tc.expected {
				t.Errorf("Expected SameSite %v, got %v", tc.expected, store.Options.SameSite)
			}
		})
	}
}

func TestSessionConfigIntegration_Defaults(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	t.Parallel()
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.configMu.Unlock()

	store := sessions.NewCookieStore([]byte(app.sessionSecret))
	store.Options = app.getSessionOptions()
	defaults := config.DefaultConfig()

	if store.Options.MaxAge != defaults.SessionMaxAge {
		t.Errorf("Expected MaxAge %d, got %d", defaults.SessionMaxAge, store.Options.MaxAge)
	}
	if store.Options.HttpOnly != defaults.SessionHttpOnly {
		t.Errorf("Expected HttpOnly %v, got %v", defaults.SessionHttpOnly, store.Options.HttpOnly)
	}
	if store.Options.Secure != defaults.SessionSecure {
		t.Errorf("Expected Secure %v, got %v", defaults.SessionSecure, store.Options.Secure)
	}

	var expectedSameSite http.SameSite
	switch defaults.SessionSameSite {
	case "Lax":
		expectedSameSite = http.SameSiteLaxMode
	case "Strict":
		expectedSameSite = http.SameSiteStrictMode
	case "None":
		expectedSameSite = http.SameSiteNoneMode
	default:
		t.Fatalf("unexpected default SessionSameSite %q", defaults.SessionSameSite)
	}
	if store.Options.SameSite != expectedSameSite {
		t.Errorf("Expected SameSite %v, got %v", expectedSameSite, store.Options.SameSite)
	}
	if store.Options.Path != "/" {
		t.Errorf("Expected Path '/', got %q", store.Options.Path)
	}
}
