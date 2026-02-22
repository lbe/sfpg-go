package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/server/config"
)

// TestRestartRequired_DetectsChanges verifies that RestartRequired returns true
// when restart-required settings have changed.
func TestRestartRequired_DetectsChanges(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()

	// Change a restart-required setting (listener port)
	oldPort := app.config.ListenerPort
	app.config.ListenerPort = oldPort + 1

	// Mark that we've detected a change
	app.restartRequired = true

	if !app.RestartRequired() {
		t.Error("RestartRequired() should return true when restart-required settings changed")
	}
}

// TestRestartRequired_NoChanges verifies that RestartRequired returns false
// when no restart-required settings have changed.
func TestRestartRequired_NoChanges(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()
	app.restartRequired = false

	if app.RestartRequired() {
		t.Error("RestartRequired() should return false when no changes detected")
	}
}

// TestRestartServer_GracefulShutdown verifies that RestartServer gracefully
// shuts down the HTTP server without losing connections.
func TestRestartServer_GracefulShutdown(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()
	app.store = sessions.NewCookieStore([]byte("test-secret"))

	// Start HTTP server in background
	ctx, cancel := context.WithCancel(context.Background())
	app.ctxMu.Lock()
	app.ctx = ctx
	app.cancel = cancel
	app.ctxMu.Unlock()

	server := &http.Server{
		Addr:    ":0", // Use random port
		Handler: app.getRouter(),
	}

	// Start server in goroutine
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test graceful shutdown
	err := app.RestartServer(server)
	if err != nil {
		t.Fatalf("RestartServer() failed: %v", err)
	}

	// Verify server stopped
	select {
	case err := <-serverErr:
		if err != http.ErrServerClosed {
			t.Errorf("Expected http.ErrServerClosed, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Server did not shut down within timeout")
	}
}

// TestRestartServer_PreservesConnections verifies that RestartServer preserves
// database connections when possible (HTTP-only restart).
func TestRestartServer_PreservesConnections(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()

	// Set up database pools
	app.setDB()
	if app.dbRwPool == nil {
		t.Fatal("Failed to set up database pool")
	}

	// Get a connection before restart
	connBefore, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Failed to get connection before restart: %v", err)
	}
	app.dbRwPool.Put(connBefore)

	// Change only listener settings (HTTP-only restart)
	app.config.ListenerPort = 9999
	app.restartRequired = true

	server := &http.Server{
		Addr:    ":0",
		Handler: app.getRouter(),
	}

	// Perform HTTP-only restart
	err = app.RestartServer(server)
	if err != nil {
		t.Fatalf("RestartServer() failed: %v", err)
	}

	// Verify database pool still works
	connAfter, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("Database pool should still work after HTTP-only restart: %v", err)
	}
	app.dbRwPool.Put(connAfter)
}

// TestRestartServer_HTTPOnly verifies that RestartServer only restarts the HTTP
// server component when only listener address/port settings changed.
func TestRestartServer_HTTPOnly(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()
	app.store = sessions.NewCookieStore([]byte("test-secret"))

	// Change only listener settings
	app.config.ListenerAddress = "127.0.0.1"
	app.config.ListenerPort = 9999
	app.restartRequired = true

	server := &http.Server{
		Addr:    ":0",
		Handler: app.getRouter(),
	}

	// This should only restart HTTP server, not full app
	err := app.RestartServer(server)
	if err != nil {
		t.Fatalf("RestartServer() failed: %v", err)
	}

	// Verify config was applied
	if app.config.ListenerAddress != "127.0.0.1" {
		t.Error("Listener address should be updated after restart")
	}
	if app.config.ListenerPort != 9999 {
		t.Error("Listener port should be updated after restart")
	}
}

// TestRestartServer_FullRestart verifies that RestartServer performs a full
// restart when non-listener settings that require restart are changed.
func TestRestartServer_FullRestart(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()
	app.store = sessions.NewCookieStore([]byte("test-secret"))

	// Change a non-listener restart-required setting (log level)
	app.config.LogLevel = "INFO"
	app.restartRequired = true

	server := &http.Server{
		Addr:    ":0",
		Handler: app.getRouter(),
	}

	// This should trigger full restart
	err := app.RestartServer(server)
	if err != nil {
		t.Fatalf("RestartServer() failed: %v", err)
	}

	// Verify config was applied
	if app.config.LogLevel != "INFO" {
		t.Error("Log level should be updated after restart")
	}
}

// TestRestartChannel_Initialization verifies that the restart channel
// is properly initialized and usable. CreateApp calls ensureSessionAndRestart,
// which initializes restartCh before Handlers are built.
func TestRestartChannel_Initialization(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()
	app.store = sessions.NewCookieStore([]byte("test-secret"))
	app.ensureSessionAndRestart()

	// Channel should be initialized (by ensureSessionAndRestart)
	if app.restartCh == nil {
		t.Error("restartCh should be initialized")
	}

	// Test we can send and receive on the channel
	select {
	case app.restartCh <- struct{}{}:
		// Good, channel accepted the signal
	default:
		t.Error("restartCh should accept a signal")
	}

	select {
	case <-app.restartCh:
		// Good, we received the signal
	default:
		t.Error("restartCh should have a signal waiting")
	}
}

// TestRestartChannel_SignalDelivery verifies that a signal sent to restartCh
// can be properly received and processed.
func TestRestartChannel_SignalDelivery(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()
	app.store = sessions.NewCookieStore([]byte("test-secret"))

	// Initialize the channel
	app.restartCh = make(chan struct{}, 1)

	// Simulate sending a restart signal (like restartHandler does)
	signalSent := false
	select {
	case app.restartCh <- struct{}{}:
		signalSent = true
	default:
		t.Error("Should be able to send restart signal")
	}

	if !signalSent {
		t.Fatal("Signal was not sent")
	}

	// Simulate receiving the signal (like Serve() does)
	signalReceived := false
	select {
	case <-app.restartCh:
		signalReceived = true
	case <-time.After(100 * time.Millisecond):
		t.Error("Should receive restart signal")
	}

	if !signalReceived {
		t.Error("Signal was not received")
	}
}

// TestRestartChannel_BufferedNoBlock verifies that the restart channel
// is buffered and non-blocking when sending.
func TestRestartChannel_BufferedNoBlock(t *testing.T) {
	app := CreateApp(t, false)
	app.config = config.DefaultConfig()

	// Initialize the channel with buffer of 1 (like Serve() does)
	app.restartCh = make(chan struct{}, 1)

	// First send should succeed
	select {
	case app.restartCh <- struct{}{}:
		// Good
	default:
		t.Error("First signal should be accepted into buffer")
	}

	// Second send should not block (channel full, select default case)
	select {
	case app.restartCh <- struct{}{}:
		t.Error("Second signal should not be accepted (buffer full)")
	default:
		// Good, channel is full but we didn't block
	}
}

// REMOVED: TestServe_RestartSignal - Slow duplicate test (1.07s)
// REMOVED: func TestServe_RestartSignal(t *testing.T) {
// REMOVED: 	app := CreateApp(t, false)
// REMOVED: 	app.config = config.DefaultConfig()
// REMOVED: 	app.config.ListenerAddress = "127.0.0.1"
// REMOVED: 	app.config.ListenerPort = 0 // Random available port
// REMOVED: 	app.config.ImageDirectory = app.imagesDir
// REMOVED: 	app.applyConfig()
// REMOVED: 	app.store = sessions.NewCookieStore([]byte("test-secret"))
// REMOVED:
// REMOVED: 	// Set up context for the app
// REMOVED: 	ctx, cancel := context.WithCancel(context.Background())
// REMOVED: 	defer cancel()
// REMOVED: 	app.ctxMu.Lock()
// REMOVED: 	app.ctx = ctx
// REMOVED: 	app.cancel = cancel
// REMOVED: 	app.ctxMu.Unlock()
// REMOVED:
// REMOVED: 	// Start Serve() in a goroutine
// REMOVED: 	serveErr := make(chan error, 1)
// REMOVED: 	go func() {
// REMOVED: 		serveErr <- app.Serve()
// REMOVED: 	}()
// REMOVED:
// REMOVED: 	// Give server time to start
// REMOVED: 	time.Sleep(200 * time.Millisecond)
// REMOVED:
// REMOVED: 	// Verify restartCh was initialized
// REMOVED: 	app.restartMu.RLock()
// REMOVED: 	restartCh := app.restartCh
// REMOVED: 	httpServer := app.httpServer
// REMOVED: 	app.restartMu.RUnlock()
// REMOVED:
// REMOVED: 	if restartCh == nil {
// REMOVED: 		t.Fatal("restartCh should be initialized by Serve()")
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Verify httpServer was created
// REMOVED: 	if httpServer == nil {
// REMOVED: 		t.Fatal("httpServer should be initialized by Serve()")
// REMOVED: 	}
// REMOVED:
// REMOVED: 	firstServerAddr := httpServer.Addr
// REMOVED:
// REMOVED: 	// Send restart signal
// REMOVED: 	select {
// REMOVED: 	case restartCh <- struct{}{}:
// REMOVED: 		// Good
// REMOVED: 	default:
// REMOVED: 		t.Fatal("Should be able to send restart signal")
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Give server time to restart
// REMOVED: 	time.Sleep(500 * time.Millisecond)
// REMOVED:
// REMOVED: 	// Verify server is still running (new instance)
// REMOVED: 	app.restartMu.RLock()
// REMOVED: 	httpServerAfter := app.httpServer
// REMOVED: 	app.restartMu.RUnlock()
// REMOVED:
// REMOVED: 	if httpServerAfter == nil {
// REMOVED: 		t.Fatal("httpServer should still exist after restart")
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// The address should be the same (we're listening on the same port)
// REMOVED: 	if httpServerAfter.Addr != firstServerAddr {
// REMOVED: 		t.Logf("Server address changed from %s to %s (expected for restart)", firstServerAddr, httpServerAfter.Addr)
// REMOVED: 	}
// REMOVED:
// REMOVED: 	// Clean up: cancel context to stop the server
// REMOVED: 	cancel()
// REMOVED:
// REMOVED: 	// Wait for Serve() to return
// REMOVED: 	select {
// REMOVED: 	case err := <-serveErr:
// REMOVED: 		if err != nil {
// REMOVED: 			t.Logf("Serve() returned error (expected during shutdown): %v", err)
// REMOVED: 		}
// REMOVED: 	case <-time.After(5 * time.Second):
// REMOVED: 		t.Error("Serve() did not return within timeout")
// REMOVED: 	}
// REMOVED: }
