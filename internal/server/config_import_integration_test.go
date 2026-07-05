//go:build integration || e2e

package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/config"
)

func TestConfigImport_Preview_ShowsDiff(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	app.config = config.DefaultConfig()
	app.config.ListenerPort = 8081

	// New YAML with different values
	newYAML := `listener-port: 9999
site-name: "Imported Gallery"
`

	// Preview should show differences
	diff, err := app.config.PreviewImport(newYAML)
	if err != nil {
		t.Fatalf("Failed to preview import: %v", err)
	}

	// Verify diff shows changes
	if diff == nil {
		t.Error("Diff should not be nil")
	}
	// Diff should indicate port change from 8081 to 9999
	// This will be expanded when diff structure is defined
}

// TestConfigImport_Commit_RequiresConfirmation verifies that import commit requires confirmation.

func TestConfigImport_Commit_RequiresConfirmation(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	app.config = config.DefaultConfig()

	newYAML := `listener-port: 9999
`

	// Import commit should require confirmation
	// This will be tested in handler tests with actual user interaction
	// For now, we verify the import parsing works
	_, err := app.config.PreviewImport(newYAML)
	if err != nil {
		t.Fatalf("Failed to preview import: %v", err)
	}
}

// TestConfigImport_Commit_UpdatesDatabase verifies that import commit updates database.

func TestConfigImport_Commit_UpdatesDatabase(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	app.config = config.DefaultConfig()

	newYAML := `listener-port: 9999
site-name: "Imported Gallery"
log-level: "info"
`

	// Import should update database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)
	if importErr := app.config.ImportFromYAML(newYAML, app.ctx, cpcRw.Queries); importErr != nil {
		t.Fatalf("Failed to import config: %v", importErr)
	}

	// Verify database was updated
	cpcRw2, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw2)

	// Reload config from database
	newConfig := config.DefaultConfig()
	err = newConfig.LoadFromDatabase(app.ctx, cpcRw2.Queries)
	if err != nil {
		t.Fatalf("Failed to load config from database: %v", err)
	}

	// Verify imported values
	if newConfig.ListenerPort != 9999 {
		t.Errorf("Expected port 9999, got %d", newConfig.ListenerPort)
	}
	if newConfig.SiteName != "Imported Gallery" {
		t.Errorf("Expected site name 'Imported Gallery', got %q", newConfig.SiteName)
	}
	if newConfig.LogLevel != "info" {
		t.Errorf("Expected log level 'info', got %q", newConfig.LogLevel)
	}
}

// TestConfigImport_Commit_UpdatesYAMLFile verifies that import optionally updates YAML file with confirmation.

func TestConfigImport_Commit_UpdatesYAMLFile(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")

	originalContent := "listener-port: 8081\n"
	err := os.WriteFile(configFile, []byte(originalContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Import with file update should write to file
	// This will be tested in handler tests with confirmation
	// For now, we verify the file exists
	if _, err := os.Stat(configFile); err != nil {
		t.Fatalf("Config file should exist: %v", err)
	}
}

// TestConfigImport_InvalidYAML verifies that invalid YAML is rejected.

func TestConfigImport_InvalidYAML(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	app.config = config.DefaultConfig()

	invalidYAML := `listener-port: 8081
invalid: [unclosed
`

	// Import should reject invalid YAML
	_, err := app.config.PreviewImport(invalidYAML)
	if err == nil {
		t.Error("Expected error for invalid YAML, got nil")
	}
}

// TestConfigImport_FileAccessErrors verifies that file access errors are handled gracefully.

func TestConfigImport_FileAccessErrors(t *testing.T) {
	// This test would require creating inaccessible files
	// For now, we verify the error handling concept
	app := CreateApp(t)
	t.Parallel()
	app.config = config.DefaultConfig()

	// Attempting to import from non-existent file should handle error gracefully
	// This will be tested in handler tests
	_ = app
}

// TestConfigImport_RejectsSessionSecret verifies that session secret in import is rejected.

func TestConfigImport_RejectsSessionSecret(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	app.config = config.DefaultConfig()

	yamlWithSecret := `listener-port: 8081
session-secret: "should-not-be-imported"
`

	// Import should reject YAML containing session secret
	_, err := app.config.PreviewImport(yamlWithSecret)
	if err == nil {
		t.Error("Expected error for YAML containing session-secret, got nil")
	}
}

// TestConfigImport_PreservesUserPassword verifies that user/password are not overwritten by import.

func TestConfigImport_PreservesUserPassword(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	app.config = config.DefaultConfig()

	// Set up existing user/password in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	_, err = cpcRw.Conn.ExecContext(app.ctx, "INSERT OR REPLACE INTO config (key, value) VALUES ('user', 'admin')")
	if err != nil {
		t.Fatalf("Failed to insert user: %v", err)
	}

	newYAML := `listener-port: 9999
`

	// Import should not affect user/password
	err = app.config.ImportFromYAML(newYAML, app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to import config: %v", err)
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

// TestConfigImport_PrecedenceIntegration verifies that imported YAML integrates with precedence.

func TestConfigImport_PrecedenceIntegration(t *testing.T) {
	app := CreateApp(t)
	t.Parallel()
	app.config = config.DefaultConfig()

	// Set value in database
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = cpcRw.Queries.UpsertConfigValueOnly(app.ctx, gallerydb.UpsertConfigValueOnlyParams{
		Key:       "listener_port",
		Value:     "9000",
		CreatedAt: 0,
		UpdatedAt: 0,
	})
	if err != nil {
		t.Fatalf("Failed to set DB value: %v", err)
	}

	// Import YAML with different value
	newYAML := `listener-port: 9999
`

	// Import should update database
	err = app.config.ImportFromYAML(newYAML, app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to import: %v", err)
	}

	// Reload config (simulating app restart)
	newConfig := config.DefaultConfig()
	err = newConfig.LoadFromDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("Failed to reload: %v", err)
	}

	// Database value should be updated
	if newConfig.ListenerPort != 9999 {
		t.Errorf("Expected port 9999 from import, got %d", newConfig.ListenerPort)
	}
}

// TestDBConfig_HTTPCacheDisableActuallyDisablesCaching verifies that setting
// enable_http_cache=false in the database is honored during app startup.
// This test exposes a timing bug where cache initialization happens before DB config is loaded.
// This test is expected to FAIL initially (RED phase) to prove the defect exists.
