package server

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/database"
)

// newPragmaInfra builds an InfrastructureService with dbPaths pointing at a
// temp DB so walCheckpointAfterCommit can stat the WAL file at dbPaths.Main+"-wal".
func newPragmaInfra(t *testing.T) *InfrastructureService {
	t.Helper()
	infra := NewInfrastructureService()
	infra.dbInitializer = &fakeDatabaseInitializer{
		setupPaths: database.DatabasePaths{Main: filepath.Join(t.TempDir(), "sfpg.db")},
		setupRw:    newFakePool(10, 2),
		setupRo:    newFakePool(10, 2),
	}
	infra.SetupDB(context.Background(), nil)
	return infra
}

// writeLargeWal creates a WAL file larger than the 256MiB checkpoint threshold at
// dbPaths.Main+"-wal" and returns its path.
func writeLargeWal(t *testing.T, main string) string {
	t.Helper()
	walPath := main + "-wal"
	f, err := os.Create(walPath)
	if err != nil {
		t.Fatalf("create WAL: %v", err)
	}
	const walSizeThreshold = 256 * 1024 * 1024
	if err := f.Truncate(walSizeThreshold + 1024); err != nil {
		f.Close()
		t.Fatalf("truncate WAL: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close WAL: %v", err)
	}
	return walPath
}

// TestWalCheckpointAfterCommit_SkippedWhenRebuildScanHeld verifies G4: while the
// RO rebuild scan cursor is open, walCheckpointAfterCommit must NOT issue a
// TRUNCATE checkpoint even when the WAL is far above the size threshold.
func TestWalCheckpointAfterCommit_SkippedWhenRebuildScanHeld(t *testing.T) {
	infra := newPragmaInfra(t)
	walPath := writeLargeWal(t, infra.dbPaths.Main)
	// Sanity: the WAL really is above the threshold.
	if info, err := os.Stat(walPath); err != nil || info.Size() <= 256*1024*1024 {
		t.Fatalf("test setup: WAL not above threshold: size=%v err=%v", info, err)
	}

	var checkpointCalls atomic.Int32
	infra.testSeams.PerformWALCheckpoint = func(ctx context.Context) {
		checkpointCalls.Add(1)
	}
	infra.testSeams.WALCheckpointQuery = func(ctx context.Context, conn *sql.Conn) (*sql.Rows, error) {
		t.Error("WALCheckpointQuery must not be called while scan-held")
		return nil, nil
	}

	// Simulate the open rebuild scan cursor.
	infra.folderIndexRebuildScanHeld.Store(true)
	defer infra.folderIndexRebuildScanHeld.Store(false)

	// D4 wrote-nothing skip must not false-green this: postFlush=true with
	// lastFlushWroteDML true still must not checkpoint while scan-held.
	infra.lastFlushWroteDML.Store(true)
	infra.walCheckpointAfterCommit(context.Background(), time.Time{}, time.Time{}, 1, true)

	if checkpointCalls.Load() != 0 {
		t.Fatalf("expected 0 checkpoint calls while scan-held, got %d", checkpointCalls.Load())
	}
}

// TestWalCheckpointAfterCommit_RunsWhenScanNotHeld verifies the checkpoint still
// runs (size path) when the rebuild scan cursor is NOT held.
func TestWalCheckpointAfterCommit_RunsWhenScanNotHeld(t *testing.T) {
	infra := newPragmaInfra(t)
	walPath := writeLargeWal(t, infra.dbPaths.Main)
	if info, err := os.Stat(walPath); err != nil || info.Size() <= 256*1024*1024 {
		t.Fatalf("test setup: WAL not above threshold: size=%v err=%v", info, err)
	}

	var checkpointCalls atomic.Int32
	infra.testSeams.PerformWALCheckpoint = func(ctx context.Context) {
		checkpointCalls.Add(1)
	}
	infra.testSeams.WALCheckpointQuery = func(ctx context.Context, conn *sql.Conn) (*sql.Rows, error) {
		t.Error("WALCheckpointQuery must not be called (PerformWALCheckpoint seam set)")
		return nil, nil
	}

	// Flag false (default): normal operation.
	if infra.folderIndexRebuildScanHeld.Load() {
		t.Fatal("scan-held flag should default false")
	}

	infra.lastFlushWroteDML.Store(true)
	infra.walCheckpointAfterCommit(context.Background(), time.Time{}, time.Time{}, 1, true)

	if checkpointCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 checkpoint call when scan not held, got %d", checkpointCalls.Load())
	}
}

// TestWalCheckpointAfterCommit_SkippedWhenLastFlushWroteNothing verifies that
// a post-flush (postFlush=true) with no DML skips size-based TRUNCATE.
func TestWalCheckpointAfterCommit_SkippedWhenLastFlushWroteNothing(t *testing.T) {
	infra := newPragmaInfra(t)
	walPath := writeLargeWal(t, infra.dbPaths.Main)
	if info, err := os.Stat(walPath); err != nil || info.Size() <= 256*1024*1024 {
		t.Fatalf("test setup: WAL not above threshold: size=%v err=%v", info, err)
	}

	var checkpointCalls atomic.Int32
	infra.testSeams.PerformWALCheckpoint = func(ctx context.Context) {
		checkpointCalls.Add(1)
	}

	// lastFlushWroteDML defaults false. postFlush=true must skip.
	infra.walCheckpointAfterCommit(context.Background(), time.Time{}, time.Time{}, 1, true)
	if checkpointCalls.Load() != 0 {
		t.Fatalf("expected 0 checkpoint calls when last flush wrote nothing, got %d", checkpointCalls.Load())
	}
}

// TestWalCheckpointAfterCommit_RunsSizeCheckpointOnMaintenanceTimerWhenWroteFalse verifies
// that the first maintenance tick (postFlush=false, zero times) still size-checkpoints
// even when lastFlushWroteDML is false.
func TestWalCheckpointAfterCommit_RunsSizeCheckpointOnMaintenanceTimerWhenWroteFalse(t *testing.T) {
	infra := newPragmaInfra(t)
	walPath := writeLargeWal(t, infra.dbPaths.Main)
	if info, err := os.Stat(walPath); err != nil || info.Size() <= 256*1024*1024 {
		t.Fatalf("test setup: WAL not above threshold: size=%v err=%v", info, err)
	}

	var checkpointCalls atomic.Int32
	infra.testSeams.PerformWALCheckpoint = func(ctx context.Context) {
		checkpointCalls.Add(1)
	}

	// postFlush=false, zero times (first maintenance tick). Must still checkpoint.
	infra.walCheckpointAfterCommit(context.Background(), time.Time{}, time.Time{}, 1, false)
	if checkpointCalls.Load() != 1 {
		t.Fatalf("expected 1 checkpoint call on maintenance timer (postFlush=false), got %d", checkpointCalls.Load())
	}
}

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
