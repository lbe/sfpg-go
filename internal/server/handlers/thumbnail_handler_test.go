package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- ThumbnailByID Tests ---

func TestThumbnailByID_NotFound(t *testing.T) {
	qh := &fakeHandlerQueries{thumbByFileErr: sql.ErrNoRows}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.ThumbnailByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 with placeholder, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "image/svg+xml") {
		t.Errorf("expected svg placeholder, got %s", w.Header().Get("Content-Type"))
	}
}

func TestThumbnailByID_DBError(t *testing.T) {
	qh := &fakeHandlerQueries{thumbByFileErr: errors.New("db error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.ThumbnailByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestThumbnailByID_InvalidID(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/abc", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()

	gh.ThumbnailByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestThumbnailByID_BlobNotFound(t *testing.T) {
	qh := &fakeHandlerQueries{thumbBlobErr: sql.ErrNoRows}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.ThumbnailByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 with placeholder, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "image/svg+xml") {
		t.Errorf("expected svg placeholder, got %s", w.Header().Get("Content-Type"))
	}
}

func TestThumbnailByID_BlobReadError(t *testing.T) {
	qh := &fakeHandlerQueries{thumbBlobErr: errors.New("blob error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.ThumbnailByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestThumbnailByID_DBConnectionError(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})
	gh.DBRoPool = errConnPool{getErr: errors.New("no db")}

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.ThumbnailByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestThumbnailByID_Success(t *testing.T) {
	qh := &fakeHandlerQueries{}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/thumbnail/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.ThumbnailByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "image/jpeg") {
		t.Errorf("expected jpeg content type, got %s", w.Header().Get("Content-Type"))
	}
	if w.Body.String() != "thumb" {
		t.Errorf("expected thumbnail body, got %q", w.Body.String())
	}
}
