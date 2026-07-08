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

// TestE2E_Serve_RestartSignal restores the pre-refactor integration test for the
// restart signal path. It starts the real HTTP server, triggers a restart, and
// verifies that Serve returns and flags the restart request.
func TestE2E_Serve_RestartSignal(t *testing.T) {
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

// TestE2E_RuntimeManager_ServeAndShutdown verifies that the server started by
// App.Serve accepts requests and stops accepting connections after Shutdown.
func TestE2E_RuntimeManager_ServeAndShutdown(t *testing.T) {
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
	app.testHookGetGalleryStatistics = func(context.Context) (GalleryStats, error) {
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
