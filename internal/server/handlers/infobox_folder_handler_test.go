package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
)

type infoBoxFolderQueries struct {
	fakeHandlerQueries
	infoCounts gallerydb.GetFolderInfoCountsByIDRow
}

func (i infoBoxFolderQueries) GetFolderInfoCountsByID(ctx context.Context, id int64) (gallerydb.GetFolderInfoCountsByIDRow, error) {
	if i.getFolderInfoCountsErr != nil {
		return gallerydb.GetFolderInfoCountsByIDRow{}, i.getFolderInfoCountsErr
	}
	return i.infoCounts, nil
}

// --- InfoBoxFolder Tests ---

func TestInfoBoxFolder_NotFound(t *testing.T) {
	qh := &fakeHandlerQueries{getFolderByIDErr: sql.ErrNoRows}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/info/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxFolder(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestInfoBoxFolder_DBError(t *testing.T) {
	qh := &fakeHandlerQueries{getFolderByIDErr: errors.New("db error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/info/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxFolder(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestInfoBoxFolder_LastModifiedAndCounts(t *testing.T) {
	updatedAt := time.Date(2026, 2, 12, 10, 0, 0, 0, time.UTC).Unix()
	qh := &infoBoxFolderQueries{
		fakeHandlerQueries: fakeHandlerQueries{
			folder: gallerydb.Folder{ID: 1, Name: "Test Folder", UpdatedAt: updatedAt},
		},
		infoCounts: gallerydb.GetFolderInfoCountsByIDRow{
			DirCount:   1,
			ImageCount: 1,
			FileCount:  1,
		},
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/info/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxFolder(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Verify Content-Type
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("expected Content-Type: text/html; charset=utf-8, got %q", contentType)
	}

	if w.Header().Get("Last-Modified") != time.Unix(updatedAt, 0).UTC().Format(http.TimeFormat) {
		t.Errorf("unexpected Last-Modified header: %q", w.Header().Get("Last-Modified"))
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if w.Header().Get("HX-Push-URL") != "" {
		t.Error("expected no HX-Push-URL for info box (must not change URL for lightbox close navigation)")
	}
	if testutil.FindElementByID(doc, "folder-image-count") == nil {
		t.Fatal("missing #folder-image-count")
	}
	if testutil.FindElementByID(doc, "folder-file-count") == nil {
		t.Fatal("missing #folder-file-count")
	}
	if testutil.FindElementByID(doc, "folder-dir-count") == nil {
		t.Fatal("missing #folder-dir-count")
	}
}

func TestInfoBoxFolder_InvalidID(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/info/folder/abc", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()

	gh.InfoBoxFolder(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestInfoBoxFolder_CountsQueryError(t *testing.T) {
	qh := &fakeHandlerQueries{getFolderInfoCountsErr: errors.New("db error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/info/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxFolder(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestInfoBoxFolder_DBConnectionError(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})
	gh.DBRoPool = errConnPool{getErr: errors.New("no db")}

	req := httptest.NewRequest(http.MethodGet, "/info/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxFolder(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestInfoBoxFolder_UpdatedAtFallback(t *testing.T) {
	qh := &fakeHandlerQueries{folder: gallerydb.Folder{ID: 1, Name: "Test", UpdatedAt: "not-int"}}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/info/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxFolder(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Header().Get("Last-Modified") == "" {
		t.Error("expected Last-Modified header to be set")
	}
	if _, err := http.ParseTime(w.Header().Get("Last-Modified")); err != nil {
		t.Errorf("expected Last-Modified to be parseable, got %q", w.Header().Get("Last-Modified"))
	}
}

func TestInfoBoxFolder_CacheBusting(t *testing.T) {
	qh := &fakeHandlerQueries{
		folder: gallerydb.Folder{ID: 99, Name: "CacheBust"},
	}
	gh := setupTestGalleryHandlers(t, qh)

	url := fmt.Sprintf("/info/folder/99?v=%s", ui.GetCacheVersion())
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", "99")
	w := httptest.NewRecorder()

	gh.InfoBoxFolder(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}
