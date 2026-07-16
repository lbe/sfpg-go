//go:build integration

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/getopt"

	_ "github.com/ncruces/go-sqlite3/driver"
)

// TestIntegration_Serve_RestartSignal restores the pre-refactor integration test for the
// restart signal path. It starts the real HTTP server, triggers a restart, and
// verifies that Serve returns and flags the restart request.
func TestIntegration_Serve_RestartSignal(t *testing.T) {
	t.Setenv("SEPG_SESSION_SECURE", "false")

	app := CreateApp(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	app.opt.Port = getopt.OptInt{Int: port, IsSet: true}

	done := make(chan error, 1)
	go func() {
		done <- app.Serve()
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if waitErr := waitForURLReady(baseURL + "/login-form"); waitErr != nil {
		t.Fatalf("server did not start: %v", waitErr)
	}

	app.TriggerRestart()

	select {
	case serveErr := <-done:
		if serveErr != nil {
			t.Fatalf("Serve returned error: %v", serveErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after TriggerRestart")
	}

	if !app.IsRestartRequested() {
		t.Fatal("restart should have been requested")
	}
}

// TestIntegration_RuntimeManager_ServeAndShutdown verifies that the server started by
// App.Serve accepts requests and stops accepting connections after Shutdown.
func TestIntegration_RuntimeManager_ServeAndShutdown(t *testing.T) {
	t.Setenv("SEPG_SESSION_SECURE", "false")

	app := CreateApp(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	app.opt.Port = getopt.OptInt{Int: port, IsSet: true}

	done := make(chan error, 1)
	go func() {
		done <- app.Serve()
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if waitErr := waitForURLReady(baseURL + "/login-form"); waitErr != nil {
		t.Fatalf("server did not start: %v", waitErr)
	}

	app.Shutdown()

	select {
	case serveErr := <-done:
		if serveErr != nil {
			t.Fatalf("Serve returned error after shutdown: %v", serveErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Shutdown")
	}

	// After shutdown the server should no longer accept connections.
	client := &http.Client{Timeout: 500 * time.Millisecond}
	_, err = client.Get(baseURL + "/login-form")
	if err == nil {
		t.Error("server should not accept connections after Shutdown")
	}
}

// TestRefreshGalleryStatsCache_ErrorPath verifies that refreshGalleryStatsCache
// returns an error and does not cache anything when getGalleryStatistics fails.
func TestRefreshGalleryStatsCache_ErrorPath(t *testing.T) {
	app := CreateApp(t)
	app.testSeams.GetGalleryStatistics = func(context.Context) (GalleryStats, error) {
		return GalleryStats{}, errors.New("stats failure")
	}

	_, err := app.refreshGalleryStatsCache(context.Background(), 12345)
	if err == nil {
		t.Fatal("expected error when getGalleryStatistics fails")
	}

	if got := app.GetGalleryStatsCached(12345); got != nil {
		t.Error("expected no cached stats after error")
	}
}

func waitForURLReady(url string) error {
	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("URL %s did not become ready", url)
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
	m.testSeams.BeforeListen = func() { cancel() }

	err := m.Serve(http.NewServeMux(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("expected nil error when context cancelled before server creation, got %v", err)
	}
}

func TestRuntimeManager_Serve_ShutdownError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := NewRuntimeManager(ctx)
	m.testSeams.Shutdown = func(context.Context) error { return errors.New("shutdown error") }
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
	m.testSeams.Shutdown = func(context.Context) error { return errors.New("shutdown error") }
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
