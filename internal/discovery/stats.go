package discovery

import (
	"sync/atomic"

	"github.com/lbe/sfpg-go/internal/server/metrics"
)

// Stats are the counters of the current or last run.
type Stats struct {
	TotalFound      atomic.Uint64 // image files the walk saw
	AlreadyExisting atomic.Uint64 // unchanged since their import
	NewlyInserted   atomic.Uint64 // processed and submitted for writing
	SkippedInvalid  atomic.Uint64 // invalid, unchanged or newly recorded
	InFlight        atomic.Int64  // taken by a worker and not finished
}

// reset zeroes the run counters. InFlight is left alone: it counts files
// workers hold now, which may include files from a canceled run.
func (s *Stats) reset() {
	s.TotalFound.Store(0)
	s.AlreadyExisting.Store(0)
	s.NewlyInserted.Store(0)
	s.SkippedInvalid.Store(0)
}

// GetStats returns the counters for the metrics dashboard.
func (s *Stats) GetStats() metrics.FileProcessingMetrics {
	return metrics.FileProcessingMetrics{
		TotalFound:      s.TotalFound.Load(),
		AlreadyExisting: s.AlreadyExisting.Load(),
		NewlyInserted:   s.NewlyInserted.Load(),
		SkippedInvalid:  s.SkippedInvalid.Load(),
		InFlight:        s.InFlight.Load(),
	}
}
