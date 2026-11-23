//go:build integration

package discovery

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/metadata"
	"github.com/lbe/sfpg-go/internal/parallelwalkdir"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/internal/workerpool"
)

// newTestExtractor returns a metadata.Extractor that the test closes.
func newTestExtractor(t *testing.T) *metadata.Extractor {
	t.Helper()
	e := metadata.New()
	t.Cleanup(func() {
		if err := e.Close(); err != nil {
			t.Errorf("extractor Close: %v", err)
		}
	})
	return e
}

// TestRunImportsAndSkipsUnchangedFiles runs discovery against a real database,
// a real writer and the real processor over three runs: the first imports,
// the second finds everything unchanged, the third processes a changed file.
// A text file named .jpg and a JPEG whose body is broken are recorded invalid.
func TestRunImportsAndSkipsUnchangedFiles(t *testing.T) {
	dir := t.TempDir()
	normalized := filepath.ToSlash(dir)
	db := openTestDatabase(t, normalized)
	writeJPEG(t, dir, "a.jpg", 16, testMtime)
	writePNG(t, dir, "sub/b.png", 12, testMtime)
	writeFile(t, dir, "broken.jpg", []byte("not an image"), testMtime)
	writeFile(t, dir, "poison.jpg", []byte{0xFF, 0xD8, 0xFF, 0x00}, testMtime)

	proc := files.NewFileProcessor(dir, newTestExtractor(t))
	t.Cleanup(func() {
		if err := proc.Close(); err != nil {
			t.Errorf("processor Close: %v", err)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	stats := &Stats{}
	svc := newTestService(t, Config{ImagesDir: dir, NormalizedImagesDir: normalized}, Deps{
		Processor: proc,
		Writer:    db.writer,
		Catalog:   db.catalog,
		State:     db.state,
		Pool:      workerpool.NewPool(ctx, 2, 1, time.Minute),
		Stats:     stats,
	})
	// Cleanups run last in first-registered order, so the workers stop
	// before svc.Wait waits for them.
	t.Cleanup(cancel)
	svc.Start()

	runAndCheck := func(name string, want metrics.FileProcessingMetrics) {
		t.Helper()
		if err := svc.Run(t.Context()); err != nil {
			t.Fatalf("%s: Run: %v", name, err)
		}
		if got := stats.GetStats(); got != want {
			t.Errorf("%s: counters = %+v, want %+v", name, got, want)
		}
		saved, err := db.state.LoadFileProcessing(t.Context(), moduleName)
		if err != nil {
			t.Fatalf("%s: LoadFileProcessing: %v", name, err)
		}
		if saved != want {
			t.Errorf("%s: saved counters = %+v, want %+v", name, saved, want)
		}
	}

	runAndCheck("first run", metrics.FileProcessingMetrics{TotalFound: 4, NewlyInserted: 2, SkippedInvalid: 2})

	if st, ok := catalogEntry(t, db.catalog, normalized, "a.jpg"); !ok || !st.FileIDValid {
		t.Errorf("a.jpg after first run: entry %+v present %v, want a valid row", st, ok)
	}
	if st, ok := catalogEntry(t, db.catalog, normalized+"/sub", "b.png"); !ok || !st.FileIDValid {
		t.Errorf("sub/b.png after first run: entry %+v present %v, want a valid row", st, ok)
	}
	if st, ok := catalogEntry(t, db.catalog, normalized, "broken.jpg"); !ok || !st.InvalidPathValid ||
		st.InvalidMtime != testMtime || st.InvalidSize != int64(len("not an image")) {
		t.Errorf("broken.jpg after first run: entry %+v present %v, want an invalid record", st, ok)
	}
	if st, ok := catalogEntry(t, db.catalog, normalized, "poison.jpg"); !ok || !st.InvalidPathValid || st.FileIDValid {
		t.Errorf("poison.jpg after first run: entry %+v present %v, want an invalid record and no file row", st, ok)
	}

	runAndCheck("second run", metrics.FileProcessingMetrics{TotalFound: 4, AlreadyExisting: 2, SkippedInvalid: 2})

	writeJPEG(t, dir, "a.jpg", 24, testMtime+60)
	runAndCheck("third run", metrics.FileProcessingMetrics{TotalFound: 4, AlreadyExisting: 1, NewlyInserted: 1, SkippedInvalid: 2})
	if st, ok := catalogEntry(t, db.catalog, normalized, "a.jpg"); !ok || st.FileMtime != testMtime+60 {
		t.Errorf("a.jpg after change: entry %+v present %v, want mtime %d", st, ok, testMtime+60)
	}
}

// writeJPEG writes a small JPEG of the given size to rel under dir.
func writeJPEG(t *testing.T, dir, rel string, side int, mtime int64) {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, gradient(side), nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	writeFile(t, dir, rel, buf.Bytes(), mtime)
}

// writePNG writes a small PNG of the given size to rel under dir.
func writePNG(t *testing.T, dir, rel string, side int, mtime int64) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, gradient(side)); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	writeFile(t, dir, rel, buf.Bytes(), mtime)
}

func gradient(side int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			img.Set(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), B: 128, A: 255})
		}
	}
	return img
}

// catalogEntry returns the catalog's state for name in dir, and whether the
// directory has an entry for it.
func catalogEntry(t *testing.T, catalog parallelwalkdir.GetDirEntMapFunc, dir, name string) (parallelwalkdir.DirEntState, bool) {
	t.Helper()
	m, err := catalog(t.Context(), []byte(dir))
	if err != nil {
		t.Fatalf("catalog(%q): %v", dir, err)
	}
	st, ok := m[parallelwalkdir.HashPathBytes([]byte(name))]
	return st, ok
}
