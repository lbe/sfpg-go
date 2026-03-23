package cachepreload

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestTaskTracker_TryClaimTask(t *testing.T) {
	tt := &TaskTracker{}
	cacheKey := "test-key"

	// First claim should succeed
	if !tt.TryClaimTask(cacheKey) {
		t.Fatal("First claim should succeed")
	}

	// Second claim should fail (already claimed)
	if tt.TryClaimTask(cacheKey) {
		t.Error("Second claim should fail - key already claimed")
	}

	// Different key should succeed
	if !tt.TryClaimTask("different-key") {
		t.Error("Claim on different key should succeed")
	}
}

func TestTaskTracker_ConcurrentClaim(t *testing.T) {
	tt := &TaskTracker{}
	cacheKey := "test-key"

	var wg sync.WaitGroup
	successes := atomic.Int64{}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tt.TryClaimTask(cacheKey) {
				successes.Add(1)
			}
		}()
	}

	wg.Wait()

	if successes.Load() != 1 {
		t.Errorf("Expected 1 successful claim, got %d", successes.Load())
	}
}
