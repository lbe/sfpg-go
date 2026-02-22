package cachepreload

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.local/sfpg/internal/scheduler"
)

func TestPreloadTask_Run_Success(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	tt := &TaskTracker{}
	metrics := &PreloadMetrics{}
	key := "GET:/gallery/1?v=x|identity"
	tt.RegisterTask(key, "sess", "task1")

	task := &PreloadTask{
		CacheKey:    key,
		Path:        "/gallery/1",
		TaskTracker: tt,
		RequestConfig: InternalRequestConfig{
			Handler:     handler,
			ETagVersion: "x",
		},
		Metrics: metrics,
	}

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tt.IsTaskPending(key) {
		t.Error("expected task unregistered after Run")
	}
	if metrics.TasksCompleted.Load() != 1 {
		t.Error("expected RecordSuccess called")
	}
}

func TestPreloadTask_Run_UnregistersOnError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	tt := &TaskTracker{}
	key := "GET:/gallery/1?v=x|identity"
	tt.RegisterTask(key, "sess", "task1")

	task := &PreloadTask{
		CacheKey:    key,
		Path:        "/gallery/1",
		TaskTracker: tt,
		RequestConfig: InternalRequestConfig{
			Handler:     handler,
			ETagVersion: "x",
		},
		Metrics: &PreloadMetrics{},
	}

	_ = task.Run(context.Background())
	if tt.IsTaskPending(key) {
		t.Error("expected task unregistered even on error")
	}
}

func TestPreloadTask_Run_RecordsFailure(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	tt := &TaskTracker{}
	metrics := &PreloadMetrics{}
	key := "GET:/gallery/1?v=x|identity"
	tt.RegisterTask(key, "sess", "task1")

	task := &PreloadTask{
		CacheKey:    key,
		Path:        "/gallery/1",
		TaskTracker: tt,
		RequestConfig: InternalRequestConfig{
			Handler:     handler,
			ETagVersion: "x",
		},
		Metrics: metrics,
	}

	_ = task.Run(context.Background())
	if metrics.TasksFailed.Load() != 1 {
		t.Error("expected RecordFailure called")
	}
}

func TestPreloadTask_SchedulerIntegration(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	tt := &TaskTracker{}
	key := "GET:/test?v=x|identity"
	tt.RegisterTask(key, "sess", "task1")

	task := &PreloadTask{
		CacheKey:    key,
		Path:        "/test",
		TaskTracker: tt,
		RequestConfig: InternalRequestConfig{
			Handler:     handler,
			ETagVersion: "x",
		},
		Metrics: nil,
	}

	sched := scheduler.NewScheduler(2)
	ctx := t.Context()

	go sched.Start(ctx)
	time.Sleep(20 * time.Millisecond) // let scheduler start

	id, err := sched.AddTask(task, scheduler.OneTime, time.Now())
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	_ = id

	time.Sleep(50 * time.Millisecond) // let task run
	if tt.IsTaskPending(key) {
		t.Error("expected task to complete and unregister")
	}
}

func TestPreloadTask_UnregistersAfterRun(t *testing.T) {
	tracker := &TaskTracker{}
	metrics := &PreloadMetrics{}

	cacheKey := "GET:/test/1?v=test|HX=false|HXTarget=|identity"
	tracker.RegisterTask(cacheKey, "session-1", "task-1")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=3600")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test"))
	})

	task := &PreloadTask{
		CacheKey:      cacheKey,
		Path:          "/test/1",
		TaskTracker:   tracker,
		RequestConfig: InternalRequestConfig{Handler: handler, ETagVersion: "test"},
		Metrics:       metrics,
	}

	ctx := context.Background()
	err := task.Run(ctx)
	if err != nil {
		t.Errorf("Run failed: %v", err)
	}

	if tracker.IsTaskPending(cacheKey) {
		t.Error("Task should be unregistered after Run()")
	}
}

// TestPreloadTask_Run_WithPooledTask verifies that a task obtained from GetPreloadTask
// runs correctly and is returned to the pool by Run()'s defer (no double-Put, no panic).
func TestPreloadTask_Run_WithPooledTask(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	tt := &TaskTracker{}
	key := "GET:/pooled?v=x|identity"
	tt.RegisterTask(key, "sess", "task1")

	task := GetPreloadTask()
	task.CacheKey = key
	task.Path = "/pooled"
	task.TaskTracker = tt
	task.RequestConfig = InternalRequestConfig{Handler: handler, ETagVersion: "x"}
	task.Metrics = &PreloadMetrics{}

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tt.IsTaskPending(key) {
		t.Error("expected task unregistered after Run")
	}
	// Task was Put in Run's defer; getting another from pool should succeed (pool still healthy).
	task2 := GetPreloadTask()
	if task2 == nil {
		t.Fatal("GetPreloadTask after Run returned nil")
	}
	PutPreloadTask(task2)
}

// TestPreloadTask_Run_WithVariant verifies the HXTarget path uses MakeInternalRequestWithVariant
// and the handler receives HX-Request and HX-Target.
func TestPreloadTask_Run_WithVariant(t *testing.T) {
	var gotHX, gotTarget string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHX = r.Header.Get("HX-Request")
		gotTarget = r.Header.Get("HX-Target")
		w.WriteHeader(http.StatusOK)
	})
	tt := &TaskTracker{}
	key := "GET:/info/image/1?v=x|identity"
	tt.RegisterTask(key, "sess", "task1")

	task := &PreloadTask{
		CacheKey:      key,
		Path:          "/info/image/1",
		HXTarget:      "box_info",
		Encoding:      "gzip",
		TaskTracker:   tt,
		RequestConfig: InternalRequestConfig{Handler: handler, ETagVersion: "x"},
		Metrics:       &PreloadMetrics{},
	}

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotHX != "true" {
		t.Errorf("HX-Request = %q, want true", gotHX)
	}
	if gotTarget != "box_info" {
		t.Errorf("HX-Target = %q, want box_info", gotTarget)
	}
}
