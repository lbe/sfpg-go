package config

import (
	"strings"
	"testing"
	"time"
)

// TestImportedConfigReportsStructuredDurationErrors verifies that duration
// validation during config import produces friendly, structured error messages
// and that valid durations are applied correctly. Duration parsing is
// centralized in applyYAMLConfigToConfig, so both the direct helper and
// BuildImportedConfig must report errors using the same readable field names.
func TestImportedConfigReportsStructuredDurationErrors(t *testing.T) {
	durationFields := []struct {
		yamlKey      string
		friendlyName string
	}{
		{"cache-max-time", "cache max time"},
		{"cache-cleanup-interval", "cache cleanup interval"},
		{"db-optimize-interval", "db optimize interval"},
		{"worker-pool-max-idle-time", "worker pool max idle time"},
		{"db-pool-monitor-interval", "db pool monitor interval"},
	}

	for _, field := range durationFields {
		t.Run("BuildImportedConfig rejects invalid "+field.friendlyName, func(t *testing.T) {
			yaml := field.yamlKey + ": not-a-duration"
			_, err := BuildImportedConfig(DefaultConfig(), yaml)
			if err == nil {
				t.Fatalf("BuildImportedConfig expected error for invalid %s, got nil", field.friendlyName)
			}
			want := "invalid " + field.friendlyName + ":"
			if !strings.Contains(err.Error(), want) {
				t.Errorf("BuildImportedConfig error = %q, want containing %q", err.Error(), want)
			}
		})
	}

	t.Run("applyYAMLValues produces structured errors", func(t *testing.T) {
		raw := map[string]interface{}{
			"cache-max-time": "not-a-duration",
		}
		cfg := DefaultConfig()
		err := applyYAMLValues(cfg, raw)
		if err == nil {
			t.Fatal("applyYAMLValues expected error for invalid cache max time, got nil")
		}
		if !strings.Contains(err.Error(), "yaml key \"cache-max-time\":") {
			t.Errorf("applyYAMLValues error = %q, want containing a cache-max-time error", err.Error())
		}
	})

	t.Run("BuildImportedConfig accepts valid durations", func(t *testing.T) {
		cfg, err := BuildImportedConfig(DefaultConfig(), "cache-max-time: 90m")
		if err != nil {
			t.Fatalf("BuildImportedConfig unexpected error: %v", err)
		}
		if cfg.CacheMaxTime != 90*time.Minute {
			t.Errorf("CacheMaxTime = %v, want %v", cfg.CacheMaxTime, 90*time.Minute)
		}
	})
}
