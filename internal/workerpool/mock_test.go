package workerpool

import (
	"testing"
)

// TestMockWorkerContext demonstrates fast worker testing without real pool.
func TestMockWorkerContext(t *testing.T) {
	mock := &MockWorkerContext{
		ShouldStopFunc: func(queueLength int) bool {
			return queueLength < 5
		},
	}

	// Test ShouldIStop
	if !mock.ShouldIStop(3) {
		t.Error("expected ShouldIStop(3) to be true")
	}
	if mock.ShouldIStop(10) {
		t.Error("expected ShouldIStop(10) to be false")
	}

	// Test counters
	mock.AddSubmitted()
	mock.AddSubmitted()
	if mock.SubmittedCount != 2 {
		t.Errorf("expected SubmittedCount 2, got %d", mock.SubmittedCount)
	}

	mock.AddCompleted()
	if mock.CompletedCount != 1 {
		t.Errorf("expected CompletedCount 1, got %d", mock.CompletedCount)
	}

	mock.AddFailed()
	if mock.FailedCount != 1 {
		t.Errorf("expected FailedCount 1, got %d", mock.FailedCount)
	}

	mock.AddSuccessful()
	if mock.SuccessfulCount != 1 {
		t.Errorf("expected SuccessfulCount 1, got %d", mock.SuccessfulCount)
	}
}

// TestMockWorkerContext_NoFunc tests default behavior.
func TestMockWorkerContext_NoFunc(t *testing.T) {
	mock := &MockWorkerContext{}

	// Without ShouldStopFunc, should always return false
	if mock.ShouldIStop(0) {
		t.Error("expected ShouldIStop to return false when no func set")
	}
	if mock.ShouldIStop(100) {
		t.Error("expected ShouldIStop to return false when no func set")
	}
}

// TestWorkerLogicWithMock demonstrates testing worker decision logic in isolation.
func TestWorkerLogicWithMock(t *testing.T) {
	tests := []struct {
		name        string
		queueLength int
		wantStop    bool
	}{
		{"empty queue", 0, true},
		{"small queue", 2, true},
		{"threshold", 4, true},
		{"above threshold", 5, false},
		{"large queue", 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &MockWorkerContext{
				ShouldStopFunc: func(queueLength int) bool {
					// Worker stops if queue has less than 5 items
					return queueLength < 5
				},
			}

			got := mock.ShouldIStop(tt.queueLength)
			if got != tt.wantStop {
				t.Errorf("ShouldIStop(%d) = %v, want %v", tt.queueLength, got, tt.wantStop)
			}
		})
	}
}

// BenchmarkMockWorkerContext shows performance vs real pool.
func BenchmarkMockWorkerContext(b *testing.B) {
	mock := &MockWorkerContext{
		ShouldStopFunc: func(queueLength int) bool {
			return queueLength == 0
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mock.ShouldIStop(i % 10)
		mock.AddSubmitted()
		mock.AddCompleted()
	}
}
