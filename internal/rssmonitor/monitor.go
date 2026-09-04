package rssmonitor

import (
	"context"
	"time"
)

const (
	// DefaultInterval is the RSS sample period.
	DefaultInterval = time.Second
	// DefaultDeltaThreshold logs when |delta_rss| exceeds this many bytes.
	DefaultDeltaThreshold = int64(1_000_000)
	// DefaultMinLogInterval is the minimum time between RSS debug logs.
	DefaultMinLogInterval = time.Minute
)

// Config controls the optional process RSS monitor.
type Config struct {
	Interval         time.Duration
	DeltaThreshold   int64
	MinLogInterval   time.Duration
	NumRWConnections func() int64
	NumROConnections func() int64
}

// Run starts the platform RSS monitor goroutine until ctx is cancelled.
// On non-Linux platforms this is a no-op.
func Run(ctx context.Context, cfg Config) {
	run(ctx, cfg)
}
