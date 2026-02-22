package files

import (
	"sync"
	"testing"
)

// TestProcessingStats_Reset verifies Reset behavior.
func TestProcessingStats_Reset(t *testing.T) {
	t.Run("resets all counters to zero", func(t *testing.T) {
		stats := &ProcessingStats{}
		stats.TotalFound.Store(100)
		stats.AlreadyExisting.Store(50)
		stats.NewlyInserted.Store(30)
		stats.SkippedInvalid.Store(10)
		stats.InFlight.Store(5)

		stats.Reset()

		if stats.TotalFound.Load() != 0 {
			t.Errorf("TotalFound: got %d, want 0", stats.TotalFound.Load())
		}
		if stats.AlreadyExisting.Load() != 0 {
			t.Errorf("AlreadyExisting: got %d, want 0", stats.AlreadyExisting.Load())
		}
		if stats.NewlyInserted.Load() != 0 {
			t.Errorf("NewlyInserted: got %d, want 0", stats.NewlyInserted.Load())
		}
		if stats.SkippedInvalid.Load() != 0 {
			t.Errorf("SkippedInvalid: got %d, want 0", stats.SkippedInvalid.Load())
		}
		if stats.InFlight.Load() != 0 {
			t.Errorf("InFlight: got %d, want 0", stats.InFlight.Load())
		}
	})

	t.Run("reset is idempotent", func(t *testing.T) {
		stats := &ProcessingStats{}
		stats.TotalFound.Store(100)

		stats.Reset()
		stats.Reset()

		if stats.TotalFound.Load() != 0 {
			t.Errorf("TotalFound after double reset: got %d, want 0", stats.TotalFound.Load())
		}
	})

	t.Run("concurrent reset is safe", func(t *testing.T) {
		stats := &ProcessingStats{}
		stats.TotalFound.Store(100)
		stats.AlreadyExisting.Store(50)
		stats.NewlyInserted.Store(30)

		done := make(chan struct{})
		for i := 0; i < 10; i++ {
			go func() {
				for j := 0; j < 100; j++ {
					stats.Reset()
				}
				done <- struct{}{}
			}()
			go func() {
				for j := 0; j < 100; j++ {
					stats.TotalFound.Add(1)
					stats.AlreadyExisting.Add(1)
					stats.NewlyInserted.Add(1)
				}
				done <- struct{}{}
			}()
		}

		for i := 0; i < 20; i++ {
			<-done
		}

		// After all concurrent operations, the values should still be valid integers
		// The exact value depends on timing, but it should be reasonable
		totalFound := stats.TotalFound.Load()
		if totalFound > 0 { // uint64 is always >= 0, check for positive value
			t.Logf("TotalFound positive after concurrent operations: %d", totalFound)
		}
	})
}

// TestProcessingStats_Reset_AllZeros verifies Reset on zero state.
func TestProcessingStats_Reset_AllZeros(t *testing.T) {
	stats := &ProcessingStats{}

	// All values should start at zero
	if stats.TotalFound.Load() != 0 {
		t.Errorf("Initial TotalFound: got %d, want 0", stats.TotalFound.Load())
	}

	// Reset should be safe even on zero values
	stats.Reset()

	if stats.TotalFound.Load() != 0 {
		t.Errorf("TotalFound after reset: got %d, want 0", stats.TotalFound.Load())
	}
}

// TestProcessingStats_Reset_WithValues tests Reset after operations.
func TestProcessingStats_Reset_WithValues(t *testing.T) {
	stats := &ProcessingStats{}

	// Simulate some operations
	stats.TotalFound.Add(10)
	stats.AlreadyExisting.Add(5)
	stats.NewlyInserted.Add(3)
	stats.SkippedInvalid.Add(2)
	stats.InFlight.Add(7)

	// Verify values were set
	if stats.TotalFound.Load() != 10 {
		t.Errorf("Before reset TotalFound: got %d, want 10", stats.TotalFound.Load())
	}

	stats.Reset()

	// Verify all are zero
	if stats.TotalFound.Load() != 0 {
		t.Errorf("After reset TotalFound: got %d, want 0", stats.TotalFound.Load())
	}
	if stats.AlreadyExisting.Load() != 0 {
		t.Errorf("After reset AlreadyExisting: got %d, want 0", stats.AlreadyExisting.Load())
	}
	if stats.NewlyInserted.Load() != 0 {
		t.Errorf("After reset NewlyInserted: got %d, want 0", stats.NewlyInserted.Load())
	}
	if stats.SkippedInvalid.Load() != 0 {
		t.Errorf("After reset SkippedInvalid: got %d, want 0", stats.SkippedInvalid.Load())
	}
	if stats.InFlight.Load() != 0 {
		t.Errorf("After reset InFlight: got %d, want 0", stats.InFlight.Load())
	}
}

// TestProcessingStats_ConcurrentAccess tests thread safety of all operations.
func TestProcessingStats_ConcurrentAccess(t *testing.T) {
	stats := &ProcessingStats{}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				stats.TotalFound.Add(1)
				stats.AlreadyExisting.Add(1)
				stats.NewlyInserted.Add(1)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				stats.Reset()
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = stats.TotalFound.Load()
				_ = stats.AlreadyExisting.Load()
				_ = stats.NewlyInserted.Load()
			}
		}()
	}

	wg.Wait()

	// Final values should be reasonable (uint64 is always non-negative)
	_ = stats.TotalFound.Load() // Verify values are accessible
	_ = stats.AlreadyExisting.Load()
	_ = stats.NewlyInserted.Load()
}
