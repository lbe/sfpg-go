package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestScheduleQuietOnce_QuietTrigger(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var listening atomic.Bool
		var quietCalls atomic.Int32
		var attemptCalled atomic.Bool
		var attemptReason atomic.Value

		cfg := quietOnceConfig{
			PollInterval: 5 * time.Millisecond,
			MaxWait:      time.Hour,
			Listening:    listening.Load,
			Quiet: func(ctx context.Context) bool {
				if !listening.Load() {
					return false
				}
				return quietCalls.Add(1) >= 2
			},
			QuietReason:   "quiet-reason",
			TimeoutReason: "timeout-reason",
			Attempt: func(ctx context.Context, reason string) bool {
				attemptCalled.Store(true)
				attemptReason.Store(reason)
				return true
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var wg sync.WaitGroup
		wg.Add(1)
		scheduleQuietOnce(ctx, cfg, func(fn func()) {
			wg.Done()
			go fn()
		})
		wg.Wait()

		// Listening is false initially; the attempt must not run. Advance the
		// bubble clock so the worker processes several ticks before asserting.
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if attemptCalled.Load() {
			t.Fatal("attempt should not be called when listening is false")
		}

		listening.Store(true)

		// Drive the bubble clock until the quiet condition (two quiet ticks)
		// triggers the attempt.
		for i := 0; i < 1000 && !attemptCalled.Load(); i++ {
			time.Sleep(5 * time.Millisecond)
			synctest.Wait()
		}
		if !attemptCalled.Load() {
			t.Fatal("expected attempt to be called when quiet + listening")
		}
		if got, _ := attemptReason.Load().(string); got != "quiet-reason" {
			t.Fatalf("attempt reason = %q, want %q", got, "quiet-reason")
		}
	})
}

func TestScheduleQuietOnce_TimeoutFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attemptCalled atomic.Bool
		var attemptReason atomic.Value

		cfg := quietOnceConfig{
			PollInterval:  time.Hour,
			MaxWait:       15 * time.Millisecond,
			Quiet:         func(ctx context.Context) bool { return false },
			TimeoutReason: "timeout-reason",
			Attempt: func(ctx context.Context, reason string) bool {
				attemptCalled.Store(true)
				attemptReason.Store(reason)
				return true
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var wg sync.WaitGroup
		wg.Add(1)
		scheduleQuietOnce(ctx, cfg, func(fn func()) {
			wg.Done()
			go fn()
		})
		wg.Wait()

		// Drive the bubble clock until the MaxWait timer fires and the
		// timeout fallback runs the attempt.
		for i := 0; i < 1000 && !attemptCalled.Load(); i++ {
			time.Sleep(5 * time.Millisecond)
			synctest.Wait()
		}
		if !attemptCalled.Load() {
			t.Fatal("expected timeout fallback to trigger attempt")
		}
		if got, _ := attemptReason.Load().(string); got != "timeout-reason" {
			t.Fatalf("attempt reason = %q, want %q", got, "timeout-reason")
		}
	})
}

func TestScheduleQuietOnce_DoneShortCircuit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var done atomic.Bool
		var attemptCalled atomic.Bool

		cfg := quietOnceConfig{
			PollInterval: 5 * time.Millisecond,
			MaxWait:      time.Hour,
			Quiet:        func(ctx context.Context) bool { return true },
			Done:         done.Load,
			Attempt: func(ctx context.Context, reason string) bool {
				attemptCalled.Store(true)
				return true
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done.Store(true)

		var wg sync.WaitGroup
		wg.Add(1)
		scheduleQuietOnce(ctx, cfg, func(fn func()) {
			wg.Done()
			go fn()
		})
		wg.Wait()

		// Done is true, so the worker must exit on its first tick without
		// calling Attempt. Advance the bubble clock, then assert.
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if attemptCalled.Load() {
			t.Fatal("attempt should not be called when Done returns true")
		}
	})
}

func TestScheduleQuietOnce_PreAttemptSkips(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var preAttempt atomic.Bool
		var attemptCalls atomic.Int32

		cfg := quietOnceConfig{
			PollInterval: 5 * time.Millisecond,
			MaxWait:      time.Hour,
			Quiet:        func(ctx context.Context) bool { return true },
			PreAttempt:   preAttempt.Load,
			Attempt: func(ctx context.Context, reason string) bool {
				attemptCalls.Add(1)
				return true
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var wg sync.WaitGroup
		wg.Add(1)
		scheduleQuietOnce(ctx, cfg, func(fn func()) {
			wg.Done()
			go fn()
		})
		wg.Wait()

		// PreAttempt is false; the attempt must be skipped. Advance the bubble
		// clock so the worker processes several ticks before asserting.
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if attemptCalls.Load() != 0 {
			t.Fatal("attempt should be skipped when PreAttempt returns false")
		}

		preAttempt.Store(true)

		// Drive the bubble clock until the next tick passes the PreAttempt gate.
		for i := 0; i < 1000 && attemptCalls.Load() == 0; i++ {
			time.Sleep(5 * time.Millisecond)
			synctest.Wait()
		}
		if attemptCalls.Load() == 0 {
			t.Fatal("expected attempt after PreAttempt returns true")
		}
	})
}

func TestScheduleQuietOnce_AttemptFailureRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attemptCalls atomic.Int32

		cfg := quietOnceConfig{
			PollInterval: 5 * time.Millisecond,
			MaxWait:      time.Hour,
			Quiet:        func(ctx context.Context) bool { return true },
			Attempt: func(ctx context.Context, reason string) bool {
				attemptCalls.Add(1)
				return attemptCalls.Load() >= 3
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var wg sync.WaitGroup
		wg.Add(1)
		scheduleQuietOnce(ctx, cfg, func(fn func()) {
			wg.Done()
			go fn()
		})
		wg.Wait()

		// Drive the bubble clock until Attempt has been called (and failed)
		// three times, proving failed attempts are retried.
		for i := 0; i < 1000 && attemptCalls.Load() < 3; i++ {
			time.Sleep(5 * time.Millisecond)
			synctest.Wait()
		}
		if attemptCalls.Load() < 3 {
			t.Fatalf("attemptCalls = %d, want at least 3 (retry on failure)", attemptCalls.Load())
		}
	})
}

func TestScheduleQuietOnce_ContextCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attemptCalled atomic.Bool

		cfg := quietOnceConfig{
			PollInterval: time.Hour,
			MaxWait:      time.Hour,
			Quiet:        func(ctx context.Context) bool { return true },
			Attempt: func(ctx context.Context, reason string) bool {
				attemptCalled.Store(true)
				return true
			},
		}

		ctx, cancel := context.WithCancel(context.Background())

		var wg sync.WaitGroup
		wg.Add(1)
		scheduleQuietOnce(ctx, cfg, func(fn func()) {
			wg.Done()
			go fn()
		})
		wg.Wait()

		cancel()

		// After cancellation the worker must exit without calling Attempt.
		synctest.Wait()
		if attemptCalled.Load() {
			t.Fatal("attempt should not be called after context cancellation")
		}
	})
}
