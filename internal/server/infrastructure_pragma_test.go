package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/database"
)

func TestInfrastructureService_SchedulePragmaOptimize_QuietTrigger(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    newFakePool(10, 2),
		setupRo:    newFakePool(10, 2),
	}
	infra.SetupDB(context.Background(), nil)
	infra.testSeams.PragmaOptimizePollInterval = 5 * time.Millisecond
	infra.testSeams.PragmaOptimizeMaxWait = time.Hour

	var optimizeCalled atomic.Int32
	infra.testSeams.PragmaOptimize = func(ctx context.Context, pool dbPoolForCheckpoint) {
		optimizeCalled.Add(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var quietCalls atomic.Int32
	quiet := func(ctx context.Context) bool {
		if !infra.pragmaOptimizeListening.Load() {
			return false
		}
		return quietCalls.Add(1) >= 2
	}

	var wg sync.WaitGroup
	wg.Add(1)
	infra.SchedulePragmaOptimize(ctx, dbconnpool.PragmaOptimizeDefault, "test", quiet, func(fn func()) {
		wg.Done()
		go fn()
	})
	wg.Wait()

	infra.SetPragmaOptimizeListening(true)

	deadline := time.Now().Add(2 * time.Second)
	for optimizeCalled.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if optimizeCalled.Load() == 0 {
		t.Fatal("expected PRAGMA optimize to be called when quiet + listening")
	}
}

func TestInfrastructureService_SchedulePragmaOptimize_StartupCAS(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    newFakePool(10, 2),
		setupRo:    newFakePool(10, 2),
	}
	infra.SetupDB(context.Background(), nil)
	infra.testSeams.PragmaOptimizePollInterval = 5 * time.Millisecond
	infra.testSeams.PragmaOptimizeMaxWait = time.Hour

	calls := 0
	var mu sync.Mutex
	infra.testSeams.PragmaOptimize = func(ctx context.Context, pool dbPoolForCheckpoint) {
		mu.Lock()
		calls++
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quiet := func(ctx context.Context) bool { return true }

	var wg sync.WaitGroup
	wg.Add(1)
	infra.SchedulePragmaOptimize(ctx, dbconnpool.PragmaOptimizeFreshConnection, "first", quiet, func(fn func()) {
		wg.Done()
		go fn()
	})
	wg.Wait()

	infra.SetPragmaOptimizeListening(true)

	// Second call with FreshConnection should be a no-op due to CAS.
	// Since CAS fails, SchedulePragmaOptimize returns immediately without
	// calling run(), so we must NOT wait on a wg inside the run callback.
	infra.SchedulePragmaOptimize(ctx, dbconnpool.PragmaOptimizeFreshConnection, "second", quiet, func(fn func()) {
		go fn()
	})

	// First goroutine should complete quickly (quiet + listening).
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("expected exactly 1 optimize call due to CAS, got %d", gotCalls)
	}
}

func TestInfrastructureService_RunShutdownPragmaOptimize_NilPool(t *testing.T) {
	infra := NewInfrastructureService()
	// dbRwPool is nil by default; runShutdownPragmaOptimize must be a no-op.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// Should not panic or hang.
	infra.runShutdownPragmaOptimize(ctx)
}

func TestInfrastructureService_SchedulePragmaOptimize_TimeoutFallback(t *testing.T) {
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: "/tmp/fake/sfpg.db"},
		setupRw:    newFakePool(10, 2),
		setupRo:    newFakePool(10, 2),
	}
	infra.SetupDB(context.Background(), nil)
	infra.testSeams.PragmaOptimizePollInterval = time.Hour
	infra.testSeams.PragmaOptimizeMaxWait = 15 * time.Millisecond

	var optimizeCalled atomic.Int32
	infra.testSeams.PragmaOptimize = func(ctx context.Context, pool dbPoolForCheckpoint) {
		optimizeCalled.Add(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quiet := func(ctx context.Context) bool { return false }

	var wg sync.WaitGroup
	wg.Add(1)
	infra.SchedulePragmaOptimize(ctx, dbconnpool.PragmaOptimizeDefault, "test", quiet, func(fn func()) {
		wg.Done()
		go fn()
	})
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for optimizeCalled.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if optimizeCalled.Load() == 0 {
		t.Fatal("expected timeout fallback to trigger PRAGMA optimize")
	}
}
