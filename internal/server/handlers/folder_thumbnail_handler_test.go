package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// --- FolderThumbnailByID Tests ---

func TestFolderThumbnailByID_NoTile(t *testing.T) {
	qh := &fakeHandlerQueries{folder: gallerydb.Folder{ID: 1, TileID: sql.NullInt64{Valid: false}}}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.FolderThumbnailByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 with placeholder, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "image/svg+xml") {
		t.Errorf("expected svg placeholder, got %s", w.Header().Get("Content-Type"))
	}
}

func TestFolderThumbnailByID_DBError(t *testing.T) {
	qh := &fakeHandlerQueries{getFolderByIDErr: errors.New("db error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.FolderThumbnailByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestFolderThumbnailByID_InvalidID(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/folder/abc", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()

	gh.FolderThumbnailByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestFolderThumbnailByID_ThumbnailNotFound(t *testing.T) {
	qh := &fakeHandlerQueries{
		folder:         gallerydb.Folder{ID: 1, TileID: sql.NullInt64{Int64: 5, Valid: true}},
		thumbByFileErr: sql.ErrNoRows,
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.FolderThumbnailByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 with placeholder, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "image/svg+xml") {
		t.Errorf("expected svg placeholder, got %s", w.Header().Get("Content-Type"))
	}
}

func TestFolderThumbnailByID_BlobReadError(t *testing.T) {
	qh := &fakeHandlerQueries{
		folder:       gallerydb.Folder{ID: 1, TileID: sql.NullInt64{Int64: 5, Valid: true}},
		thumbBlobErr: errors.New("blob error"),
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.FolderThumbnailByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestFolderThumbnailByID_DBConnectionError(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})
	gh.DBRoPool = errConnPool{getErr: errors.New("no db")}

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.FolderThumbnailByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestFolderThumbnailByID_FolderNotFound(t *testing.T) {
	qh := &fakeHandlerQueries{getFolderByIDErr: sql.ErrNoRows}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.FolderThumbnailByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 with placeholder, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "image/svg+xml") {
		t.Errorf("expected svg placeholder, got %s", w.Header().Get("Content-Type"))
	}
}

func TestFolderThumbnailByID_Success(t *testing.T) {
	qh := &fakeHandlerQueries{folder: gallerydb.Folder{ID: 1, TileID: sql.NullInt64{Int64: 5, Valid: true}}}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/folder/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.FolderThumbnailByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "image/jpeg") {
		t.Errorf("expected jpeg content type, got %s", w.Header().Get("Content-Type"))
	}
}
