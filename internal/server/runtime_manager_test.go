package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRuntimeManager_SetRestartRequired(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())

	if m.RestartRequired() {
		t.Error("RestartRequired should be false initially")
	}

	m.SetRestartRequired(true)
	if !m.RestartRequired() {
		t.Error("RestartRequired should be true after SetRestartRequired(true)")
	}

	m.SetRestartRequired(false)
	if m.RestartRequired() {
		t.Error("RestartRequired should be false after SetRestartRequired(false)")
	}
}

func TestRuntimeManager_RestartRequired_InitialFalse(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())
	if m.RestartRequired() {
		t.Error("RestartRequired should be false initially")
	}
}

func TestRuntimeManager_RestartRequired_DetectsChanges(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())
	m.SetRestartRequired(true)
	if !m.RestartRequired() {
		t.Error("RestartRequired should detect changes")
	}
}

func TestRuntimeManager_RestartRequired_NoChanges(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())
	m.SetRestartRequired(false)
	if m.RestartRequired() {
		t.Error("RestartRequired should be false when no changes detected")
	}
}

func TestRuntimeManager_IsRestartRequested_InitialFalse(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())
	if m.IsRestartRequested() {
		t.Error("IsRestartRequested should be false initially")
	}
}

func TestRuntimeManager_IsRestartRequested_AfterTrigger(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())
	m.TriggerRestart()
	if !m.IsRestartRequested() {
		t.Error("IsRestartRequested should be true after TriggerRestart")
	}
}

func TestRuntimeManager_TriggerRestart_NoServer(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())
	m.TriggerRestart()
	if !m.IsRestartRequested() {
		t.Error("IsRestartRequested should be true even when no server is running")
	}
}

func TestRuntimeManager_ExecRestart_Success(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())

	var execCalled bool
	var gotPath string
	var gotArgs []string
	m.testSeams.Executable = func() (string, error) { return "/test/exe", nil }
	m.testSeams.ExecCommand = func(path string, args []string, env []string) error {
		execCalled = true
		gotPath = path
		gotArgs = args
		return nil
	}

	m.ExecRestart()

	if !execCalled {
		t.Fatal("execCommand should have been called")
	}
	if gotPath != "/test/exe" {
		t.Errorf("execCommand path = %q, want %q", gotPath, "/test/exe")
	}
	if len(gotArgs) == 0 {
		t.Error("execCommand args should not be empty")
	}
}

func TestRuntimeManager_ExecRestart_ExecutableError(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())

	m.testSeams.Executable = func() (string, error) { return "", errors.New("executable error") }

	var exitCode int
	var exitCalled bool
	m.testSeams.Exit = func(code int) {
		exitCalled = true
		exitCode = code
	}

	m.ExecRestart()

	if !exitCalled {
		t.Fatal("exit should have been called")
	}
	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1", exitCode)
	}
}

func TestRuntimeManager_ExecRestart_ExecError(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())

	m.testSeams.Executable = func() (string, error) { return "/test/exe", nil }
	m.testSeams.ExecCommand = func(string, []string, []string) error { return errors.New("exec error") }

	var exitCode int
	var exitCalled bool
	m.testSeams.Exit = func(code int) {
		exitCalled = true
		exitCode = code
	}

	m.ExecRestart()

	if !exitCalled {
		t.Fatal("exit should have been called")
	}
	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1", exitCode)
	}
}

func TestRuntimeManager_GalleryStats_NeverNil(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())
	if gs := m.GalleryStats(); gs == nil {
		t.Fatal("GalleryStats() should never return nil")
	}
}

func TestRuntimeManager_Exit_FallsBackToOsExit(t *testing.T) {
	m := NewRuntimeManager(context.Background())
	// testSeams.Exit is intentionally nil.

	var gotCode int
	var called bool
	old := osExit
	osExit = func(code int) {
		called = true
		gotCode = code
	}
	defer func() { osExit = old }()

	m.exit(7)

	if !called {
		t.Error("osExit should have been called")
	}
	if gotCode != 7 {
		t.Errorf("exit code = %d, want 7", gotCode)
	}
}

// TestRuntimeManager_Serve_DefaultCoverage exercises Serve and shutdownServer by
// cancelling the manager context after the server has started listening.
func TestRuntimeManager_Serve_DefaultCoverage(t *testing.T) {
	m := NewRuntimeManager(context.Background())

	// BeforeListen is invoked before the server starts listening; closing the
	// channel lets the test cancel Serve deterministically instead of sleeping.
	started := make(chan struct{})
	m.testSeams.BeforeListen = func() { close(started) }

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.Serve(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "127.0.0.1:0")
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server to start listening")
	}

	m.cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Serve to return")
	}
}
