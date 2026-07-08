package config

import (
	"context"
	"strings"
	"testing"
)

// TestImportFromYAML_ErrorPaths covers validation and persistence failures.
func TestImportFromYAML_ErrorPaths(t *testing.T) {
	t.Run("validation failure", func(t *testing.T) {
		cfg := DefaultConfig()
		saver := &mockSaver{}

		yaml := "db-max-pool-size: 5\ndb-min-idle-connections: 10"
		err := cfg.ImportFromYAML(yaml, context.Background(), saver)
		if err == nil {
			t.Fatal("ImportFromYAML expected error, got nil")
		}
		if !strings.Contains(err.Error(), "imported config is invalid") {
			t.Errorf("error = %q, want containing 'imported config is invalid'", err.Error())
		}
		if cfg.DBMaxPoolSize != 5 || cfg.DBMinIdleConnections != 10 {
			t.Errorf("receiver not updated: DBMaxPoolSize=%d, DBMinIdleConnections=%d", cfg.DBMaxPoolSize, cfg.DBMinIdleConnections)
		}
	})

	t.Run("save failure", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ListenerPort = 9090
		saver := &mockSaver{failKey: "listener_port"}

		yaml := "listener-port: 9090"
		err := cfg.ImportFromYAML(yaml, context.Background(), saver)
		if err == nil {
			t.Fatal("ImportFromYAML expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to save imported config to database") {
			t.Errorf("error = %q, want containing 'failed to save imported config to database'", err.Error())
		}
		if cfg.ListenerPort != 9090 {
			t.Errorf("receiver not updated: ListenerPort=%d, want 9090", cfg.ListenerPort)
		}
	})
}

// TestPreviewImport_ErrorPaths covers invalid YAML, session-secret rejection,
// and applyYAMLValues failures.
func TestPreviewImport_ErrorPaths(t *testing.T) {
	base := DefaultConfig()

	t.Run("invalid YAML syntax", func(t *testing.T) {
		_, err := base.PreviewImport("invalid: yaml: content: [")
		if err == nil {
			t.Fatal("PreviewImport expected error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid YAML syntax") {
			t.Errorf("error = %q, want containing 'invalid YAML syntax'", err.Error())
		}
	})

	t.Run("session-secret rejected", func(t *testing.T) {
		_, err := base.PreviewImport("session-secret: super-secret")
		if err == nil {
			t.Fatal("PreviewImport expected error, got nil")
		}
		if !strings.Contains(err.Error(), "session-secret cannot be imported") {
			t.Errorf("error = %q, want containing 'session-secret cannot be imported'", err.Error())
		}
	})

	t.Run("applyYAMLValues failure invalid duration", func(t *testing.T) {
		_, err := base.PreviewImport("cache-max-time: not-a-duration")
		if err == nil {
			t.Fatal("PreviewImport expected error, got nil")
		}
		if !strings.Contains(err.Error(), "applying preview YAML values") {
			t.Errorf("error = %q, want containing 'applying preview YAML values'", err.Error())
		}
	})
}

// TestPreviewImport_DoesNotMutateReceiver confirms that PreviewImport leaves the
// receiver config unchanged even when given valid YAML.
func TestPreviewImport_DoesNotMutateReceiver(t *testing.T) {
	base := DefaultConfig()
	base.ListenerPort = 8081

	diff, err := base.PreviewImport("listener-port: 9999")
	if err != nil {
		t.Fatalf("PreviewImport unexpected error: %v", err)
	}
	if base.ListenerPort != 8081 {
		t.Errorf("receiver mutated: ListenerPort=%d, want 8081", base.ListenerPort)
	}
	if diff == nil {
		t.Fatal("PreviewImport returned nil diff")
	}
	if !strings.Contains(diff.NewYAML, "9999") {
		t.Errorf("NewYAML = %q, want containing '9999'", diff.NewYAML)
	}
}

// TestImportFromYAML_UpdatesReceiver confirms that ImportFromYAML mutates the
// receiver to the imported values before validation and persistence.
func TestImportFromYAML_UpdatesReceiver(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SiteName = "Original"
	saver := &mockSaver{}

	yaml := "site-name: Updated"
	if err := cfg.ImportFromYAML(yaml, context.Background(), saver); err != nil {
		t.Fatalf("ImportFromYAML unexpected error: %v", err)
	}
	if cfg.SiteName != "Updated" {
		t.Errorf("SiteName = %q, want 'Updated'", cfg.SiteName)
	}
}
