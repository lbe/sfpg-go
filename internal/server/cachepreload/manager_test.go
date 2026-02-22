package cachepreload

import (
	"context"
	"testing"
	"time"

	"go.local/sfpg/internal/scheduler"
)

func TestPreloadManager_IsEnabled_InitialState(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, true)
	defer pm.Shutdown()
	if !pm.IsEnabled() {
		t.Error("expected IsEnabled true when created with initialEnabled true")
	}
}

func TestPreloadManager_IsEnabled_InitiallyDisabled(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, false)
	defer pm.Shutdown()
	if pm.IsEnabled() {
		t.Error("expected IsEnabled false when created with initialEnabled false")
	}
}

func TestPreloadManager_SetEnabled_EnableWhenDisabled(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, false)
	defer pm.Shutdown()

	pm.SetEnabled(true)
	if !pm.IsEnabled() {
		t.Error("expected IsEnabled true after SetEnabled(true)")
	}
	if pm.GetScheduler() == nil {
		t.Error("expected scheduler to be created when enabled")
	}
}

func TestPreloadManager_SetEnabled_DisableWhenEnabled(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, true)
	defer pm.Shutdown()

	pm.waitForSchedulerStart()
	pm.SetEnabled(false)
	if pm.IsEnabled() {
		t.Error("expected IsEnabled false after SetEnabled(false)")
	}
	if pm.GetScheduler() != nil {
		t.Error("expected scheduler to be nil when disabled")
	}
}

func TestPreloadManager_SetEnabled_EnableWhenAlreadyEnabled_NoOp(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, true)
	defer pm.Shutdown()
	pm.waitForSchedulerStart()

	origSched := pm.GetScheduler()
	pm.SetEnabled(true)
	if pm.GetScheduler() != origSched {
		t.Error("expected same scheduler when SetEnabled(true) on already enabled")
	}
}

func TestPreloadManager_SetEnabled_DisableWhenAlreadyDisabled_NoOp(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, false)
	defer pm.Shutdown()
	pm.SetEnabled(false)
	if pm.GetScheduler() != nil {
		t.Error("expected scheduler to remain nil when SetEnabled(false) on already disabled")
	}
}

func TestPreloadManager_SetEnabled_Callback(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, false)
	defer pm.Shutdown()

	var called bool
	var gotEnabled bool
	pm.SetOnSetEnabled(func(enabled bool) {
		called = true
		gotEnabled = enabled
	})

	pm.SetEnabled(true)
	if !called {
		t.Error("expected onSetEnabled callback to be called")
	}
	if !gotEnabled {
		t.Error("expected callback to receive true")
	}
}

func TestPreloadManager_ScheduleFolderPreload_WhenDisabled_NoOp(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, false)
	defer pm.Shutdown()
	pm.ScheduleFolderPreload(context.Background(), 23, "sess-1", "")
	// No panic, no-op
}

func TestPreloadManager_Shutdown(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, true)
	pm.waitForSchedulerStart()
	pm.Shutdown()
	if pm.IsEnabled() {
		t.Error("expected IsEnabled false after Shutdown")
	}
	if pm.GetScheduler() != nil {
		t.Error("expected scheduler nil after Shutdown")
	}
}

func TestPreloadManager_ConcurrentSetEnabled(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, true)
	defer pm.Shutdown()
	pm.waitForSchedulerStart()

	done := make(chan struct{})
	go func() {
		for i := range 20 {
			pm.SetEnabled(i%2 == 0)
			time.Sleep(1 * time.Millisecond)
		}
		close(done)
	}()
	<-done
	// No race, no panic
}

// Verify scheduler is usable for AddTask (used by later phases).
func TestPreloadManager_SchedulerAcceptsTask(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, true)
	defer pm.Shutdown()
	pm.waitForSchedulerStart()

	sched := pm.GetScheduler()
	if sched == nil {
		t.Fatal("scheduler nil")
	}
	id, err := sched.AddTask(&noopTask{}, scheduler.OneTime, time.Now())
	if err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty task ID")
	}
}

type noopTask struct{}

func (t *noopTask) Run(ctx context.Context) error {
	return nil
}
