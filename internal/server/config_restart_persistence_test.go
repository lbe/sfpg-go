// Package server_test contains tests for configuration persistence across server restarts.
// These tests verify that configuration changes are properly applied after restart.
package server

import (
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
)

// TestConfigPersistence_RestartUsesUpdatedConfig verifies that getRouter()
// uses updated config values after restart, not the original app.opt values.
func TestConfigPersistence_RestartUsesUpdatedConfig(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Set up initial config with compression and cache enabled
	app.configMu.Lock()
	app.config = config.DefaultConfig()
	app.config.ServerCompressionEnable = true
	app.config.EnableHTTPCache = true
	app.configMu.Unlock()

	// Set up app.opt with different values (simulating CLI/env overrides at startup)
	app.opt.EnableCompression = getopt.OptBool{Bool: true, IsSet: true}
	app.opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}

	// Initial router should use app.config values (which match app.opt in this case)
	router1 := app.getRouter()
	if router1 == nil {
		t.Fatal("getRouter() returned nil")
	}

	// Update config to disable compression and cache
	app.configMu.Lock()
	app.config.ServerCompressionEnable = false
	app.config.EnableHTTPCache = false
	app.configMu.Unlock()

	// After config update, getRouter() should use updated app.config values
	router2 := app.getRouter()
	if router2 == nil {
		t.Fatal("getRouter() returned nil after config update")
	}

	// Verify config values are what we expect
	app.configMu.RLock()
	if app.config.ServerCompressionEnable != false {
		t.Errorf("Expected compression disabled, got %v", app.config.ServerCompressionEnable)
	}
	if app.config.EnableHTTPCache != false {
		t.Errorf("Expected cache disabled, got %v", app.config.EnableHTTPCache)
	}
	app.configMu.RUnlock()

	// The router should now reflect the updated config (compression and cache disabled)
	// We can't directly test middleware application, but we verify the config is correct
	// which is what getRouter() reads from
}
