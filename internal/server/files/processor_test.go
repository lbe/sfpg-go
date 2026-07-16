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
	"github.com/lbe/sfpg-go/internal/queue"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

func TestRecordInvalidFileFromPath(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		td := t.TempDir()
		fullPath := filepath.Join(td, "bad.txt")
		if err := os.WriteFile(fullPath, []byte("not an image"), 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		info, err := os.Stat(fullPath)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}

		fp := &fakeProcessor{}
		processErr := errors.New("non-image file: text/plain")
		if err := recordInvalidFileFromPath(context.Background(), fp, fullPath, "bad.txt", processErr); err != nil {
			t.Fatalf("recordInvalidFileFromPath: %v", err)
		}

		if len(fp.recordInvalidCalls) != 1 {
			t.Fatalf("expected 1 RecordInvalidFile call, got %d", len(fp.recordInvalidCalls))
		}
		call := fp.recordInvalidCalls[0]
		if call.path != "bad.txt" {
			t.Errorf("path = %q, want %q", call.path, "bad.txt")
		}
		if call.mtime != info.ModTime().Unix() {
			t.Errorf("mtime = %d, want %d", call.mtime, info.ModTime().Unix())
		}
		if call.size != info.Size() {
			t.Errorf("size = %d, want %d", call.size, info.Size())
		}
		if call.reason != "non-image" {
			t.Errorf("reason = %q, want %q", call.reason, "non-image")
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
		if err != nil {
			t.Fatalf("expected nil for invalid markers, got: %v", err)
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
	q := queue.NewQueue[string](1)
	if err := q.Enqueue("/tmp/Images/test.jpg"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	fp := &fakeProcessor{}
	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poolFunc := NewPoolFuncWithProcessor(fp, q, "/tmp/Images", testRemovePrefix, nil)
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

func TestRunPoolWorkerWithProcessor_ErrorPaths(t *testing.T) {
	t.Run("remove prefix error", func(t *testing.T) {
		q := queue.NewQueue[string](1)
		if err := q.Enqueue("/bad/prefix/file.jpg"); err != nil {
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
			done <- runPoolWorkerWithProcessor(ctx, pool, nil, q.Len, 1, fp, q, "/tmp/Images", testRemovePrefix, nil)
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
		q := queue.NewQueue[string](2)
		if err := q.Enqueue("/tmp/Images/process-bad.jpg"); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		if err := q.Enqueue("/tmp/Images/thumb-bad.jpg"); err != nil {
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
			done <- runPoolWorkerWithProcessor(ctx, pool, nil, q.Len, 1, fp, q, "/tmp/Images", testRemovePrefix, nil)
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
	q := queue.NewQueue[string](1)

	processor := NewFileProcessor(nil, nil, nil, "/tmp/Images", &mockUnifiedBatcher{})
	defer processor.Close()

	pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
	pool.Stats.RunningWorkers.Add(1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	poolFunc := NewPoolFuncWithProcessor(processor, q, "/tmp/Images", testRemovePrefix, nil)
	if err := poolFunc(ctx, pool, nil, nil, q.Len, 1); err != nil {
		t.Fatalf("expected nil error on cancelled context, got %v", err)
	}
}

func TestRunPoolWorkerWithProcessor_Stats(t *testing.T) {
	t.Run("skipped invalid", func(t *testing.T) {
		q := queue.NewQueue[string](1)
		if err := q.Enqueue("/tmp/Images/skip.jpg"); err != nil {
			t.Fatalf("enqueue: %v", err)
		}

		fp := &fakeProcessor{skipAsInvalidByPath: map[string]bool{"skip.jpg": true}}
		pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
		pool.Stats.RunningWorkers.Add(1)
		stats := &ProcessingStats{}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		baseline := pool.Stats.CompletedTasks.Load()
		go func() {
			done <- runPoolWorkerWithProcessor(ctx, pool, nil, q.Len, 1, fp, q, "/tmp/Images", testRemovePrefix, stats)
		}()

		waitForCompleted(t, pool, baseline+1)
		cancel()

		if err := <-done; err != nil {
			t.Fatalf("runPoolWorkerWithProcessor returned error: %v", err)
		}
		if stats.SkippedInvalid.Load() != 1 {
			t.Errorf("expected SkippedInvalid 1, got %d", stats.SkippedInvalid.Load())
		}
	})

	t.Run("already existing", func(t *testing.T) {
		q := queue.NewQueue[string](1)
		if err := q.Enqueue("/tmp/Images/exists.jpg"); err != nil {
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
			done <- runPoolWorkerWithProcessor(ctx, pool, nil, q.Len, 1, fp, q, "/tmp/Images", testRemovePrefix, stats)
		}()

		waitForCompleted(t, pool, baseline+1)
		cancel()

		if err := <-done; err != nil {
			t.Fatalf("runPoolWorkerWithProcessor returned error: %v", err)
		}
		if stats.AlreadyExisting.Load() != 1 {
			t.Errorf("expected AlreadyExisting 1, got %d", stats.AlreadyExisting.Load())
		}
	})

	t.Run("queue closed", func(t *testing.T) {
		q := queue.NewQueue[string](1)
		if err := q.Enqueue("/tmp/Images/closed.jpg"); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		q.Close()

		fp := &fakeProcessor{}
		pool := workerpool.NewPool(context.Background(), 1, 1, 10*time.Millisecond)
		pool.Stats.RunningWorkers.Add(1)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		poolFunc := NewPoolFuncWithProcessor(fp, q, "/tmp/Images", testRemovePrefix, nil)
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
