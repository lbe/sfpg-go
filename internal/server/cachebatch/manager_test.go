package cachebatch

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

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

func (m *mockQueries) HttpCacheExistsByKey(ctx context.Context, key string) (int64, error) {
	if m.existsByKeyErr != nil {
		return 0, m.existsByKeyErr
	}
	if m.existsByKey != nil && m.existsByKey[key] {
		return 1, nil
	}
	return 0, nil
}

func TestManager_Run_BlocksWhenDiscoveryActive(t *testing.T) {
	// Uses real modulestate with test DB - see integration test.
	t.Skip("discovery-active guard tested in integration test with real modulestate")
}

func TestManager_Run_BlocksWhenAlreadyRunning(t *testing.T) {
	ctx := context.Background()
	targets := []gallerydb.BatchLoadTarget{
		{Path: "/gallery/1", Htmx: "true", HxTarget: "gallery-content", Encoding: "gzip"},
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

	started := make(chan struct{})
	go func() {
		close(started)
		_ = mgr.Run(ctx)
		close(blockCh)
	}()
	<-started
	// Give first Run time to enter and start processing
	// (it will block in handler until we trigger ErrAlreadyRunning)

	// Second run should fail immediately
	err := mgr.Run(ctx)
	if err != ErrAlreadyRunning {
		t.Errorf("Run() = %v, want ErrAlreadyRunning", err)
	}
}

func TestManager_Run_SkipsCachedEntries(t *testing.T) {
	ctx := context.Background()
	targets := []gallerydb.BatchLoadTarget{
		{Path: "/gallery/1", Htmx: "true", HxTarget: "gallery-content", Encoding: "gzip"},
	}
	cacheKey := cachepreload.GenerateCacheKeyWithHX("GET", "/gallery/1", "v=v1", "true", "gallery-content", "gzip")
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
		{Path: "/gallery/999", Htmx: "true", HxTarget: "gallery-content", Encoding: "gzip"},
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
