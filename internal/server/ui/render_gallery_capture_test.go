package ui

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/lbe/sfpg-go/web"
)

// fakeCaptureWriter implements bodyCaptureWriter for testing.
// It records whether CommitCapturedBody or ResetCapturedBody were called.
type fakeCaptureWriter struct {
	sink      *bytes.Buffer
	committed bool
	reset     bool
	commitErr error
}

func (f *fakeCaptureWriter) CaptureBodyWriter() io.Writer {
	if f.sink == nil {
		f.sink = new(bytes.Buffer)
	}
	return f.sink
}

func (f *fakeCaptureWriter) CommitCapturedBody() error {
	f.committed = true
	return f.commitErr
}

func (f *fakeCaptureWriter) ResetCapturedBody() {
	f.reset = true
	if f.sink != nil {
		f.sink.Reset()
	}
}

// Write is a no-op so fakeCaptureWriter satisfies io.Writer for type assertion
// compatibility (some callers may write directly).
func (f *fakeCaptureWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestRenderGalleryInto_CapturePath verifies that renderGalleryInto uses the
// capture sink path when w implements bodyCaptureWriter.
func TestRenderGalleryInto_CapturePath(t *testing.T) {
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Minimal gallery data — enough for template execution to succeed
	// with non-nil slices.
	data := map[string]any{
		"Breadcrumbs": []any{},
		"GalleryName": "Test",
		"ImageCount":  1,
		"IsImageView": false,
		"Thumbs":      []any{},
	}

	t.Run("partial render fills sink and commits", func(t *testing.T) {
		cw := &fakeCaptureWriter{}
		err := renderGalleryInto(cw, func(w io.Writer) error {
			if err := galleryPartialTemplate.Execute(w, data); err != nil {
				return err
			}
			return galleryOOBTemplate.Execute(w, data)
		})
		if err != nil {
			t.Fatalf("renderGalleryInto returned error: %v", err)
		}
		if cw.sink == nil || cw.sink.Len() == 0 {
			t.Error("expected sink to be filled on success")
		}
		if !cw.committed {
			t.Error("expected CommitCapturedBody to be called on success")
		}
		if cw.reset {
			t.Error("expected ResetCapturedBody NOT to be called on success")
		}
	})

	t.Run("full layout render fills sink and commits", func(t *testing.T) {
		if galleryTemplate == nil {
			t.Fatal("galleryTemplate not initialized")
		}
		cw := &fakeCaptureWriter{}
		err := renderGalleryInto(cw, func(w io.Writer) error {
			return galleryTemplate.ExecuteTemplate(w, "layout", data)
		})
		if err != nil {
			t.Fatalf("renderGalleryInto returned error: %v", err)
		}
		if cw.sink == nil || cw.sink.Len() == 0 {
			t.Error("expected sink to be filled on success")
		}
		if !cw.committed {
			t.Error("expected CommitCapturedBody to be called on success")
		}
		if cw.reset {
			t.Error("expected ResetCapturedBody NOT to be called on success")
		}
	})

	t.Run("exec error calls Reset and not Commit", func(t *testing.T) {
		cw := &fakeCaptureWriter{}
		execErr := errors.New("exec failure")
		err := renderGalleryInto(cw, func(w io.Writer) error {
			return execErr
		})
		if !errors.Is(err, execErr) {
			t.Fatalf("expected exec error, got: %v", err)
		}
		if !cw.reset {
			t.Error("expected ResetCapturedBody on exec error")
		}
		if cw.committed {
			t.Error("expected CommitCapturedBody NOT to be called on exec error")
		}
	})

	t.Run("commit error is propagated", func(t *testing.T) {
		cw := &fakeCaptureWriter{
			commitErr: errors.New("commit failure"),
		}
		err := renderGalleryInto(cw, func(w io.Writer) error {
			return nil
		})
		if err == nil || err.Error() != "commit failure" {
			t.Fatalf("expected commit error, got: %v", err)
		}
		if !cw.committed {
			t.Error("expected CommitCapturedBody to be called")
		}
	})
}

// TestRenderGalleryInto_FallbackPath verifies the non-capture writer path
// using the galleryBufPool + single Write approach.
func TestRenderGalleryInto_FallbackPath(t *testing.T) {
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	data := map[string]any{
		"Breadcrumbs": []any{},
		"GalleryName": "Test",
		"ImageCount":  1,
		"IsImageView": false,
		"Thumbs":      []any{},
	}

	t.Run("partial with bytes.Buffer uses fallback pool path", func(t *testing.T) {
		var buf bytes.Buffer
		err := renderGalleryInto(&buf, func(w io.Writer) error {
			if err := galleryPartialTemplate.Execute(w, data); err != nil {
				return err
			}
			return galleryOOBTemplate.Execute(w, data)
		})
		if err != nil {
			t.Fatalf("renderGalleryInto returned error: %v", err)
		}
		if buf.Len() == 0 {
			t.Error("expected output in buffer")
		}
	})

	t.Run("full layout with bytes.Buffer uses fallback pool path", func(t *testing.T) {
		if galleryTemplate == nil {
			t.Fatal("galleryTemplate not initialized")
		}
		var buf bytes.Buffer
		err := renderGalleryInto(&buf, func(w io.Writer) error {
			return galleryTemplate.ExecuteTemplate(w, "layout", data)
		})
		if err != nil {
			t.Fatalf("renderGalleryInto returned error: %v", err)
		}
		if buf.Len() == 0 {
			t.Error("expected output in buffer")
		}
	})
}
