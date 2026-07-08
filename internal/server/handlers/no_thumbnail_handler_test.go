package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lbe/sfpg-go/internal/testutil"
)

// --- NoThumbnail Tests ---

func TestNoThumbnail_ReturnsSVG(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	w := httptest.NewRecorder()

	gh.NoThumbnail(w)

	if w.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("expected svg content type, got %s", w.Header().Get("Content-Type"))
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if testutil.FindElementByTag(doc, "svg") == nil {
		t.Fatal("missing svg element")
	}
}

// errWriteResponseWriter is a fake http.ResponseWriter whose Write always fails.
type errWriteResponseWriter struct {
	header http.Header
	status int
}

func (w *errWriteResponseWriter) Header() http.Header {
	return w.header
}

func (w *errWriteResponseWriter) WriteHeader(status int) {
	w.status = status
}

func (w *errWriteResponseWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestNoThumbnail_WriteError(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	fw := &errWriteResponseWriter{header: make(http.Header)}
	gh.NoThumbnail(fw)

	if fw.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("expected svg content type, got %s", fw.Header().Get("Content-Type"))
	}
}
