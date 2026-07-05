package server

import (
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
)

// TestProcessRestart_RequestsRestartAndExecs verifies that requestRestart flags
// a process restart and that execRestart calls the injectable execCommand with
// the current executable and arguments.
func TestProcessRestart_RequestsRestartAndExecs(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	var execCalled bool
	var gotPath string
	var gotArgs []string
	app.execCommand = func(path string, args []string, env []string) error {
		execCalled = true
		gotPath = path
		gotArgs = args
		return nil
	}

	app.TriggerRestart()

	if !app.restartRequested.Load() {
		t.Fatal("expected restartRequested to be true after requestRestart")
	}

	// execCommand should not be called by requestRestart; that happens in execRestart.
	if execCalled {
		t.Fatal("execCommand should not be called by requestRestart")
	}

	app.ExecRestart()

	if !execCalled {
		t.Fatal("expected execCommand to be called by execRestart")
	}
	if gotPath == "" {
		t.Error("expected non-empty executable path")
	}
	if len(gotArgs) == 0 {
		t.Error("expected non-empty argument list")
	}
}
