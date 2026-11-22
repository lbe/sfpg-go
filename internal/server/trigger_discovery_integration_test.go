//go:build integration

package server

import (
	"context"
	"errors"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/database"
)

// dropFolderIndexTable makes the next folder-index rebuild fail for real: the
// rebuild needs file_folder_index, so a missing table is a rebuild failure.
func dropFolderIndexTable(t *testing.T, app *App) {
	t.Helper()
	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("get RW connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)
	if _, err := cpc.Conn.ExecContext(context.Background(), "DROP TABLE file_folder_index"); err != nil {
		t.Fatalf("drop file_folder_index: %v", err)
	}
}

// TestTriggerDiscovery_RunUsesAppContext verifies that the run gets the app's
// context, not the caller's: a canceled caller context does not stop it. The
// dropped table makes the run reach the rebuild, so the error is the rebuild's,
// not the caller's cancellation.
func TestTriggerDiscovery_RunUsesAppContext(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()
	dropFolderIndexTable(t, app)

	callerCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := app.TriggerDiscovery(callerCtx)
	if !errors.Is(err, database.ErrFolderIndexRebuild) {
		t.Fatalf("TriggerDiscovery error = %v, want ErrFolderIndexRebuild", err)
	}
	if app.getCtx().Err() != nil {
		t.Errorf("app context canceled by caller: %v", app.getCtx().Err())
	}
}

// TestTriggerDiscovery_ReturnsRunErrorWithoutRestart verifies that a failed
// run's error reaches the caller, errors.Is still matches the folder-index
// rebuild sentinel, and the failure does not request a restart.
func TestTriggerDiscovery_ReturnsRunErrorWithoutRestart(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()
	dropFolderIndexTable(t, app)

	err := app.TriggerDiscovery(context.Background())
	if !errors.Is(err, database.ErrFolderIndexRebuild) {
		t.Fatalf("TriggerDiscovery error = %v, want ErrFolderIndexRebuild", err)
	}
	if app.IsRestartRequested() {
		t.Error("TriggerDiscovery must not request restart on run failure")
	}
}

// TestTriggerDiscovery_DoesNotRequestRestartWhenFlagOn is the POST
// /server/discovery invariant: TriggerDiscovery itself never auto-restarts.
// Startup restart is the startup goroutine's job, after the automatic run only.
func TestTriggerDiscovery_DoesNotRequestRestartWhenFlagOn(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.ConfigManager.ConfigMu.Lock()
	app.ConfigManager.Config.RestartAfterDiscovery = true
	app.ConfigManager.ConfigMu.Unlock()

	if err := app.TriggerDiscovery(context.Background()); err != nil {
		t.Fatalf("TriggerDiscovery: %v", err)
	}
	if app.IsRestartRequested() {
		t.Error("TriggerDiscovery must not request restart; that is startup-only")
	}
}

// TestTriggerDiscovery_CanceledAppContextReturnsCanceled is the inverse of the
// app-context test: once the app is stopping, a run returns context.Canceled
// even though the caller's context is live.
func TestTriggerDiscovery_CanceledAppContextReturnsCanceled(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()
	app.RuntimeManager.stop()

	err := app.TriggerDiscovery(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("TriggerDiscovery error = %v, want context.Canceled", err)
	}
}
