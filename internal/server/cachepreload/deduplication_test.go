package cachepreload

import (
	"sync"
	"testing"
)

func TestTaskTracker_RegisterAndIsPending(t *testing.T) {
	tt := &TaskTracker{}
	key := "GET:/gallery/1?v=x|HX=false|HXTarget=|identity"

	if tt.IsTaskPending(key) {
		t.Error("expected not pending before register")
	}
	if !tt.RegisterTask(key, "sess1", "task1") {
		t.Error("expected RegisterTask to return true (registered)")
	}
	if !tt.IsTaskPending(key) {
		t.Error("expected pending after register")
	}
}

func TestTaskTracker_RegisterDuplicate_ReturnsFalse(t *testing.T) {
	tt := &TaskTracker{}
	key := "GET:/gallery/1?v=x|identity"

	tt.RegisterTask(key, "sess1", "task1")
	if tt.RegisterTask(key, "sess2", "task2") {
		t.Error("expected RegisterTask to return false (duplicate)")
	}
}

func TestTaskTracker_Unregister_AllowsReschedule(t *testing.T) {
	tt := &TaskTracker{}
	key := "GET:/gallery/1?v=x|identity"

	tt.RegisterTask(key, "sess1", "task1")
	tt.UnregisterTask(key)
	if tt.IsTaskPending(key) {
		t.Error("expected not pending after unregister")
	}
	if !tt.RegisterTask(key, "sess1", "task2") {
		t.Error("expected RegisterTask to succeed after unregister")
	}
}

func TestTaskTracker_CancelSessionTasks(t *testing.T) {
	tt := &TaskTracker{}
	tt.RegisterTask("key1", "sess1", "task1")
	tt.RegisterTask("key2", "sess1", "task2")
	tt.RegisterTask("key3", "sess2", "task3")

	ids := tt.CancelSessionTasks("sess1")
	if len(ids) != 2 {
		t.Errorf("expected 2 task IDs, got %d", len(ids))
	}
	if tt.IsTaskPending("key1") || tt.IsTaskPending("key2") {
		t.Error("expected sess1 tasks to be removed")
	}
	if !tt.IsTaskPending("key3") {
		t.Error("expected sess2 task to remain")
	}
}

func TestTaskTracker_ConcurrentAccess(t *testing.T) {
	tt := &TaskTracker{}
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "GET:/gallery/1?v=x|identity"
			tt.RegisterTask(key, "sess", "task")
			tt.IsTaskPending(key)
			tt.UnregisterTask(key)
		}(i)
	}
	wg.Wait()
}
