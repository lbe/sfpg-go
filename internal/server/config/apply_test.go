package config

import (
	"context"
	"errors"
	"slices"
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
		raw := map[string]any{
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

func TestApplyValidationError(t *testing.T) {
	sentinel := errors.New("port invalid")
	err := &ApplyValidationError{err: sentinel}

	if got := err.Error(); got != sentinel.Error() {
		t.Errorf("Error() = %q, want %q", got, sentinel.Error())
	}
	if !errors.Is(err, sentinel) {
		t.Error("errors.Is(err, sentinel) = false, want true")
	}

	var nilErr *ApplyValidationError
	if got := nilErr.Error(); got != "configuration validation failed" {
		t.Errorf("nil Error() = %q, want %q", got, "configuration validation failed")
	}
	if got := nilErr.Unwrap(); got != nil {
		t.Errorf("nil Unwrap() = %v, want nil", got)
	}
	if errors.Is(nilErr, sentinel) {
		t.Error("errors.Is(nilErr, sentinel) = true, want false")
	}
}

func TestApplyPersistenceError(t *testing.T) {
	sentinel := errors.New("db down")
	err := &ApplyPersistenceError{err: sentinel}

	if got := err.Error(); got != sentinel.Error() {
		t.Errorf("Error() = %q, want %q", got, sentinel.Error())
	}
	if !errors.Is(err, sentinel) {
		t.Error("errors.Is(err, sentinel) = false, want true")
	}

	var nilErr *ApplyPersistenceError
	if got := nilErr.Error(); got != "failed to persist configuration" {
		t.Errorf("nil Error() = %q, want %q", got, "failed to persist configuration")
	}
	if got := nilErr.Unwrap(); got != nil {
		t.Errorf("nil Unwrap() = %v, want nil", got)
	}
	if errors.Is(nilErr, sentinel) {
		t.Error("errors.Is(nilErr, sentinel) = true, want false")
	}
}

func TestApplyConfig(t *testing.T) {
	ctx := context.Background()
	current := DefaultConfig()
	current.ListenerPort = 8081

	t.Run("happy path with restart required", func(t *testing.T) {
		svc := &fakeService{}
		candidate := DefaultConfig()
		candidate.ListenerPort = 9090

		result, err := ApplyConfig(ctx, svc, current, candidate)
		if err != nil {
			t.Fatalf("ApplyConfig unexpected error: %v", err)
		}
		if result.Config.ListenerPort != candidate.ListenerPort {
			t.Errorf("Config.ListenerPort = %d, want %d", result.Config.ListenerPort, candidate.ListenerPort)
		}
		if !result.RestartRequired {
			t.Error("RestartRequired = false, want true")
		}
		if !slices.Contains(result.RestartRequiredKeys, "listener_port") {
			t.Errorf("RestartRequiredKeys = %v, want containing %q", result.RestartRequiredKeys, "listener_port")
		}
	})

	t.Run("no restart required", func(t *testing.T) {
		svc := &fakeService{}
		candidate := DefaultConfig()
		candidate.SiteName = "Changed Site Name"

		result, err := ApplyConfig(ctx, svc, current, candidate)
		if err != nil {
			t.Fatalf("ApplyConfig unexpected error: %v", err)
		}
		if result.RestartRequired {
			t.Error("RestartRequired = true, want false")
		}
		if len(result.RestartRequiredKeys) != 0 {
			t.Errorf("RestartRequiredKeys = %v, want empty", result.RestartRequiredKeys)
		}
	})

	t.Run("validation error", func(t *testing.T) {
		sentinel := errors.New("invalid")
		svc := &fakeService{validateErr: sentinel}
		candidate := DefaultConfig()

		_, err := ApplyConfig(ctx, svc, current, candidate)
		if err == nil {
			t.Fatal("ApplyConfig expected error, got nil")
		}
		if _, ok := errors.AsType[*ApplyValidationError](err); !ok {
			t.Errorf("err type = %T, want *ApplyValidationError", err)
		}
		if !errors.Is(err, sentinel) {
			t.Error("errors.Is(err, sentinel) = false, want true")
		}
	})

	t.Run("persistence error", func(t *testing.T) {
		sentinel := errors.New("db down")
		svc := &fakeService{saveErr: sentinel}
		candidate := DefaultConfig()

		_, err := ApplyConfig(ctx, svc, current, candidate)
		if err == nil {
			t.Fatal("ApplyConfig expected error, got nil")
		}
		if _, ok := errors.AsType[*ApplyPersistenceError](err); !ok {
			t.Errorf("err type = %T, want *ApplyPersistenceError", err)
		}
		if !errors.Is(err, sentinel) {
			t.Error("errors.Is(err, sentinel) = false, want true")
		}
	})

	t.Run("nil service", func(t *testing.T) {
		candidate := DefaultConfig()
		_, err := ApplyConfig(ctx, nil, current, candidate)
		if err == nil {
			t.Error("ApplyConfig(nil service) expected error, got nil")
		}
	})

	t.Run("nil current", func(t *testing.T) {
		svc := &fakeService{}
		candidate := DefaultConfig()
		_, err := ApplyConfig(ctx, svc, nil, candidate)
		if err == nil {
			t.Error("ApplyConfig(nil current) expected error, got nil")
		}
	})

	t.Run("nil candidate", func(t *testing.T) {
		svc := &fakeService{}
		_, err := ApplyConfig(ctx, svc, current, nil)
		if err == nil {
			t.Error("ApplyConfig(nil candidate) expected error, got nil")
		}
	})
}

// TestImportPreservesLiveValues_OnlyOverridesYAMLFields verifies that
// importing configuration from YAML preserves all current live values for
// fields absent from the YAML, while applying values explicitly present.
func TestImportPreservesLiveValues_OnlyOverridesYAMLFields(t *testing.T) {
	base := DefaultConfig()
	base.SessionHttpOnly = true
	base.SessionSecure = false
	base.LogLevel = "debug"
	base.SiteName = "Old Site"

	yamlContent := `session-http-only: false
log-level: error
`

	imported, err := BuildImportedConfig(base, yamlContent)
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
	if imported.SiteName != "Old Site" {
		t.Errorf("expected omitted site-name to preserve live value %q, got %q", "Old Site", imported.SiteName)
	}
}

// TestNormalizePath covers path normalization edge cases.
func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"whitespace only", "   ", ""},
		{"clean path", "foo/bar/../baz", "foo/baz"},
		{"trim and clean", "  /tmp/../var/log  ", "/var/log"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizePath(tt.input)
			if got != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
