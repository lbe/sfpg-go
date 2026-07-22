//go:build integration

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dque"
	"github.com/lbe/sfpg-go/internal/getopt"
)

const largeDQueSeedCount = 500

func seedDQueBatchedWrites(t *testing.T, dqueDir string, n int) {
	t.Helper()
	if err := os.MkdirAll(dqueDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dqueDir, err)
	}
	dq, err := dque.NewOrOpen[BatchedWrite]("writebatcher", dqueDir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen: %v", err)
	}
	for i := range n {
		item := BatchedWrite{
			CacheEntry: &cachelite.HTTPCacheEntry{
				Key:  fmt.Sprintf("seed-%d", i),
				Path: fmt.Sprintf("/gallery/%d", i),
			},
		}
		if err := dq.Enqueue(&item); err != nil {
			t.Fatalf("dque.Enqueue(%d): %v", i, err)
		}
	}
	if sz := dq.Size(); sz != n {
		t.Fatalf("seeded dque size = %d, want %d", sz, n)
	}
	if err := dq.Close(); err != nil {
		t.Fatalf("dque.Close: %v", err)
	}
}

func dqueDirForApp(app *App) string {
	return filepath.Join(filepath.Dir(app.dbPaths.Main), filepath.Base(app.dbPaths.Main)+"-dque")
}

func newAppForDQueLifecycleTest(t *testing.T, tempDir string) *App {
	t.Helper()
	opt := getopt.Opt{
		SessionSecret: getopt.OptString{
			String: "test-secret-for-dque-lifecycle-test",
			IsSet:  true,
		},
	}
	app := New(opt, "test")
	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	app.setDB()
	app.setConfigDefaults()
	return app
}

// TestReconfigurePools_LargeDQue_DoesNotBlock verifies pool reconfigure during startup
// does not synchronously drain a large persisted dque backlog (the :8084 hang).
func TestReconfigurePools_LargeDQue_DoesNotBlock(t *testing.T) {
	tempDir := t.TempDir()
	app := newAppForDQueLifecycleTest(t, tempDir)
	defer app.RuntimeManager.cancel()

	if app.writeBatcher != nil {
		if err := app.writeBatcher.Close(); err != nil {
			t.Fatalf("initial writeBatcher.Close: %v", err)
		}
	}
	seedDQueBatchedWrites(t, dqueDirForApp(app), largeDQueSeedCount)

	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.DBMaxPoolSize = 250
	app.ConfigManager.ConfigMu.Unlock()

	start := time.Now()
	if err := app.reconfigurePoolsFromConfig(); err != nil {
		t.Fatalf("reconfigurePoolsFromConfig: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("reconfigurePoolsFromConfig took %v with large dque backlog; want under 5s", elapsed)
	}

	app.StartWriteBatcher(app.RuntimeManager.ctx, true)

	if app.writeBatcher == nil {
		t.Fatal("writeBatcher nil after reconfigure")
	}
	if stats := app.writeBatcher.GetStats(); !stats.DQueEnabled {
		t.Fatal("writeBatcher dque not enabled after reconfigure")
	}
}

// TestShutdown_LargeDQue_DoesNotBlock verifies shutdown does not block on dque drain.
func TestShutdown_LargeDQue_DoesNotBlock(t *testing.T) {
	tempDir := t.TempDir()
	app := newAppForDQueLifecycleTest(t, tempDir)
	defer app.RuntimeManager.cancel()

	if app.writeBatcher != nil {
		if err := app.writeBatcher.Close(); err != nil {
			t.Fatalf("initial writeBatcher.Close: %v", err)
		}
	}
	seedDQueBatchedWrites(t, dqueDirForApp(app), largeDQueSeedCount)

	ctx := app.RuntimeManager.ctx
	wb, err := app.buildWriteBatcher(ctx, 1000, 200*time.Millisecond, true)
	if err != nil {
		t.Fatalf("buildWriteBatcher: %v", err)
	}
	app.writeBatcher = wb

	if pc := app.writeBatcher.PendingCount(); pc != int64(largeDQueSeedCount) {
		t.Fatalf("PendingCount() = %d, want %d recovered dque items", pc, largeDQueSeedCount)
	}

	start := time.Now()
	app.InfrastructureService.Shutdown()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Shutdown took %v with large dque backlog; want under 5s", elapsed)
	}

	dq, err := dque.NewOrOpen[BatchedWrite]("writebatcher", dqueDirForApp(app), 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen after shutdown: %v", err)
	}
	defer dq.Close()
	if sz := dq.Size(); sz == 0 {
		t.Fatal("dque empty after shutdown; overflow must remain on disk")
	}
}
