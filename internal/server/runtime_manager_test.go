package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
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

func TestRuntimeManager_TriggerRestart_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewRuntimeManager(ctx)
	addr := getEphemeralAddr(t)
	done := make(chan error, 1)
	go func() {
		done <- m.Serve(http.NewServeMux(), addr)
	}()

	// Wait for server to be ready.
	if err := waitForServerReady(t, addr); err != nil {
		t.Fatalf("server did not become ready: %v", err)
	}

	m.TriggerRestart()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after TriggerRestart")
	}

	if !m.IsRestartRequested() {
		t.Error("IsRestartRequested should be true after TriggerRestart")
	}
}

func TestRuntimeManager_TriggerRestart_Idempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewRuntimeManager(ctx)
	addr := getEphemeralAddr(t)
	done := make(chan error, 1)
	go func() {
		done <- m.Serve(http.NewServeMux(), addr)
	}()

	if err := waitForServerReady(t, addr); err != nil {
		t.Fatalf("server did not become ready: %v", err)
	}

	m.TriggerRestart()
	m.TriggerRestart() // second call should not panic

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after TriggerRestart")
	}

	if !m.IsRestartRequested() {
		t.Error("IsRestartRequested should be true after TriggerRestart")
	}
}

func TestRuntimeManager_ExecRestart_Success(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())

	var execCalled bool
	var gotPath string
	var gotArgs []string
	m.testHookExecutable = func() (string, error) { return "/test/exe", nil }
	m.testHookExecCommand = func(path string, args []string, env []string) error {
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

	m.testHookExecutable = func() (string, error) { return "", errors.New("executable error") }

	var exitCode int
	var exitCalled bool
	m.testHookExit = func(code int) {
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

	m.testHookExecutable = func() (string, error) { return "/test/exe", nil }
	m.testHookExecCommand = func(string, []string, []string) error { return errors.New("exec error") }

	var exitCode int
	var exitCalled bool
	m.testHookExit = func(code int) {
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

func TestRuntimeManager_GetGalleryStatsCached_Missing(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())
	if got := m.GetGalleryStatsCached(123); got != nil {
		t.Errorf("GetGalleryStatsCached should return nil when cache is empty, got %+v", got)
	}
}

func TestRuntimeManager_GetGalleryStatsCached_Stale(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())
	m.SetGalleryStatsCache(&GalleryStats{Folders: "1"}, 123)
	if got := m.GetGalleryStatsCached(456); got != nil {
		t.Error("GetGalleryStatsCached should return nil when discoveryLastStartedAt mismatches")
	}
}

func TestRuntimeManager_GetGalleryStatsCached_Hit(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())
	stats := &GalleryStats{Folders: "42", Images: "100"}
	m.SetGalleryStatsCache(stats, 123)

	got := m.GetGalleryStatsCached(123)
	if got == nil {
		t.Fatal("GetGalleryStatsCached should return cached stats when key matches")
	}
	if got.Folders != stats.Folders || got.Images != stats.Images {
		t.Errorf("cached stats mismatch: got %+v, want %+v", got, stats)
	}

	// Mutating the returned copy must not affect the cache.
	got.Folders = "999"
	got2 := m.GetGalleryStatsCached(123)
	if got2.Folders != stats.Folders {
		t.Error("GetGalleryStatsCached should return a copy, not the original pointer")
	}
}

func TestRuntimeManager_SetGalleryStatsCache(t *testing.T) {
	t.Parallel()
	m := NewRuntimeManager(context.Background())
	stats := &GalleryStats{Folders: "5"}
	m.SetGalleryStatsCache(stats, 999)

	got := m.GetGalleryStatsCached(999)
	if got == nil || got.Folders != "5" {
		t.Errorf("SetGalleryStatsCache did not store stats correctly: got %+v", got)
	}
}

func TestRuntimeManager_Serve_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := NewRuntimeManager(ctx)
	addr := getEphemeralAddr(t)

	done := make(chan error, 1)
	go func() {
		done <- m.Serve(http.NewServeMux(), addr)
	}()

	if err := waitForServerReady(t, addr); err != nil {
		t.Fatalf("server did not become ready: %v", err)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error on context cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}
}

func TestRuntimeManager_Serve_ListenError(t *testing.T) {
	m := NewRuntimeManager(context.Background())
	err := m.Serve(http.NewServeMux(), "127.0.0.1:-1")
	if err == nil {
		t.Fatal("expected error for invalid bind address")
	}
}

func TestRuntimeManager_Serve_NilServerShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := NewRuntimeManager(ctx)
	m.testHookBeforeListen = func() { cancel() }

	err := m.Serve(http.NewServeMux(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("expected nil error when context cancelled before server creation, got %v", err)
	}
}

func TestRuntimeManager_Serve_ShutdownError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := NewRuntimeManager(ctx)
	m.testHookShutdown = func(context.Context) error { return errors.New("shutdown error") }
	addr := getEphemeralAddr(t)

	done := make(chan error, 1)
	go func() {
		done <- m.Serve(http.NewServeMux(), addr)
	}()

	if err := waitForServerReady(t, addr); err != nil {
		t.Fatalf("server did not become ready: %v", err)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error on context cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}
}

func TestRuntimeManager_TriggerRestart_ShutdownError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewRuntimeManager(ctx)
	m.testHookShutdown = func(context.Context) error { return errors.New("shutdown error") }
	addr := getEphemeralAddr(t)
	done := make(chan error, 1)
	go func() {
		done <- m.Serve(http.NewServeMux(), addr)
	}()

	if err := waitForServerReady(t, addr); err != nil {
		t.Fatalf("server did not become ready: %v", err)
	}

	m.TriggerRestart()

	if !m.IsRestartRequested() {
		t.Error("IsRestartRequested should be true after TriggerRestart")
	}

	// The test hook prevented the real server shutdown, so cancel the context
	// to make Serve return.
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}
}

// getEphemeralAddr picks a free TCP port and returns its address. The listener
// is closed before returning; the caller must bind to the address promptly.
func getEphemeralAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// waitForServerReady polls the given address until it accepts a connection.
func waitForServerReady(t *testing.T, addr string) error {
	t.Helper()

	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + "/")
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("server did not become ready")
}

func TestRuntimeManager_Exit_FallsBackToOsExit(t *testing.T) {
	m := NewRuntimeManager(context.Background())
	// testHookExit is intentionally nil.

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
