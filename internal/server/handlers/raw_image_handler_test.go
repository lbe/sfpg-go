package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.local/sfpg/internal/gallerydb"
)

// --- RawImageByID Tests ---

func TestRawImageByID_NotFound(t *testing.T) {
	qh := &fakeHandlerQueries{getFileViewByIDErr: sql.ErrNoRows}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/raw-image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.RawImageByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestRawImageByID_DBError(t *testing.T) {
	qh := &fakeHandlerQueries{getFileViewByIDErr: errors.New("db error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/raw-image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.RawImageByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestRawImageByID_DBConnectionError(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})
	gh.DBRoPool = errConnPool{getErr: errors.New("no db")}

	req := httptest.NewRequest(http.MethodGet, "/raw-image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.RawImageByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestRawImageByID_InvalidID(t *testing.T) {
	qh := &fakeHandlerQueries{}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/raw-image/abc", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()

	gh.RawImageByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestRawImageByID_PathTraversal(t *testing.T) {
	qh := &fakeHandlerQueries{fileView: gallerydb.FileView{ID: 1, Path: "../evil.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}}}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/raw-image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.RawImageByID(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

func TestRawImageByID_AllowsValidPath(t *testing.T) {
	imagesDir := t.TempDir()
	filePath := filepath.Join(imagesDir, "ok.jpg")
	if err := os.WriteFile(filePath, []byte("ok"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	qh := &fakeHandlerQueries{fileView: gallerydb.FileView{ID: 1, Path: "ok.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}}}
	gh := setupTestGalleryHandlers(t, qh)
	gh.ImagesDir = imagesDir
	gh.GetImagesDir = func() string { return imagesDir }

	req := httptest.NewRequest(http.MethodGet, "/raw-image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.RawImageByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "image/jpeg") && !strings.Contains(w.Header().Get("Content-Type"), "image/") {
		t.Errorf("expected image content type, got %s", w.Header().Get("Content-Type"))
	}
}
