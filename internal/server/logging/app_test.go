package logging

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/config"
)

func TestSetupBootstrap(t *testing.T) {
	rootDir := t.TempDir()
	sch := scheduler.NewScheduler(0)

	logger, err := SetupBootstrap(rootDir, sch, "x.y.z")
	if err != nil {
		t.Fatalf("SetupBootstrap failed: %v", err)
	}
	defer func() {
		_ = logger.Shutdown()
	}()

	logsDir := filepath.Join(rootDir, "logs")
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		t.Fatalf("expected logs directory to exist at %q", logsDir)
	}
}

func TestSetupBootstrap_Error(t *testing.T) {
	rootDir := t.TempDir()
	filePath := filepath.Join(rootDir, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("test"), 0o644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	if _, err := SetupBootstrap(filePath, nil, "x.y.z"); err == nil {
		t.Fatal("expected SetupBootstrap to fail when rootDir is a file")
	}
}

func TestSetupBootstrap_WithProfiler(t *testing.T) {
	stop, err := profiler.Start(profiler.Config{Mode: "cpu"})
	if err != nil {
		t.Fatalf("failed to start profiler: %v", err)
	}
	defer stop()

	rootDir := t.TempDir()
	logger, err := SetupBootstrap(rootDir, nil, "x.y.z")
	if err != nil {
		t.Fatalf("SetupBootstrap failed: %v", err)
	}
	defer func() {
		_ = logger.Shutdown()
	}()
}

func TestReload(t *testing.T) {
	rootDir := t.TempDir()
	sch := scheduler.NewScheduler(0)
	logger, err := SetupBootstrap(rootDir, sch, "x.y.z")
	if err != nil {
		t.Fatalf("SetupBootstrap failed: %v", err)
	}
	defer func() {
		_ = logger.Shutdown()
	}()

	t.Run("returns error when config is nil", func(t *testing.T) {
		if err := Reload(logger, nil, nil); err == nil {
			t.Fatal("expected error when config is nil")
		}
	})

	t.Run("reloads with valid config", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.LogDirectory = "logs"
		cfg.LogLevel = "info"
		cfg.LogRollover = "daily"
		cfg.LogRetentionCount = 1

		if err := Reload(logger, cfg, nil); err != nil {
			t.Fatalf("Reload failed: %v", err)
		}
	})

	t.Run("returns error for invalid log level", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.LogDirectory = "logs"
		cfg.LogLevel = "nope"

		if err := Reload(logger, cfg, nil); err == nil {
			t.Fatal("expected error for invalid log level")
		}
	})
}
