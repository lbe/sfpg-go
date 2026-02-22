package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- ImageByID Tests ---

func TestImageByID_NotFound(t *testing.T) {
	qh := &fakeHandlerQueries{getFileViewByIDErr: sql.ErrNoRows}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.ImageByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestImageByID_InvalidID(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/image/abc", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()

	gh.ImageByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestImageByID_DBConnectionError(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})
	gh.DBRoPool = errConnPool{getErr: errors.New("no db")}

	req := httptest.NewRequest(http.MethodGet, "/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.ImageByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestImageByID_BreadcrumbError(t *testing.T) {
	qh := &breadcrumbErrorQueries{}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.ImageByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestImageByID_DBError(t *testing.T) {
	qh := &fakeHandlerQueries{getFileViewByIDErr: errors.New("db error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.ImageByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestImageByID_Success(t *testing.T) {
	qh := &fakeHandlerQueries{}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.ImageByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}
