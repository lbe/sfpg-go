package server

import (
	"context"
	"testing"
)

// TestApp_TriggerRestart_DelegatesToRuntimeManager verifies that
// App.TriggerRestart sets the restart requested flag on the RuntimeManager.
func TestApp_TriggerRestart_DelegatesToRuntimeManager(t *testing.T) {
	app := &App{
		RuntimeManager: NewRuntimeManager(context.Background()),
	}

	if app.IsRestartRequested() {
		t.Fatal("IsRestartRequested should be false initially")
	}

	app.TriggerRestart()

	if !app.IsRestartRequested() {
		t.Error("IsRestartRequested should be true after TriggerRestart")
	}
}

// TestApp_ExecRestart_DelegatesToRuntimeManager verifies that
// App.ExecRestart delegates to RuntimeManager.ExecRestart using its test seams.
func TestApp_ExecRestart_DelegatesToRuntimeManager(t *testing.T) {
	app := &App{
		RuntimeManager: NewRuntimeManager(context.Background()),
	}

	var execCalled bool
	var gotPath string
	app.RuntimeManager.testSeams.Executable = func() (string, error) { return "/test/exe", nil }
	app.RuntimeManager.testSeams.ExecCommand = func(path string, args []string, env []string) error {
		execCalled = true
		gotPath = path
		return nil
	}
	app.RuntimeManager.testSeams.Exit = func(code int) {}

	app.ExecRestart()

	if !execCalled {
		t.Fatal("ExecCommand was not invoked")
	}
	if gotPath != "/test/exe" {
		t.Errorf("ExecCommand path = %q, want %q", gotPath, "/test/exe")
	}
}
