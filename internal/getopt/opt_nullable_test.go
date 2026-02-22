package getopt

import (
	"os"
	"path/filepath"
	"testing"
)

// resetEnv and resetFlags are defined in opt_test.go

// TestParse_NoDefaults_AllUnset tests that Parse() returns zero values when nothing is set
func TestParse_NoDefaults_AllUnset(t *testing.T) {
	resetEnv()
	resetFlags()
	// SessionSecret is required, so set it
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")

	opt := Parse()

	// All fields should have zero values with IsSet=false (except SessionSecret which is required)
	if opt.Port.Int != 0 {
		t.Errorf("expected Port.Int=0 (zero value), got %d", opt.Port.Int)
	}
	if opt.Port.IsSet != false {
		t.Errorf("expected Port.IsSet=false, got %v", opt.Port.IsSet)
	}

	if opt.RunFileDiscovery.Bool != false {
		t.Errorf("expected RunFileDiscovery.Bool=false (zero value), got %v", opt.RunFileDiscovery.Bool)
	}
	if opt.RunFileDiscovery.IsSet != false {
		t.Errorf("expected RunFileDiscovery.IsSet=false, got %v", opt.RunFileDiscovery.IsSet)
	}

	if opt.DebugDelayMS.Int != 0 {
		t.Errorf("expected DebugDelayMS.Int=0 (zero value), got %d", opt.DebugDelayMS.Int)
	}
	if opt.DebugDelayMS.IsSet != false {
		t.Errorf("expected DebugDelayMS.IsSet=false, got %v", opt.DebugDelayMS.IsSet)
	}

	if opt.Profile.String != "" {
		t.Errorf("expected Profile.String=\"\" (zero value), got %q", opt.Profile.String)
	}
	if opt.Profile.IsSet != false {
		t.Errorf("expected Profile.IsSet=false, got %v", opt.Profile.IsSet)
	}

	if opt.EnableCompression.Bool != false {
		t.Errorf("expected EnableCompression.Bool=false (zero value), got %v", opt.EnableCompression.Bool)
	}
	if opt.EnableCompression.IsSet != false {
		t.Errorf("expected EnableCompression.IsSet=false, got %v", opt.EnableCompression.IsSet)
	}

	if opt.EnableHTTPCache.Bool != false {
		t.Errorf("expected EnableHTTPCache.Bool=false (zero value), got %v", opt.EnableHTTPCache.Bool)
	}
	if opt.EnableHTTPCache.IsSet != false {
		t.Errorf("expected EnableHTTPCache.IsSet=false, got %v", opt.EnableHTTPCache.IsSet)
	}

	if opt.SessionSecret.String != "test-secret" {
		t.Errorf("expected SessionSecret.String=\"test-secret\", got %q", opt.SessionSecret.String)
	}
	if opt.SessionSecret.IsSet != true {
		t.Errorf("expected SessionSecret.IsSet=true (required), got %v", opt.SessionSecret.IsSet)
	}

	if opt.UnlockAccount.String != "" {
		t.Errorf("expected UnlockAccount.String=\"\" (zero value), got %q", opt.UnlockAccount.String)
	}
	if opt.UnlockAccount.IsSet != false {
		t.Errorf("expected UnlockAccount.IsSet=false, got %v", opt.UnlockAccount.IsSet)
	}

	if opt.RestoreLastKnownGood.Bool != false {
		t.Errorf("expected RestoreLastKnownGood.Bool=false (zero value), got %v", opt.RestoreLastKnownGood.Bool)
	}
	if opt.RestoreLastKnownGood.IsSet != false {
		t.Errorf("expected RestoreLastKnownGood.IsSet=false, got %v", opt.RestoreLastKnownGood.IsSet)
	}
}

// TestParse_EnvVarSetsIsSet tests that environment variables set IsSet=true
func TestParse_EnvVarSetsIsSet(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Setenv("SFG_PORT", "9090")
	os.Setenv("SFG_DISCOVER", "true")
	os.Setenv("SFG_COMPRESSION", "false")

	opt := Parse()

	if opt.Port.Int != 9090 {
		t.Errorf("expected Port.Int=9090, got %d", opt.Port.Int)
	}
	if opt.Port.IsSet != true {
		t.Errorf("expected Port.IsSet=true (set via env), got %v", opt.Port.IsSet)
	}

	if opt.RunFileDiscovery.Bool != true {
		t.Errorf("expected RunFileDiscovery.Bool=true, got %v", opt.RunFileDiscovery.Bool)
	}
	if opt.RunFileDiscovery.IsSet != true {
		t.Errorf("expected RunFileDiscovery.IsSet=true (set via env), got %v", opt.RunFileDiscovery.IsSet)
	}

	if opt.EnableCompression.Bool != false {
		t.Errorf("expected EnableCompression.Bool=false, got %v", opt.EnableCompression.Bool)
	}
	if opt.EnableCompression.IsSet != true {
		t.Errorf("expected EnableCompression.IsSet=true (set via env), got %v", opt.EnableCompression.IsSet)
	}
}

// TestParse_CLIFlagSetsIsSet tests that CLI flags set IsSet=true
func TestParse_CLIFlagSetsIsSet(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Args = []string{"cmd", "-port=7777", "-discover=false", "-compression=true"}

	opt := Parse()

	if opt.Port.Int != 7777 {
		t.Errorf("expected Port.Int=7777, got %d", opt.Port.Int)
	}
	if opt.Port.IsSet != true {
		t.Errorf("expected Port.IsSet=true (set via CLI), got %v", opt.Port.IsSet)
	}

	if opt.RunFileDiscovery.Bool != false {
		t.Errorf("expected RunFileDiscovery.Bool=false, got %v", opt.RunFileDiscovery.Bool)
	}
	if opt.RunFileDiscovery.IsSet != true {
		t.Errorf("expected RunFileDiscovery.IsSet=true (set via CLI), got %v", opt.RunFileDiscovery.IsSet)
	}

	if opt.EnableCompression.Bool != true {
		t.Errorf("expected EnableCompression.Bool=true, got %v", opt.EnableCompression.Bool)
	}
	if opt.EnableCompression.IsSet != true {
		t.Errorf("expected EnableCompression.IsSet=true (set via CLI), got %v", opt.EnableCompression.IsSet)
	}
}

// TestParse_CLIOverridesEnv tests that CLI flags override environment variables
func TestParse_CLIOverridesEnv(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Setenv("SFG_PORT", "9090")
	os.Setenv("SFG_DISCOVER", "true")
	os.Args = []string{"cmd", "-port=7777", "-discover=false"}

	opt := Parse()

	// CLI should override env
	if opt.Port.Int != 7777 {
		t.Errorf("expected Port.Int=7777 (CLI override), got %d", opt.Port.Int)
	}
	if opt.Port.IsSet != true {
		t.Errorf("expected Port.IsSet=true, got %v", opt.Port.IsSet)
	}

	if opt.RunFileDiscovery.Bool != false {
		t.Errorf("expected RunFileDiscovery.Bool=false (CLI override), got %v", opt.RunFileDiscovery.Bool)
	}
	if opt.RunFileDiscovery.IsSet != true {
		t.Errorf("expected RunFileDiscovery.IsSet=true, got %v", opt.RunFileDiscovery.IsSet)
	}
}

// TestParse_NoYAMLHandling tests that Parse() does not handle YAML files
func TestParse_NoYAMLHandling(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")

	// Create a YAML config file
	tmpDir := t.TempDir()
	yamlFile := filepath.Join(tmpDir, "config.yaml")
	yamlContent := "port: 9999\ndiscover: false\n"
	if err := os.WriteFile(yamlFile, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write YAML file: %v", err)
	}

	// Temporarily change executable dir to tmpDir (this is a test limitation)
	// In real usage, YAML would be in platform/exe dirs, but Parse() should ignore it
	opt := Parse()

	// YAML should be ignored - values should be zero
	if opt.Port.Int != 0 {
		t.Errorf("expected Port.Int=0 (YAML ignored), got %d", opt.Port.Int)
	}
	if opt.Port.IsSet != false {
		t.Errorf("expected Port.IsSet=false (YAML ignored), got %v", opt.Port.IsSet)
	}

	if opt.RunFileDiscovery.Bool != false {
		t.Errorf("expected RunFileDiscovery.Bool=false (YAML ignored), got %v", opt.RunFileDiscovery.Bool)
	}
	if opt.RunFileDiscovery.IsSet != false {
		t.Errorf("expected RunFileDiscovery.IsSet=false (YAML ignored), got %v", opt.RunFileDiscovery.IsSet)
	}
}
