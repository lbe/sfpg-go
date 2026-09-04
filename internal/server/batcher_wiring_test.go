package server

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// testDB opens an in-memory SQLite database for testing.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// testBeginTx returns a BeginTx function using the given database.
func testBeginTx(db *sql.DB) func(context.Context) (*sql.Tx, error) {
	return func(ctx context.Context) (*sql.Tx, error) {
		return db.BeginTx(ctx, nil)
	}
}

// makeTestFile creates a minimal files.File for testing.
func makeTestFile(path, name string) *files.File {
	return &files.File{
		Path: path,
		File: gallerydb.File{
			Filename: name,
			FolderID: sql.NullInt64{Int64: 1, Valid: true},
		},
	}
}

func TestFileBatcher_SubmitFile(t *testing.T) {
	t.Run("successfully submits file", func(t *testing.T) {
		db := testDB(t)
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      testBeginTx(db),
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() { _ = wb.Close() })
		fb := newFileBatcher(wb, &atomic.Int64{}, &atomic.Bool{}, &atomic.Bool{}, &atomic.Int64{})

		file := makeTestFile("/test/path.jpg", "test.jpg")

		err = fb.SubmitFile(file)
		if err != nil {
			t.Fatalf("SubmitFile failed: %v", err)
		}
		if fb.PendingCount() != 1 {
			t.Errorf("expected pending count 1, got %d", fb.PendingCount())
		}
	})

	t.Run("returns error when batcher is full", func(t *testing.T) {
		// Block flush until the worker has consumed item 1, then fill the channel.
		var blockMu sync.Mutex
		blockMu.Lock()
		flushEntered := make(chan struct{})
		var flushOnce sync.Once

		db := testDB(t)
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx: testBeginTx(db),
			Flush: func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
				flushOnce.Do(func() { close(flushEntered) })
				blockMu.Lock()
				_ = len(batch)
				blockMu.Unlock()
				return nil
			},
			MaxBatchSize: 1,
			ChannelSize:  1,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() {
			blockMu.Unlock()
			wb.Close()
		})
		fb := newFileBatcher(wb, &atomic.Int64{}, &atomic.Bool{}, &atomic.Bool{}, &atomic.Int64{})

		err = fb.SubmitFile(makeTestFile("/test/1.jpg", "1.jpg"))
		if err != nil {
			t.Fatalf("first SubmitFile failed: %v", err)
		}
		<-flushEntered

		err = fb.SubmitFile(makeTestFile("/test/2.jpg", "2.jpg"))
		if err != nil {
			t.Fatalf("second SubmitFile failed: %v", err)
		}

		err = fb.SubmitFile(makeTestFile("/test/3.jpg", "3.jpg"))
		if !errors.Is(err, writebatcher.ErrFull) {
			t.Errorf("expected ErrFull, got %v", err)
		}
	})
}

func TestFileBatcher_PendingCount(t *testing.T) {
	t.Run("returns zero when empty", func(t *testing.T) {
		db := testDB(t)
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      testBeginTx(db),
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() { _ = wb.Close() })
		fb := newFileBatcher(wb, &atomic.Int64{}, &atomic.Bool{}, &atomic.Bool{}, &atomic.Int64{})

		if count := fb.PendingCount(); count != 0 {
			t.Errorf("expected pending count 0, got %d", count)
		}
	})

	t.Run("returns count after submissions", func(t *testing.T) {
		db := testDB(t)
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      testBeginTx(db),
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() { _ = wb.Close() })
		fb := newFileBatcher(wb, &atomic.Int64{}, &atomic.Bool{}, &atomic.Bool{}, &atomic.Int64{})

		file := makeTestFile("/test/1.jpg", "1.jpg")
		for range 5 {
			_ = fb.SubmitFile(file)
		}

		if count := fb.PendingCount(); count != 5 {
			t.Errorf("expected pending count 5, got %d", count)
		}
	})
}

func TestFileBatcher_NilGuard(t *testing.T) {
	fb := newFileBatcher(nil, &atomic.Int64{}, &atomic.Bool{}, &atomic.Bool{}, &atomic.Int64{})

	// SubmitFile must return ErrClosed, not panic
	err := fb.SubmitFile(&files.File{Path: "/test/path.jpg"})
	if !errors.Is(err, writebatcher.ErrClosed) {
		t.Errorf("SubmitFile with nil batcher: expected ErrClosed, got %v", err)
	}

	// PendingCount must return 0, not panic
	if count := fb.PendingCount(); count != 0 {
		t.Errorf("PendingCount with nil batcher: expected 0, got %d", count)
	}

	// SubmitFolderIndex must return ErrClosed, not panic
	err = fb.SubmitFolderIndex(files.FolderIndexRow{FileID: 1, FolderID: 1})
	if !errors.Is(err, writebatcher.ErrClosed) {
		t.Errorf("SubmitFolderIndex with nil batcher: expected ErrClosed, got %v", err)
	}

	// FolderIndexInflight must return 0, not panic
	if n := fb.FolderIndexInflight(); n != 0 {
		t.Errorf("FolderIndexInflight with nil batcher: expected 0, got %d", n)
	}
}

// makeFolderIndexRow returns a FolderIndexRow with the given ids.
func makeFolderIndexRow(fileID, folderID int64) files.FolderIndexRow {
	return files.FolderIndexRow{
		FileID:     fileID,
		FolderID:   folderID,
		Generation: 1,
	}
}

func TestFileBatcher_SubmitFolderIndex(t *testing.T) {
	t.Run("increments inflight before submit", func(t *testing.T) {
		db := testDB(t)
		// Block flush so the item stays pending and inflight is observable.
		var blockMu sync.Mutex
		blockMu.Lock()
		flushEntered := make(chan struct{})
		var flushOnce sync.Once
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx: testBeginTx(db),
			Flush: func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
				flushOnce.Do(func() { close(flushEntered) })
				blockMu.Lock()
				_ = len(batch)
				blockMu.Unlock()
				return nil
			},
			MaxBatchSize: 1,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() {
			blockMu.Unlock()
			wb.Close()
		})
		var inflight, generation atomic.Int64
		var rebuildActive atomic.Bool
		var rebuildScan atomic.Bool
		fb := newFileBatcher(wb, &inflight, &rebuildActive, &rebuildScan, &generation)

		// Before submit, inflight is 0.
		if got := fb.FolderIndexInflight(); got != 0 {
			t.Fatalf("inflight before submit: got %d, want 0", got)
		}

		// Submit; item is pending (flush blocked), so inflight must be 1.
		if err := fb.SubmitFolderIndex(makeFolderIndexRow(1, 1)); err != nil {
			t.Fatalf("SubmitFolderIndex failed: %v", err)
		}
		if got := fb.FolderIndexInflight(); got != 1 {
			t.Errorf("inflight after submit (pending): got %d, want 1", got)
		}
		// Row is pending in the batcher.
		if got := fb.PendingCount(); got != 1 {
			t.Errorf("pending after submit: got %d, want 1", got)
		}
	})

	t.Run("undo on submit error preserves inflight", func(t *testing.T) {
		db := testDB(t)
		// ChannelSize 1, force ErrFull on this submit (no dque).
		var blockMu sync.Mutex
		blockMu.Lock()
		flushEntered := make(chan struct{})
		var flushOnce sync.Once
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx: testBeginTx(db),
			Flush: func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
				flushOnce.Do(func() { close(flushEntered) })
				blockMu.Lock()
				_ = len(batch)
				blockMu.Unlock()
				return nil
			},
			MaxBatchSize: 1,
			ChannelSize:  1,
			DQueDirPath:  "", // no overflow; ErrFull on full
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() {
			blockMu.Unlock()
			wb.Close()
		})
		var inflight, generation atomic.Int64
		var rebuildActive atomic.Bool
		var rebuildScan atomic.Bool
		fb := newFileBatcher(wb, &inflight, &rebuildActive, &rebuildScan, &generation)

		// First submit consumes the single channel slot (flush blocked).
		if err := fb.SubmitFolderIndex(makeFolderIndexRow(1, 1)); err != nil {
			t.Fatalf("first SubmitFolderIndex failed: %v", err)
		}
		<-flushEntered

		// Worker holds item 1 in flush; refill the channel so the next submit
		// must hit ErrFull (undo must leave inflight unchanged).
		if err := fb.SubmitFolderIndex(makeFolderIndexRow(2, 1)); err != nil {
			t.Fatalf("second SubmitFolderIndex failed: %v", err)
		}

		before := fb.FolderIndexInflight()
		if err := fb.SubmitFolderIndex(makeFolderIndexRow(3, 1)); !errors.Is(err, writebatcher.ErrFull) {
			t.Fatalf("third SubmitFolderIndex: expected ErrFull, got %v", err)
		}
		if got := fb.FolderIndexInflight(); got != before {
			t.Errorf("inflight after ErrFull: got %d, want unchanged %d", got, before)
		}
	})

	t.Run("retries while pending falls then succeeds", func(t *testing.T) {
		db := testDB(t)
		// Flush sleeps so the worker stays busy after receiving item1, leaving the
		// channel full for a third submit to hit ErrFull and enter the retry loop.
		// ChannelSize=2 lets item1 and item2 both be received/enqueued while item3
		// backs up and must retry until the worker drains item1 and pending falls.
		const flushSleep = 150 * time.Millisecond
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx: testBeginTx(db),
			Flush: func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
				time.Sleep(flushSleep)
				return nil
			},
			MaxBatchSize: 1,
			ChannelSize:  2,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() { wb.Close() })
		var inflight, generation atomic.Int64
		var rebuildActive atomic.Bool
		var rebuildScan atomic.Bool
		fb := newFileBatcher(wb, &inflight, &rebuildActive, &rebuildScan, &generation)

		// Generation the production OnSuccess path will NOT decrement, so this
		// test isolates the retry/inflight behavior of SubmitFolderIndex itself.
		const stuckGen = int64(99)
		row := func(id int64) files.FolderIndexRow {
			return files.FolderIndexRow{FileID: id, FolderID: 1, Generation: stuckGen}
		}

		// item1 is received and the worker sleeps; item2 also enqueues (room after
		// item1 leaves the channel). item3 hits a full channel -> ErrFull -> retry.
		if err := fb.SubmitFolderIndex(row(1)); err != nil {
			t.Fatalf("first SubmitFolderIndex failed: %v", err)
		}
		if err := fb.SubmitFolderIndex(row(2)); err != nil {
			t.Fatalf("second SubmitFolderIndex failed: %v", err)
		}
		done := make(chan struct{})
		go func() {
			_ = fb.SubmitFolderIndex(row(3)) // blocks: ErrFull -> retry -> success
			close(done)
		}()

		// Both item1 and item2 must be enqueued (pending>0) before item3 can be
		// blocked behind a full channel; then wait for item3 to land via retry.
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("retrying SubmitFolderIndex did not complete")
		}
		// Each of the three rows incremented inflight via submitFolderIndexOnce;
		// the stuck generation prevents any OnSuccess decrement, so inflight=3.
		if got := fb.FolderIndexInflight(); got != 3 {
			t.Errorf("expected inflight 3 after retry submit, got %d", got)
		}
	})

	t.Run("returns error when pending stuck", func(t *testing.T) {
		db := testDB(t)
		// ChannelSize 1, flush blocked so pending cannot fall.
		var blockMu sync.Mutex
		blockMu.Lock()
		flushEntered := make(chan struct{})
		var flushOnce sync.Once
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx: testBeginTx(db),
			Flush: func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error {
				flushOnce.Do(func() { close(flushEntered) })
				blockMu.Lock()
				_ = len(batch)
				blockMu.Unlock()
				return nil
			},
			MaxBatchSize: 1,
			ChannelSize:  1,
			DQueDirPath:  "", // no overflow; ErrFull on full
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() {
			blockMu.Unlock()
			wb.Close()
		})
		var inflight, generation atomic.Int64
		var rebuildActive atomic.Bool
		// Short stall so CI does not wait 30s on the stuck-pending path.
		fb := &fileBatcher{wb: wb, inflight: &inflight, rebuildActive: &rebuildActive, generation: &generation, submitStall: 10 * time.Millisecond}

		// Fill the slot; flush is blocked so it never drains.
		if err := fb.SubmitFolderIndex(makeFolderIndexRow(1, 1)); err != nil {
			t.Fatalf("first SubmitFolderIndex failed: %v", err)
		}
		<-flushEntered
		// Worker holds item 1 in flush; refill the channel so the next submit
		// hits ErrFull, retries until the 10ms stall, then returns (pending
		// never falls).
		if err := fb.SubmitFolderIndex(makeFolderIndexRow(2, 1)); err != nil {
			t.Fatalf("second SubmitFolderIndex failed: %v", err)
		}
		start := time.Now()
		if err := fb.SubmitFolderIndex(makeFolderIndexRow(3, 1)); !errors.Is(err, writebatcher.ErrFull) {
			t.Fatalf("stuck SubmitFolderIndex: expected ErrFull, got %v", err)
		}
		if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
			t.Errorf("stuck submit returned too fast: %v", elapsed)
		}
		// Undo must have preserved the two successful submits' inflight.
		if got := fb.FolderIndexInflight(); got != 2 {
			t.Errorf("expected inflight 2 (undo ran), got %d", got)
		}
	})

	t.Run("SetFolderIndexRebuildActive stores flag", func(t *testing.T) {
		db := testDB(t)
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      testBeginTx(db),
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() { wb.Close() })
		var inflight, generation atomic.Int64
		var rebuildActive atomic.Bool
		var rebuildScan atomic.Bool
		fb := newFileBatcher(wb, &inflight, &rebuildActive, &rebuildScan, &generation)

		fb.SetFolderIndexRebuildActive(true)
		if !rebuildActive.Load() {
			t.Error("expected rebuildActive true after SetFolderIndexRebuildActive(true)")
		}
		fb.SetFolderIndexRebuildActive(false)
		if rebuildActive.Load() {
			t.Error("expected rebuildActive false after SetFolderIndexRebuildActive(false)")
		}
	})

	t.Run("BumpFolderIndexGeneration returns distinct non-zero values", func(t *testing.T) {
		db := testDB(t)
		wb, err := writebatcher.New[BatchedWrite](context.Background(), writebatcher.Config[BatchedWrite]{
			BeginTx:      testBeginTx(db),
			Flush:        func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error { return nil },
			MaxBatchSize: 10,
			ChannelSize:  10,
		})
		if err != nil {
			t.Fatalf("New writebatcher: %v", err)
		}
		t.Cleanup(func() { wb.Close() })
		var inflight, generation atomic.Int64
		var rebuildActive atomic.Bool
		var rebuildScan atomic.Bool
		fb := newFileBatcher(wb, &inflight, &rebuildActive, &rebuildScan, &generation)

		a := fb.BumpFolderIndexGeneration()
		b := fb.BumpFolderIndexGeneration()
		if a == 0 {
			t.Errorf("first generation should be non-zero, got %d", a)
		}
		if b == 0 {
			t.Errorf("second generation should be non-zero, got %d", b)
		}
		if a == b {
			t.Errorf("generations should differ: a=%d b=%d", a, b)
		}
		if got := generation.Load(); got != b {
			t.Errorf("stored generation = %d, want second bump %d", got, b)
		}
	})
}
