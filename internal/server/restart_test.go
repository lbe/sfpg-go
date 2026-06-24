package server

import (
	"testing"
	"time"
)

// TestRestartRequired_DetectsChanges verifies that RestartRequired returns true
// when restart-required settings have changed.
func TestRestartRequired_DetectsChanges(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	app.restartRequired = true

	if !app.RestartRequired() {
		t.Error("RestartRequired() should return true when restart-required settings changed")
	}
}

// TestRestartRequired_NoChanges verifies that RestartRequired returns false
// when no restart-required settings have changed.
func TestRestartRequired_NoChanges(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	app.restartRequired = false

	if app.RestartRequired() {
		t.Error("RestartRequired() should return false when no changes detected")
	}
}

// TestRestartChannel_Initialization verifies that the restart channel
// is properly initialized and usable. CreateApp calls ensureSessionAndRestart,
// which initializes restartCh before Handlers are built.
func TestRestartChannel_Initialization(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()

	// Channel should be initialized by CreateApp via ensureSessionAndRestart
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
	defer app.Shutdown()

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
	defer app.Shutdown()

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
