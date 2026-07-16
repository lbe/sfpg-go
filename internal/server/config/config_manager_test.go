package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
)

// TestConfigManager_LogLoadedConfigDiagnostics_EmitsGuardrailWarnings verifies
// that logLoadedConfigDiagnostics emits validation and guardrail warnings for
// contradictory configuration values. It tests the ConfigManager method directly
// without creating a full App.
func TestConfigManager_LogLoadedConfigDiagnostics_EmitsGuardrailWarnings(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	original := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(original)
	})

	cm := NewConfigManager()
	cfg := DefaultConfig()

	// Intentionally contradictory/sensitive combinations to trigger guardrails.
	cfg.DBMaxPoolSize = 4
	cfg.DBMinIdleConnections = 9
	cfg.SessionSameSite = "None"
	cfg.SessionSecure = false
	cfg.EnableHTTPCache = true
	cfg.CacheMaxSize = 1024
	cfg.CacheMaxEntrySize = 2048

	cm.LogLoadedConfigDiagnostics(cfg)

	logs := logBuf.String()
	if !strings.Contains(logs, "loaded configuration failed strict validation") {
		t.Fatalf("expected strict validation warning log, got: %s", logs)
	}
	if !strings.Contains(logs, "configuration guardrail warning") {
		t.Fatalf("expected guardrail warning logs, got: %s", logs)
	}
	if !strings.Contains(logs, "db_min_idle_gt_db_max_pool") {
		t.Fatalf("expected DB pool guardrail check log, got: %s", logs)
	}
	if !strings.Contains(logs, "session_samesite_none_without_secure") {
		t.Fatalf("expected session guardrail check log, got: %s", logs)
	}
	if !strings.Contains(logs, "cache_entry_size_exceeds_cache_size") {
		t.Fatalf("expected cache guardrail check log, got: %s", logs)
	}
	if !strings.Contains(logs, "hint") {
		t.Fatalf("expected remediation hints in warning logs, got: %s", logs)
	}
}

// TestConfigManager_ConfigServiceRoundTrip verifies SetConfigService stores the
// service and GetConfig/SetConfig round-trip the config pointer under the mutex.
func TestConfigManager_ConfigServiceRoundTrip(t *testing.T) {
	cm := NewConfigManager()

	if cm.GetConfig() != nil {
		t.Fatalf("GetConfig should return nil initially, got %+v", cm.GetConfig())
	}

	cfg := DefaultConfig()
	cfg.SiteName = "round-trip-test"
	cm.SetConfig(cfg)

	got := cm.GetConfig()
	if got != cfg {
		t.Fatalf("GetConfig did not return the same pointer: got %p want %p", got, cfg)
	}
	if got.SiteName != "round-trip-test" {
		t.Errorf("SiteName = %q, want %q", got.SiteName, "round-trip-test")
	}

	cm.SetConfig(nil)
	if cm.GetConfig() != nil {
		t.Fatalf("GetConfig should return nil after SetConfig(nil), got %+v", cm.GetConfig())
	}

	// SetConfigService should store the service pointer for later use.
	var svc ConfigService
	cm.SetConfigService(svc)
	if cm.ConfigService != svc {
		t.Fatal("SetConfigService did not store the service")
	}
}

// TestConfigManager_GetETagVersion verifies GetETagVersion falls back to the
// default when no config is loaded and returns the configured value otherwise.
func TestConfigManager_GetETagVersion(t *testing.T) {
	cm := NewConfigManager()

	wantDefault := DefaultConfig().ETagVersion
	if got := cm.GetETagVersion(); got != wantDefault {
		t.Errorf("GetETagVersion() with nil config = %q, want default %q", got, wantDefault)
	}

	cfg := DefaultConfig()
	cfg.ETagVersion = "custom-etag-123"
	cm.SetConfig(cfg)

	if got := cm.GetETagVersion(); got != "custom-etag-123" {
		t.Errorf("GetETagVersion() = %q, want %q", got, "custom-etag-123")
	}

	// Empty string on a non-nil config also falls back to the default.
	cm.Config.ETagVersion = ""
	if got := cm.GetETagVersion(); got != wantDefault {
		t.Errorf("GetETagVersion() with empty string = %q, want default %q", got, wantDefault)
	}
}

// TestConfigManager_UpdateConfigWithPrecedence verifies that the config pointer
// is stored and CLI values override fields that are not listed as changed.
func TestConfigManager_UpdateConfigWithPrecedence(t *testing.T) {
	cm := NewConfigManager()

	cfg := DefaultConfig()
	cfg.ListenerPort = 8081

	opt := getopt.Opt{
		Port: getopt.OptInt{Int: 8083, IsSet: true},
	}

	// listener_port is not in changedFields, so the CLI value should win.
	cm.UpdateConfigWithPrecedence(cfg, []string{"site_name"}, opt)

	got := cm.GetConfig()
	if got == nil {
		t.Fatal("expected config to be set")
	}
	if got.ListenerPort != 8083 {
		t.Errorf("ListenerPort = %d, want %d (CLI override)", got.ListenerPort, 8083)
	}

	// listener_port is changed by the user, so the config value should win.
	cfg2 := DefaultConfig()
	cfg2.ListenerPort = 8084
	cm.UpdateConfigWithPrecedence(cfg2, []string{"listener_port"}, opt)

	got2 := cm.GetConfig()
	if got2.ListenerPort != 8084 {
		t.Errorf("ListenerPort = %d, want %d (user change preserved)", got2.ListenerPort, 8084)
	}
}
