package config

import (
	"context"
	"strings"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

func TestGetLastKnownGoodDiff_NoConfig(t *testing.T) {
	cfg := DefaultConfig()
	_, err := cfg.GetLastKnownGoodDiff(context.Background(), mockConfigQueries{configs: nil})
	if err == nil {
		t.Fatal("expected error when last known good config is missing")
	}
}

func TestPreviewImport_InvalidYAML(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	_, err := cfg.PreviewImport("listener-port: [")
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "invalid YAML") && !strings.Contains(err.Error(), "invalid YAML syntax") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyImageDirectory_Empty(t *testing.T) {
	imagesDir, normalized, err := ApplyImageDirectory("")
	if err != nil {
		t.Fatalf("expected ApplyImageDirectory to return nil error for empty path, got %v", err)
	}
	if imagesDir != "" || normalized != "" {
		t.Fatal("expected empty image directory outputs for empty input")
	}
}

func TestImportFromYAML(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultConfig()
	saver := &mockSaver{}

	if err := cfg.ImportFromYAML("log-level: info\n", ctx, saver); err != nil {
		t.Fatalf("ImportFromYAML failed: %v", err)
	}

	if err := cfg.ImportFromYAML("session-secret: nope\n", ctx, saver); err == nil {
		t.Fatal("expected ImportFromYAML to fail with session-secret")
	}

	if err := cfg.ImportFromYAML("listener-port: [", ctx, saver); err == nil {
		t.Fatal("expected ImportFromYAML to fail with invalid yaml")
	}
}

func TestPreviewImportAndLastKnownGoodDiff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	preview, err := cfg.PreviewImport("log-level: warn\n")
	if err != nil {
		t.Fatalf("PreviewImport failed: %v", err)
	}
	if !contains(preview.Changes, "log-level") {
		t.Fatalf("expected log-level change, got %v", preview.Changes)
	}

	if _, cfgErr := cfg.PreviewImport("session-secret: nope\n"); cfgErr == nil {
		t.Fatal("expected PreviewImport to fail when session-secret is present")
	}

	lastKnownGoodYAML := "log-level: warn\n"
	mock := mockConfigQueries{configs: []gallerydb.Config{{Key: "LastKnownGoodConfig", Value: lastKnownGoodYAML}}}
	diff, err := cfg.GetLastKnownGoodDiff(context.Background(), mock)
	if err != nil {
		t.Fatalf("GetLastKnownGoodDiff failed: %v", err)
	}
	if !contains(diff.Changes, "log-level") {
		t.Fatalf("expected log-level change in diff, got %v", diff.Changes)
	}
}

func TestRestoreLastKnownGood(t *testing.T) {
	cfg := DefaultConfig()
	yamlContent, err := cfg.ExportToYAML()
	if err != nil {
		t.Fatalf("ExportToYAML failed: %v", err)
	}
	mock := mockConfigQueries{configs: []gallerydb.Config{{Key: "LastKnownGoodConfig", Value: yamlContent}}}

	restored, err := cfg.RestoreLastKnownGood(context.Background(), mock)
	if err != nil {
		t.Fatalf("RestoreLastKnownGood failed: %v", err)
	}
	if restored == nil {
		t.Fatal("expected restored config")
	}
}
