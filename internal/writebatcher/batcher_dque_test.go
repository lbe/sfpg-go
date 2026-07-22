package writebatcher

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dque"
)

// flushGate synchronizes worker blocking in tests. Create with newFlushGate(),
// use block() to wrap a FlushFunc, call wait() until the worker enters FlushFunc,
// and unblock() to release it. The cleanup() method unblocks safely for use in defer.
type flushGate struct {
	startOnce sync.Once
	started   chan struct{}
	proceed   chan struct{}
}

func newFlushGate() *flushGate {
	return &flushGate{
		started: make(chan struct{}),
		proceed: make(chan struct{}),
	}
}

// block returns a FlushFunc[T] that blocks until unblock is called.
// The first call signals that the worker has entered the flush path.
func blockFlush[T any](g *flushGate) FlushFunc[T] {
	return func(ctx context.Context, tx *sql.Tx, batch []T) error {
		g.startOnce.Do(func() { close(g.started) })
		<-g.proceed
		return nil
	}
}

// wait blocks until the worker first enters the blocked FlushFunc.
func (g *flushGate) wait() { <-g.started }

// unblock releases the worker from the blocked FlushFunc.
func (g *flushGate) unblock() { close(g.proceed) }

// cleanup unblocks the worker if it hasn't been unblocked yet.
// Safe to call multiple times; idempotent after the first call.
func (g *flushGate) cleanup() {
	select {
	case <-g.proceed:
	default:
		close(g.proceed)
	}
}

func TestNew_WithDQueDirPath(t *testing.T) {
	// Use a non-existent subdirectory to verify New() creates it.
	baseDir := t.TempDir()
	dqueDir := filepath.Join(baseDir, "dque")

	cfg := Config[int]{
		BeginTx:     func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
		Flush:       func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		DQueDirPath: dqueDir,
	}
	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	defer wb.Close()

	if wb.dq == nil {
		t.Error("expected dq to be non-nil when DQueDirPath is set")
	}
	if wb.dqNotify == nil {
		t.Error("expected dqNotify to be non-nil when DQueDirPath is set")
	}
	if _, err := os.Stat(dqueDir); os.IsNotExist(err) {
		t.Error("expected dque directory to exist on disk after New")
	}
	if pc := wb.pendingCount.Load(); pc != 0 {
		t.Errorf("expected pendingCount 0 for fresh dque, got %d", pc)
	}

	// Turbo mode should be enabled
	if wb.dq != nil {
		if err := wb.dq.TurboOn(); err == nil {
			t.Error("expected TurboOn to fail because turbo should already be on by default")
		}
	}
}

func TestNew_WithDQueDirPath_CrashRecovery(t *testing.T) {
	dir := t.TempDir()

	// Simulate a crash: create dque, enqueue items, close without draining
	q, err := dque.NewOrOpen[int]("writebatcher", dir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen: %v", err)
	}
	for i := range 5 {
		if err = q.Enqueue(&i); err != nil {
			t.Fatalf("Enqueue(%d): %v", i, err)
		}
	}
	q.Close()

	// Create new batcher pointing at same directory
	var mu sync.Mutex
	flushed := 0
	flushCh := make(chan struct{}, 10)
	cfg := Config[int]{
		BeginTx: func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			mu.Lock()
			flushed += len(batch)
			mu.Unlock()
			flushCh <- struct{}{}
			return nil
		},
		DQueDirPath:  dir,
		MaxBatchSize: 100,
	}
	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	// Verify pendingCount reflects recovered items
	if pc := wb.pendingCount.Load(); pc != 5 {
		t.Errorf("expected pendingCount 5 from crash recovery, got %d", pc)
	}

	// Submit more items, then close without draining persisted overflow.
	for i := range 3 {
		if err := wb.Submit(100 + i); err != nil {
			t.Fatalf("Submit(%d): %v", i, err)
		}
	}
	if err := wb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := persistedDQueSize(t, dir); got != 5 {
		t.Errorf("persisted dque size after Close = %d, want 5 recovered items on disk", got)
	}
}

func TestNew_WithInvalidDQueDirPath(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a regular file to block directory creation
	blockFile := filepath.Join(tmpDir, "block")
	if err := os.WriteFile(blockFile, []byte("block"), 0644); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(blockFile, "dque") // parent is a file, not a directory

	cfg := Config[int]{
		BeginTx:     func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
		Flush:       func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		DQueDirPath: badPath,
	}
	_, err := New(context.Background(), cfg)
	if err == nil {
		t.Error("expected error for invalid dque dir path, got nil")
	}
}

func TestNew_WithDQueDirPath_DefaultItemsPerSegment(t *testing.T) {
	tests := []struct {
		name   string
		perSeg int
	}{
		{name: "zero defaults to 250", perSeg: 0},
		{name: "negative defaults to 250", perSeg: -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := Config[int]{
				BeginTx:             func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
				Flush:               func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
				DQueDirPath:         dir,
				DQueItemsPerSegment: tc.perSeg,
			}
			wb, err := New(context.Background(), cfg)
			if err != nil {
				t.Fatalf("New() returned error with DQueItemsPerSegment=%d: %v", tc.perSeg, err)
			}
			defer wb.Close()

			if wb.dq == nil {
				t.Fatal("expected dq to be non-nil")
			}
			// dque rejects itemsPerSegment <= 0, so if New() succeeded
			// it means the batcher defaulted it to 250 before creating the dque.
		})
	}
}

func TestNew_DQueTurboAlwaysEnabled(t *testing.T) {
	dir := t.TempDir()
	cfg := Config[int]{
		BeginTx:     func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
		Flush:       func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		DQueDirPath: dir,
	}
	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	defer wb.Close()

	if wb.dq == nil {
		t.Fatal("expected dq to be non-nil")
	}
	if err := wb.dq.TurboOn(); err == nil {
		t.Error("expected TurboOn to fail because turbo is always enabled with DQueDirPath")
	}
}

func TestNew_WithoutDQueDirPath(t *testing.T) {
	cfg := Config[int]{
		BeginTx: func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
		Flush:   func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		// DQueDirPath is empty — no dque
	}
	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	defer wb.Close()

	if wb.dq != nil {
		t.Error("expected dq to be nil when DQueDirPath is empty")
	}
	if wb.dqNotify != nil {
		t.Error("expected dqNotify to be nil when DQueDirPath is empty")
	}

	// DQueDirPath is empty — no directory should have been created.
	// (We cannot os.Stat an empty path, so this is verified implicitly
	// by the absence of any MkdirAll call when DQueDirPath is empty.)

	// Verify channel-based submit still works
	if err := wb.Submit(42); err != nil {
		t.Errorf("unexpected Submit error without dque: %v", err)
	}
}

// TestWorker_DrainsDQue verifies the worker drains overflowed dque items via Submit
// and flushes them all, with pendingCount reaching zero.
func TestWorker_DrainsDQue(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var flushed []int

	gate := newFlushGate()

	db := testDB(t)
	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			gate.startOnce.Do(func() { close(gate.started) })
			<-gate.proceed
			mu.Lock()
			flushed = append(flushed, batch...)
			mu.Unlock()
			return nil
		},
		MaxBatchSize:        1,
		ChannelSize:         1,
		FlushInterval:       10 * time.Second,
		DQueDirPath:         dir,
		DQueItemsPerSegment: 100,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		gate.cleanup()
		wb.Close()
	}()

	// Block worker in FlushFunc with first item
	if err := wb.Submit(1); err != nil {
		t.Fatalf("Submit(1): %v", err)
	}
	gate.wait()

	// Fill channel then overflow to dque
	if err := wb.Submit(2); err != nil {
		t.Fatalf("Submit(2): %v", err)
	}
	overflowVals := []int{3, 4, 5}
	for _, v := range overflowVals {
		if err := wb.Submit(v); err != nil {
			t.Fatalf("Submit(%d): %v", v, err)
		}
	}

	// Sanity-check: dque must hold the overflowed items
	if sz := wb.dq.Size(); sz != len(overflowVals) {
		t.Fatalf("test setup: expected dque size %d, got %d", len(overflowVals), sz)
	}

	// Unblock the worker — with new code it drains dque
	gate.unblock()

	// Wait until all items are flushed (pendingCount reaches 0)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if wb.PendingCount() == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	expectedTotal := 1 + 1 + len(overflowVals) // item1 + item2 + dque items
	if len(flushed) != expectedTotal {
		t.Errorf("expected %d items flushed (channel + dque), got %d: %v", expectedTotal, len(flushed), flushed)
	}
	if pc := wb.PendingCount(); pc != 0 {
		t.Errorf("expected pendingCount 0, got %d", pc)
	}
}

// TestWorker_InterleavedDrain verifies that when dque has items and new items
// are concurrently submitted to the channel, both dque and channel items are
// processed (channel items are not starved during dque drain).
func TestWorker_InterleavedDrain(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var flushed []int

	gate := newFlushGate()

	db := testDB(t)
	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			gate.startOnce.Do(func() { close(gate.started) })
			<-gate.proceed
			mu.Lock()
			flushed = append(flushed, batch...)
			mu.Unlock()
			return nil
		},
		MaxBatchSize:        1,
		ChannelSize:         2,
		FlushInterval:       10 * time.Second,
		DQueDirPath:         dir,
		DQueItemsPerSegment: 100,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		gate.cleanup()
		wb.Close()
	}()

	// Block worker in FlushFunc
	if err := wb.Submit(1); err != nil {
		t.Fatalf("Submit(1): %v", err)
	}
	gate.wait()

	// Fill channel (size 2) + overflow to dque
	if err := wb.Submit(2); err != nil {
		t.Fatalf("Submit(2): %v", err)
	}
	if err := wb.Submit(3); err != nil {
		t.Fatalf("Submit(3): %v", err)
	}
	dqueVals := []int{4, 5, 6}
	for _, v := range dqueVals {
		if err := wb.Submit(v); err != nil {
			t.Fatalf("Submit(%d): %v", v, err)
		}
	}

	if sz := wb.dq.Size(); sz != len(dqueVals) {
		t.Fatalf("test setup: expected dque size %d, got %d", len(dqueVals), sz)
	}

	// Unblock worker and concurrently submit more items via channel
	gate.unblock()

	chVals := []int{7, 8, 9}
	for _, v := range chVals {
		if err := wb.Submit(v); err != nil {
			t.Fatalf("Submit(%d): %v", v, err)
		}
	}

	// Wait for all items to be flushed
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if wb.PendingCount() == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	expectedTotal := 1 + 2 + len(dqueVals) + len(chVals) // block item + channel + dque + concurrent channel
	if len(flushed) != expectedTotal {
		t.Errorf("expected %d items flushed (channel + dque), got %d: %v", expectedTotal, len(flushed), flushed)
	}

	// Verify dque items were processed
	flushedSet := make(map[int]bool)
	for _, v := range flushed {
		flushedSet[v] = true
	}
	for _, v := range dqueVals {
		if !flushedSet[v] {
			t.Errorf("dque item %d was not flushed", v)
		}
	}

	// Verify concurrent channel items were not starved
	for _, v := range chVals {
		if !flushedSet[v] {
			t.Errorf("channel item %d was starved (not flushed)", v)
		}
	}
}

// TestWorker_DqNotify_Wake verifies that after dque is drained empty and the
// worker is blocking on the main select, a dqNotify signal caused by a new
// dque overflow wakes the worker and causes it to drain the dque items.
func TestWorker_DqNotify_Wake(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var flushed []int

	db := testDB(t)
	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			mu.Lock()
			flushed = append(flushed, batch...)
			mu.Unlock()
			return nil
		},
		MaxBatchSize:  100,
		FlushInterval: 50 * time.Millisecond,
		ChannelSize:   2,
		DQueDirPath:   dir,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	// Pre-seed dque with items then signal dqNotify.
	// New code: worker wakes from dqNotify case in main select, drains dque.
	// Old code: no dqNotify case; worker stays blocked on channel.
	for _, v := range []int{10, 20, 30} {
		val := v
		if err := wb.dq.Enqueue(&val); err != nil {
			t.Fatalf("dque enqueue %d: %v", v, err)
		}
	}
	wb.pendingCount.Add(3)
	wb.overflowCount.Add(3)

	select {
	case wb.dqNotify <- struct{}{}:
	default:
	}

	// Wait for processing
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if wb.PendingCount() == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	if len(flushed) != 3 {
		t.Errorf("expected 3 dque items flushed after dqNotify signal, got %d: %v", len(flushed), flushed)
	}
	mu.Unlock()
}

// TestWorker_ContextCancel_DuringDrain verifies that when the context is
// cancelled while the dque has many items (>10), the worker exits promptly
// (within 2 seconds) and does not hang in the drain loop.
func TestWorker_ContextCancel_DuringDrain(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())

	db := testDB(t)
	cfg := Config[int]{
		BeginTx:       testBeginTx(db),
		Flush:         func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
		ChannelSize:   10,
		DQueDirPath:   dir,
	}

	// Pre-seed dque before the worker starts so cancel can arrive during recovery.
	dq, err := dque.New[int]("writebatcher", dir, 250)
	if err != nil {
		t.Fatalf("dque.New: %v", err)
	}
	const numItems = 15
	for i := range numItems {
		val := i
		if err = dq.Enqueue(&val); err != nil {
			t.Fatalf("dque enqueue %d: %v", i, err)
		}
	}
	if err = dq.Close(); err != nil {
		t.Fatalf("dque.Close: %v", err)
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	cancel()

	exitTimer := time.NewTimer(2 * time.Second)
	defer exitTimer.Stop()

	select {
	case <-wb.done:
	case <-exitTimer.C:
		t.Fatal("worker did not exit within 2 seconds of context cancellation")
	}
}

// TestWorker_FlushTimerDuringDrain verifies that when draining dque items
// that exceed MaxBatchSize, batch flushes happen during the drain (trigger
// reason "size_limit") and not just after the drain completes.
func TestWorker_FlushTimerDuringDrain(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var flushCount int

	gate := newFlushGate()

	db := testDB(t)
	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			gate.startOnce.Do(func() { close(gate.started) })
			<-gate.proceed
			mu.Lock()
			flushCount++
			mu.Unlock()
			return nil
		},
		MaxBatchSize:  4,
		ChannelSize:   1,
		FlushInterval: 50 * time.Millisecond,
		DQueDirPath:   dir,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		gate.cleanup()
		wb.Close()
	}()

	// Submit 4 items to trigger flush (MaxBatchSize=4) and block worker
	for _, v := range []int{0, 1, 2, 3} {
		if err := wb.Submit(v); err != nil {
			t.Fatalf("Submit(%d): %v", v, err)
		}
	}
	gate.wait()

	// Fill channel (size 1)
	if err := wb.Submit(4); err != nil {
		t.Fatalf("Submit(4): %v", err)
	}

	// Overflow many items to dque (>MaxBatchSize to trigger multiple flushes during drain)
	for i := 5; i < 15; i++ {
		if err := wb.Submit(i); err != nil {
			t.Fatalf("Submit(%d): %v", i, err)
		}
	}

	// Verify dque has items
	if sz := wb.dq.Size(); sz < 9 {
		t.Fatalf("test setup: expected at least 9 dque items, got %d", sz)
	}

	// Unblock worker
	gate.unblock()

	// Wait for all items to be flushed
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if wb.PendingCount() == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	// Old code: only 1 flush (the blocked [0,1,2,3] batch) before Close.
	// Items 4-14 are either in channel (item 4) or dque (items 5-14).
	// New code: multiple flushes during dque drain (batches of MaxBatchSize=4).
	if flushCount < 3 {
		t.Errorf("expected at least 3 flushes during drain (batched flush by size), got %d", flushCount)
	}
}

// TestWorker_ChannelClose_DrainsDQue verifies channel close does not drain dque overflow.
func TestWorker_ChannelClose_DrainsDQue(t *testing.T) {
	dir := t.TempDir()

	db := testDB(t)
	cfg := Config[int]{
		BeginTx:        testBeginTx(db),
		Flush:          func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		MaxBatchSize:   10000,
		FlushInterval:  10 * time.Second,
		ChannelSize:    10,
		DQueDirPath:    dir,
		DeferDQueDrain: true,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, v := range []int{1, 2, 3} {
		val := v
		if err := wb.dq.Enqueue(&val); err != nil {
			t.Fatalf("dque enqueue %d: %v", v, err)
		}
	}
	wb.pendingCount.Add(3)
	wb.overflowCount.Add(3)

	if err := wb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := persistedDQueSize(t, dir); got != 3 {
		t.Errorf("persisted dque size = %d, want 3", got)
	}
}

// TestSubmit_OverflowToDQue tests that when the channel is full and dque is
// configured, Submit overflows items to dque instead of returning ErrFull.
func TestSubmit_OverflowToDQue(t *testing.T) {
	dir := t.TempDir()

	type overflowItem struct {
		Val int
	}

	gate := newFlushGate()

	db := testDB(t)
	cfg := Config[overflowItem]{
		BeginTx:             testBeginTx(db),
		Flush:               blockFlush[overflowItem](gate),
		MaxBatchSize:        1,
		ChannelSize:         1,
		FlushInterval:       10 * time.Second,
		DQueDirPath:         dir,
		DQueItemsPerSegment: 10,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		gate.cleanup()
		wb.Close()
	}()

	// Submit first item — worker picks it up and blocks in FlushFunc
	if err = wb.Submit(overflowItem{Val: 1}); err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	gate.wait()

	// Fill the channel to capacity
	if err = wb.Submit(overflowItem{Val: 2}); err != nil {
		t.Fatalf("submit 2: %v", err)
	}

	// Submit third item — channel is full, should overflow to dque.
	err = wb.Submit(overflowItem{Val: 3})
	if errors.Is(err, ErrFull) {
		t.Error("Submit returned ErrFull but should have overflowed to dque when DQueDirPath is configured")
	}
	if err != nil {
		t.Errorf("unexpected error from Submit: %v", err)
	}

	// Verify the dque contains the overflowed item
	if wb.dq == nil {
		t.Fatal("dque should be configured")
	}
	if size := wb.dq.Size(); size != 1 {
		t.Errorf("expected dque Size() = 1, got %d", size)
	}

	// Dequeue the overflowed item and verify its data
	item, deqErr := wb.dq.Dequeue()
	if deqErr != nil {
		t.Errorf("dque.Dequeue failed: %v", deqErr)
	} else if item.Val != 3 {
		t.Errorf("expected overflowed item Val=3, got %d", item.Val)
	}
}

// TestSubmit_Overflow_IncrementsOverflowCount tests that each overflow
// submission increments Stats.OverflowCount.
func TestSubmit_Overflow_IncrementsOverflowCount(t *testing.T) {
	dir := t.TempDir()

	type overflowItem struct {
		Val int
	}

	gate := newFlushGate()

	db := testDB(t)
	cfg := Config[overflowItem]{
		BeginTx:             testBeginTx(db),
		Flush:               blockFlush[overflowItem](gate),
		MaxBatchSize:        1,
		ChannelSize:         1,
		FlushInterval:       10 * time.Second,
		DQueDirPath:         dir,
		DQueItemsPerSegment: 10,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		gate.cleanup()
		wb.Close()
	}()

	// Submit first item — worker picks it up and blocks in FlushFunc
	if err = wb.Submit(overflowItem{Val: 1}); err != nil {
		t.Fatalf("submit 1: %v", err)
	}

	gate.wait()

	// Fill the channel
	if err = wb.Submit(overflowItem{Val: 2}); err != nil {
		t.Fatalf("submit 2: %v", err)
	}

	// Submit overflow items to dque.
	const overflowCount = 3
	for i := range overflowCount {
		err = wb.Submit(overflowItem{Val: 10 + i})
		if errors.Is(err, ErrFull) {
			t.Errorf("overflow %d: expected dque overflow, got ErrFull", i)
		}
		if err != nil {
			t.Errorf("overflow %d: unexpected error: %v", i, err)
		}
	}

	stats := wb.GetStats()
	if stats.OverflowCount != overflowCount {
		t.Errorf("expected OverflowCount = %d, got %d", overflowCount, stats.OverflowCount)
	}
}

// TestSubmit_Overflow_IncrementsPendingCount tests that overflow items
// increment pendingCount (matches number of overflowed items).
func TestSubmit_Overflow_IncrementsPendingCount(t *testing.T) {
	dir := t.TempDir()

	type overflowItem struct {
		Val int
	}

	gate := newFlushGate()

	db := testDB(t)
	cfg := Config[overflowItem]{
		BeginTx:             testBeginTx(db),
		Flush:               blockFlush[overflowItem](gate),
		MaxBatchSize:        1,
		ChannelSize:         1,
		FlushInterval:       10 * time.Second,
		DQueDirPath:         dir,
		DQueItemsPerSegment: 10,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		gate.cleanup()
		wb.Close()
	}()

	// Submit first item — worker picks it up and blocks in FlushFunc
	if err = wb.Submit(overflowItem{Val: 1}); err != nil {
		t.Fatalf("submit 1: %v", err)
	}

	gate.wait()

	// Fill the channel
	if err = wb.Submit(overflowItem{Val: 2}); err != nil {
		t.Fatalf("submit 2: %v", err)
	}

	// Submit overflow items
	const overflowCount = 3
	for i := range overflowCount {
		err = wb.Submit(overflowItem{Val: 10 + i})
		if errors.Is(err, ErrFull) {
			t.Errorf("overflow %d: expected dque overflow, got ErrFull", i)
		}
		if err != nil {
			t.Errorf("overflow %d: unexpected error: %v", i, err)
		}
	}

	// PendingCount should include: 1 in-flight (blocked in FlushFunc) + 1 in channel + overflowCount
	cnt := wb.PendingCount()
	expected := int64(1 + 1 + overflowCount) // 5 total
	if cnt != expected {
		t.Errorf("expected PendingCount() = %d, got %d", expected, cnt)
	}
}

// TestSubmit_Overflow_SendsDqNotify tests that dqNotify receives a signal
// after each overflow submission (non-blocking send, buffer-1 channel).
func TestSubmit_Overflow_SendsDqNotify(t *testing.T) {
	dir := t.TempDir()

	type overflowItem struct {
		Val int
	}

	gate := newFlushGate()

	db := testDB(t)
	cfg := Config[overflowItem]{
		BeginTx:             testBeginTx(db),
		Flush:               blockFlush[overflowItem](gate),
		MaxBatchSize:        1,
		ChannelSize:         1,
		FlushInterval:       10 * time.Second,
		DQueDirPath:         dir,
		DQueItemsPerSegment: 10,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		gate.cleanup()
		wb.Close()
	}()

	// Submit first item — worker picks it up and blocks in FlushFunc
	if err = wb.Submit(overflowItem{Val: 1}); err != nil {
		t.Fatalf("submit 1: %v", err)
	}

	gate.wait()

	// Fill the channel
	if err = wb.Submit(overflowItem{Val: 2}); err != nil {
		t.Fatalf("submit 2: %v", err)
	}

	// Submit overflow item — should signal dqNotify
	err = wb.Submit(overflowItem{Val: 3})
	if errors.Is(err, ErrFull) {
		t.Error("Submit returned ErrFull but should have overflowed to dque")
	}
	if err != nil {
		t.Errorf("unexpected error from Submit: %v", err)
	}

	// Verify dqNotify was signaled (non-blocking receive on buffer-1 channel)
	select {
	case <-wb.dqNotify:
		// signaled — correct
	default:
		t.Error("expected dqNotify to be signaled after overflow Submit")
	}
}

// TestSubmit_ErrQuotaExceeded verifies that when MaxDiskBytes is set and the
// dque disk usage already meets or exceeds the quota, Submit returns
// ErrQuotaExceeded instead of overflowing to disk.
func TestSubmit_ErrQuotaExceeded(t *testing.T) {
	gate := newFlushGate()
	mq := &mockDQue[int]{
		diskBytesFn: func() int64 { return 5000 },
		enqueueFn: func(item *int) error {
			t.Error("Enqueue should not be called when quota is exceeded")
			return nil
		},
	}

	db := testDB(t)
	cfg := Config[int]{
		BeginTx:      testBeginTx(db),
		Flush:        blockFlush[int](gate),
		MaxBatchSize: 1,
		ChannelSize:  1,
		MaxDiskBytes: 4096,
		testQueue:    mq,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		gate.cleanup()
		wb.Close()
	}()

	// Submit first item — worker picks it up and blocks in FlushFunc.
	err = wb.Submit(1)
	if err != nil {
		t.Fatalf("Submit(1): %v", err)
	}
	gate.wait()

	// Fill the channel (size 1).
	err = wb.Submit(2)
	if err != nil {
		t.Fatalf("Submit(2): %v", err)
	}

	// Submit third item — channel full, overflows to dque path.
	// diskBytesFn returns 5000 >= MaxDiskBytes (4096) → ErrQuotaExceeded.
	err = wb.Submit(3)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("expected ErrQuotaExceeded, got %v", err)
	}
}

// TestClose_DrainsDQue verifies Close returns promptly and preserves dque overflow on disk.
func TestClose_DrainsDQue(t *testing.T) {
	dir := t.TempDir()

	type drainItem struct {
		Val int
	}

	gate := newFlushGate()

	db := testDB(t)
	cfg := Config[drainItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []drainItem) error {
			gate.startOnce.Do(func() { close(gate.started) })
			<-gate.proceed
			return nil
		},
		MaxBatchSize:  1,
		ChannelSize:   1,
		FlushInterval: 10 * time.Second,
		DQueDirPath:   dir,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer gate.cleanup()

	if err := wb.Submit(drainItem{Val: 1}); err != nil {
		t.Fatalf("Submit 1: %v", err)
	}
	gate.wait()

	for i := 2; i <= 7; i++ {
		if err := wb.Submit(drainItem{Val: i}); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}

	done := make(chan struct{})
	go func() {
		gate.unblock()
		_ = wb.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked with dque overflow backlog")
	}

	if got := persistedDQueSizeFor[drainItem](t, dir); got == 0 {
		t.Fatal("persisted dque empty after Close")
	}
}

// TestClose_WithOverflowInFlight verifies that concurrent Submits during Close
// do not lose items. It runs a goroutine that submits while Close is in
// progress and checks that the total flushed count matches the total
// successful submit count.
func TestClose_WithOverflowInFlight(t *testing.T) {
	dir := t.TempDir()

	type inflightItem struct {
		Val int
	}

	var (
		mu               sync.Mutex
		flushed          []int
		successfulSubmit int64
	)

	gate := newFlushGate()

	db := testDB(t)
	cfg := Config[inflightItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []inflightItem) error {
			gate.startOnce.Do(func() { close(gate.started) })
			<-gate.proceed
			mu.Lock()
			for _, item := range batch {
				flushed = append(flushed, item.Val)
			}
			mu.Unlock()
			return nil
		},
		MaxBatchSize:  1,
		ChannelSize:   1,
		FlushInterval: 10 * time.Second,
		DQueDirPath:   dir,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer gate.cleanup()

	// Block the worker so we can fill the channel and overflow to dque.
	if err := wb.Submit(inflightItem{Val: 1}); err != nil {
		t.Fatalf("Submit 1: %v", err)
	}
	gate.wait()
	// Items 2-7: item 2 fills the 1-cap channel, items 3-7 overflow to dque.
	for i := 2; i <= 7; i++ {
		if err := wb.Submit(inflightItem{Val: i}); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}

	// Start concurrent Submitters that will try to overflow.
	const concurrentCount = 20
	var submitWg sync.WaitGroup
	submitWg.Add(concurrentCount)
	for g := range concurrentCount {
		go func(g int) {
			defer submitWg.Done()
			err := wb.Submit(inflightItem{Val: 100 + g})
			if err == nil {
				atomic.AddInt64(&successfulSubmit, 1)
			}
		}(g)
	}

	// Give goroutines time to reach the overflow path.
	time.Sleep(50 * time.Millisecond)

	// Release the blocked flush so the worker starts draining.
	gate.unblock()

	// Call Close while overflow Submits are in-flight.
	_ = wb.Close()

	submitWg.Wait()

	mu.Lock()
	flushedCount := len(flushed)
	mu.Unlock()

	totalSubmitted := int(atomic.LoadInt64(&successfulSubmit)) + 7 // 7 pre-Close items

	if flushedCount > totalSubmitted {
		t.Errorf("flushed %d items but only %d submitted successfully", flushedCount, totalSubmitted)
	}
	persisted := persistedDQueSizeFor[inflightItem](t, dir)
	if flushedCount+persisted < totalSubmitted {
		t.Errorf("lost items: flushed=%d persisted=%d submitted=%d", flushedCount, persisted, totalSubmitted)
	}
}

// TestClose_OverflowMuBarrier verifies that Close acquires overflowMu after mu,
// blocking until any in-flight Submit in the overflow path completes.
func TestClose_OverflowMuBarrier(t *testing.T) {
	dir := t.TempDir()

	db := testDB(t)
	cfg := Config[int]{
		BeginTx:     testBeginTx(db),
		Flush:       func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		DQueDirPath: dir,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Simulate a Submitter in the overflow path by incrementing overflowWG.
	wb.overflowWG.Add(1)

	closeDone := make(chan struct{})
	go func() {
		wb.Close()
		close(closeDone)
	}()

	// Close must block because overflowWG has an uncompleted Add.
	select {
	case <-closeDone:
		t.Error("Close() returned while overflowWG is held — it should block")
	case <-time.After(200 * time.Millisecond):
		// Expected: Close blocked.
	}

	wb.overflowWG.Done()
	<-closeDone // wait for Close to complete
}

// TestClose_DoesNotPanicOnEmptyDque verifies that Close handles an empty dque
// gracefully — no panic and no error.
func TestClose_DoesNotPanicOnEmptyDque(t *testing.T) {
	dir := t.TempDir()

	db := testDB(t)
	cfg := Config[int]{
		BeginTx:     testBeginTx(db),
		Flush:       func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		DQueDirPath: dir,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Close with no items ever submitted — dque is empty.
	if err := wb.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

// TestPendingCount_NeverNegative verifies that pendingCount never drops below
// zero during normal operation. It submits 100 items (mixed channel and
// overflow), monitors pendingCount in a background goroutine, and asserts
// the minimum observed value is >= 0.
func TestPendingCount_NeverNegative(t *testing.T) {
	dir := t.TempDir()

	type negItem struct {
		Val int
	}

	var (
		mu      sync.Mutex
		flushed int
	)

	db := testDB(t)
	cfg := Config[negItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []negItem) error {
			mu.Lock()
			flushed += len(batch)
			mu.Unlock()
			return nil
		},
		MaxBatchSize:  1,
		ChannelSize:   1,
		FlushInterval: 10 * time.Second,
		DQueDirPath:   dir,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	// Monitor pendingCount in a background goroutine.
	var minPending atomic.Int64
	minPending.Store(1000000)

	var monitorWg sync.WaitGroup
	monitorWg.Add(1)
	monitorDone := make(chan struct{})
	go func() {
		defer monitorWg.Done()
		for {
			select {
			case <-monitorDone:
				return
			default:
				cnt := wb.PendingCount()
				for {
					prev := minPending.Load()
					if cnt >= prev {
						break
					}
					if minPending.CompareAndSwap(prev, cnt) {
						break
					}
				}
				time.Sleep(time.Microsecond)
			}
		}
	}()

	// Submit 100 items — with ChannelSize=1 the worker picks up item 1,
	// item 2 fills the channel, and remaining items overflow to dque.
	for i := range 100 {
		for {
			err := wb.Submit(negItem{Val: i})
			if errors.Is(err, ErrFull) {
				time.Sleep(time.Millisecond)
				continue
			}
			if err != nil {
				t.Fatalf("Submit %d: %v", i, err)
			}
			break
		}
	}

	// Wait for all items to flush.
	deadline := time.After(10 * time.Second)
	for wb.PendingCount() != 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for pendingCount to reach 0")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	close(monitorDone)
	monitorWg.Wait()

	if min := minPending.Load(); min < 0 {
		t.Errorf("pendingCount went below 0: min = %d", min)
	}

	mu.Lock()
	flushedCount := flushed
	mu.Unlock()
	if flushedCount != 100 {
		t.Errorf("expected 100 items flushed, got %d", flushedCount)
	}
}

// TestPendingCount_CrashRecovery simulates a process crash where items were
// persisted in the dque but never flushed. It creates a dque directly,
// enqueues items, closes the dque, then creates a new batcher pointing to
// the same directory. It verifies pendingCount is initialised to the dque
// size and eventually reaches zero after processing.
func TestPendingCount_CrashRecovery(t *testing.T) {
	dir := t.TempDir()

	type crashItem struct {
		Val int
	}

	// Step 1: Create a dque directly and enqueue items (simulate pre-crash state).
	dq, err := dque.New[crashItem]("writebatcher", dir, 10)
	if err != nil {
		t.Fatalf("dque.New: %v", err)
	}

	const numItems = 3
	for i := range numItems {
		item := crashItem{Val: i}
		if err = dq.Enqueue(&item); err != nil {
			t.Fatalf("dque.Enqueue %d: %v", i, err)
		}
	}

	if sz := dq.Size(); sz != numItems {
		t.Fatalf("expected dque size %d, got %d", numItems, sz)
	}

	// Close the dque so the new batcher can open it.
	if err = dq.Close(); err != nil {
		t.Fatalf("dque.Close: %v", err)
	}

	// Step 2: Create a batcher with the same directory (simulates restart).
	db := testDB(t)
	cfg := Config[crashItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []crashItem) error {
			return nil
		},
		MaxBatchSize:        10000,
		FlushInterval:       10 * time.Second,
		DQueDirPath:         dir,
		DQueItemsPerSegment: 10,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Step 3: Verify pendingCount is initialised to the dque size.
	cnt := wb.PendingCount()
	if cnt != int64(numItems) {
		t.Errorf("expected PendingCount() = %d at startup, got %d", numItems, cnt)
	}

	// Step 4: Close returns promptly; recovered items remain on disk.
	if err := wb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := persistedDQueSizeFor[crashItem](t, dir); got != numItems {
		t.Errorf("persisted dque size after Close = %d, want %d recovered items on disk", got, numItems)
	}
}

// TestGetStats_WithDQue verifies that when dque is configured, GetStats reports
// DQueEnabled=true, DQueSize reflects the current dque queue depth (0 initially,
// >0 after overflow, 0 after flush), and OverflowCount increments with each
// overflow submission.
func TestGetStats_WithDQue(t *testing.T) {
	dir := t.TempDir()

	type item struct {
		Val int
	}

	gate := newFlushGate()

	db := testDB(t)
	cfg := Config[item]{
		BeginTx:             testBeginTx(db),
		Flush:               blockFlush[item](gate),
		MaxBatchSize:        1,
		ChannelSize:         1,
		FlushInterval:       10 * time.Second,
		DQueDirPath:         dir,
		DQueItemsPerSegment: 10,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		gate.cleanup()
		wb.Close()
	}()

	// Step 1: Verify initial stats before any submissions.
	stats := wb.GetStats()
	if !stats.DQueEnabled {
		t.Error("expected DQueEnabled to be true when DQueDirPath is set")
	}
	if stats.DQueSize != 0 {
		t.Errorf("expected DQueSize = 0 initially, got %d", stats.DQueSize)
	}
	if stats.OverflowCount != 0 {
		t.Errorf("expected OverflowCount = 0 initially, got %d", stats.OverflowCount)
	}

	// Step 2: Submit first item — worker picks it up and blocks in FlushFunc.
	if err := wb.Submit(item{Val: 1}); err != nil {
		t.Fatalf("Submit 1: %v", err)
	}
	gate.wait()

	// Fill the channel.
	if err := wb.Submit(item{Val: 2}); err != nil {
		t.Fatalf("Submit 2: %v", err)
	}

	// Overflow items to dque.
	const overflowItems = 3
	for i := range overflowItems {
		if err := wb.Submit(item{Val: 10 + i}); err != nil {
			t.Fatalf("overflow submit %d: %v", i, err)
		}
	}

	// Step 3: Verify stats after overflow.
	stats = wb.GetStats()
	if !stats.DQueEnabled {
		t.Error("expected DQueEnabled to be true after overflow")
	}
	if stats.DQueSize <= 0 {
		t.Errorf("expected DQueSize > 0 after overflow, got %d", stats.DQueSize)
	}
	if stats.OverflowCount != overflowItems {
		t.Errorf("expected OverflowCount = %d after overflow, got %d", overflowItems, stats.OverflowCount)
	}

	// Step 4: Unblock worker and drain all items.
	gate.unblock()
	if err := wb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Step 5: Verify stats after flush.
	stats = wb.GetStats()
	if stats.DQueSize != 0 {
		t.Errorf("expected DQueSize = 0 after flush, got %d", stats.DQueSize)
	}
}

// TestGetStats_DiskBytesFields verifies that GetStats reports DiskBytes and
// MaxDiskBytes from the mock dque.
func TestGetStats_DiskBytesFields(t *testing.T) {
	mq := &mockDQue[int]{
		sizeFn:      func() int { return 0 },
		diskBytesFn: func() int64 { return 4096 },
	}

	db := testDB(t)
	cfg := Config[int]{
		BeginTx:      testBeginTx(db),
		Flush:        func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		MaxBatchSize: 100,
		MaxDiskBytes: 10240,
		testQueue:    mq,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	stats := wb.GetStats()
	if stats.DiskBytes != 4096 {
		t.Errorf("expected DiskBytes = 4096, got %d", stats.DiskBytes)
	}
	if stats.MaxDiskBytes != 10240 {
		t.Errorf("expected MaxDiskBytes = 10240, got %d", stats.MaxDiskBytes)
	}
	if !stats.DQueEnabled {
		t.Error("expected DQueEnabled = true")
	}
	if stats.DQueSize != 0 {
		t.Errorf("expected DQueSize = 0, got %d", stats.DQueSize)
	}

	// Also verify DiskBytes is 0 when diskBytesFn is not set (nil check).
	mq2 := &mockDQue[int]{
		sizeFn: func() int { return 0 },
	}
	cfg2 := Config[int]{
		BeginTx:      testBeginTx(db),
		Flush:        func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		MaxBatchSize: 100,
		testQueue:    mq2,
	}
	wb2, err := New(context.Background(), cfg2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb2.Close()

	stats2 := wb2.GetStats()
	if stats2.DiskBytes != 0 {
		t.Errorf("expected DiskBytes = 0 without diskBytesFn, got %d", stats2.DiskBytes)
	}
	if stats2.MaxDiskBytes != 0 {
		t.Errorf("expected MaxDiskBytes = 0 without config, got %d", stats2.MaxDiskBytes)
	}
}

// TestGetStats_DQueSize_AfterFlush verifies that DQueSize reports 0 after
// overflowing items and flushing all of them.
func TestGetStats_DQueSize_AfterFlush(t *testing.T) {
	dir := t.TempDir()

	type item struct {
		Val int
	}

	gate := newFlushGate()

	db := testDB(t)
	cfg := Config[item]{
		BeginTx:             testBeginTx(db),
		Flush:               blockFlush[item](gate),
		MaxBatchSize:        1,
		ChannelSize:         1,
		FlushInterval:       10 * time.Second,
		DQueDirPath:         dir,
		DQueItemsPerSegment: 10,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		gate.cleanup()
		wb.Close()
	}()

	// Step 1: Submit first item — worker picks it up and blocks in FlushFunc.
	if err := wb.Submit(item{Val: 1}); err != nil {
		t.Fatalf("Submit 1: %v", err)
	}
	gate.wait()

	// Fill the channel.
	if err := wb.Submit(item{Val: 2}); err != nil {
		t.Fatalf("Submit 2: %v", err)
	}

	// Overflow items to dque.
	const overflowItems = 5
	for i := range overflowItems {
		if err := wb.Submit(item{Val: 10 + i}); err != nil {
			t.Fatalf("overflow submit %d: %v", i, err)
		}
	}

	// Step 2: Verify DQueSize > 0 after overflow.
	stats := wb.GetStats()
	if stats.DQueSize <= 0 {
		t.Errorf("expected DQueSize > 0 after overflow, got %d", stats.DQueSize)
	}

	// Step 3: Flush all items by unblocking the worker and closing.
	gate.unblock()
	if err := wb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Step 4: Verify DQueSize returns to 0 after flush.
	stats = wb.GetStats()
	if stats.DQueSize != 0 {
		t.Errorf("expected DQueSize = 0 after flush, got %d", stats.DQueSize)
	}
}

// Test_E2E_OverflowAbsorbsBurst tests that a WriteBatcher with a small channel
// and dque overflow can absorb a burst of submissions without returning ErrFull.
func Test_E2E_OverflowAbsorbsBurst(t *testing.T) {
	dir := t.TempDir()

	type burstItem struct {
		Val int
	}

	var (
		mu      sync.Mutex
		flushed []int
	)

	db := testDB(t)
	cfg := Config[burstItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []burstItem) error {
			mu.Lock()
			for _, item := range batch {
				flushed = append(flushed, item.Val)
			}
			mu.Unlock()
			return nil
		},
		MaxBatchSize:        50,
		FlushInterval:       10 * time.Millisecond,
		ChannelSize:         4, // intentionally small to force overflow
		DQueDirPath:         dir,
		DQueItemsPerSegment: 250,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	const numItems = 500
	for i := range numItems {
		err := wb.Submit(burstItem{Val: i})
		if errors.Is(err, ErrFull) {
			t.Errorf("Submit %d returned ErrFull — dque overflow should have absorbed the burst", i)
		}
		if err != nil && !errors.Is(err, ErrFull) {
			t.Fatalf("Submit %d: unexpected error: %v", i, err)
		}
	}

	// Wait for pendingCount to reach 0 (all items processed)
	deadline := time.After(30 * time.Second)
	for wb.PendingCount() != 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for pendingCount to reach 0")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Verify all items were received by the flush callback
	mu.Lock()
	flushedCount := len(flushed)
	mu.Unlock()

	if flushedCount != numItems {
		t.Errorf("expected %d items flushed, got %d", numItems, flushedCount)
	}
}

// Test_E2E_CrashRecovery tests that items persisted in a dque survive a
// close/reopen cycle and are recovered by a new WriteBatcher.
func Test_E2E_CrashRecovery(t *testing.T) {
	dir := t.TempDir()

	type recoveryItem struct {
		ID int
	}

	const numItems = 5

	// Step 1: Create a dque directly and enqueue items (simulate crash state
	// where items were persisted to dque but never flushed).
	dq, dqErr := dque.New[recoveryItem]("writebatcher", dir, 10)
	if dqErr != nil {
		t.Fatalf("dque.New: %v", dqErr)
	}

	for i := range numItems {
		item := recoveryItem{ID: i}
		if enqErr := dq.Enqueue(&item); enqErr != nil {
			t.Fatalf("dque.Enqueue %d: %v", i, enqErr)
		}
	}

	if sz := dq.Size(); sz != numItems {
		t.Fatalf("expected dque size %d, got %d", numItems, sz)
	}

	// Close the dque so a new batcher can acquire the flock.
	if closeErr := dq.Close(); closeErr != nil {
		t.Fatalf("dque.Close: %v", closeErr)
	}

	// Step 2: Create a batcher with the same directory (simulates post-crash restart).
	db := testDB(t)
	cfg := Config[recoveryItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []recoveryItem) error {
			return nil
		},
		MaxBatchSize:        100,
		FlushInterval:       10 * time.Second,
		DQueDirPath:         dir,
		DQueItemsPerSegment: 10,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Step 3: Verify pendingCount is initialised to the number of items in dque.
	if cnt := wb.PendingCount(); cnt != int64(numItems) {
		t.Errorf("expected PendingCount() = %d at startup (matching dque size), got %d", numItems, cnt)
	}

	// Step 4: Close returns promptly; recovered items remain on disk.
	if err := wb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := persistedDQueSizeFor[recoveryItem](t, dir); got != numItems {
		t.Errorf("persisted dque size after Close = %d, want %d recovered items on disk", got, numItems)
	}
}

// Test_E2E_CrashRecovery_OverCapacity verifies that crash recovery handles
// more items in the dque than the channel buffer, without deadlock.
// The worker drains dque items incrementally via drainDQueAll on its first
// iteration, avoiding unbounded memory allocation on startup.
func Test_E2E_CrashRecovery_OverCapacity(t *testing.T) {
	dir := t.TempDir()

	type capItem struct {
		ID int
	}

	// Create enough items to exceed the default channel capacity (1024)
	const numItems = 1100

	dq, err := dque.New[capItem]("writebatcher", dir, 250)
	if err != nil {
		t.Fatalf("dque.New: %v", err)
	}

	for i := range numItems {
		item := capItem{ID: i}
		if enqErr := dq.Enqueue(&item); enqErr != nil {
			t.Fatalf("dque.Enqueue %d: %v", i, enqErr)
		}
	}

	if sz := dq.Size(); sz != numItems {
		t.Fatalf("expected dque size %d, got %d", numItems, sz)
	}

	if closeErr := dq.Close(); closeErr != nil {
		t.Fatalf("dque.Close: %v", closeErr)
	}

	db := testDB(t)
	cfg := Config[capItem]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []capItem) error {
			return nil
		},
		MaxBatchSize: 100,
		ChannelSize:  128, // intentionally small to exercise worker-drain path
		DQueDirPath:  dir,
	}

	// This must NOT deadlock: the dque has more items than channel capacity.
	// The worker drains dque items incrementally via drainDQueAll on its first
	// iteration, avoiding a synchronous memory drain at startup.
	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Close must return promptly even when dque exceeds channel capacity.
	closeStart := time.Now()
	if err := wb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(closeStart); elapsed > 2*time.Second {
		t.Errorf("Close took %v, want under 2s (non-blocking dque shutdown)", elapsed)
	}

	if got := persistedDQueSizeFor[capItem](t, dir); got != numItems {
		t.Errorf("persisted dque size after Close = %d, want %d recovered items on disk", got, numItems)
	}
}

// Test_E2E_DQueDir_CreatedAutomatically verifies that the dque directory is
// created automatically when DQueDirPath is set and does not exist.
func Test_E2E_DQueDir_CreatedAutomatically(t *testing.T) {
	// Use a non-existent path inside a temp dir
	baseDir := t.TempDir()
	nonExistentDir := filepath.Join(baseDir, "auto-created", "dque")

	// Verify the directory does not exist yet
	if _, err := os.Stat(nonExistentDir); !os.IsNotExist(err) {
		t.Fatal("test precondition failed: directory should not exist yet")
	}

	db := testDB(t)
	cfg := Config[int]{
		BeginTx:     testBeginTx(db),
		Flush:       func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		DQueDirPath: nonExistentDir,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	// Verify the directory was created automatically
	if _, err := os.Stat(nonExistentDir); os.IsNotExist(err) {
		t.Error("dque directory was not created automatically by New")
	}

	// Verify dque is enabled in stats
	stats := wb.GetStats()
	if !stats.DQueEnabled {
		t.Error("expected DQueEnabled = true when DQueDirPath is configured")
	}
}

// TestReconfigure_CloseOldFirst verifies that closing a batcher before creating
// a new one with the same dque directory allows the new batcher to acquire the
// flock. This validates the close-before-create ordering required by
// reconfigurePoolsFromConfig to prevent deadlock on the dque file lock.
func TestReconfigure_CloseOldFirst(t *testing.T) {
	dir := t.TempDir()

	// Step 1: Create a batcher with dque (this acquires the flock).
	db1 := testDB(t)
	cfg1 := Config[int]{
		BeginTx:     testBeginTx(db1),
		Flush:       func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		DQueDirPath: dir,
	}
	wb1, err1 := New(context.Background(), cfg1)
	if err1 != nil {
		t.Fatalf("first New: %v", err1)
	}

	// Step 2: Close the old batcher BEFORE creating a new one (releases flock).
	if closeErr := wb1.Close(); closeErr != nil {
		t.Fatalf("first Close: %v", closeErr)
	}

	// Step 3: Create a new batcher with the same dque directory.
	// This must succeed because the flock was released by Close.
	db2 := testDB(t)
	cfg2 := Config[int]{
		BeginTx:     testBeginTx(db2),
		Flush:       func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		DQueDirPath: dir,
	}
	wb2, err := New(context.Background(), cfg2)
	if err != nil {
		t.Fatalf("second New with same DQueDirPath failed — old batcher may not have released flock: %v", err)
	}
	defer wb2.Close()

	// Step 4: Verify the new batcher works correctly by submitting a few items.
	if err := wb2.Submit(42); err != nil {
		t.Errorf("Submit on new batcher failed: %v", err)
	}

	if err := wb2.Close(); err != nil {
		t.Errorf("second Close failed: %v", err)
	}
}

// mockDQue is a deterministic dqueQueue implementation for tests.
type mockDQue[T any] struct {
	sizeFn      func() int
	diskBytesFn func() int64
	dequeueFn   func() (*T, error)
	enqueueFn   func(*T) error
	turboOnFn   func() error
	closeFn     func() error
	enqueueLog  []*T
}

func (m *mockDQue[T]) Size() int {
	if m.sizeFn != nil {
		return m.sizeFn()
	}
	return 0
}

func (m *mockDQue[T]) Dequeue() (*T, error) {
	if m.dequeueFn != nil {
		return m.dequeueFn()
	}
	return nil, dque.ErrEmpty
}

func (m *mockDQue[T]) Enqueue(item *T) error {
	m.enqueueLog = append(m.enqueueLog, item)
	if m.enqueueFn != nil {
		return m.enqueueFn(item)
	}
	return nil
}

func (m *mockDQue[T]) DiskBytes() int64 {
	if m.diskBytesFn != nil {
		return m.diskBytesFn()
	}
	return 0
}

func (m *mockDQue[T]) TurboOn() error {
	if m.turboOnFn != nil {
		return m.turboOnFn()
	}
	return nil
}

func (m *mockDQue[T]) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

func TestDrainDQueAll_DrainsItemsOnDqNotify(t *testing.T) {
	item := 42
	var mMu sync.Mutex
	dequeued := false
	mq := &mockDQue[int]{
		sizeFn: func() int {
			mMu.Lock()
			defer mMu.Unlock()
			if dequeued {
				return 0
			}
			return 1
		},
		dequeueFn: func() (*int, error) {
			mMu.Lock()
			defer mMu.Unlock()
			if dequeued {
				return nil, dque.ErrEmpty
			}
			dequeued = true
			return &item, nil
		},
	}

	var mu sync.Mutex
	var flushed []int

	db := testDB(t)
	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			mu.Lock()
			defer mu.Unlock()
			flushed = append(flushed, batch...)
			return nil
		},
		MaxBatchSize:  100,
		FlushInterval: 50 * time.Millisecond,
		testQueue:     mq,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer wb.Close()

	select {
	case wb.dqNotify <- struct{}{}:
	default:
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if wb.PendingCount() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 1 || flushed[0] != 42 {
		t.Errorf("expected [42] flushed, got %v", flushed)
	}
	if wb.PendingCount() != 0 {
		t.Errorf("expected PendingCount() = 0, got %d", wb.PendingCount())
	}
}

func TestDrainDQueAll_DQueDequeueError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	mq := &mockDQue[int]{
		sizeFn: func() int { return 1 },
		dequeueFn: func() (*int, error) {
			return nil, errors.New("dequeue denied")
		},
	}

	db := testDB(t)
	cfg := Config[int]{
		BeginTx:       testBeginTx(db),
		Flush:         func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
		testQueue:     mq,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	select {
	case wb.dqNotify <- struct{}{}:
	default:
	}

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-wb.done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not exit after context cancellation")
	}
}

func TestFlushChannelExit_ChannelEmptyFlushesBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	mq := &mockDQue[int]{
		sizeFn: func() int { return 0 },
	}

	var mu sync.Mutex
	var flushed []int

	db := testDB(t)
	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			mu.Lock()
			defer mu.Unlock()
			flushed = append(flushed, batch...)
			return nil
		},
		MaxBatchSize:  10,
		FlushInterval: 10 * time.Second,
		testQueue:     mq,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, v := range []int{1, 2, 3} {
		if err := wb.Submit(v); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}

	cancel()

	select {
	case <-wb.done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not exit after context cancellation")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 3 {
		t.Errorf("expected 3 items flushed, got %d: %v", len(flushed), flushed)
	}
	if wb.PendingCount() != 0 {
		t.Errorf("expected PendingCount() = 0, got %d", wb.PendingCount())
	}
}

func TestFlushChannelExit_DQueDequeueError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var mMu sync.Mutex
	dqueSize := 0
	mq := &mockDQue[int]{
		sizeFn: func() int {
			mMu.Lock()
			defer mMu.Unlock()
			return dqueSize
		},
		dequeueFn: func() (*int, error) {
			return nil, errors.New("dequeue denied")
		},
	}

	var mu sync.Mutex
	var flushed []int

	db := testDB(t)
	cfg := Config[int]{
		BeginTx: testBeginTx(db),
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			mu.Lock()
			defer mu.Unlock()
			flushed = append(flushed, batch...)
			return nil
		},
		MaxBatchSize:  10,
		FlushInterval: 10 * time.Second,
		testQueue:     mq,
	}

	wb, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, v := range []int{1, 2, 3} {
		if err := wb.Submit(v); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}

	mMu.Lock()
	dqueSize = 1
	mMu.Unlock()
	wb.pendingCount.Add(1)
	wb.overflowCount.Add(1)

	cancel()

	select {
	case <-wb.done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not exit after context cancellation")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 3 {
		t.Errorf("expected 3 channel items flushed, got %d: %v", len(flushed), flushed)
	}
	if wb.PendingCount() != 1 {
		t.Errorf("expected PendingCount() = 1 (dque item preserved), got %d", wb.PendingCount())
	}
}
