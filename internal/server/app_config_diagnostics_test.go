package server

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
)

func TestLogLoadedConfigDiagnostics_EmitsGuardrailWarnings(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	original := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(original)
	})

	app := New(getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}, "x.y.z")
	cfg := config.DefaultConfig()

	// Intentionally contradictory/sensitive combinations to trigger guardrails.
	cfg.DBMaxPoolSize = 4
	cfg.DBMinIdleConnections = 9
	cfg.SessionSameSite = "None"
	cfg.SessionSecure = false
	cfg.EnableHTTPCache = true
	cfg.CacheMaxSize = 1024
	cfg.CacheMaxEntrySize = 2048

	app.logLoadedConfigDiagnostics(cfg)

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
