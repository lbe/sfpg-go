package server

import (
	"testing"

	"github.com/lbe/sfpg-go/internal/server/config"
)

// TestImportPreservesLiveValues_OnlyOverridesYAMLFields verifies that
// importing configuration from YAML preserves all current live values for
// fields absent from the YAML, while applying values explicitly present.
func TestImportPreservesLiveValues_OnlyOverridesYAMLFields(t *testing.T) {
	base := config.DefaultConfig()
	base.SessionHttpOnly = true
	base.SessionSecure = false
	base.ServerCompressionEnable = false
	base.LogLevel = "debug"
	base.SiteName = "Old Site"

	yamlContent := `session-http-only: false
log-level: error
`

	imported, err := config.BuildImportedConfig(base, yamlContent)
	if err != nil {
		t.Fatalf("BuildImportedConfig failed: %v", err)
	}

	// Fields explicitly set in YAML override the live value.
	if imported.SessionHttpOnly {
		t.Errorf("expected YAML session-http-only: false, got true")
	}
	if imported.LogLevel != "error" {
		t.Errorf("expected YAML log-level: error, got %q", imported.LogLevel)
	}

	// Fields absent from YAML preserve their live (base) value.
	if imported.SessionSecure {
		t.Errorf("expected omitted session-secure to preserve live value false, got true")
	}
	if imported.ServerCompressionEnable {
		t.Errorf("expected omitted compression to preserve live value false, got true")
	}
	if imported.SiteName != "Old Site" {
		t.Errorf("expected omitted site-name to preserve live value %q, got %q", "Old Site", imported.SiteName)
	}
}
