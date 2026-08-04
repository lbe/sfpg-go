package cachepreload

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/scheduler"
)

// requireScheduler asserts that the scheduler is available synchronously.
// NewPreloadManager(_, true) and SetEnabled(true) both create the scheduler
// before returning, so there is nothing to poll for.
func requireScheduler(t *testing.T, pm *PreloadManager) *scheduler.Scheduler {
	t.Helper()
	s := pm.GetScheduler()
	if s == nil {
		t.Fatal("expected scheduler to be available synchronously when enabled")
	}
	return s
}

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

	requireScheduler(t, pm)
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
	requireScheduler(t, pm)

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
	pm.ScheduleFolderPreload(context.Background(), 23, "sess-1")
	// No panic, no-op
}

func TestPreloadManager_Shutdown(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, true)
	requireScheduler(t, pm)
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

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pm.SetEnabled(true)
			pm.SetEnabled(false)
		}()
	}
	wg.Wait()
	// No race, no panic
}

// Verify scheduler is usable for AddTask (used by later phases).
func TestPreloadManager_SchedulerAcceptsTask(t *testing.T) {
	pm := NewPreloadManager([]string{"/gallery/"}, true)
	defer pm.Shutdown()

	sched := requireScheduler(t, pm)
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
