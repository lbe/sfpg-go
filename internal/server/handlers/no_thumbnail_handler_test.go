package handlers

import (
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
