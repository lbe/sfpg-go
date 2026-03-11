package cachebatch

import "sync/atomic"

// Metrics holds batch load run statistics.
type Metrics struct {
	TargetsTotal     int64
	TargetsScheduled int64
	TargetsCompleted int64
	TargetsFailed    int64
	TargetsSkipped   int64
	InFlight         int64
	IsRunning        int32
	LastStartedAt    int64
	LastFinishedAt   int64
}

// Snapshot returns a copy of current metrics.
func (m *Metrics) Snapshot() Metrics {
	return Metrics{
		TargetsTotal:     atomic.LoadInt64(&m.TargetsTotal),
		TargetsScheduled: atomic.LoadInt64(&m.TargetsScheduled),
		TargetsCompleted: atomic.LoadInt64(&m.TargetsCompleted),
		TargetsFailed:    atomic.LoadInt64(&m.TargetsFailed),
		TargetsSkipped:   atomic.LoadInt64(&m.TargetsSkipped),
		InFlight:         atomic.LoadInt64(&m.InFlight),
		IsRunning:        atomic.LoadInt32(&m.IsRunning),
		LastStartedAt:    atomic.LoadInt64(&m.LastStartedAt),
		LastFinishedAt:   atomic.LoadInt64(&m.LastFinishedAt),
	}
}

// RecordCompleted increments completed and decrements in-flight.
func (m *Metrics) RecordCompleted() {
	atomic.AddInt64(&m.TargetsCompleted, 1)
	atomic.AddInt64(&m.InFlight, -1)
}

// RecordFailed increments failed and decrements in-flight.
func (m *Metrics) RecordFailed() {
	atomic.AddInt64(&m.TargetsFailed, 1)
	atomic.AddInt64(&m.InFlight, -1)
}

// RecordSkipped increments skipped.
func (m *Metrics) RecordSkipped() {
	atomic.AddInt64(&m.TargetsSkipped, 1)
}
