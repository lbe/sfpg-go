package cachepreload

import (
	"sync"
	"time"
)

// taskInfo holds metadata for a pending preload task.
type taskInfo struct {
	sessionID string
	taskID    string
	createdAt time.Time
}

// TaskTracker provides deduplication for preload tasks using full cache keys.
// It prevents scheduling duplicate tasks for the same cache entry.
type TaskTracker struct {
	pending sync.Map // map[cacheKey]taskInfo
}

// IsTaskPending returns true if a task for this cache key is already scheduled/running.
func (t *TaskTracker) IsTaskPending(cacheKey string) bool {
	_, ok := t.pending.Load(cacheKey)
	return ok
}

// RegisterTask marks a cache key as having a pending task.
// Returns false if task already pending (caller should skip).
func (t *TaskTracker) RegisterTask(cacheKey, sessionID, taskID string) bool {
	ti := &taskInfo{sessionID: sessionID, taskID: taskID, createdAt: time.Now()}
	_, loaded := t.pending.LoadOrStore(cacheKey, ti)
	return !loaded // true if we registered (was not pending), false if already pending
}

// UnregisterTask removes a cache key from pending (called when task completes).
func (t *TaskTracker) UnregisterTask(cacheKey string) {
	t.pending.Delete(cacheKey)
}

// CancelSessionTasks marks all tasks for a session as cancelled.
// Returns list of taskIDs to cancel via scheduler.RemoveTask().
func (t *TaskTracker) CancelSessionTasks(sessionID string) []string {
	var taskIDs []string
	t.pending.Range(func(key, value any) bool {
		ti := value.(*taskInfo)
		if ti.sessionID == sessionID {
			taskIDs = append(taskIDs, ti.taskID)
			t.pending.Delete(key)
		}
		return true
	})
	return taskIDs
}

// TryClaimTask attempts to claim a cache key for processing.
// Returns true if the key was successfully claimed, false if it's already claimed.
// This method is thread-safe and ensures only one goroutine processes a given cache key.
func (t *TaskTracker) TryClaimTask(cacheKey string) bool {
	// Try to register the task
	return t.RegisterTask(cacheKey, "", "")
}
