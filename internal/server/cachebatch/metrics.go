package cachebatch

import "sync/atomic"

// Metrics holds batch load run statistics.
// All int64 fields are updated with sync/atomic; use Snapshot for a consistent view.
type Metrics struct {
	TargetsTotal        int64 // total targets from GetBatchLoadTargets
	TargetsScheduled    int64 // enqueued for processing
	TargetsCompleted    int64 // completed successfully
	TargetsFailed       int64 // request failed
	TargetsSkipped      int64 // already cached (HttpCacheExistsByKey)
	ThrottlesSkipped    int64 // number of times scheduling was delayed (queue >80% full)
	BackpressureSkipped int64 // targets skipped due to backpressure (queue >95% full)
	InFlight            int64 // currently in progress
	IsRunning           int32 // 1 if a run is active, 0 otherwise
	LastStartedAt       int64 // Unix time of last Run start
	LastFinishedAt      int64 // Unix time of last Run finish
}

// Snapshot returns a copy of current metrics.
func (m *Metrics) Snapshot() Metrics {
	return Metrics{
		TargetsTotal:        atomic.LoadInt64(&m.TargetsTotal),
		TargetsScheduled:    atomic.LoadInt64(&m.TargetsScheduled),
		TargetsCompleted:    atomic.LoadInt64(&m.TargetsCompleted),
		TargetsFailed:       atomic.LoadInt64(&m.TargetsFailed),
		TargetsSkipped:      atomic.LoadInt64(&m.TargetsSkipped),
		ThrottlesSkipped:    atomic.LoadInt64(&m.ThrottlesSkipped),
		BackpressureSkipped: atomic.LoadInt64(&m.BackpressureSkipped),
		InFlight:            atomic.LoadInt64(&m.InFlight),
		IsRunning:           atomic.LoadInt32(&m.IsRunning),
		LastStartedAt:       atomic.LoadInt64(&m.LastStartedAt),
		LastFinishedAt:      atomic.LoadInt64(&m.LastFinishedAt),
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

// RecordThrottled increments the throttle counter (scheduling was delayed due to high queue utilization).
func (m *Metrics) RecordThrottled() {
	atomic.AddInt64(&m.ThrottlesSkipped, 1)
}

// RecordBackpressureSkipped increments backpressure skipped.
func (m *Metrics) RecordBackpressureSkipped() {
	atomic.AddInt64(&m.BackpressureSkipped, 1)
}
