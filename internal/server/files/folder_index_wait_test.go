package files

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubUnifiedBatcher is a minimal UnifiedBatcher for unit-testing the wait loop.
// FolderIndexInflight returns whatever the test sets via inflightErr/inflight.
type stubUnifiedBatcher struct {
	inflight   int64
	submitErr  error
	rebuild    bool
	scanHeld   bool
	generCount int64
}

func (s *stubUnifiedBatcher) SubmitFile(file *File) error { return nil }

func (s *stubUnifiedBatcher) SubmitFolderIndex(row FolderIndexRow) error { return s.submitErr }

func (s *stubUnifiedBatcher) PendingCount() int64 { return 0 }

func (s *stubUnifiedBatcher) FolderIndexInflight() int64 { return s.inflight }

func (s *stubUnifiedBatcher) SetFolderIndexRebuildActive(active bool) { s.rebuild = active }

func (s *stubUnifiedBatcher) SetFolderIndexRebuildScanHeld(held bool) { s.scanHeld = held }

func (s *stubUnifiedBatcher) BumpFolderIndexGeneration() int64 {
	s.generCount++
	return s.generCount
}

// TestWaitFolderIndexInflight_UnchangedFails: when inflight never reaches 0 the
// wait must fail after the (short) stall rather than waiting 30s.
func TestWaitFolderIndexInflight_UnchangedFails(t *testing.T) {
	stub := &stubUnifiedBatcher{inflight: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := waitFolderIndexInflight(ctx, stub, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected error when inflight stays > 0")
	}
}

// TestWaitFolderIndexInflight_ReachesZero: when inflight drops to 0 the wait
// returns nil immediately regardless of stall.
func TestWaitFolderIndexInflight_ReachesZero(t *testing.T) {
	stub := &stubUnifiedBatcher{inflight: 0}
	ctx := context.Background()

	if err := waitFolderIndexInflight(ctx, stub, 30*time.Second); err != nil {
		t.Fatalf("expected nil when inflight already 0, got %v", err)
	}
}

// TestWaitFolderIndexInflight_CanceledContext: a canceled context fails the
// wait even if inflight is still > 0.
func TestWaitFolderIndexInflight_CanceledContext(t *testing.T) {
	stub := &stubUnifiedBatcher{inflight: 5}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := waitFolderIndexInflight(ctx, stub, 30*time.Second)
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestRebuildFileFolderIndex_CanceledReturnsCtxErr verifies that a canceled
// context makes RebuildFileFolderIndex return ctx.Err() (not the sentinel), so
// startup does not treat it as a fatal rebuild failure.
func TestRebuildFileFolderIndex_CanceledReturnsCtxErr(t *testing.T) {
	_, rwPool, _, _ := createTestPoolsAndDir(t)
	stub := &stubUnifiedBatcher{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RebuildFileFolderIndex(ctx, rwPool, rwPool, stub)
	if err == nil {
		t.Fatal("expected error with canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v (must not be ErrFolderIndexRebuild)", err)
	}
}
