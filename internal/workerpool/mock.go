package workerpool

// MockWorkerContext is a mock implementation of WorkerContext for testing.
type MockWorkerContext struct {
	ShouldStopFunc  func(int) bool
	SubmittedCount  int
	CompletedCount  int
	FailedCount     int
	SuccessfulCount int
}

// ShouldIStop returns true if the mock function says so.
func (m *MockWorkerContext) ShouldIStop(queueLength int) bool {
	if m.ShouldStopFunc != nil {
		return m.ShouldStopFunc(queueLength)
	}
	return false
}

// AddSubmitted increments the submitted counter.
func (m *MockWorkerContext) AddSubmitted() {
	m.SubmittedCount++
}

// AddCompleted increments the completed counter.
func (m *MockWorkerContext) AddCompleted() {
	m.CompletedCount++
}

// AddFailed increments the failed counter.
func (m *MockWorkerContext) AddFailed() {
	m.FailedCount++
}

// AddSuccessful increments the successful counter.
func (m *MockWorkerContext) AddSuccessful() {
	m.SuccessfulCount++
}

// Ensure MockWorkerContext implements WorkerContext
var _ WorkerContext = (*MockWorkerContext)(nil)
