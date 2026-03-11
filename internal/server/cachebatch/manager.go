package cachebatch

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
)

const (
	defaultMaxWorkers = 8
	defaultQueueSize  = 1000
)

// Manager runs batch cache load with bounded concurrency.
type Manager struct {
	running atomic.Bool
	config  Config
	metrics *Metrics
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
	if n := runtime.NumCPU(); n < maxWorkers {
		maxWorkers = n
	}
	queueSize := defaultQueueSize

	jobs := make(chan job, queueSize)
	var wg sync.WaitGroup

	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				m.runJob(ctx, j, queries, cfg, metrics)
			}
		}()
	}

	queryStr := "v=" + etagVersion
	for _, t := range targets {
		cacheKey := cachepreload.GenerateCacheKeyWithHX("GET", t.Path, queryStr, t.Htmx, t.HxTarget, t.Encoding)
		exists, err := queries.HttpCacheExistsByKey(ctx, cacheKey)
		if err != nil {
			slog.Warn("batch load: HttpCacheExistsByKey failed", "path", t.Path, "err", err)
			continue
		}
		if exists != 0 {
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
	err := cachepreload.MakeInternalRequestWithVariant(ctx, cfg, j.target.Path, j.target.HxTarget, j.target.Encoding)
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
