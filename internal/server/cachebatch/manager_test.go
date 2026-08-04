package cachebatch

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
)

type mockQueries struct {
	targets        []gallerydb.BatchLoadTarget
	existsByKey    map[string]bool
	getTargetsErr  error
	existsByKeyErr error
}

func (m *mockQueries) GetBatchLoadTargets(ctx context.Context) ([]gallerydb.BatchLoadTarget, error) {
	if m.getTargetsErr != nil {
		return nil, m.getTargetsErr
	}
	return m.targets, nil
}

func (m *mockQueries) HttpCacheExistsByKey(ctx context.Context, key string) (bool, error) {
	if m.existsByKeyErr != nil {
		return false, m.existsByKeyErr
	}
	if m.existsByKey != nil && m.existsByKey[key] {
		return true, nil
	}
	return false, nil
}

type mockModuleStateService struct {
	active bool
	err    error
}

func (m *mockModuleStateService) IsActive(ctx context.Context, name string) (bool, error) {
	return m.active, m.err
}

func TestManager_Run_BlocksWhenDiscoveryActive(t *testing.T) {
	ctx := context.Background()
	cfg := Config{
		GetQueries:         func() (BatchLoadQueries, func()) { return &mockQueries{}, nil },
		GetHandler:         func() http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}) },
		GetETagVersion:     func() string { return "v1" },
		ModuleStateService: &mockModuleStateService{active: true},
	}

	mgr := NewManager(cfg)
	err := mgr.Run(ctx)
	if !errors.Is(err, ErrDiscoveryActive) {
		t.Errorf("Run() = %v, want ErrDiscoveryActive", err)
	}
}

func TestManager_Run_BlocksWhenAlreadyRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		targets := []gallerydb.BatchLoadTarget{
			{Path: "/gallery/1", Variant: "gallery-content"},
		}
		blockCh := make(chan struct{})
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-blockCh // block until test lets it proceed
			w.WriteHeader(http.StatusOK)
		})
		q := &mockQueries{targets: targets}

		cfg := Config{
			GetQueries:     func() (BatchLoadQueries, func()) { return q, nil },
			GetHandler:     func() http.Handler { return handler },
			GetETagVersion: func() string { return "v1" },
		}

		mgr := NewManager(cfg)

		go func() {
			_ = mgr.Run(ctx)
		}()

		// Wait returns only when the first Run goroutine is durably blocked
		// (its worker is waiting in the handler on blockCh), which happens
		// strictly after the running-lock CAS succeeded and IsRunning was set.
		synctest.Wait()

		// Second run should fail immediately
		err := mgr.Run(ctx)
		if !errors.Is(err, ErrAlreadyRunning) {
			t.Errorf("Run() = %v, want ErrAlreadyRunning", err)
		}

		// Unblock the first run so it finishes and the bubble drains.
		close(blockCh)
		synctest.Wait()
	})
}

func TestManager_Run_SkipsCachedEntries(t *testing.T) {
	ctx := context.Background()
	targets := []gallerydb.BatchLoadTarget{
		{Path: "/gallery/1", Variant: "gallery-content"},
	}
	cacheKey := cachelite.NewCacheKey(cachelite.CacheKeyParams{
		Method:  "GET",
		Path:    "/gallery/1",
		Query:   "v=v1",
		Variant: "gallery-content",
	})
	exists := map[string]bool{cacheKey: true}
	q := &mockQueries{targets: targets, existsByKey: exists}

	callCount := atomic.Int32{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	cfg := Config{
		GetQueries:     func() (BatchLoadQueries, func()) { return q, nil },
		GetHandler:     func() http.Handler { return handler },
		GetETagVersion: func() string { return "v1" },
	}

	mgr := NewManager(cfg)
	err := mgr.Run(ctx)
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}

	snap := mgr.Metrics().Snapshot()
	if snap.TargetsScheduled != 0 {
		t.Errorf("TargetsScheduled = %d, want 0 (cached entries skipped)", snap.TargetsScheduled)
	}
	if snap.TargetsSkipped != 1 {
		t.Errorf("TargetsSkipped = %d, want 1", snap.TargetsSkipped)
	}
	if callCount.Load() != 0 {
		t.Errorf("handler was called %d times, want 0 (cached)", callCount.Load())
	}
}

func TestManager_Run_404CountedAsFailure(t *testing.T) {
	ctx := context.Background()
	targets := []gallerydb.BatchLoadTarget{
		{Path: "/gallery/999", Variant: "gallery-content"},
	}
	q := &mockQueries{targets: targets}

	handler := http.NotFoundHandler()

	cfg := Config{
		GetQueries:     func() (BatchLoadQueries, func()) { return q, nil },
		GetHandler:     func() http.Handler { return handler },
		GetETagVersion: func() string { return "v1" },
	}

	mgr := NewManager(cfg)
	err := mgr.Run(ctx)
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}

	snap := mgr.Metrics().Snapshot()
	if snap.TargetsTotal != 1 {
		t.Errorf("TargetsTotal = %d, want 1", snap.TargetsTotal)
	}
	if snap.TargetsScheduled != 1 {
		t.Errorf("TargetsScheduled = %d, want 1", snap.TargetsScheduled)
	}
	if snap.TargetsFailed != 1 {
		t.Errorf("TargetsFailed = %d, want 1 (404)", snap.TargetsFailed)
	}
	if snap.TargetsCompleted != 0 {
		t.Errorf("TargetsCompleted = %d, want 0", snap.TargetsCompleted)
	}
}

// TestManager_GetBatchLoadSnapshot verifies the metrics collector snapshot helper.
func TestManager_GetBatchLoadSnapshot(t *testing.T) {
	mgr := NewManager(Config{})
	mgr.metrics.TargetsTotal = 7
	mgr.metrics.TargetsScheduled = 6
	mgr.metrics.TargetsCompleted = 5
	mgr.metrics.TargetsFailed = 4
	mgr.metrics.TargetsSkipped = 3
	mgr.metrics.InFlight = 2
	mgr.metrics.IsRunning = 1
	mgr.metrics.LastStartedAt = 1234567890
	mgr.metrics.LastFinishedAt = 1234567891

	snap := mgr.GetBatchLoadSnapshot()
	if snap.TargetsTotal != 7 {
		t.Errorf("TargetsTotal = %d, want 7", snap.TargetsTotal)
	}
	if snap.TargetsScheduled != 6 {
		t.Errorf("TargetsScheduled = %d, want 6", snap.TargetsScheduled)
	}
	if snap.TargetsCompleted != 5 {
		t.Errorf("TargetsCompleted = %d, want 5", snap.TargetsCompleted)
	}
	if snap.TargetsFailed != 4 {
		t.Errorf("TargetsFailed = %d, want 4", snap.TargetsFailed)
	}
	if snap.TargetsSkipped != 3 {
		t.Errorf("TargetsSkipped = %d, want 3", snap.TargetsSkipped)
	}
	if snap.InFlight != 2 {
		t.Errorf("InFlight = %d, want 2", snap.InFlight)
	}
	if snap.IsRunning != 1 {
		t.Errorf("IsRunning = %d, want 1", snap.IsRunning)
	}
	if snap.LastStartedAt != 1234567890 {
		t.Errorf("LastStartedAt = %d, want 1234567890", snap.LastStartedAt)
	}
	if snap.LastFinishedAt != 1234567891 {
		t.Errorf("LastFinishedAt = %d, want 1234567891", snap.LastFinishedAt)
	}
}

func TestManager_Run_SuccessCountsAsCompleted(t *testing.T) {
	ctx := context.Background()
	targets := []gallerydb.BatchLoadTarget{
		{Path: "/gallery/1", Variant: "gallery-content"},
	}
	q := &mockQueries{targets: targets}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cfg := Config{
		GetQueries:     func() (BatchLoadQueries, func()) { return q, nil },
		GetHandler:     func() http.Handler { return handler },
		GetETagVersion: func() string { return "v1" },
	}

	mgr := NewManager(cfg)
	err := mgr.Run(ctx)
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}

	snap := mgr.Metrics().Snapshot()
	if snap.TargetsTotal != 1 {
		t.Errorf("TargetsTotal = %d, want 1", snap.TargetsTotal)
	}
	if snap.TargetsScheduled != 1 {
		t.Errorf("TargetsScheduled = %d, want 1", snap.TargetsScheduled)
	}
	if snap.TargetsCompleted != 1 {
		t.Errorf("TargetsCompleted = %d, want 1", snap.TargetsCompleted)
	}
	if snap.TargetsFailed != 0 {
		t.Errorf("TargetsFailed = %d, want 0", snap.TargetsFailed)
	}
	if snap.InFlight != 0 {
		t.Errorf("InFlight = %d, want 0", snap.InFlight)
	}
}

func TestManager_Run_ErrorPaths(t *testing.T) {
	tests := []struct {
		name       string
		cfg        Config
		wantErr    error
		wantErrMsg string
	}{
		{
			name: "ModuleStateService_error",
			cfg: Config{
				GetQueries:         func() (BatchLoadQueries, func()) { return &mockQueries{}, nil },
				GetHandler:         func() http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}) },
				GetETagVersion:     func() string { return "v1" },
				ModuleStateService: &mockModuleStateService{err: errors.New("isactive failed")},
			},
			wantErrMsg: "isactive failed",
		},
		{
			name: "DiscoveryActive",
			cfg: Config{
				GetQueries:         func() (BatchLoadQueries, func()) { return &mockQueries{}, nil },
				GetHandler:         func() http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}) },
				GetETagVersion:     func() string { return "v1" },
				ModuleStateService: &mockModuleStateService{active: true},
			},
			wantErr: ErrDiscoveryActive,
		},
		{
			name: "NilQueries",
			cfg: Config{
				GetQueries:     func() (BatchLoadQueries, func()) { return nil, nil },
				GetHandler:     func() http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}) },
				GetETagVersion: func() string { return "v1" },
			},
			wantErrMsg: "GetQueries returned nil",
		},
		{
			name: "GetTargetsError",
			cfg: Config{
				GetQueries: func() (BatchLoadQueries, func()) {
					return &mockQueries{getTargetsErr: errors.New("targets failed")}, nil
				},
				GetHandler:     func() http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}) },
				GetETagVersion: func() string { return "v1" },
			},
			wantErrMsg: "targets failed",
		},
		{
			name: "NilHandler",
			cfg: Config{
				GetQueries:     func() (BatchLoadQueries, func()) { return &mockQueries{}, nil },
				GetHandler:     func() http.Handler { return nil },
				GetETagVersion: func() string { return "v1" },
			},
			wantErr: errNilHandler,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mgr := NewManager(tt.cfg)
			err := mgr.Run(ctx)
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("Run() = %v, want error %v", err, tt.wantErr)
			}
			if tt.wantErrMsg != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrMsg)) {
				t.Errorf("Run() = %v, want error containing %q", err, tt.wantErrMsg)
			}
		})
	}
}

func TestManager_Run_PutQueriesCleanupCalled(t *testing.T) {
	ctx := context.Background()
	targets := []gallerydb.BatchLoadTarget{
		{Path: "/gallery/1", Variant: "gallery-content"},
	}
	q := &mockQueries{targets: targets}

	cleanedUp := false
	cleanup := func() { cleanedUp = true }

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cfg := Config{
		GetQueries:     func() (BatchLoadQueries, func()) { return q, cleanup },
		GetHandler:     func() http.Handler { return handler },
		GetETagVersion: func() string { return "v1" },
	}

	mgr := NewManager(cfg)
	if err := mgr.Run(ctx); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !cleanedUp {
		t.Error("putQueries cleanup was not called")
	}
}

func TestManager_Run_DefaultETagVersion(t *testing.T) {
	ctx := context.Background()
	targets := []gallerydb.BatchLoadTarget{
		{Path: "/gallery/1", Variant: "full"},
	}
	q := &mockQueries{targets: targets}

	var rawQuery string
	var mu sync.Mutex
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		rawQuery = r.URL.RawQuery
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	cfg := Config{
		GetQueries:     func() (BatchLoadQueries, func()) { return q, nil },
		GetHandler:     func() http.Handler { return handler },
		GetETagVersion: func() string { return "" },
	}

	mgr := NewManager(cfg)
	if err := mgr.Run(ctx); err != nil {
		t.Fatalf("Run() = %v", err)
	}

	mu.Lock()
	got := rawQuery
	mu.Unlock()
	if got != "v=0" {
		t.Errorf("request query = %q, want %q", got, "v=0")
	}
}

func TestManager_Run_HttpCacheExistsByKeyError(t *testing.T) {
	ctx := context.Background()
	targets := []gallerydb.BatchLoadTarget{
		{Path: "/gallery/1", Variant: "gallery-content"},
	}
	q := &mockQueries{targets: targets, existsByKeyErr: errors.New("exists check failed")}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called when HttpCacheExistsByKey fails")
	})

	cfg := Config{
		GetQueries:     func() (BatchLoadQueries, func()) { return q, nil },
		GetHandler:     func() http.Handler { return handler },
		GetETagVersion: func() string { return "v1" },
	}

	mgr := NewManager(cfg)
	if err := mgr.Run(ctx); err != nil {
		t.Fatalf("Run() = %v", err)
	}

	snap := mgr.Metrics().Snapshot()
	if snap.TargetsScheduled != 0 {
		t.Errorf("TargetsScheduled = %d, want 0", snap.TargetsScheduled)
	}
	if snap.TargetsFailed != 0 {
		t.Errorf("TargetsFailed = %d, want 0", snap.TargetsFailed)
	}
	if snap.TargetsSkipped != 0 {
		t.Errorf("TargetsSkipped = %d, want 0", snap.TargetsSkipped)
	}
}

func TestManager_Run_BackpressureSkipsTargets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		origRunBatchWarm := runBatchWarm
		origNumCPU := numCPU
		origThrottleSleep := throttleSleep
		defer func() {
			runBatchWarm = origRunBatchWarm
			numCPU = origNumCPU
			throttleSleep = origThrottleSleep
		}()

		blockCh := make(chan struct{})
		runBatchWarm = func(ctx context.Context, cfg cachepreload.InternalRequestConfig, path, variant string) error {
			<-blockCh
			return nil
		}
		numCPU = func() int { return 1 }
		throttleSleep = func(time.Duration) {}

		ctx := context.Background()
		targets := make([]gallerydb.BatchLoadTarget, 1200)
		for i := range targets {
			targets[i] = gallerydb.BatchLoadTarget{Path: "/gallery/1", Variant: "gallery-content"}
		}
		q := &mockQueries{targets: targets}

		cfg := Config{
			GetQueries:     func() (BatchLoadQueries, func()) { return q, nil },
			GetHandler:     func() http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}) },
			GetETagVersion: func() string { return "v1" },
		}

		mgr := NewManager(cfg)
		errCh := make(chan error, 1)
		go func() {
			errCh <- mgr.Run(ctx)
		}()

		// Wait returns only when the Run goroutine is durably blocked in
		// wg.Wait, i.e. after the producer loop processed every target and
		// recorded all backpressure skips.
		synctest.Wait()
		if got := mgr.Metrics().Snapshot().BackpressureSkipped; got == 0 {
			t.Error("BackpressureSkipped = 0, want > 0")
		}
		close(blockCh)

		if err := <-errCh; err != nil {
			t.Fatalf("Run() = %v", err)
		}

		snap := mgr.Metrics().Snapshot()
		if snap.BackpressureSkipped == 0 {
			t.Error("BackpressureSkipped = 0, want > 0")
		}
	})
}

func TestManager_Run_ThrottleScheduling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		origRunBatchWarm := runBatchWarm
		origNumCPU := numCPU
		origThrottleSleep := throttleSleep
		defer func() {
			runBatchWarm = origRunBatchWarm
			numCPU = origNumCPU
			throttleSleep = origThrottleSleep
		}()

		blockCh := make(chan struct{})
		runBatchWarm = func(ctx context.Context, cfg cachepreload.InternalRequestConfig, path, variant string) error {
			<-blockCh
			return nil
		}
		numCPU = func() int { return 1 }
		throttleSleep = func(time.Duration) {}

		ctx := context.Background()
		targets := make([]gallerydb.BatchLoadTarget, 850)
		for i := range targets {
			targets[i] = gallerydb.BatchLoadTarget{Path: "/gallery/1", Variant: "gallery-content"}
		}
		q := &mockQueries{targets: targets}

		cfg := Config{
			GetQueries:     func() (BatchLoadQueries, func()) { return q, nil },
			GetHandler:     func() http.Handler { return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}) },
			GetETagVersion: func() string { return "v1" },
		}

		mgr := NewManager(cfg)
		errCh := make(chan error, 1)
		go func() {
			errCh <- mgr.Run(ctx)
		}()

		// Wait returns only when the Run goroutine is durably blocked in
		// wg.Wait, i.e. after the producer loop processed every target and
		// recorded all throttle skips.
		synctest.Wait()
		if got := mgr.Metrics().Snapshot().ThrottlesSkipped; got == 0 {
			t.Error("ThrottlesSkipped = 0, want > 0")
		}
		close(blockCh)

		if err := <-errCh; err != nil {
			t.Fatalf("Run() = %v", err)
		}

		snap := mgr.Metrics().Snapshot()
		if snap.ThrottlesSkipped == 0 {
			t.Error("ThrottlesSkipped = 0, want > 0")
		}
	})
}
