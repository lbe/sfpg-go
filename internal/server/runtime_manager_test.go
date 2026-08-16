package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
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
	var gotEnv []string
	m.testSeams.Executable = func() (string, error) { return "/test/exe", nil }
	m.testSeams.ExecCommand = func(path string, args []string, env []string) error {
		execCalled = true
		gotPath = path
		gotArgs = args
		gotEnv = env
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
	if !envHasValue(gotEnv, skipStartupDiscoveryEnv, "1") {
		t.Errorf("exec env should contain %s=1, got %v", skipStartupDiscoveryEnv, gotEnv)
	}
}

// envHasValue reports whether env contains an exact key=value entry.
func envHasValue(env []string, key, value string) bool {
	for _, kv := range env {
		if kv == key+"="+value {
			return true
		}
	}
	return false
}

// TestRuntimeManager_ExecRestart_InjectsSkipEnv verifies ExecRestart adds
// SEPG_SKIP_STARTUP_DISCOVERY=1 to the captured exec env slice without leaving
// a stale =0 entry.
func TestRuntimeManager_ExecRestart_InjectsSkipEnv(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())

	var gotEnv []string
	m.testSeams.Executable = func() (string, error) { return "/test/exe", nil }
	m.testSeams.ExecCommand = func(path string, args []string, env []string) error {
		gotEnv = append([]string(nil), env...)
		return nil
	}

	m.ExecRestart()

	if envHasValue(gotEnv, skipStartupDiscoveryEnv, "0") {
		t.Errorf("exec env must not contain %s=0, got %v", skipStartupDiscoveryEnv, gotEnv)
	}
	if !envHasValue(gotEnv, skipStartupDiscoveryEnv, "1") {
		t.Errorf("exec env should contain %s=1, got %v", skipStartupDiscoveryEnv, gotEnv)
	}
}

func TestEnvironWithSkipStartupDiscovery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  []string
	}{
		{"missing adds", []string{"HOME=/root"}},
		{"already zero replaced", []string{skipStartupDiscoveryEnv + "=0", "HOME=/root"}},
		{"already one stays one", []string{skipStartupDiscoveryEnv + "=1", "HOME=/root"}},
		{"zero among others replaced", []string{"A=1", skipStartupDiscoveryEnv + "=0", "B=2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := environWithSkipStartupDiscovery(tt.env)

			countOne := 0
			countZero := 0
			for _, kv := range got {
				if kv == skipStartupDiscoveryEnv+"=1" {
					countOne++
				}
				if kv == skipStartupDiscoveryEnv+"=0" {
					countZero++
				}
			}
			if countOne != 1 {
				t.Errorf("environWithSkipStartupDiscovery(%v) = %v, want exactly one %s=1 (got %d)", tt.env, got, skipStartupDiscoveryEnv, countOne)
			}
			if countZero != 0 {
				t.Errorf("environWithSkipStartupDiscovery(%v) = %v, must not keep %s=0", tt.env, got, skipStartupDiscoveryEnv)
			}

			for _, kv := range tt.env {
				if strings.HasPrefix(kv, skipStartupDiscoveryEnv+"=") {
					continue
				}
				if !envHasValue(got, kv[:strings.IndexByte(kv, '=')], kv[strings.IndexByte(kv, '=')+1:]) {
					t.Errorf("environWithSkipStartupDiscovery(%v) = %v, lost original entry %q", tt.env, got, kv)
				}
			}
		})
	}
}

func TestEnvTruthySkipStartupDiscovery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  []string
		want bool
	}{
		{"missing is false", []string{"A=1"}, false},
		{"one is true", []string{skipStartupDiscoveryEnv + "=1"}, true},
		{"zero is false", []string{skipStartupDiscoveryEnv + "=0"}, false},
		{"other value is false", []string{skipStartupDiscoveryEnv + "=yes"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := envTruthySkipStartupDiscovery(tt.env); got != tt.want {
				t.Errorf("envTruthySkipStartupDiscovery(%v) = %v, want %v", tt.env, got, tt.want)
			}
		})
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
