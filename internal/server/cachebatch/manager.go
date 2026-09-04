// Package cachebatch provides batch cache loading with bounded concurrency.
// It warms the HTTP cache for gallery, info box, and lightbox routes, skips
// already-cached entries, and blocks when discovery is active.
package cachebatch

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
)

const (
	defaultMaxWorkers = 8    // max concurrent batch load workers
	defaultQueueSize  = 1000 // job queue capacity
	throttleDelay     = 50 * time.Millisecond
)

var (
	// runBatchWarm is a testable hook for warming a single cache entry.
	// It dispatches to MakeInternalRequest (full variant, no HX headers) or
	// MakeInternalRequestWithHXTarget (HTMX variants) based on the variant name.
	runBatchWarm = func(ctx context.Context, cfg cachepreload.InternalRequestConfig, path, variant string) error {
		switch variant {
		case "full":
			return cachepreload.MakeInternalRequest(ctx, cfg, path)
		case "gallery-content":
			return cachepreload.MakeInternalRequestWithHXTarget(ctx, cfg, path, "gallery-content")
		case "box_info":
			return cachepreload.MakeInternalRequestWithHXTarget(ctx, cfg, path, "box_info")
		case "lightbox-ui":
			return cachepreload.MakeInternalRequestWithHXTarget(ctx, cfg, path, "lightbox-ui")
		default:
			return cachepreload.MakeInternalRequestWithHXTarget(ctx, cfg, path, variant)
		}
	}

	// numCPU is a testable hook for runtime.NumCPU.
	numCPU = runtime.NumCPU

	// throttleSleep is a testable hook for time.Sleep during throttling.
	throttleSleep = time.Sleep
)

// Manager runs batch cache load with bounded concurrency.
type Manager struct {
	running     atomic.Bool
	config      Config
	metrics     *Metrics
	isThrottled bool
}

// NewManager creates a new BatchLoadManager.
func NewManager(cfg Config) *Manager {
	return &Manager{
		config:  cfg,
		metrics: &Metrics{},
	}
}

// Metrics returns the metrics instance for snapshot/recording.
func (m *Manager) Metrics() *Metrics {
	return m.metrics
}

// GetBatchLoadSnapshot returns a snapshot of the current metrics for the metrics collector.
func (m *Manager) GetBatchLoadSnapshot() Metrics {
	return m.metrics.Snapshot()
}

// Run executes a batch load run. Returns ErrDiscoveryActive if discovery is running,
// ErrAlreadyRunning if another run is in progress, or nil on success.
func (m *Manager) Run(ctx context.Context) error {
	if m.config.ModuleStateService != nil {
		active, err := m.config.ModuleStateService.IsActive(ctx, "discovery")
		if err != nil {
			return err
		}
		if active {
			return ErrDiscoveryActive
		}
	}

	if !m.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	defer m.running.Store(false)

	metrics := m.metrics
	atomic.StoreInt32(&metrics.IsRunning, 1)
	atomic.StoreInt64(&metrics.LastStartedAt, nowUnix())
	defer func() {
		atomic.StoreInt32(&metrics.IsRunning, 0)
		atomic.StoreInt64(&metrics.LastFinishedAt, nowUnix())
	}()

	queries, putQueries := m.config.GetQueries()
	if queries == nil {
		return errors.New("GetQueries returned nil")
	}
	if putQueries != nil {
		defer putQueries()
	}

	targets, err := queries.GetBatchLoadTargets(ctx)
	if err != nil {
		return err
	}

	atomic.StoreInt64(&metrics.TargetsTotal, int64(len(targets)))

	handler := m.config.GetHandler()
	if handler == nil {
		return errNilHandler
	}
	etagVersion := m.config.GetETagVersion()
	if etagVersion == "" {
		etagVersion = "0"
	}

	cfg := cachepreload.InternalRequestConfig{
		Handler:     handler,
		ETagVersion: etagVersion,
	}

	maxWorkers := defaultMaxWorkers
	if n := numCPU(); n < maxWorkers {
		maxWorkers = n
	}
	queueSize := defaultQueueSize

	jobs := make(chan job, queueSize)
	var wg sync.WaitGroup

	// Function to calculate pending queue depth
	pendingCount := func() int {
		return len(jobs)
	}

	for i := 0; i < maxWorkers; i++ {
		wg.Go(func() {
			for j := range jobs {
				m.runJob(ctx, j, queries, cfg, metrics)
			}
		})
	}

	queryStr := "v=" + etagVersion
	for _, t := range targets {
		// Check backpressure and throttle if needed
		utilization := float64(pendingCount()) / float64(queueSize)
		if utilization > 0.95 {
			metrics.RecordBackpressureSkipped()
			slog.Debug("batch load: skipping target due to high backpressure",
				"utilization", utilization, "path", t.Path)
			continue
		}

		// Throttle scheduling when queue is moderately full
		if utilization > 0.8 {
			m.metrics.RecordThrottled()
			if !m.isThrottled {
				m.isThrottled = true
				slog.Debug("batch load: throttling scheduling due to queue utilization",
					"utilization", utilization, "threshold", "0.8")
			}
			throttleSleep(throttleDelay)
			// Reset throttled flag after delay
			m.isThrottled = false
		}

		params := cachelite.CacheKeyParams{
			Method:  "GET",
			Path:    t.Path,
			Query:   queryStr,
			Variant: t.Variant,
		}
		cacheKey := cachelite.NewCacheKey(params)
		exists, err := queries.HttpCacheExistsByKey(ctx, cacheKey)
		if err != nil {
			slog.Warn("batch load: HttpCacheExistsByKey failed", "path", t.Path, "err", err)
			continue
		}
		if exists {
			metrics.RecordSkipped()
			continue
		}
		atomic.AddInt64(&metrics.TargetsScheduled, 1)
		atomic.AddInt64(&metrics.InFlight, 1)
		select {
		case jobs <- job{target: t}:
		case <-ctx.Done():
			atomic.AddInt64(&metrics.InFlight, -1)
			atomic.AddInt64(&metrics.TargetsScheduled, -1)
			goto drain
		}
	}

drain:
	close(jobs)
	wg.Wait()

	return nil
}

type job struct {
	target gallerydb.BatchLoadTarget
}

func (m *Manager) runJob(ctx context.Context, j job, queries BatchLoadQueries, cfg cachepreload.InternalRequestConfig, metrics *Metrics) {
	err := runBatchWarm(ctx, cfg, j.target.Path, j.target.Variant)
	if err != nil {
		metrics.RecordFailed()
		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("batch load request failed", "path", j.target.Path, "err", err)
		}
		return
	}
	metrics.RecordCompleted()
}

func nowUnix() int64 {
	return time.Now().Unix()
}
