package server

import (
	"context"
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/log"
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

// TestApp_ExecRestart_PassesActiveLogFile verifies App.ExecRestart injects the
// current logger path into the exec environment.
func TestApp_ExecRestart_PassesActiveLogFile(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret-with-at-least-32-bytes-long", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()

	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()
	wantLog := app.logger.FilePath()

	var gotEnv []string
	app.RuntimeManager.testSeams.Executable = func() (string, error) { return "/test/exe", nil }
	app.RuntimeManager.testSeams.ExecCommand = func(path string, args []string, env []string) error {
		gotEnv = append([]string(nil), env...)
		return nil
	}
	app.RuntimeManager.testSeams.Exit = func(code int) {}

	app.ExecRestart()

	if !envHasValue(gotEnv, log.ContinueLogFileEnv, wantLog) {
		t.Errorf("exec env should contain %s=%s, got %v", log.ContinueLogFileEnv, wantLog, gotEnv)
	}
}
