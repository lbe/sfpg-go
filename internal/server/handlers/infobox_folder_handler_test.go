package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/testutil"
)

type infoBoxFolderQueries struct {
	fakeHandlerQueries
	fileViews   []gallerydb.FileView
	folderViews []gallerydb.FolderView
}

func (i infoBoxFolderQueries) GetFileViewsByFolderIDOrderByFileName(ctx context.Context, folderID sql.NullInt64) ([]gallerydb.FileView, error) {
	return i.fileViews, nil
}

func (i infoBoxFolderQueries) GetFoldersViewsByParentIDOrderByName(ctx context.Context, parent sql.NullInt64) ([]gallerydb.FolderView, error) {
	return i.folderViews, nil
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
		fileViews: []gallerydb.FileView{
			{ID: 1, Path: "image.jpg"},
			{ID: 2, Path: "notes.txt"},
		},
		folderViews: []gallerydb.FolderView{{ID: 2, Name: "Child"}},
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/info/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxFolder(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Header().Get("Last-Modified") != time.Unix(updatedAt, 0).UTC().Format(http.TimeFormat) {
		t.Errorf("unexpected Last-Modified header: %q", w.Header().Get("Last-Modified"))
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if testutil.FindElementByID(doc, "folder-image-count") == nil {
		t.Fatal("missing #folder-image-count")
	}
	if testutil.FindElementByID(doc, "folder-file-count") == nil {
		t.Fatal("missing #folder-file-count")
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

func TestInfoBoxFolder_SubfolderQueryError(t *testing.T) {
	qh := &fakeHandlerQueries{getSubFoldersErr: errors.New("db error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/info/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxFolder(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestInfoBoxFolder_FileListQueryError(t *testing.T) {
	qh := &fakeHandlerQueries{getImagesErr: errors.New("db error")}
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
