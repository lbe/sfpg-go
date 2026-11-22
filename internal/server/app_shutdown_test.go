package server

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

// Regression for thermos R-002: Shutdown must wait for an in-flight discovery
// run before the database pools close.
func TestApp_Shutdown_WaitsForDiscoveryBeforeClosingPools(t *testing.T) {
	app := CreateApp(t)

	done := startBlockedDiscovery(t, app)

	discoveryIdleBeforeClose := false
	app.InfrastructureService.testSeams.BeforeCloseDatabasePools = func() {
		discoveryIdleBeforeClose = app.SubsystemManager.discovery.Idle()
	}

	app.Shutdown()
	<-done

	if !discoveryIdleBeforeClose {
		t.Fatal("discovery was still running when database pools closed")
	}
}

// Regression for thermos R-005 / CQ-003: App.Shutdown must not call scheduler.Shutdown
// while Start is running (always ErrSchedulerRunning).
func TestApp_Shutdown_DoesNotLogSchedulerShutdownError(t *testing.T) {
	app := CreateApp(t)

	go func() {
		_ = app.SubsystemManager.scheduler.Start(app.RuntimeManager.ctx)
	}()
	time.Sleep(20 * time.Millisecond)

	logs := withLogCapture(t, slog.LevelError, func() {
		app.Shutdown()
	})
	if strings.Contains(logs, "error shutting down scheduler") {
		t.Fatalf("unexpected scheduler shutdown error log: %s", logs)
	}
}
