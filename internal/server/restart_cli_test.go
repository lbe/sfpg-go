package server

import (
	"testing"

	"go.local/sfpg/internal/getopt"
	"go.local/sfpg/internal/server/config"
)

// TestCase1_CLIOverridesUnchangedField verifies:
// User starts with -port=8083, DB has port=8081, user changes compression only.
// After UpdateConfig, port should be 8083 (CLI value), not 8081 (DB value).
func TestCase1_CLIOverridesUnchangedField(t *testing.T) {
	// Create app with CLI port=8083
	opt := getopt.Opt{
		Port: getopt.OptInt{Int: 8083, IsSet: true},
	}
	app := CreateAppWithOpt(t, false, opt)
	defer app.Shutdown()

	// Simulate DB having port=8081 (different from CLI)
	app.configMu.Lock()
	app.config.ListenerPort = 8081
	app.configMu.Unlock()

	// Simulate user changing compression (not port) via web UI
	// changedFields does NOT include "listener_port"
	newConfig := config.DefaultConfig()
	newConfig.ListenerPort = 8081             // DB value (unchanged)
	newConfig.ServerCompressionEnable = false // User changed compression

	// Call UpdateConfig with compression in changedFields
	app.configHandlers.UpdateConfig(newConfig, []string{"server_compression_enable"})

	// After UpdateConfig, port should be 8083 (CLI value) because it wasn't changed
	app.configMu.RLock()
	port := app.config.ListenerPort
	app.configMu.RUnlock()

	if port != 8083 {
		t.Errorf("Case 1 FAILED: Expected port 8083 (CLI value), got %d. "+
			"CLI values should override unchanged fields.", port)
	} else {
		t.Logf("Case 1 PASSED: Port is 8083 (CLI value) as expected")
	}
}

// TestCase2_UserChangeOverridesCLI verifies:
// User starts with -port=8083, changes port to 8084 in modal.
// After UpdateConfig, port should be 8084 (user change), not 8083 (CLI value).
func TestCase2_UserChangeOverridesCLI(t *testing.T) {
	// Create app with CLI port=8083
	opt := getopt.Opt{
		Port: getopt.OptInt{Int: 8083, IsSet: true},
	}
	app := CreateAppWithOpt(t, false, opt)
	defer app.Shutdown()

	// Simulate user changing port to 8084 via web UI
	newConfig := config.DefaultConfig()
	newConfig.ListenerPort = 8084 // User changed port

	// Call UpdateConfig with listener_port in changedFields
	app.configHandlers.UpdateConfig(newConfig, []string{"listener_port"})

	// After UpdateConfig, port should be 8084 (user change)
	app.configMu.RLock()
	port := app.config.ListenerPort
	app.configMu.RUnlock()

	if port != 8084 {
		t.Errorf("Case 2 FAILED: Expected port 8084 (user change), got %d. "+
			"User changes should override CLI values.", port)
	} else {
		t.Logf("Case 2 PASSED: Port is 8084 (user change) as expected")
	}
}
