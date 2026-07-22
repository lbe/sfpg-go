package server

import (
	"context"
	"time"
)

// quietOnceConfig configures a one-shot background task that runs when
// the system is quiet, or after a max-wait timeout, whichever comes first.
type quietOnceConfig struct {
	PollInterval  time.Duration
	MaxWait       time.Duration
	Listening     func() bool
	Quiet         func(context.Context) bool
	Done          func() bool
	PreAttempt    func() bool
	QuietReason   string
	TimeoutReason string
	Attempt       func(ctx context.Context, reason string) bool
}

// scheduleQuietOnce runs a polling loop in a new goroutine (launched via run)
// that calls cfg.Attempt once the system is quiet and (optionally) listening.
// If the quiet condition is never met, cfg.Attempt is called once with
// cfg.TimeoutReason after cfg.MaxWait elapses.
//
// The loop stops after the first successful Attempt (returns true) on the
// quiet path, or after the single timeout attempt. Callers may supply
// optional Done and PreAttempt gates to short-circuit or skip ticks.
//
// If cfg.Quiet is nil, scheduleQuietOnce returns immediately.
func scheduleQuietOnce(ctx context.Context, cfg quietOnceConfig, run func(func())) {
	if cfg.Quiet == nil {
		return
	}

	run(func() {
		timer := time.NewTimer(cfg.MaxWait)
		ticker := time.NewTicker(cfg.PollInterval)
		defer timer.Stop()
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				cfg.Attempt(ctx, cfg.TimeoutReason)
				return
			case <-ticker.C:
				if cfg.Done != nil && cfg.Done() {
					return
				}
				if cfg.Listening != nil && !cfg.Listening() {
					continue
				}
				if !cfg.Quiet(ctx) {
					continue
				}
				if cfg.PreAttempt != nil && !cfg.PreAttempt() {
					continue
				}
				if cfg.Attempt(ctx, cfg.QuietReason) {
					return
				}
			}
		}
	})
}
