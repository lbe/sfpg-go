package discovery

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/thumbnail"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

// echoProcessor returns a processor whose files are named by the path it was
// given, so tests can see the path discovery passed.
func echoProcessor() *fakeProcessor {
	return &fakeProcessor{
		process: func(_ context.Context, path string) (*files.File, error) {
			return &files.File{Path: path}, nil
		},
	}
}

func TestImportFileSubmitsProcessedFile(t *testing.T) {
	dir := t.TempDir()
	w := &fakeWriter{}
	var inserted []int64
	s := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: filepath.ToSlash(dir)}, Deps{
		Processor:      echoProcessor(),
		Writer:         w,
		OnFileInserted: func(size int64) { inserted = append(inserted, size) },
	})

	ok := s.importFile(t.Context(), PathWork{
		Path:      []byte(filepath.Join(dir, "sub", "a.jpg")),
		MtimeUnix: testMtime,
		SizeBytes: 123,
	})
	if !ok {
		t.Fatal("importFile() = false, want true")
	}
	if want := []string{"sub/a.jpg"}; !slices.Equal(w.submittedPaths(), want) {
		t.Errorf("submitted = %q, want %q", w.submittedPaths(), want)
	}
	if want := []int64{123}; !slices.Equal(inserted, want) {
		t.Errorf("OnFileInserted sizes = %v, want %v", inserted, want)
	}
	if want := (metrics.FileProcessingMetrics{NewlyInserted: 1}); s.deps.Stats.GetStats() != want {
		t.Errorf("counters = %+v, want %+v", s.deps.Stats.GetStats(), want)
	}
}

func TestImportFileRecordsInvalidFile(t *testing.T) {
	dir := t.TempDir()
	data := []byte("not a jpeg")
	writeFile(t, dir, "bad.jpg", data, testMtime)
	w := &fakeWriter{}
	s := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: filepath.ToSlash(dir)}, Deps{
		Processor: &fakeProcessor{process: func(context.Context, string) (*files.File, error) {
			return nil, errors.New("decode: bad header")
		}},
		Writer: w,
	})

	ok := s.importFile(t.Context(), PathWork{
		Path:      []byte(filepath.Join(dir, "bad.jpg")),
		MtimeUnix: testMtime,
		SizeBytes: int64(len(data)),
	})
	if ok {
		t.Fatal("importFile() = true, want false")
	}
	want := []files.InvalidFile{{Path: "bad.jpg", Mtime: testMtime, Size: int64(len(data)), Reason: "decode"}}
	if got := w.invalidFiles(); !slices.Equal(got, want) {
		t.Errorf("invalid records = %+v, want %+v", got, want)
	}
	if len(w.submittedPaths()) != 0 {
		t.Errorf("submitted = %q, want none", w.submittedPaths())
	}
	if want := (metrics.FileProcessingMetrics{SkippedInvalid: 1}); s.deps.Stats.GetStats() != want {
		t.Errorf("counters = %+v, want %+v", s.deps.Stats.GetStats(), want)
	}
}

func TestImportFileCanceledRecordsNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "slow.jpg", []byte("data"), testMtime)
	w := &fakeWriter{}
	s := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: filepath.ToSlash(dir)}, Deps{
		Processor: &fakeProcessor{process: func(ctx context.Context, _ string) (*files.File, error) {
			return nil, ctx.Err()
		}},
		Writer: w,
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	ok := s.importFile(ctx, PathWork{Path: []byte(filepath.Join(dir, "slow.jpg")), MtimeUnix: testMtime, SizeBytes: 4})
	if ok {
		t.Fatal("importFile() = true, want false")
	}
	if got := w.invalidFiles(); len(got) != 0 {
		t.Errorf("invalid records = %+v, want none", got)
	}
	if want := (metrics.FileProcessingMetrics{}); s.deps.Stats.GetStats() != want {
		t.Errorf("counters = %+v, want %+v", s.deps.Stats.GetStats(), want)
	}
}

// TestImportFileSubmitFailureReportsFalse checks that a file the writer
// rejects is not counted as inserted and gives its thumbnail buffer back.
func TestImportFileSubmitFailureReportsFalse(t *testing.T) {
	dir := t.TempDir()
	inserted := false
	w := &fakeWriter{submitErr: errors.New("writer closed")}
	var rejected *files.File
	s := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: filepath.ToSlash(dir)}, Deps{
		Processor: &fakeProcessor{process: func(_ context.Context, path string) (*files.File, error) {
			buf := thumbnail.GetBytesBuffer()
			buf.WriteString("thumb")
			rejected = &files.File{Path: path, Thumbnail: buf}
			return rejected, nil
		}},
		Writer:         w,
		OnFileInserted: func(int64) { inserted = true },
	})

	ok := s.importFile(t.Context(), PathWork{Path: []byte(filepath.Join(dir, "a.jpg")), MtimeUnix: testMtime, SizeBytes: 1})
	if ok {
		t.Error("importFile() = true, want false")
	}
	if inserted {
		t.Error("OnFileInserted ran for a file that was not submitted")
	}
	if got := w.invalidFiles(); len(got) != 0 {
		t.Errorf("invalid records = %+v, want none", got)
	}
	if rejected.Thumbnail != nil {
		t.Error("the rejected file still holds its thumbnail buffer")
	}
}

func TestImportFileOutsideImagesDirIsNotProcessed(t *testing.T) {
	dir := t.TempDir()
	w := &fakeWriter{}
	s := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: filepath.ToSlash(dir)}, Deps{
		Processor: &fakeProcessor{process: func(_ context.Context, path string) (*files.File, error) {
			t.Errorf("processor reached for %q, which is outside the images directory", path)
			return nil, errors.New("unexpected")
		}},
		Writer: w,
	})

	ok := s.importFile(t.Context(), PathWork{Path: []byte("/elsewhere/a.jpg"), MtimeUnix: testMtime, SizeBytes: 1})
	if ok {
		t.Error("importFile() = true, want false")
	}
	if len(w.submittedPaths()) != 0 || len(w.invalidFiles()) != 0 {
		t.Errorf("wrote submitted=%q invalid=%+v, want nothing", w.submittedPaths(), w.invalidFiles())
	}
}

// TestWorkerStopsWhenCanceled cancels the workers while one processes a file:
// that file is not recorded invalid, and the next queued file is not taken.
func TestWorkerStopsWhenCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		w := &fakeWriter{}
		s := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: filepath.ToSlash(dir)}, Deps{
			Processor: &fakeProcessor{process: func(pctx context.Context, _ string) (*files.File, error) {
				cancel() // shutdown arrives while the file is processed
				return nil, fmt.Errorf("extract metadata: %w", pctx.Err())
			}},
			Writer: w,
			Pool:   workerpool.NewPool(ctx, 1, 1, time.Minute),
		})
		for _, name := range []string{"first.jpg", "second.jpg"} {
			writeFile(t, dir, name, []byte("x"), testMtime)
			if err := s.backlog.enqueue(PathWork{Path: []byte(filepath.Join(dir, name)), MtimeUnix: testMtime, SizeBytes: 1}); err != nil {
				t.Fatal(err)
			}
		}
		s.Start()
		s.Wait()

		if got := w.invalidFiles(); len(got) != 0 {
			t.Errorf("invalid records = %+v, want none for canceled work", got)
		}
		if n := s.QueueLen(); n != 1 {
			t.Errorf("QueueLen = %d after cancellation, want the second file left queued", n)
		}
		if n := s.deps.Stats.InFlight.Load(); n != 0 {
			t.Errorf("InFlight = %d after the workers stopped, want 0", n)
		}
	})
}
