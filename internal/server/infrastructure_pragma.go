package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

const (
	defaultPragmaOptimizePollInterval = 30 * time.Second
	defaultPragmaOptimizeMaxWait      = time.Hour
)

// SetPragmaOptimizeListening enables the startup PRAGMA optimize to proceed once
// the system is quiet. Must be called after the HTTP server calls onServerListening.
func (s *InfrastructureService) SetPragmaOptimizeListening(listening bool) {
	s.pragmaOptimizeListening.Store(listening)
}

// SchedulePragmaOptimize schedules a one-shot PRAGMA optimize that runs when
// the system is quiet (quiet callback returns true) and the server is listening.
//
// For mask == PragmaOptimizeFreshConnection (0x10002), the startup CAS ensures
// this runs at most once per process lifetime. Other masks bypass the CAS so
// event-driven callers (discovery, migration) can schedule independently.
//
// Parameters:
//   - quiet: function that reports whether the system is idle enough
//   - run:   function to launch the background goroutine (e.g. wg.Go)
func (s *InfrastructureService) SchedulePragmaOptimize(ctx context.Context, mask int, reason string, quiet func(ctx context.Context) bool, run func(func())) {
	if mask == dbconnpool.PragmaOptimizeFreshConnection {
		if !s.startupPragmaOptimizeStarted.CompareAndSwap(false, true) {
			return
		}
	}

	if quiet == nil {
		return
	}

	scheduleQuietOnce(ctx, quietOnceConfig{
		PollInterval:  s.pragmaOptimizePollInterval(),
		MaxWait:       s.pragmaOptimizeMaxWait(),
		Listening:     func() bool { return s.pragmaOptimizeListening.Load() },
		Quiet:         quiet,
		Done:          nil,
		PreAttempt:    func() bool { return s.dbRwPool != nil },
		QuietReason:   reason,
		TimeoutReason: reason + "-timeout",
		Attempt:       func(ctx context.Context, r string) bool { return s.attemptPragmaOptimize(ctx, mask, r) },
	}, run)
}

func (s *InfrastructureService) pragmaOptimizePollInterval() time.Duration {
	if s.testSeams.PragmaOptimizePollInterval > 0 {
		return s.testSeams.PragmaOptimizePollInterval
	}
	return defaultPragmaOptimizePollInterval
}

func (s *InfrastructureService) pragmaOptimizeMaxWait() time.Duration {
	if s.testSeams.PragmaOptimizeMaxWait > 0 {
		return s.testSeams.PragmaOptimizeMaxWait
	}
	return defaultPragmaOptimizeMaxWait
}

// attemptPragmaOptimize runs PRAGMA optimize on the RW pool. Returns true on
// success so callers can stop polling.
func (s *InfrastructureService) attemptPragmaOptimize(ctx context.Context, mask int, reason string) bool {
	if s.dbRwPool == nil {
		return false
	}

	if s.testSeams.PragmaOptimize != nil {
		s.testSeams.PragmaOptimize(ctx, s.dbRwPool)
		return true
	}

	cpc, err := s.dbRwPool.Get()
	if err != nil {
		slog.Warn("PRAGMA optimize: failed to get connection", "reason", reason, "err", err)
		return false
	}
	defer s.dbRwPool.Put(cpc)

	if err := dbconnpool.RunPragmaOptimize(ctx, cpc.Conn, mask); err != nil {
		slog.Warn("PRAGMA optimize failed",
			"reason", reason,
			"mask", mask,
			"err", err,
		)
		return false
	}
	return true
}

// runShutdownPragmaOptimize performs a plain PRAGMA optimize during shutdown.
// Uses a context with timeout. No-op when the RW pool is nil.
func (s *InfrastructureService) runShutdownPragmaOptimize(ctx context.Context) {
	if s.dbRwPool == nil {
		return
	}

	cpc, err := s.dbRwPool.Get()
	if err != nil {
		slog.Warn("shutdown PRAGMA optimize: failed to get connection", "err", err)
		return
	}
	defer s.dbRwPool.Put(cpc)

	if err := dbconnpool.RunPragmaOptimize(ctx, cpc.Conn, dbconnpool.PragmaOptimizeDefault); err != nil {
		slog.Warn("shutdown PRAGMA optimize failed", "err", err)
	}
}
