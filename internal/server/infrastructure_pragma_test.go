package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/database"
)

func TestInfrastructureService_SchedulePragmaOptimize_QuietTrigger(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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

		infra.SchedulePragmaOptimize(ctx, dbconnpool.PragmaOptimizeDefault, "test", quiet, func(fn func()) {
			go fn()
		})

		infra.SetPragmaOptimizeListening(true)

		// Bubble time (fake clock): let the poll loop reach the quiet
		// condition, then wait until the scheduled goroutine is done.
		time.Sleep(1 * time.Second)
		synctest.Wait()
		if optimizeCalled.Load() == 0 {
			t.Fatal("expected PRAGMA optimize to be called when quiet + listening")
		}
	})
}

func TestInfrastructureService_SchedulePragmaOptimize_StartupCAS(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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

		infra.SchedulePragmaOptimize(ctx, dbconnpool.PragmaOptimizeFreshConnection, "first", quiet, func(fn func()) {
			go fn()
		})

		infra.SetPragmaOptimizeListening(true)

		// Second call with FreshConnection should be a no-op due to CAS.
		// Since CAS fails, SchedulePragmaOptimize returns immediately without
		// calling run(), so we must NOT wait on a wg inside the run callback.
		infra.SchedulePragmaOptimize(ctx, dbconnpool.PragmaOptimizeFreshConnection, "second", quiet, func(fn func()) {
			go fn()
		})

		// Bubble time (fake clock): let the first goroutine complete
		// (quiet + listening), then wait until it is done before asserting.
		time.Sleep(1 * time.Second)
		synctest.Wait()
		mu.Lock()
		gotCalls := calls
		mu.Unlock()
		if gotCalls != 1 {
			t.Fatalf("expected exactly 1 optimize call due to CAS, got %d", gotCalls)
		}
	})
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
	synctest.Test(t, func(t *testing.T) {
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

		infra.SchedulePragmaOptimize(ctx, dbconnpool.PragmaOptimizeDefault, "test", quiet, func(fn func()) {
			go fn()
		})

		// Bubble time (fake clock): advance past MaxWait so the timeout
		// fallback fires, then wait until the goroutine is done.
		time.Sleep(1 * time.Second)
		synctest.Wait()
		if optimizeCalled.Load() == 0 {
			t.Fatal("expected timeout fallback to trigger PRAGMA optimize")
		}
	})
}
