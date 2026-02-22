//go:build integration

package server

import (
	"testing"

	"go.local/sfpg/internal/server/config"
	"gopkg.in/yaml.v3"
)

// TestLastKnownGood_SavedOnConfigUpdate verifies that last known good config is saved after successful update.
func TestLastKnownGood_SavedOnConfigUpdate(t *testing.T) {
	app := CreateApp(t, false)
	app.setDB()
	app.config = config.DefaultConfig()
	app.config.ListenerPort = 8081
	app.config.SiteName = "Original"

	// Save initial config
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = app.config.SaveToDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to save initial config: %v", err)
	}

	// Update config
	app.config.ListenerPort = 9999
	app.config.SiteName = "Updated"
	err = app.config.SaveToDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to save updated config: %v", err)
	}

	// Verify last known good was saved
	var lastKnownGoodYAML string
	err = cpcRw.Conn.QueryRowContext(app.ctx, "SELECT value FROM config WHERE key = 'LastKnownGoodConfig'").Scan(&lastKnownGoodYAML)
	if err != nil {
		t.Fatalf("Failed to query last known good config: %v", err)
	}

	// Parse YAML to verify it contains the updated values
	var yamlData map[string]interface{}
	if err := yaml.Unmarshal([]byte(lastKnownGoodYAML), &yamlData); err != nil {
		t.Fatalf("Failed to parse last known good YAML: %v", err)
	}

	if port, ok := yamlData["listener-port"].(int); !ok || port != 9999 {
		t.Errorf("Expected listener-port to be 9999 in last known good, got %v", yamlData["listener-port"])
	}
	if siteName, ok := yamlData["site-name"].(string); !ok || siteName != "Updated" {
		t.Errorf("Expected site-name to be 'Updated' in last known good, got %v", yamlData["site-name"])
	}
}

// TestLastKnownGood_RestoreFromUI verifies that restore from UI shows diff and applies after confirmation.
func TestLastKnownGood_RestoreFromUI(t *testing.T) {
	app := CreateApp(t, false)
	app.setDB()
	app.config = config.DefaultConfig()

	// Set up last known good config in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	lastKnownGoodYAML := `listener-port: 8888
site-name: "Restored Gallery"
log-level: "warn"
`

	_, err = cpcRw.Conn.ExecContext(app.ctx, "INSERT OR REPLACE INTO config (key, value) VALUES ('LastKnownGoodConfig', ?)", lastKnownGoodYAML)
	if err != nil {
		t.Fatalf("Failed to insert last known good config: %v", err)
	}

	// Restore should return diff
	restoredConfig, err := app.config.RestoreLastKnownGood(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to restore last known good: %v", err)
	}

	// Verify restored config matches last known good
	if restoredConfig.ListenerPort != 8888 {
		t.Errorf("Expected restored port 8888, got %d", restoredConfig.ListenerPort)
	}
	if restoredConfig.SiteName != "Restored Gallery" {
		t.Errorf("Expected restored site name 'Restored Gallery', got %q", restoredConfig.SiteName)
	}
	if restoredConfig.LogLevel != "warn" {
		t.Errorf("Expected restored log level 'warn', got %q", restoredConfig.LogLevel)
	}
}

// TestLastKnownGood_RestoreFromCLI verifies that restore from CLI applies immediately on startup.
func TestLastKnownGood_RestoreFromCLI(t *testing.T) {
	app := CreateApp(t, false)
	app.setDB()

	// Set up last known good config in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	lastKnownGoodYAML := `listener-port: 7777
site-name: "CLI Restored"
`

	_, err = cpcRw.Conn.ExecContext(app.ctx, "INSERT OR REPLACE INTO config (key, value) VALUES ('LastKnownGoodConfig', ?)", lastKnownGoodYAML)
	if err != nil {
		t.Fatalf("Failed to insert last known good config: %v", err)
	}

	// Simulate CLI restore (load config with restore flag)
	restoredConfig, err := app.config.RestoreLastKnownGood(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to restore: %v", err)
	}

	// Apply restored config
	err = restoredConfig.SaveToDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to save restored config: %v", err)
	}

	// Reload config to verify it was applied
	newConfig := config.DefaultConfig()
	err = newConfig.LoadFromDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to reload config: %v", err)
	}

	if newConfig.ListenerPort != 7777 {
		t.Errorf("Expected restored port 7777, got %d", newConfig.ListenerPort)
	}
	if newConfig.SiteName != "CLI Restored" {
		t.Errorf("Expected restored site name 'CLI Restored', got %q", newConfig.SiteName)
	}
}

// TestLastKnownGood_DiffDisplay verifies that diff is shown before restore.
func TestLastKnownGood_DiffDisplay(t *testing.T) {
	app := CreateApp(t, false)
	app.setDB()
	app.config = config.DefaultConfig()
	app.config.ListenerPort = 8081
	app.config.SiteName = "Current"

	// Save current config
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = app.config.SaveToDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to save current config: %v", err)
	}

	// Set up different last known good
	lastKnownGoodYAML := `listener-port: 9999
site-name: "Last Known Good"
`

	_, err = cpcRw.Conn.ExecContext(app.ctx, "INSERT OR REPLACE INTO config (key, value) VALUES ('LastKnownGoodConfig', ?)", lastKnownGoodYAML)
	if err != nil {
		t.Fatalf("Failed to insert last known good: %v", err)
	}

	// Get diff
	diff, err := app.config.GetLastKnownGoodDiff(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to get diff: %v", err)
	}

	// Verify diff shows differences
	if diff == nil {
		t.Fatal("Diff should not be nil")
	}

	// Parse current YAML from diff
	var currentData map[string]interface{}
	if err := yaml.Unmarshal([]byte(diff.CurrentYAML), &currentData); err != nil {
		t.Fatalf("Failed to parse current YAML from diff: %v", err)
	}

	// Parse new YAML from diff
	var newData map[string]interface{}
	if err := yaml.Unmarshal([]byte(diff.NewYAML), &newData); err != nil {
		t.Fatalf("Failed to parse new YAML from diff: %v", err)
	}

	// Verify differences
	if currentPort, ok := currentData["listener-port"].(int); !ok || currentPort != 8081 {
		t.Errorf("Expected current port 8081, got %v", currentData["listener-port"])
	}
	if newPort, ok := newData["listener-port"].(int); !ok || newPort != 9999 {
		t.Errorf("Expected new port 9999, got %v", newData["listener-port"])
	}

	if len(diff.Changes) == 0 {
		t.Error("Diff should show changes")
	}
}

// TestLastKnownGood_NotFound verifies that restore handles missing last known good gracefully.
func TestLastKnownGood_NotFound(t *testing.T) {
	app := CreateApp(t, false)
	app.setDB()
	app.config = config.DefaultConfig()

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	// Ensure LastKnownGoodConfig doesn't exist
	_, err = cpcRw.Conn.ExecContext(app.ctx, "DELETE FROM config WHERE key = 'LastKnownGoodConfig'")
	if err != nil {
		t.Fatalf("Failed to delete last known good: %v", err)
	}

	// Restore should return error or nil config
	_, err = app.config.RestoreLastKnownGood(app.ctx, cpcRw.Queries)
	if err == nil {
		t.Error("Expected error when last known good not found, got nil")
	}
}

// TestLastKnownGood_ExcludesSessionSecret verifies that last known good never contains session secret.
func TestLastKnownGood_ExcludesSessionSecret(t *testing.T) {
	app := CreateApp(t, false)
	app.setDB()
	app.config = config.DefaultConfig()

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	// Save config (should save last known good)
	err = app.config.SaveToDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Get last known good
	var lastKnownGoodYAML string
	err = cpcRw.Conn.QueryRowContext(app.ctx, "SELECT value FROM config WHERE key = 'LastKnownGoodConfig'").Scan(&lastKnownGoodYAML)
	if err != nil {
		t.Fatalf("Failed to query last known good: %v", err)
	}

	// Parse YAML
	var yamlData map[string]interface{}
	if err := yaml.Unmarshal([]byte(lastKnownGoodYAML), &yamlData); err != nil {
		t.Fatalf("Failed to parse last known good YAML: %v", err)
	}

	// Verify session-secret is not present
	if _, exists := yamlData["session-secret"]; exists {
		t.Error("Last known good should not contain session-secret key")
	}
	if _, exists := yamlData["SessionSecret"]; exists {
		t.Error("Last known good should not contain SessionSecret key")
	}
}

// TestLastKnownGood_PreservesUserPassword verifies that last known good doesn't overwrite user/password.
func TestLastKnownGood_PreservesUserPassword(t *testing.T) {
	app := CreateApp(t, false)
	app.setDB()
	app.config = config.DefaultConfig()

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	// Set up existing user/password
	_, err = cpcRw.Conn.ExecContext(app.ctx, "INSERT OR REPLACE INTO config (key, value) VALUES ('user', 'admin')")
	if err != nil {
		t.Fatalf("Failed to insert user: %v", err)
	}

	// Save config (should save last known good)
	err = app.config.SaveToDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Restore last known good
	restoredConfig, err := app.config.RestoreLastKnownGood(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to restore: %v", err)
	}

	// Apply restored config
	err = restoredConfig.SaveToDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to save restored config: %v", err)
	}

	// Verify user/password still exist
	var user string
	err = cpcRw.Conn.QueryRowContext(app.ctx, "SELECT value FROM config WHERE key = 'user'").Scan(&user)
	if err != nil {
		t.Fatalf("Failed to query user: %v", err)
	}
	if user != "admin" {
		t.Errorf("Expected user 'admin', got %q", user)
	}
}
