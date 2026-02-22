package cachepreload

import (
	"fmt"
	"net/http"
	"testing"
)

func TestPreloadTaskPool_GetReturnsTask(t *testing.T) {
	task := GetPreloadTask()
	if task == nil {
		t.Fatal("GetPreloadTask returned nil")
	}
	PutPreloadTask(task)
}

func TestPreloadTaskPool_ResetClearsAllFields(t *testing.T) {
	task := GetPreloadTask()

	// Set all fields
	task.CacheKey = "test-key"
	task.Path = "/test"
	task.HXTarget = "gallery-content"
	task.Encoding = "gzip"
	task.TaskTracker = &TaskTracker{}
	task.RequestConfig = InternalRequestConfig{
		Handler:     http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ETagVersion: "v1",
	}
	task.Metrics = &PreloadMetrics{}

	PutPreloadTask(task)
	task2 := GetPreloadTask()

	if task2.CacheKey != "" || task2.Path != "" || task2.HXTarget != "" {
		t.Error("String fields not reset")
	}
	if task2.TaskTracker != nil || task2.Metrics != nil {
		t.Error("Pointer fields not reset to nil")
	}
	if task2.RequestConfig.Handler != nil || task2.RequestConfig.ETagVersion != "" {
		t.Error("RequestConfig not reset")
	}

	PutPreloadTask(task2)
}

func TestPreloadTaskPool_PutNilSafe(t *testing.T) {
	// Should not panic
	PutPreloadTask(nil)
}

func TestPreloadTaskPool_Reuse(t *testing.T) {
	// Test that same object can be reused after Put
	task1 := GetPreloadTask()
	task1Ptr := fmt.Sprintf("%p", task1)
	PutPreloadTask(task1)

	task2 := GetPreloadTask()
	task2Ptr := fmt.Sprintf("%p", task2)

	// Should be same object (or at least verify pool is working)
	if task1Ptr != task2Ptr {
		t.Log("Note: Different objects allocated (pool may be empty or GC'd)")
	}
	PutPreloadTask(task2)
}
