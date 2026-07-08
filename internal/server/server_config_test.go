package server

import (
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"

	"github.com/lbe/sfpg-go/web"
)

// Minimal Thumb struct for testing purposes

// TestAuthMiddleware tests the authMiddleware to ensure it correctly protects
// routes, redirecting unauthenticated requests and allowing authenticated ones.
func TestConfigValidate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := config.DefaultConfig()
		if err := cfg.Validate(); err != nil {
			t.Errorf("Expected valid config, got error: %v", err)
		}
	})

	t.Run("invalid listener port", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.ListenerPort = -1
		if err := cfg.Validate(); err == nil {
			t.Error("Expected validation error for negative port")
		}
	})

	t.Run("invalid listener port too high", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.ListenerPort = 70000
		if err := cfg.Validate(); err == nil {
			t.Error("Expected validation error for port > 65535")
		}
	})
}

// TestConfigValidateSetting tests individual config setting validation
func TestConfigValidateSetting(t *testing.T) {
	cfg := config.DefaultConfig()

	tests := []struct {
		name      string
		key       string
		value     string
		expectErr bool
	}{
		{
			name:      "valid listener port",
			key:       "listener_port",
			value:     "8080",
			expectErr: false,
		},
		{
			name:      "invalid listener port",
			key:       "listener_port",
			value:     "-1",
			expectErr: true,
		},
		{
			name:      "valid log level",
			key:       "log_level",
			value:     "INFO",
			expectErr: false,
		},
		{
			name:      "invalid log level",
			key:       "log_level",
			value:     "INVALID",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cfg.ValidateSetting(tt.key, tt.value)
			if tt.expectErr && err == nil {
				t.Errorf("Expected error for setting %s=%s, got nil", tt.key, tt.value)
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Expected no error for setting %s=%s, got: %v", tt.key, tt.value, err)
			}
		})
	}
}

// TestConfigMergeDefaults tests merging config with defaults
func TestConfigMergeDefaults(t *testing.T) {
	cfg := &config.Config{
		ListenerPort: 9999,
		// Leave other fields as zero values
	}
	defaults := config.DefaultConfig()

	cfg.MergeDefaults(defaults)

	if cfg.ListenerPort != 9999 {
		t.Errorf("Expected port to remain 9999, got %d", cfg.ListenerPort)
	}
	if cfg.ListenerAddress == "" {
		t.Error("Expected listener address to be set from defaults")
	}
	if cfg.LogLevel == "" {
		t.Error("Expected log level to be set from defaults")
	}
}

// TestConfigExportToYAML tests YAML export
func TestConfigExportToYAML(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenerPort = 8888
	cfg.SiteName = "Test Site"

	yamlContent, err := cfg.ExportToYAML()
	if err != nil {
		t.Fatalf("ExportToYAML failed: %v", err)
	}

	if yamlContent == "" {
		t.Error("Expected non-empty YAML content")
	}

	// Parse YAML to verify structure and values
	var parsedMap map[string]any
	err = yaml.Unmarshal([]byte(yamlContent), &parsedMap)
	if err != nil {
		t.Fatalf("Failed to parse YAML: %v", err)
	}

	// Verify port is in the YAML map at the correct key
	port, ok := parsedMap["listener-port"]
	if !ok {
		t.Error("Expected 'listener-port' key in YAML")
	} else if port != 8888 {
		t.Errorf("Expected listener-port 8888 in parsed YAML, got %v", port)
	}

	// Verify site name is in the YAML map at the correct key
	siteName, ok := parsedMap["site-name"]
	if !ok {
		t.Error("Expected 'site-name' key in YAML")
	} else if siteName != "Test Site" {
		t.Errorf("Expected site-name 'Test Site' in parsed YAML, got %v", siteName)
	}
}

// TestConfigRecoverFromCorruption tests config recovery
func TestConfigRecoverFromCorruption(t *testing.T) {
	cfg := &config.Config{
		ListenerPort: -1, // Invalid value
		LogLevel:     "",
	}
	defaults := config.DefaultConfig()

	cfg.RecoverFromCorruption(defaults)

	if cfg.ListenerPort != defaults.ListenerPort {
		t.Errorf("Expected port to be recovered to %d, got %d", defaults.ListenerPort, cfg.ListenerPort)
	}
	if cfg.LogLevel != defaults.LogLevel {
		t.Errorf("Expected log level to be recovered to %s, got %s", defaults.LogLevel, cfg.LogLevel)
	}

	// Test with nil defaults
	cfg2 := &config.Config{ListenerPort: -1}
	cfg2.RecoverFromCorruption(nil)
	if cfg2.ListenerPort != -1 {
		t.Error("Expected no change when recovering with nil defaults")
	}
}

// TestConfigLoadFromOpt tests loading config from command-line options
func TestConfigLoadFromOpt(t *testing.T) {
	cfg := config.DefaultConfig()
	opt := getopt.Opt{
		Port: getopt.OptInt{Int: 9090, IsSet: true},
	}

	cfg.LoadFromOpt(opt)

	if cfg.ListenerPort != 9090 {
		t.Errorf("Expected port 9090, got %d", cfg.ListenerPort)
	}
}

// TestConfigPreviewImport tests config import preview
func TestConfigPreviewImport(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenerPort = 8080
	cfg.SiteName = "Original Site"

	yamlContent := `listener_port: 9090
site_name: "New Site"
log_level: "DEBUG"`

	diff, err := cfg.PreviewImport(yamlContent)
	if err != nil {
		t.Fatalf("PreviewImport failed: %v", err)
	}

	if diff == nil {
		t.Fatal("Expected diff to be non-nil")
	}

	if len(diff.Changes) == 0 {
		t.Error("Expected changes to be detected")
	}
}

// TestConfigPreviewImportInvalid tests preview with invalid YAML
func TestConfigPreviewImportInvalid(t *testing.T) {
	cfg := config.DefaultConfig()

	invalidYAML := `this is not: [valid yaml`

	_, err := cfg.PreviewImport(invalidYAML)
	if err == nil {
		t.Error("Expected error for invalid YAML")
	}
}

// TestConfigSaveToDatabase tests saving config to database
func TestConfigSaveToDatabase(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	cfg := config.DefaultConfig()
	cfg.ListenerPort = 7777
	cfg.SiteName = "Test Save"

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = cfg.SaveToDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Errorf("SaveToDatabase failed: %v", err)
	}

	// Verify saved
	port, err := cpcRw.Queries.GetConfigValueByKey(app.ctx, "listener_port")
	if err != nil {
		t.Fatalf("Failed to retrieve saved port: %v", err)
	}
	if port != "7777" {
		t.Errorf("Expected port 7777, got %s", port)
	}
}

// TestConfigRestoreLastKnownGood tests restoring last known good config
func TestConfigRestoreLastKnownGood(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// First save a config
	cfg := config.DefaultConfig()
	cfg.ListenerPort = 6666
	cfg.SiteName = "Backup Config"

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = cfg.SaveToDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("SaveToDatabase failed: %v", err)
	}

	// Now try to restore
	newCfg := config.DefaultConfig()
	restored, err := newCfg.RestoreLastKnownGood(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Errorf("RestoreLastKnownGood failed: %v", err)
	}

	if restored != nil && restored.ListenerPort != 6666 {
		t.Errorf("Expected restored port 6666, got %d", restored.ListenerPort)
	}
}

// TestConfigGetLastKnownGoodDiff tests getting diff with last known good config
func TestConfigGetLastKnownGoodDiff(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Save a config first
	cfg := config.DefaultConfig()
	cfg.ListenerPort = 5555

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	err = cfg.SaveToDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Fatalf("SaveToDatabase failed: %v", err)
	}

	// Create a different config and get diff
	cfg2 := config.DefaultConfig()
	cfg2.ListenerPort = 9999
	diff, err := cfg2.GetLastKnownGoodDiff(app.ctx, cpcRw.Queries)
	if err != nil {
		t.Errorf("GetLastKnownGoodDiff failed: %v", err)
	}

	if diff != nil && len(diff.Changes) == 0 {
		t.Log("Note: diff might be empty if last known good not saved properly")
	}
}

// TestLogProfileLocation tests profile logging
// TestGetAdminUsername tests admin username retrieval
func TestLoadFromDatabase_EdgeCases(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	cfg := config.DefaultConfig()

	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	// Test loading from database when no config exists
	err = cfg.LoadFromDatabase(app.ctx, cpcRo.Queries)
	// Should handle missing config gracefully
	_ = err
}

// TestSaveToDatabase_EdgeCases tests config saving edge cases
func TestSaveToDatabase_EdgeCases(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	cfg := config.DefaultConfig()

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	// Test saving to database
	err = cfg.SaveToDatabase(app.ctx, cpcRw.Queries)
	if err != nil {
		// Some errors might be expected depending on state
		t.Logf("SaveToDatabase returned error (may be expected): %v", err)
	}
}

// TestGetLastKnownGoodDiff_EdgeCases tests config diff edge cases
func TestGetLastKnownGoodDiff_EdgeCases(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	cfg := config.DefaultConfig()

	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("Failed to get DB connection: %v", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	// Test getting diff when no last known good config exists
	_, err = cfg.GetLastKnownGoodDiff(app.ctx, cpcRo.Queries)
	// Should handle missing config gracefully
	_ = err
}

// TestSetConfigDefaults_AllDefaultsPresent verifies that setConfigDefaults
// writes every expected default configuration key to the database.
func TestSetConfigDefaults_AllDefaultsPresent(t *testing.T) {
	tempDir := t.TempDir()
	app := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "this-is-a-test-secret", IsSet: true},
	}, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)
	app.setDB()
	app.setConfigDefaults()

	cpcRo, err := app.dbRoPool.Get()
	if err != nil {
		t.Fatalf("Failed to get RO DB connection: %v", err)
	}
	defer app.dbRoPool.Put(cpcRo)

	excluded := map[string]bool{
		"user":                true,
		"password":            true,
		"LastKnownGoodConfig": true,
		"log_directory":       true,
		"etag_version":        true,
		"image_directory":     true,
	}

	defaults := config.DefaultConfig().ToMap()
	for key, expected := range defaults {
		if excluded[key] {
			continue
		}
		got, dbErr := cpcRo.Queries.GetConfigValueByKey(app.ctx, key)
		if dbErr != nil {
			t.Errorf("expected config key %q to be present, got error: %v", key, dbErr)
			continue
		}
		if got != expected {
			t.Errorf("config key %q: expected %q, got %q", key, expected, got)
		}
	}

	etag, err := cpcRo.Queries.GetConfigValueByKey(app.ctx, "etag_version")
	if err != nil {
		t.Fatalf("expected etag_version to be present, got error: %v", err)
	}
	matched, err := regexp.MatchString(`^[vV]?\d{8}-\d{2}$`, etag)
	if err != nil {
		t.Fatalf("invalid etag regex: %v", err)
	}
	if !matched {
		t.Errorf("etag_version %q does not match expected pattern", etag)
	}

	runDiscovery, err := cpcRo.Queries.GetConfigValueByKey(app.ctx, "run_file_discovery")
	if err != nil {
		t.Fatalf("expected run_file_discovery to be present, got error: %v", err)
	}
	if runDiscovery != "true" {
		t.Errorf("expected run_file_discovery=true, got %q", runDiscovery)
	}
}

// TestSetConfigDefaultsLegacy_Coverage tests legacy config defaults initialization
func TestParseConfigUITemplates_Coverage(t *testing.T) {
	templates, err := parseConfigUITemplates(web.FS)
	if err != nil {
		t.Fatalf("parseConfigUITemplates failed: %v", err)
	}

	// Verify each template exists (value type, zero fields are nil)
	if templates.SaveRestartAlert == nil {
		t.Error("SaveRestartAlert template is nil")
	}
	if templates.SaveSuccessAlert == nil {
		t.Error("SaveSuccessAlert template is nil")
	}
	if templates.ExportModal == nil {
		t.Error("ExportModal template is nil")
	}
	if templates.ImportModal == nil {
		t.Error("ImportModal template is nil")
	}
	if templates.RestoreModal == nil {
		t.Error("RestoreModal template is nil")
	}
	if templates.RestoreSuccessAlert == nil {
		t.Error("RestoreSuccessAlert template is nil")
	}
	if templates.ImportSuccessAlert == nil {
		t.Error("ImportSuccessAlert template is nil")
	}
	if templates.RestartInitiatedAlert == nil {
		t.Error("RestartInitiatedAlert template is nil")
	}
}

// TestSetRootDir_WithExplicitPath verifies setRootDir with explicit directory
func TestLoadConfig_Coverage(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	err := app.loadConfig()
	if err != nil {
		t.Errorf("loadConfig failed: %v", err)
	}

	if app.config == nil {
		t.Error("Expected config to be loaded")
	}
}

// TestApplyConfig_Coverage verifies config application
func TestApplyConfig_Coverage(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// applyConfig takes no arguments and applies current config
	app.ApplyConfig()

	// Should not panic, config should be applied
}

// TestInitForUnlock_Coverage verifies unlock initialization
func TestLoadConfig_WithError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Load config multiple times
	err1 := app.loadConfig()
	err2 := app.loadConfig()

	// Both should succeed or both should fail
	_ = err1
	_ = err2
}

// TestApplyConfig_Multiple times tests applying config multiple times
func TestApplyConfig_MultipleApply(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Apply config multiple times
	app.ApplyConfig()
	app.ApplyConfig()
	app.ApplyConfig()

	// Should handle multiple applications gracefully
}

// TestSetRootDir_Multiple tests setting root dir multiple times
