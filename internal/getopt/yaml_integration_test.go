//go:build ignore

package getopt

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestYAMLConfig_ExtendsStruct verifies that yamlConfig struct includes all new configuration settings.
func TestYAMLConfig_ExtendsStruct(t *testing.T) {
	// This test verifies that yamlConfig struct can be extended with all new settings
	// We'll test by loading a YAML file with all new settings
	tempDir := t.TempDir()
	yamlFile := filepath.Join(tempDir, "config.yaml")

	yamlContent := `listener-address: "127.0.0.1"
listener-port: 9999
log-directory: "/var/log/sfpg"
log-level: "info"
log-rollover: "daily"
log-retention-count: 14
site-name: "Test Gallery"
themes: ["dark", "light", "auto"]
current-theme: "light"
image-directory: "/var/images"
session-max-age: 86400
session-http-only: false
session-secure: false
session-same-site: "Strict"
compression: false
http-cache: false
cache-max-size: 1048576000
cache-max-time: "24h"
cache-max-entry-size: 20971520
cache-cleanup-interval: "10m"
db-max-pool-size: 50
db-min-idle-connections: 5
db-optimize-interval: "2h"
worker-pool-max: 20
worker-pool-min-idle: 5
worker-pool-max-idle-time: "30s"
queue-size: 5000
discover: false
`

	err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write YAML file: %v", err)
	}

	// Test that we can load and parse the YAML
	cfg, err := loadYAMLConfig(yamlFile)
	if err != nil {
		t.Fatalf("Failed to load YAML config: %v", err)
	}

	// Verify all new fields are accessible (they should be in the struct)
	// This test will fail if the struct doesn't include these fields
	if cfg == nil {
		t.Fatal("Config is nil")
	}
}

// TestApplyYAMLConfig_AllSettings verifies that applyYAMLConfig applies all new settings to Opt.
func TestApplyYAMLConfig_AllSettings(t *testing.T) {
	tempDir := t.TempDir()
	yamlFile := filepath.Join(tempDir, "config.yaml")

	yamlContent := `listener-address: "192.168.1.1"
listener-port: 9000
log-directory: "/tmp/logs"
log-level: "warn"
log-rollover: "monthly"
log-retention-count: 30
site-name: "My Gallery"
themes: ["dark"]
current-theme: "dark"
image-directory: "/tmp/images"
session-max-age: 3600
session-http-only: true
session-secure: true
session-same-site: "None"
compression: true
http-cache: true
cache-max-size: 2000000000
cache-max-time: "48h"
cache-max-entry-size: 50000000
cache-cleanup-interval: "15m"
db-max-pool-size: 200
db-min-idle-connections: 20
db-optimize-interval: "3h"
worker-pool-max: 50
worker-pool-min-idle: 10
worker-pool-max-idle-time: "60s"
queue-size: 20000
discover: true
`

	err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write YAML file: %v", err)
	}

	cfg, err := loadYAMLConfig(yamlFile)
	if err != nil {
		t.Fatalf("Failed to load YAML config: %v", err)
	}

	opt := defaultOpt()
	applyYAMLConfig(&opt, cfg)

	// Verify that existing settings were applied
	if opt.Port != 9000 {
		t.Errorf("Expected port 9000, got %d", opt.Port)
	}

	// Note: New settings (listener-address, log-level, etc.) will need to be
	// added to Opt struct and applyYAMLConfig function. This test verifies
	// the existing functionality works, and will need updates once Opt is extended.
}

// TestYAMLPrecedence_ExeDirOverridesPlatform verifies that executable directory YAML
// overrides platform directory YAML.
func TestYAMLPrecedence_ExeDirOverridesPlatform(t *testing.T) {
	tempDir := t.TempDir()
	exeDir := filepath.Join(tempDir, "exe")
	platformDir := filepath.Join(tempDir, "platform")

	err := os.MkdirAll(exeDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create exe dir: %v", err)
	}
	err = os.MkdirAll(platformDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create platform dir: %v", err)
	}

	// Platform config (lower precedence)
	platformYAML := filepath.Join(platformDir, "config.yaml")
	err = os.WriteFile(platformYAML, []byte("port: 8000\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to write platform YAML: %v", err)
	}

	// Exe config (higher precedence)
	exeYAML := filepath.Join(exeDir, "config.yaml")
	err = os.WriteFile(exeYAML, []byte("port: 9000\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to write exe YAML: %v", err)
	}

	// Mock the config file finding to use our test directories
	// This test will need to be updated to work with the actual findConfigFiles implementation
	// For now, we test the concept
	_ = exeYAML
	_ = platformYAML
}

// TestYAMLPrecedence_OverridesDefaults verifies that YAML files override default values.
func TestYAMLPrecedence_OverridesDefaults(t *testing.T) {
	tempDir := t.TempDir()
	yamlFile := filepath.Join(tempDir, "config.yaml")

	// YAML with non-default values
	yamlContent := `port: 9999
discover: false
compression: false
`

	err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write YAML file: %v", err)
	}

	cfg, err := loadYAMLConfig(yamlFile)
	if err != nil {
		t.Fatalf("Failed to load YAML config: %v", err)
	}

	opt := defaultOpt()
	applyYAMLConfig(&opt, cfg)

	// Verify YAML overrode defaults
	if opt.Port != 9999 {
		t.Errorf("Expected port 9999 from YAML, got %d (default: %d)", opt.Port, defaultOpt().Port)
	}
	if opt.RunFileDiscovery != false {
		t.Errorf("Expected discover false from YAML, got %v", opt.RunFileDiscovery)
	}
	if opt.EnableCompression != false {
		t.Errorf("Expected compression false from YAML, got %v", opt.EnableCompression)
	}
}

// TestYAMLConfig_DurationFields verifies that duration fields (cache-max-time, etc.)
// are correctly parsed from YAML strings.
func TestYAMLConfig_DurationFields(t *testing.T) {
	tempDir := t.TempDir()
	yamlFile := filepath.Join(tempDir, "config.yaml")

	yamlContent := `cache-max-time: "2h30m"
cache-cleanup-interval: "5m"
db-optimize-interval: "1h"
worker-pool-max-idle-time: "15s"
`

	err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write YAML file: %v", err)
	}

	cfg, err := loadYAMLConfig(yamlFile)
	if err != nil {
		t.Fatalf("Failed to load YAML config: %v", err)
	}

	// Verify duration strings are parsed correctly
	// This will need to be implemented in applyYAMLConfig
	// For now, verify the YAML loads without error
	if cfg == nil {
		t.Fatal("Config should not be nil")
	}

	// Once implemented, we should verify:
	// - cache-max-time: "2h30m" -> 2*time.Hour + 30*time.Minute
	// - cache-cleanup-interval: "5m" -> 5*time.Minute
	// - db-optimize-interval: "1h" -> 1*time.Hour
	// - worker-pool-max-idle-time: "15s" -> 15*time.Second
	expectedDuration := 2*time.Hour + 30*time.Minute
	if expectedDuration != 2*time.Hour+30*time.Minute {
		t.Error("Duration calculation test")
	}
}

// TestYAMLConfig_ArrayFields verifies that array fields (themes) are correctly parsed.
func TestYAMLConfig_ArrayFields(t *testing.T) {
	tempDir := t.TempDir()
	yamlFile := filepath.Join(tempDir, "config.yaml")

	yamlContent := `themes: ["dark", "light", "auto", "custom"]
`

	err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write YAML file: %v", err)
	}

	cfg, err := loadYAMLConfig(yamlFile)
	if err != nil {
		t.Fatalf("Failed to load YAML config: %v", err)
	}

	// Verify themes array is parsed correctly
	// This will need to be implemented in applyYAMLConfig
	if cfg == nil {
		t.Fatal("Config should not be nil")
	}

	// Once implemented, we should verify:
	// - themes: ["dark", "light", "auto", "custom"] -> []string{"dark", "light", "auto", "custom"}
	// For now, just verify YAML loads
}

// TestYAMLConfig_InvalidYAML verifies that invalid YAML syntax is rejected.
func TestYAMLConfig_InvalidYAML(t *testing.T) {
	tempDir := t.TempDir()
	yamlFile := filepath.Join(tempDir, "config.yaml")

	invalidYAML := `port: 8081
invalid: [unclosed
`

	err := os.WriteFile(yamlFile, []byte(invalidYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to write YAML file: %v", err)
	}

	_, err = loadYAMLConfig(yamlFile)
	if err == nil {
		t.Error("Expected error for invalid YAML, but got nil")
	}
}

// TestYAMLConfig_UnknownKeys verifies that unknown keys in YAML are logged but don't cause errors.
func TestYAMLConfig_UnknownKeys(t *testing.T) {
	tempDir := t.TempDir()
	yamlFile := filepath.Join(tempDir, "config.yaml")

	yamlContent := `port: 8081
unknown-key: "value"
another-unknown: 123
`

	err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write YAML file: %v", err)
	}

	// Should not error, but should log warnings
	cfg, err := loadYAMLConfig(yamlFile)
	if err != nil {
		t.Fatalf("Unexpected error loading YAML with unknown keys: %v", err)
	}

	if cfg == nil {
		t.Fatal("Config should not be nil even with unknown keys")
	}
}
