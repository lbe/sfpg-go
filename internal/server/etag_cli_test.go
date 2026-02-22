package server

import (
	"context"
	"regexp"
	"testing"

	"go.local/sfpg/internal/getopt"
)

func TestApp_IncrementETag(t *testing.T) {
	app := CreateApp(t, false)
	ctx := context.Background()

	// Get current ETag version
	cfg, err := app.configService.Load(ctx)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	originalETag := cfg.ETagVersion
	if originalETag == "" {
		t.Fatal("Original ETag is empty")
	}

	// Call IncrementETag method
	newETag, err := app.IncrementETag()
	if err != nil {
		t.Fatalf("IncrementETag() error = %v", err)
	}

	// Verify format
	pattern := `^\d{8}-\d{2}$`
	matched, _ := regexp.MatchString(pattern, newETag)
	if !matched {
		t.Errorf("New ETag %q does not match pattern %q", newETag, pattern)
	}

	// Verify it was incremented
	if newETag == originalETag {
		t.Error("ETag was not incremented")
	}

	// Verify it was saved to database
	reloaded, err := app.configService.Load(ctx)
	if err != nil {
		t.Fatalf("Reload config: %v", err)
	}

	if reloaded.ETagVersion != newETag {
		t.Errorf("Saved ETag = %q, want %q", reloaded.ETagVersion, newETag)
	}
}

func TestApp_InitForIncrementETag(t *testing.T) {
	// Create app with minimal initialization
	app := &App{
		ctx: context.Background(),
	}

	opt := getopt.Opt{}
	err := app.InitForIncrementETag(opt)
	if err != nil {
		t.Fatalf("InitForIncrementETag() error = %v", err)
	}

	// Verify essential services are initialized
	if app.dbRwPool == nil {
		t.Error("dbRwPool not initialized")
	}
	if app.configService == nil {
		t.Error("ConfigService not initialized")
	}

	// Verify can load config
	cfg, err := app.configService.Load(app.ctx)
	if err != nil {
		t.Fatalf("Load config after init: %v", err)
	}
	if cfg.ETagVersion == "" {
		t.Error("ETagVersion is empty after init")
	}
}
