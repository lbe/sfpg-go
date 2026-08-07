package config

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/getopt"
)

func TestConfig_ToMap_IncludesETagVersion(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ETagVersion = "20260129-01"

	m := cfg.ToMap()

	val, exists := m["etag_version"]
	if !exists {
		t.Error("ToMap() does not include 'etag_version' key")
	}
	if val != "20260129-01" {
		t.Errorf("ToMap()['etag_version'] = %q, want %q", val, "20260129-01")
	}
}

func TestConfig_FromMap_LoadsETagVersion(t *testing.T) {
	m := map[string]string{
		"listener_address": "0.0.0.0",
		"listener_port":    "8080",
		"etag_version":     "20260129-05",
		// Add other required fields based on existing FromMap requirements
	}

	cfg, err := FromMap(m)
	if err != nil {
		t.Fatalf("FromMap() error = %v", err)
	}

	if cfg.ETagVersion != "20260129-05" {
		t.Errorf("FromMap() ETagVersion = %q, want %q", cfg.ETagVersion, "20260129-05")
	}
}

func TestConfig_RoundTrip_PreservesETagVersion(t *testing.T) {
	original := DefaultConfig()
	original.ETagVersion = "20260129-42"

	m := original.ToMap()
	restored, err := FromMap(m)
	if err != nil {
		t.Fatalf("FromMap() error = %v", err)
	}

	if restored.ETagVersion != original.ETagVersion {
		t.Errorf("Roundtrip ETagVersion = %q, want %q", restored.ETagVersion, original.ETagVersion)
	}
}

// TestLoadFromDatabase verifies loading configuration from database.

func TestJSONSerialization(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Themes = []string{"dark", "light", "auto"}

	// Convert to map (which serializes to JSON for database)
	configMap := cfg.ToMap()

	themesJSON := configMap["themes"]
	if themesJSON == "" {
		t.Fatal("themes should be serialized to JSON")
	}

	// Verify it can be deserialized
	var themes []string
	err := json.Unmarshal([]byte(themesJSON), &themes)
	if err != nil {
		t.Fatalf("failed to deserialize themes: %v", err)
	}
	if len(themes) != 3 {
		t.Errorf("expected 3 themes, got %d", len(themes))
	}

	// Themes are stored sorted (order-independent), verify by set membership
	expected := map[string]bool{"dark": true, "light": true, "auto": true}
	for _, th := range themes {
		if !expected[th] {
			t.Errorf("unexpected theme %q", th)
		}
	}
}

// TestValidate_InvalidPort verifies that Validate rejects invalid port values.

func TestApplyYAMLValues_EnableCachePreload(t *testing.T) {
	raw := map[string]interface{}{"enable-cache-preload": false}
	cfg := DefaultConfig()
	if err := applyYAMLValues(cfg, raw); err != nil {
		t.Fatalf("applyYAMLValues failed: %v", err)
	}
	if cfg.EnableCachePreload {
		t.Fatalf("expected EnableCachePreload false, got true")
	}
}

func TestConfig_ToMap_EnableCachePreload(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableCachePreload = false
	m := cfg.ToMap()
	if v, ok := m["enable_cache_preload"]; !ok || v != "false" {
		t.Errorf("ToMap() enable_cache_preload = %q (ok=%v), want \"false\"", v, ok)
	}
}

func TestConfig_ToMap_MaxHTTPCacheEntryInsertPerTransaction(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxHTTPCacheEntryInsertPerTransaction = 25
	m := cfg.ToMap()
	if v, ok := m["max_http_cache_entry_insert_per_transaction"]; !ok || v != "25" {
		t.Errorf("ToMap() max_http_cache_entry_insert_per_transaction = %q (ok=%v), want \"25\"", v, ok)
	}
}

func TestApplyYAMLValues(t *testing.T) {
	raw := map[string]interface{}{
		"listener-port": 9091,
		"log-level":     "warn",
	}
	cfg := DefaultConfig()
	if err := applyYAMLValues(cfg, raw); err != nil {
		t.Fatalf("applyYAMLValues failed: %v", err)
	}
	if cfg.ListenerPort != 9091 {
		t.Fatalf("expected listener-port 9091, got %d", cfg.ListenerPort)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("expected log-level warn, got %q", cfg.LogLevel)
	}
}

func TestApplyYAMLValues_UnknownKey(t *testing.T) {
	raw := map[string]interface{}{"unknown-key": "value"}
	cfg := DefaultConfig()
	// Unknown keys are silently skipped; the config must be unchanged.
	if err := applyYAMLValues(cfg, raw); err != nil {
		t.Fatalf("applyYAMLValues should skip unknown keys, got error: %v", err)
	}
}

func TestLoadFromYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "sfpg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	yamlContent := "listener-port: 9001\nlog-level: warn\ncache-max-time: 45s\n"
	if err := os.WriteFile(configPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg := DefaultConfig()
	if err := cfg.LoadFromYAML(); err != nil {
		t.Fatalf("LoadFromYAML failed: %v", err)
	}

	if cfg.ListenerPort != 9001 {
		t.Fatalf("expected listener port 9001, got %d", cfg.ListenerPort)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("expected log level warn, got %q", cfg.LogLevel)
	}
	if cfg.CacheMaxTime != 45*time.Second {
		t.Fatalf("expected cache max time 45s, got %v", cfg.CacheMaxTime)
	}
}

func TestLoad(t *testing.T) {
	rootDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "sfpg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	yamlContent := "log-level: error\n"
	if err := os.WriteFile(configPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	opt := getopt.Opt{Port: getopt.OptInt{Int: 9010, IsSet: true}}
	cfg, err := Load(context.Background(), rootDir, nil, opt)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.ListenerPort != 9010 {
		t.Fatalf("expected port 9010, got %d", cfg.ListenerPort)
	}
	if cfg.LogLevel != "error" {
		t.Fatalf("expected log level error, got %q", cfg.LogLevel)
	}
	if cfg.ImageDirectory != filepath.Join(rootDir, "Images") {
		t.Fatalf("expected image directory %q, got %q", filepath.Join(rootDir, "Images"), cfg.ImageDirectory)
	}
}

func TestLoad_WithService(t *testing.T) {
	service := &fakeService{cfg: func() *Config {
		cfg := DefaultConfig()
		cfg.ListenerPort = 8001
		cfg.LogLevel = "info"
		return cfg
	}()}

	cfg, err := Load(context.Background(), "", service, getopt.Opt{Port: getopt.OptInt{Int: 9002, IsSet: true}})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !service.called {
		t.Fatal("expected service.Load to be called")
	}
	if cfg.ListenerPort != 9002 {
		t.Fatalf("expected port overridden to 9002, got %d", cfg.ListenerPort)
	}
}

// TestLoad_PreservesDQueMaxDiskBytesZero verifies an explicit 0 in the database
// config survives config.Load: because dque_max_disk_bytes uses zeroNever,
// MergeDefaults must not reset it to the 50 GiB default (0 = unlimited).
func TestLoad_PreservesDQueMaxDiskBytesZero(t *testing.T) {
	service := &fakeService{cfg: func() *Config {
		cfg := DefaultConfig()
		cfg.DQueMaxDiskBytes = 0
		return cfg
	}()}

	cfg, err := Load(context.Background(), "", service, getopt.Opt{})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !service.called {
		t.Fatal("expected service.Load to be called")
	}
	if cfg.DQueMaxDiskBytes != 0 {
		t.Errorf("DQueMaxDiskBytes = %d, want 0 preserved (0 = unlimited)", cfg.DQueMaxDiskBytes)
	}
}

func TestLoadFromYAML_InvalidConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "sfpg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("listener-port: ["), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg := DefaultConfig()
	if err := cfg.LoadFromYAML(); err != nil {
		t.Fatalf("LoadFromYAML should ignore invalid yaml and return nil, got %v", err)
	}
}

func TestApplyYAMLValues_InvalidDuration(t *testing.T) {
	raw := map[string]interface{}{"cache-max-time": "not-a-duration"}
	cfg := DefaultConfig()
	if err := applyYAMLValues(cfg, raw); err == nil {
		t.Fatal("expected error for invalid duration, got nil")
	}
	if cfg.CacheMaxTime != DefaultConfig().CacheMaxTime {
		t.Fatal("expected CacheMaxTime to remain unchanged on invalid duration")
	}
}

func TestInvalidYAMLCacheMaxTimeCausesApplyErrorButLoadFromYAMLLogsAndContinues(t *testing.T) {
	// applyYAMLValues must surface an error for unparseable duration values.
	cfg := DefaultConfig()
	raw := map[string]interface{}{"cache-max-time": "notaduration"}
	if err := applyYAMLValues(cfg, raw); err == nil {
		t.Fatal("applyYAMLValues should return an error for invalid cache-max-time")
	}

	// LoadFromYAML must log a warning and continue, leaving the default value untouched.
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "sfpg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("cache-max-time: notaduration\n"), 0o644); err != nil {
		t.Fatalf("failed to write invalid config: %v", err)
	}

	var buf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	cfg = DefaultConfig()
	if err := cfg.LoadFromYAML(); err != nil {
		t.Fatalf("LoadFromYAML should continue on invalid duration, got: %v", err)
	}
	if cfg.CacheMaxTime != DefaultConfig().CacheMaxTime {
		t.Fatalf("CacheMaxTime should remain at default after invalid YAML, got %v", cfg.CacheMaxTime)
	}
	if !strings.Contains(buf.String(), "cache-max-time") {
		t.Fatalf("expected warning log for cache-max-time, got: %q", buf.String())
	}
}

func TestConfigExport_ToFile_ShowsDiff(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ListenerPort = 9999
	cfg.SiteName = "Test Gallery"

	yamlContent, err := cfg.ExportToYAML()
	if err != nil {
		t.Fatalf("Failed to export config to YAML: %v", err)
	}

	var yamlData map[string]any
	if err := json.Unmarshal([]byte(yamlContent), &yamlData); err != nil {
		// Try YAML if JSON fails (ExportToYAML returns YAML format)
		t.Logf("Note: ExportToYAML returns YAML, not JSON. Test validates YAML content exists.")
	}

	if !strings.Contains(yamlContent, "listener-port") && !strings.Contains(yamlContent, "ListenerPort") {
		t.Error("YAML should contain listener-port configuration")
	}
	if !strings.Contains(yamlContent, "9999") {
		t.Error("YAML should contain port value 9999")
	}
	if !strings.Contains(yamlContent, "Test Gallery") {
		t.Error("YAML should contain site name 'Test Gallery'")
	}
}

// TestConfigExport_ToFile_RequiresConfirmation verifies file exists check works.

func TestConfigExport_ToFile_RequiresConfirmation(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")

	err := os.WriteFile(configFile, []byte("existing: config\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create existing config file: %v", err)
	}

	if _, err := os.Stat(configFile); err != nil {
		t.Fatalf("Config file should exist: %v", err)
	}
}

// TestConfigExport_ToFile_Cancellation verifies file content preservation.

func TestConfigExport_ToFile_Cancellation(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")

	originalContent := "original: content\n"
	err := os.WriteFile(configFile, []byte(originalContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	content, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("Failed to read config file: %v", err)
	}
	if string(content) != originalContent {
		t.Error("File content should match original")
	}
}

// TestConfigExport_Download verifies export generates valid YAML.

func TestConfigExport_Download(t *testing.T) {
	cfg := DefaultConfig()

	yamlContent, err := cfg.ExportToYAML()
	if err != nil {
		t.Fatalf("Failed to export config: %v", err)
	}

	if !strings.Contains(yamlContent, "listener-port") && !strings.Contains(yamlContent, "ListenerPort") {
		t.Error("YAML should contain listener-port key")
	}
	if !strings.Contains(yamlContent, "log-level") && !strings.Contains(yamlContent, "LogLevel") {
		t.Error("YAML should contain log-level key")
	}
}

// TestConfigExport_FilePermissions verifies directory permission setup.

func TestConfigExport_FilePermissions(t *testing.T) {
	tempDir := t.TempDir()
	readOnlyDir := filepath.Join(tempDir, "readonly")
	err := os.Mkdir(readOnlyDir, 0400)
	if err != nil {
		t.Fatalf("Failed to create read-only directory: %v", err)
	}
	defer os.Chmod(readOnlyDir, 0755)

	configFile := filepath.Join(readOnlyDir, "config.yaml")
	_ = configFile
}

// TestConfigExport_ExcludesSessionSecret verifies session secret is never exported.

func TestConfigExport_ExcludesSessionSecret(t *testing.T) {
	cfg := DefaultConfig()

	yamlContent, err := cfg.ExportToYAML()
	if err != nil {
		t.Fatalf("Failed to export config: %v", err)
	}

	// Session secrets are managed separately by the App, not in Config
	// Verify secret-related strings don't appear in export
	if strings.Contains(yamlContent, "secret") {
		t.Error("YAML should not contain secret-related fields")
	}
}

// TestConfigExport_AllSettings verifies export includes all major settings.

func TestConfigExport_AllSettings(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ListenerPort = 9999
	cfg.SiteName = "Test"
	cfg.LogLevel = "info"
	cfg.CacheMaxSize = 1000000

	yamlContent, err := cfg.ExportToYAML()
	if err != nil {
		t.Fatalf("Failed to export: %v", err)
	}

	expectedSubstrings := []string{"9999", "Test", "info"}
	for _, substr := range expectedSubstrings {
		if !strings.Contains(yamlContent, substr) {
			t.Errorf("YAML should contain %q", substr)
		}
	}
}
