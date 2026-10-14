package server

import (
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Regression for thermos R-002: pools must stay open until discoveryRunning clears.
func TestApp_Shutdown_KeepsPoolsOpenUntilDiscoveryFinishes(t *testing.T) {
	app := CreateApp(t)

	app.discoveryRunning.Store(true)

	poolsOpenBeforeClose := atomic.Bool{}
	app.InfrastructureService.testSeams.BeforeCloseDatabasePools = func() {
		if app.dbRwPool == nil {
			t.Error("dbRwPool nil before closeDatabasePools")
			return
		}
		poolsOpenBeforeClose.Store(true)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		app.discoveryRunning.Store(false)
	}()

	app.Shutdown()

	if !poolsOpenBeforeClose.Load() {
		t.Fatal("shutdown did not observe open pools before closeDatabasePools")
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
