package server

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestWalkImageDir_EnqueuesOnlySupportedNonZeroImages verifies that walkImageDir()
// enqueues only non-zero-sized files with extensions matching (jpg|jpeg|png|gif),
// and skips zero-length and non-image files.
func TestWalkImageDir_EnqueuesOnlySupportedNonZeroImages(t *testing.T) {
	t.Setenv("SEPG_SESSION_SECRET", "test-secret")

	app := CreateApp(t, false)
	defer app.Shutdown()

	// Create a small set of files in the Images directory
	mustWrite := func(rel string, size int) string {
		p := filepath.Join(app.imagesDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir failed for %s: %v", p, err)
		}
		var data []byte
		if size > 0 {
			data = make([]byte, size)
			for i := range data {
				data[i] = byte(i%251 + 1)
			}
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatalf("write failed for %s: %v", p, err)
		}
		return p
	}

	// Supported and non-zero
	a := mustWrite("a.jpg", 10)
	b := mustWrite("b.jpeg", 1)
	c := mustWrite("c.png", 2)
	d := mustWrite("d.gif", 3)
	e := mustWrite("UPPER.JPG", 4)
	// Nested
	f := mustWrite("nested/x.jpg", 5)
	// Zero-length should be ignored
	_ = mustWrite("zero.jpg", 0)
	_ = mustWrite("nested/zero.png", 0)
	// Unsupported extensions should be ignored by walkImageDir's regex
	_ = mustWrite("photo.webp", 8)
	_ = mustWrite("doc.txt", 12)
	_ = mustWrite("image.tiff", 14)

	// Execute the walker which enqueues qualifying files into app.q
	app.walkImageDir()

	// Collect queued items; order is not guaranteed, so sort for comparison
	got := app.q.Slice()
	sort.Strings(got)

	want := []string{a, b, c, d, e, f}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("unexpected queue length: got %d, want %d; got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mismatch at %d: got %q, want %q\nall got=%v\nall want=%v", i, got[i], want[i], got, want)
		}
	}

	// Ensure sender accounting returned to zero
	if app.qSendersActive.Load() != 0 {
		t.Fatalf("qSendersActive not zero after walk: %d", app.qSendersActive.Load())
	}
}
