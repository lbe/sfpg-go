package writebatcher

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dque"
)

func persistedDQueSizeFor[T any](t *testing.T, dir string) int {
	t.Helper()
	dq, err := dque.NewOrOpen[T]("writebatcher", dir, 250)
	if err != nil {
		t.Fatalf("dque.NewOrOpen: %v", err)
	}
	defer dq.Close()
	return dq.Size()
}

func persistedDQueSize(t *testing.T, dir string) int {
	return persistedDQueSizeFor[int](t, dir)
}

// TestClose_DoesNotDrainDQue verifies shutdown returns quickly and leaves overflow on disk.
func TestClose_DoesNotDrainDQue(t *testing.T) {
	dir := t.TempDir()
	const seeded = 500

	db := testDB(t)
	cfg := Config[int]{
		BeginTx:        testBeginTx(db),
		Flush:          func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		MaxBatchSize:   10000,
		FlushInterval:  10 * time.Second,
		ChannelSize:    4,
		DQueDirPath:    dir,
		DeferDQueDrain: true, // avoid racing the worker's eager drain before Close
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < seeded; i++ {
		v := i
		if err := wb.dq.Enqueue(&v); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	wb.pendingCount.Add(seeded)
	wb.overflowCount.Add(int64(seeded))

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := wb.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked longer than 5s with large dque backlog")
	}

	if got := persistedDQueSize(t, dir); got != seeded {
		t.Errorf("persisted dque size after Close = %d, want %d", got, seeded)
	}
}

// TestClose_CompletesWhileWorkerDrainingDQue verifies Close does not wait for full dque drain.
func TestClose_CompletesWhileWorkerDrainingDQue(t *testing.T) {
	dir := t.TempDir()

	db := testDB(t)
	cfg := Config[int]{
		BeginTx:       testBeginTx(db),
		Flush:         func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		MaxBatchSize:  1,
		FlushInterval: 10 * time.Second,
		ChannelSize:   1,
		DQueDirPath:   dir,
	}

	wb, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 50; i++ {
		v := i
		if err := wb.dq.Enqueue(&v); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	wb.pendingCount.Add(50)
	wb.overflowCount.Add(50)
	select {
	case wb.dqNotify <- struct{}{}:
	default:
	}

	done := make(chan struct{})
	go func() {
		_ = wb.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked while worker was draining dque")
	}

	if got := persistedDQueSize(t, dir); got == 0 {
		t.Fatal("persisted dque empty after Close")
	}
}

// TestReconfigure_CloseOldFirst_LargeDQue verifies flock handoff without draining dque on Close.
func TestReconfigure_CloseOldFirst_LargeDQue(t *testing.T) {
	dir := t.TempDir()
	const seeded = 200

	db1 := testDB(t)
	cfg1 := Config[int]{
		BeginTx:       testBeginTx(db1),
		Flush:         func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		MaxBatchSize:  10000,
		FlushInterval: 10 * time.Second,
		ChannelSize:   4,
		DQueDirPath:   dir,
	}
	wb1, err := New(context.Background(), cfg1)
	if err != nil {
		t.Fatalf("first New: %v", err)
	}

	for i := 0; i < seeded; i++ {
		v := i
		if err = wb1.dq.Enqueue(&v); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	wb1.pendingCount.Add(seeded)
	wb1.overflowCount.Add(seeded)

	done := make(chan struct{})
	var closeErr error
	go func() {
		closeErr = wb1.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("first Close blocked on large dque")
	}
	if closeErr != nil {
		t.Fatalf("first Close: %v", closeErr)
	}

	db2 := testDB(t)
	cfg2 := Config[int]{
		BeginTx:       testBeginTx(db2),
		Flush:         func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		DQueDirPath:   dir,
		MaxBatchSize:  10000,
		FlushInterval: 10 * time.Second,
	}
	wb2, err := New(context.Background(), cfg2)
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	defer wb2.Close()

	if got := wb2.dq.Size(); got == 0 {
		t.Fatal("dque empty after reconfigure handoff")
	}
}

// TestWorker_ChannelClose_DoesNotDrainDQue verifies Close leaves deferred dque
// overflow on disk: drain is deferred, so channel close must not dequeue it.
func TestWorker_ChannelClose_DoesNotDrainDQue(t *testing.T) {
	dir := t.TempDir()

	db := testDB(t)
	cfg := Config[int]{
		BeginTx:        testBeginTx(db),
		Flush:          func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		MaxBatchSize:   10000,
		FlushInterval:  10 * time.Second,
		ChannelSize:    4,
		DQueDirPath:    dir,
		DeferDQueDrain: true, // avoid racing the worker's eager drain before Close
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
