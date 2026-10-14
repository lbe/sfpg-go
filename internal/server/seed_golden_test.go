package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSeedTestDBIfAbsent(t *testing.T) {
	t.Run("empty dir seeds main and thumbs", func(t *testing.T) {
		root := t.TempDir()
		if err := seedTestDBIfAbsent(root); err != nil {
			t.Fatalf("seed: %v", err)
		}
		mainDB := filepath.Join(root, "DB", "sfpg.db")
		thumbsDB := filepath.Join(root, "DB", "thumbs", "thumbs.db")
		for _, p := range []string{mainDB, thumbsDB} {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("stat %s: %v", p, err)
			}
		}
	})

	t.Run("existing sfpg.db is not overwritten", func(t *testing.T) {
		root := t.TempDir()
		dbDir := filepath.Join(root, "DB")
		if err := os.MkdirAll(dbDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		mainDB := filepath.Join(dbDir, "sfpg.db")
		const sentinel = "sentinel-do-not-overwrite"
		if err := os.WriteFile(mainDB, []byte(sentinel), 0o644); err != nil {
			t.Fatalf("write sentinel: %v", err)
		}
		if err := seedTestDBIfAbsent(root); err != nil {
			t.Fatalf("seed: %v", err)
		}
		b, err := os.ReadFile(mainDB)
		if err != nil {
			t.Fatalf("read sentinel: %v", err)
		}
		if string(b) != sentinel {
			t.Fatalf("sfpg.db was overwritten")
		}
	})
}

func TestSetupDB_AfterTestDBSeed(t *testing.T) {
	root := t.TempDir()
	if err := seedTestDBIfAbsent(root); err != nil {
		t.Fatalf("seed: %v", err)
	}
	infra := NewInfrastructureService()
	infra.rootDir = root
	infra.SetupDB(context.Background(), nil)
	infra.Shutdown()
}
