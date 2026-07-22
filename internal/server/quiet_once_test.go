package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduleQuietOnce_QuietTrigger(t *testing.T) {
	var listening atomic.Bool
	var quietCalls atomic.Int32
	var attemptCalled atomic.Bool
	var attemptReason string

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
			attemptReason = reason
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

	// Listening is false initially; attempt should not be called.
	time.Sleep(20 * time.Millisecond)
	if attemptCalled.Load() {
		t.Fatal("attempt should not be called when listening is false")
	}

	listening.Store(true)

	deadline := time.Now().Add(2 * time.Second)
	for !attemptCalled.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !attemptCalled.Load() {
		t.Fatal("expected attempt to be called when quiet + listening")
	}
	if attemptReason != "quiet-reason" {
		t.Fatalf("attempt reason = %q, want %q", attemptReason, "quiet-reason")
	}
}

func TestScheduleQuietOnce_TimeoutFallback(t *testing.T) {
	var attemptCalled atomic.Bool
	var attemptReason string

	cfg := quietOnceConfig{
		PollInterval:  time.Hour,
		MaxWait:       15 * time.Millisecond,
		Quiet:         func(ctx context.Context) bool { return false },
		TimeoutReason: "timeout-reason",
		Attempt: func(ctx context.Context, reason string) bool {
			attemptCalled.Store(true)
			attemptReason = reason
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

	deadline := time.Now().Add(2 * time.Second)
	for !attemptCalled.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !attemptCalled.Load() {
		t.Fatal("expected timeout fallback to trigger attempt")
	}
	if attemptReason != "timeout-reason" {
		t.Fatalf("attempt reason = %q, want %q", attemptReason, "timeout-reason")
	}
}

func TestScheduleQuietOnce_DoneShortCircuit(t *testing.T) {
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

	time.Sleep(30 * time.Millisecond)
	if attemptCalled.Load() {
		t.Fatal("attempt should not be called when Done returns true")
	}
}

func TestScheduleQuietOnce_PreAttemptSkips(t *testing.T) {
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

	// PreAttempt is false; attempt should be skipped.
	time.Sleep(20 * time.Millisecond)
	if attemptCalls.Load() != 0 {
		t.Fatal("attempt should be skipped when PreAttempt returns false")
	}

	preAttempt.Store(true)

	deadline := time.Now().Add(2 * time.Second)
	for attemptCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if attemptCalls.Load() == 0 {
		t.Fatal("expected attempt after PreAttempt returns true")
	}
}

func TestScheduleQuietOnce_AttemptFailureRetries(t *testing.T) {
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

	deadline := time.Now().Add(2 * time.Second)
	for attemptCalls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if attemptCalls.Load() < 3 {
		t.Fatalf("attemptCalls = %d, want at least 3 (retry on failure)", attemptCalls.Load())
	}
}

func TestScheduleQuietOnce_ContextCancel(t *testing.T) {
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

	time.Sleep(5 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	if attemptCalled.Load() {
		t.Fatal("attempt should not be called after context cancellation")
	}
}
