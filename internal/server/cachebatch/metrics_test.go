package cachebatch

import "testing"

func TestMetrics_RecordHelpers(t *testing.T) {
	t.Run("RecordCompleted", func(t *testing.T) {
		m := &Metrics{InFlight: 1}
		m.RecordCompleted()
		snap := m.Snapshot()
		if snap.TargetsCompleted != 1 {
			t.Errorf("TargetsCompleted = %d, want 1", snap.TargetsCompleted)
		}
		if snap.InFlight != 0 {
			t.Errorf("InFlight = %d, want 0", snap.InFlight)
		}
	})

	t.Run("RecordThrottled", func(t *testing.T) {
		m := &Metrics{}
		m.RecordThrottled()
		snap := m.Snapshot()
		if snap.ThrottlesSkipped != 1 {
			t.Errorf("ThrottlesSkipped = %d, want 1", snap.ThrottlesSkipped)
		}
	})

	t.Run("RecordBackpressureSkipped", func(t *testing.T) {
		m := &Metrics{}
		m.RecordBackpressureSkipped()
		snap := m.Snapshot()
		if snap.BackpressureSkipped != 1 {
			t.Errorf("BackpressureSkipped = %d, want 1", snap.BackpressureSkipped)
		}
	})
}
