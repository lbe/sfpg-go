package files

import (
	"context"
	"database/sql"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

func testDiscoveryPath(path string) DiscoveryPathWork {
	return DiscoveryPathWork{Path: []byte(path), MtimeUnix: 1, SizeBytes: 1}
}

func TestRecordInvalidFileFromPath(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		processor, _, rwPool, imagesDir := createTestProcessor(t, nil)
		fullPath := filepath.Join(imagesDir, "bad.txt")
		if err := os.WriteFile(fullPath, []byte("not an image"), 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		info, err := os.Stat(fullPath)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}

		processErr := errors.New("non-image file: text/plain")
		if recErr := recordInvalidFileFromPath(context.Background(), processor, fullPath, "bad.txt", processErr); recErr != nil {
			t.Fatalf("recordInvalidFileFromPath: %v", recErr)
		}

		cpcRw, err := rwPool.Get()
		if err != nil {
			t.Fatalf("get rw: %v", err)
		}
		defer rwPool.Put(cpcRw)
		inv, err := cpcRw.Queries.GetInvalidFileByPath(context.Background(), "bad.txt")
		if err != nil {
			t.Fatalf("GetInvalidFileByPath: %v", err)
		}
		if inv.Path != "bad.txt" || inv.Mtime != info.ModTime().Unix() || inv.Size != info.Size() {
			t.Fatalf("invalid row: %+v", inv)
		}
		if !inv.Reason.Valid || inv.Reason.String != "non-image" {
			t.Fatalf("reason: %+v", inv.Reason)
		}
		rootID, err := cpcRw.Queries.GetFolderIDByPath(context.Background(), "")
		if err != nil {
			t.Fatalf("GetFolderIDByPath root: %v", err)
		}
		if inv.FolderID != rootID {
			t.Fatalf("folder_id = %d, want %d", inv.FolderID, rootID)
		}
	})

	t.Run("stat failure", func(t *testing.T) {
		fp := &fakeProcessor{}
		err := recordInvalidFileFromPath(context.Background(), fp, "/does/not/exist.txt", "missing.txt", errors.New("boom"))
		if err == nil {
			t.Fatal("expected error for missing file")
		}
		if len(fp.recordInvalidCalls) != 0 {
			t.Errorf("expected no RecordInvalidFile calls, got %d", len(fp.recordInvalidCalls))
		}
	})
}

func TestProcessFileContents(t *testing.T) {
	t.Run("non-image file", func(t *testing.T) {
		td := t.TempDir()
		fn := filepath.Join(td, "not-image.txt")
		if err := os.WriteFile(fn, []byte("hello world"), 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		file := &File{ImagesDir: td, Path: "not-image.txt", File: File{}.File}
		err := processFileContents(file)
		if err == nil {
			t.Fatal("expected non-image error, got nil")
		}
		if !strings.Contains(err.Error(), "non-image") {
			t.Errorf("expected non-image error, got: %v", err)
		}
	})

	t.Run("invalid JPEG markers", func(t *testing.T) {
		td := t.TempDir()
		fn := filepath.Join(td, "poison.jpg")
		// SOI + 0xFF + stuffing byte: detected as JPEG by magic, but no valid marker.
		data := []byte{0xFF, 0xD8, 0xFF, 0x00}
		if err := os.WriteFile(fn, data, 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		file := &File{
			ImagesDir: td,
			Path:      "poison.jpg",
			File:      gallerydb.File{SizeBytes: sql.NullInt64{Int64: int64(len(data)), Valid: true}},
		}
		err := processFileContents(file)
		if err == nil {
			t.Fatal("expected error for invalid markers, got nil")
		}
		if !strings.Contains(err.Error(), "invalid JPEG markers") {
			t.Errorf("expected invalid JPEG markers error, got: %v", err)
		}
	})

	t.Run("file open error", func(t *testing.T) {
		file := &File{ImagesDir: "/does/not/exist", Path: "missing.jpg", File: File{}.File}
		err := processFileContents(file)
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("valid image", func(t *testing.T) {
		td := t.TempDir()
		fn := filepath.Join(td, "valid.png")
		f, err := os.Create(fn)
		if err != nil {
			t.Fatalf("create temp file: %v", err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		if encErr := png.Encode(f, img); encErr != nil {
			f.Close()
			t.Fatalf("encode png: %v", encErr)
		}
		if closeErr := f.Close(); closeErr != nil {
			t.Fatalf("close: %v", closeErr)
		}
		info, err := os.Stat(fn)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}

		file := &File{
			ImagesDir: td,
			Path:      "valid.png",
			File:      gallerydb.File{SizeBytes: sql.NullInt64{Int64: info.Size(), Valid: true}},
		}
		if err := processFileContents(file); err != nil {
			t.Fatalf("processFileContents: %v", err)
		}
		if !file.File.Width.Valid || file.File.Width.Int64 != 4 {
			t.Errorf("Width = %v, want 4", file.File.Width)
		}
		if !file.File.Height.Valid || file.File.Height.Int64 != 4 {
			t.Errorf("Height = %v, want 4", file.File.Height)
		}
		if !file.File.Md5.Valid || file.File.Md5.String == "" {
			t.Error("expected Md5 populated")
		}
		if !file.File.Phash.Valid {
			t.Error("expected Phash populated")
		}
		if file.Thumbnail == nil || file.Thumbnail.Len() == 0 {
			t.Error("expected Thumbnail populated")
		}
	})

	t.Run("decode config error", func(t *testing.T) {
		td := t.TempDir()
		fn := filepath.Join(td, "corrupt.png")
		// PNG magic followed by invalid chunks.
		if err := os.WriteFile(fn, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0xFF, 0xFF}, 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		info, err := os.Stat(fn)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}

		file := &File{
			ImagesDir: td,
			Path:      "corrupt.png",
			File:      gallerydb.File{SizeBytes: sql.NullInt64{Int64: info.Size(), Valid: true}},
		}
		err = processFileContents(file)
		if err == nil {
			t.Fatal("expected decode error")
		}
	})
}

func TestNewPoolFuncWithProcessor_Success(t *testing.T) {
	q := queue.NewQueue[DiscoveryPathWork](1)
	if err := q.Enqueue(testDiscoveryPath("/tmp/Images/test.jpg")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	fp := &fakeProcessor{}
	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poolFunc := NewPoolFuncWithProcessor(fp, q, "/tmp/Images", testRemovePrefix, nil, nil)
	done := make(chan error, 1)
	baseline := pool.Stats.CompletedTasks.Load()

	go func() {
		done <- poolFunc(ctx, pool, nil, nil, q.Len, 1)
	}()

	waitForCompleted(t, pool, baseline+1)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("runPoolWorkerWithProcessor returned error: %v", err)
	}
	if pool.Stats.SuccessfulTasks.Load() == 0 {
		t.Fatalf("expected successful task count to be > 0")
	}
}

func TestRunPoolWorkerWithProcessor_OnFileInserted(t *testing.T) {
	q := queue.NewQueue[DiscoveryPathWork](1)
	if err := q.Enqueue(testDiscoveryPath("/tmp/Images/test.jpg")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	fp := &fakeProcessor{}
	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)
	stats := &ProcessingStats{}

	var insertedSizes []int64
	onFileInserted := func(size int64) {
		insertedSizes = append(insertedSizes, size)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	baseline := pool.Stats.CompletedTasks.Load()
	go func() {
		done <- runPoolWorkerWithProcessor(ctx, pool, nil, q.Len, 1, fp, q, "/tmp/Images", testRemovePrefix, stats, onFileInserted)
	}()

	waitForCompleted(t, pool, baseline+1)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("runPoolWorkerWithProcessor returned error: %v", err)
	}

	if len(insertedSizes) != 1 {
		t.Fatalf("onFileInserted called %d times, want 1", len(insertedSizes))
	}
	if insertedSizes[0] <= 0 {
		t.Errorf("onFileInserted size = %d, want > 0", insertedSizes[0])
	}
}

func TestRunPoolWorkerWithProcessor_PoisonPillJPEG(t *testing.T) {
	var submitCount int
	ub := &mockUnifiedBatcher{
		SubmitFileFunc: func(*File) error {
			submitCount++
			return nil
		},
	}
	processor, roPool, _, imagesDir := createTestProcessor(t, ub)

	relPath := "poison.jpg"
	data := []byte{0xFF, 0xD8, 0xFF, 0x00}
	if err := os.WriteFile(filepath.Join(imagesDir, relPath), data, 0o644); err != nil {
		t.Fatalf("write poison pill: %v", err)
	}

	q := queue.NewQueue[DiscoveryPathWork](1)
	if err := q.Enqueue(testDiscoveryPath(filepath.Join(imagesDir, relPath))); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	baseline := pool.Stats.CompletedTasks.Load()
	go func() {
		done <- runPoolWorkerWithProcessor(ctx, pool, roPool, q.Len, 1, processor, q, imagesDir, testRemovePrefix, nil, nil)
	}()

	waitForCompleted(t, pool, baseline+1)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("runPoolWorkerWithProcessor returned error: %v", err)
	}
	if pool.Stats.FailedTasks.Load() != 1 {
		t.Errorf("FailedTasks = %d, want 1", pool.Stats.FailedTasks.Load())
	}
	if submitCount != 0 {
		t.Errorf("SubmitFile calls = %d, want 0", submitCount)
	}

	cpcRo, err := roPool.Get()
	if err != nil {
		t.Fatalf("get ro: %v", err)
	}
	defer roPool.Put(cpcRo)

	inv, err := cpcRo.Queries.GetInvalidFileByPath(context.Background(), relPath)
	if err != nil {
		t.Fatalf("GetInvalidFileByPath: %v", err)
	}
	if !inv.Reason.Valid || inv.Reason.String != "jpeg-markers" {
		t.Errorf("invalid_files reason = %v, want jpeg-markers", inv.Reason)
	}

	_, err = cpcRo.Queries.GetFileByPath(context.Background(), relPath)
	if err == nil {
		t.Fatal("expected no files row for poison pill")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetFileByPath: %v", err)
	}
}

func TestRunPoolWorkerWithProcessor_ErrorPaths(t *testing.T) {
	t.Run("remove prefix error", func(t *testing.T) {
		q := queue.NewQueue[DiscoveryPathWork](1)
		if err := q.Enqueue(testDiscoveryPath("/bad/prefix/file.jpg")); err != nil {
			t.Fatalf("enqueue: %v", err)
		}

		fp := &fakeProcessor{}
		pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
		pool.Stats.RunningWorkers.Add(1)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		baseline := pool.Stats.CompletedTasks.Load()
		go func() {
			done <- runPoolWorkerWithProcessor(ctx, pool, nil, q.Len, 1, fp, q, "/tmp/Images", testRemovePrefix, nil, nil)
		}()

		waitForCompleted(t, pool, baseline+1)
		cancel()

		if err := <-done; err != nil {
			t.Fatalf("runPoolWorkerWithProcessor returned error: %v", err)
		}
		if pool.Stats.FailedTasks.Load() == 0 {
			t.Fatalf("expected failed task count to be > 0")
		}
	})

	t.Run("process and thumbnail errors", func(t *testing.T) {
		q := queue.NewQueue[DiscoveryPathWork](2)
		if err := q.Enqueue(testDiscoveryPath("/tmp/Images/process-bad.jpg")); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		if err := q.Enqueue(testDiscoveryPath("/tmp/Images/thumb-bad.jpg")); err != nil {
			t.Fatalf("enqueue: %v", err)
		}

		fp := &fakeProcessor{
			processErrByPath: map[string]error{
				"process-bad.jpg": errors.New("process failed"),
			},
			thumbErrByPath: map[string]error{
				"thumb-bad.jpg": errors.New("thumb failed"),
			},
		}
		pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
		pool.Stats.RunningWorkers.Add(1)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		baseline := pool.Stats.CompletedTasks.Load()
		go func() {
			done <- runPoolWorkerWithProcessor(ctx, pool, nil, q.Len, 1, fp, q, "/tmp/Images", testRemovePrefix, nil, nil)
		}()

		waitForCompleted(t, pool, baseline+2)
		cancel()

		if err := <-done; err != nil {
			t.Fatalf("runPoolWorkerWithProcessor returned error: %v", err)
		}
		if pool.Stats.FailedTasks.Load() < 2 {
			t.Fatalf("expected failed task count >= 2")
		}
	})
}

func TestRunPoolWorker_ContextCancelled(t *testing.T) {
	q := queue.NewQueue[DiscoveryPathWork](1)

	processor := NewFileProcessor(nil, nil, nil, "/tmp/Images", &mockUnifiedBatcher{})
	defer processor.Close()

	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	poolFunc := NewPoolFuncWithProcessor(processor, q, "/tmp/Images", testRemovePrefix, nil, nil)
	if err := poolFunc(ctx, pool, nil, nil, q.Len, 1); err != nil {
		t.Fatalf("expected nil error on cancelled context, got %v", err)
	}
}

func TestRunPoolWorkerWithProcessor_Stats(t *testing.T) {
	t.Run("processing failure records invalid", func(t *testing.T) {
		processor, roPool, _, imagesDir := createTestProcessor(t, &mockUnifiedBatcher{})
		relPath := "bad-worker.jpg"
		data := []byte{0xFF, 0xD8, 0xFF, 0x00}
		if err := os.WriteFile(filepath.Join(imagesDir, relPath), data, 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		q := queue.NewQueue[DiscoveryPathWork](1)
		if err := q.Enqueue(testDiscoveryPath(filepath.Join(imagesDir, relPath))); err != nil {
			t.Fatalf("enqueue: %v", err)
		}

		pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
		pool.Stats.RunningWorkers.Add(1)
		stats := &ProcessingStats{}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		baseline := pool.Stats.CompletedTasks.Load()
		go func() {
			done <- runPoolWorkerWithProcessor(ctx, pool, roPool, q.Len, 1, processor, q, imagesDir, testRemovePrefix, stats, nil)
		}()

		waitForCompleted(t, pool, baseline+1)
		cancel()

		if err := <-done; err != nil {
			t.Fatalf("runPoolWorkerWithProcessor returned error: %v", err)
		}
		if stats.SkippedInvalid.Load() != 1 {
			t.Errorf("expected SkippedInvalid 1, got %d", stats.SkippedInvalid.Load())
		}
		if stats.NewlyInserted.Load() != 0 {
			t.Errorf("expected NewlyInserted 0, got %d", stats.NewlyInserted.Load())
		}
	})

	t.Run("submit path counts as newly inserted", func(t *testing.T) {
		q := queue.NewQueue[DiscoveryPathWork](1)
		if err := q.Enqueue(testDiscoveryPath("/tmp/Images/exists.jpg")); err != nil {
			t.Fatalf("enqueue: %v", err)
		}

		fp := &fakeProcessor{}
		pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
		pool.Stats.RunningWorkers.Add(1)
		stats := &ProcessingStats{}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Override ProcessFile to return Exists=true.
		fp.processExistsByPath = map[string]bool{"exists.jpg": true}

		done := make(chan error, 1)
		baseline := pool.Stats.CompletedTasks.Load()
		go func() {
			done <- runPoolWorkerWithProcessor(ctx, pool, nil, q.Len, 1, fp, q, "/tmp/Images", testRemovePrefix, stats, nil)
		}()

		waitForCompleted(t, pool, baseline+1)
		cancel()

		if err := <-done; err != nil {
			t.Fatalf("runPoolWorkerWithProcessor returned error: %v", err)
		}
		if stats.AlreadyExisting.Load() != 0 {
			t.Errorf("expected AlreadyExisting 0, got %d", stats.AlreadyExisting.Load())
		}
		if stats.NewlyInserted.Load() != 1 {
			t.Errorf("expected NewlyInserted 1, got %d", stats.NewlyInserted.Load())
		}
		fp.mu.Lock()
		generated := fp.generated
		fp.mu.Unlock()
		if generated != 1 {
			t.Errorf("expected SubmitFileForWrite for existing path, generated=%d want 1", generated)
		}
	})

	t.Run("queue closed", func(t *testing.T) {
		q := queue.NewQueue[DiscoveryPathWork](1)
		if err := q.Enqueue(testDiscoveryPath("/tmp/Images/closed.jpg")); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		q.Close()

		fp := &fakeProcessor{}
		pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
		pool.Stats.RunningWorkers.Add(1)

		ctx := t.Context()

		poolFunc := NewPoolFuncWithProcessor(fp, q, "/tmp/Images", testRemovePrefix, nil, nil)
		if err := poolFunc(ctx, pool, nil, nil, q.Len, 1); err != nil {
			t.Fatalf("expected nil error on closed queue, got %v", err)
		}
	})
}

func TestProcessingStats_GetStats(t *testing.T) {
	t.Run("returns stats from underlying processing stats", func(t *testing.T) {
		stats := &ProcessingStats{}
		stats.TotalFound.Store(100)
		stats.AlreadyExisting.Store(50)
		stats.NewlyInserted.Store(30)
		stats.SkippedInvalid.Store(10)
		stats.InFlight.Store(5)

		got := stats.GetStats()

		want := metrics.FileProcessingMetrics{
			TotalFound:      100,
			AlreadyExisting: 50,
			NewlyInserted:   30,
			SkippedInvalid:  10,
			InFlight:        5,
		}
		if got != want {
			t.Errorf("GetStats() = %+v, want %+v", got, want)
		}
	})

	t.Run("returns zeros for empty stats", func(t *testing.T) {
		stats := &ProcessingStats{}

		got := stats.GetStats()

		want := metrics.FileProcessingMetrics{}
		if got != want {
			t.Errorf("GetStats() = %+v, want %+v", got, want)
		}
	})
}

func TestProcessDiscoveryWorkerFile_RequiresWalkMetadata(t *testing.T) {
	file := &File{ImagesDir: t.TempDir(), Path: "x.jpg"}
	err := processDiscoveryWorkerFile(file)
	if err == nil {
		t.Fatal("expected error for missing walk metadata")
	}
}

func TestProcessDiscoveryWorkerFile_AlwaysProcessFileContents(t *testing.T) {
	td := t.TempDir()
	fn := filepath.Join(td, "walk.png")
	f, err := os.Create(fn)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if encErr := png.Encode(f, img); encErr != nil {
		f.Close()
		t.Fatalf("encode: %v", encErr)
	}
	f.Close()
	info, err := os.Stat(fn)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	file := &File{
		ImagesDir: td,
		Path:      "walk.png",
		File: gallerydb.File{
			Mtime:     sql.NullInt64{Int64: info.ModTime().Unix(), Valid: true},
			SizeBytes: sql.NullInt64{Int64: info.Size(), Valid: true},
		},
	}
	if err := processDiscoveryWorkerFile(file); err != nil {
		t.Fatalf("processDiscoveryWorkerFile: %v", err)
	}
	if file.Thumbnail == nil || file.Thumbnail.Len() == 0 {
		t.Fatal("expected thumbnail from processFileContents")
	}
	if file.Exists {
		t.Fatal("processDiscoveryWorkerFile must leave Exists false before write")
	}
}

func TestProcessDiscoveryFile_ExistingRowStillProcessesContents(t *testing.T) {
	processor, _, rwPool, imagesDir := createTestProcessor(t, nil)
	path := createTestImage(t, imagesDir, "reprocess.jpg")

	ctx := context.Background()
	cpcRw, err := rwPool.Get()
	if err != nil {
		t.Fatalf("rw get: %v", err)
	}
	tx, err := cpcRw.Conn.BeginTx(ctx, nil)
	if err != nil {
		rwPool.Put(cpcRw)
		t.Fatalf("begin tx: %v", err)
	}
	imp := &gallerylib.Importer{Q: cpcRw.Queries.WithTx(tx)}
	seed := &File{
		Path: path,
		File: gallerydb.File{
			Mtime:     sql.NullInt64{Int64: 1, Valid: true},
			SizeBytes: sql.NullInt64{Int64: 100, Valid: true},
			MimeType:  sql.NullString{String: "image/jpeg", Valid: true},
		},
	}
	if writeErr := WriteFileInTx(ctx, imp, seed); writeErr != nil {
		tx.Rollback()
		rwPool.Put(cpcRw)
		t.Fatalf("seed WriteFileInTx: %v", writeErr)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		rwPool.Put(cpcRw)
		t.Fatalf("commit: %v", commitErr)
	}
	rwPool.Put(cpcRw)

	info, err := os.Stat(filepath.Join(imagesDir, path))
	if err != nil {
		t.Fatalf("stat image: %v", err)
	}

	walkFile := &File{
		Path: path,
		File: gallerydb.File{
			Mtime:     sql.NullInt64{Int64: info.ModTime().Unix(), Valid: true},
			SizeBytes: sql.NullInt64{Int64: info.Size(), Valid: true},
		},
	}
	out, err := processor.ProcessDiscoveryFile(ctx, walkFile)
	if err != nil {
		t.Fatalf("ProcessDiscoveryFile: %v", err)
	}
	if out.Thumbnail == nil || out.Thumbnail.Len() == 0 {
		t.Fatal("expected processFileContents to run for existing DB row")
	}
	if out.Exists {
		t.Fatal("discovery worker must not set Exists before WriteFileInTx")
	}
}

func TestProcessDiscoveryFile_AlwaysProcessFileContents(t *testing.T) {
	processor, _, rwPool, imagesDir := createTestProcessor(t, nil)
	path := createTestImage(t, imagesDir, "discovery-dequeue.jpg")
	info, err := os.Stat(filepath.Join(imagesDir, path))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	ctx := context.Background()
	file := &File{
		Path: path,
		File: gallerydb.File{
			Mtime:     sql.NullInt64{Int64: info.ModTime().Unix(), Valid: true},
			SizeBytes: sql.NullInt64{Int64: info.Size(), Valid: true},
		},
	}
	out, err := processor.ProcessDiscoveryFile(ctx, file)
	if err != nil {
		t.Fatalf("ProcessDiscoveryFile: %v", err)
	}
	if out.Thumbnail == nil || out.Thumbnail.Len() == 0 {
		t.Fatal("expected processFileContents for dequeued discovery item")
	}
	_ = rwPool // pool used internally
}
