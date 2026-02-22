package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/testutil"
)

type metadataQueriesZeroLatLong struct{}

func (metadataQueriesZeroLatLong) GetExifByFile(ctx context.Context, fileID int64) (gallerydb.ExifMetadatum, error) {
	return gallerydb.ExifMetadatum{
		FileID:    fileID,
		Latitude:  sql.NullFloat64{Float64: 0, Valid: true},
		Longitude: sql.NullFloat64{Float64: 0, Valid: true},
	}, nil
}

func (metadataQueriesZeroLatLong) GetIPTCByFile(ctx context.Context, fileID int64) (gallerydb.IptcMetadatum, error) {
	return gallerydb.IptcMetadatum{}, sql.ErrNoRows
}

// --- InfoBoxImage Tests ---

func TestInfoBoxImage_NotFound(t *testing.T) {
	qh := &fakeHandlerQueries{getFileViewByIDErr: sql.ErrNoRows}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/info/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxImage(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestInfoBoxImage_DBError(t *testing.T) {
	qh := &fakeHandlerQueries{getFileViewByIDErr: errors.New("db error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/info/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxImage(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestInfoBoxImage_MetadataError(t *testing.T) {
	qh := &fakeHandlerQueries{}
	gh := setupTestGalleryHandlers(t, qh)
	gh.GetMetadataQueries = func(*dbconnpool.CpConn) MetadataQueries {
		return mockMetadataQueriesWithError{exifErr: errors.New("exif error")}
	}

	req := httptest.NewRequest(http.MethodGet, "/info/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxImage(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestInfoBoxImage_Success(t *testing.T) {
	qh := &fakeHandlerQueries{}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/info/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxImage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestInfoBoxImage_ZeroLatLongDoesNotRenderMap(t *testing.T) {
	qh := &fakeHandlerQueries{}
	gh := setupTestGalleryHandlers(t, qh)
	gh.GetMetadataQueries = func(*dbconnpool.CpConn) MetadataQueries {
		return metadataQueriesZeroLatLong{}
	}

	req := httptest.NewRequest(http.MethodGet, "/info/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxImage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if testutil.FindElementByID(doc, "exif-maps-link") != nil {
		t.Fatal("expected no map link for zero lat/long")
	}
}

func TestInfoBoxImage_InvalidID(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/info/image/abc", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()

	gh.InfoBoxImage(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestInfoBoxImage_FileListError(t *testing.T) {
	qh := &fakeHandlerQueries{getImagesErr: errors.New("db error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/info/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxImage(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestInfoBoxImage_IptcError(t *testing.T) {
	qh := &fakeHandlerQueries{}
	gh := setupTestGalleryHandlers(t, qh)
	gh.GetMetadataQueries = func(*dbconnpool.CpConn) MetadataQueries {
		return mockMetadataQueriesWithError{iptcErr: errors.New("iptc error")}
	}

	req := httptest.NewRequest(http.MethodGet, "/info/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxImage(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestInfoBoxImage_DBConnectionError(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})
	gh.DBRoPool = errConnPool{getErr: errors.New("no db")}

	req := httptest.NewRequest(http.MethodGet, "/info/image/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.InfoBoxImage(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}
