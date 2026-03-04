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

	"golang.org/x/net/html"
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

func TestInfoBoxImage_DownloadLinkHasCorrectFilename(t *testing.T) {
	qh := &fakeHandlerQueries{
		fileView: gallerydb.FileView{
			ID:       123,
			Filename: "my-photo.jpg",
			Path:     "subdir/my-photo.jpg",
			FolderID: sql.NullInt64{Int64: 1, Valid: true},
		},
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/info/image/123", nil)
	req.SetPathValue("id", "123")
	w := httptest.NewRecorder()

	gh.InfoBoxImage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Find all <a> tags with download attribute (even if empty)
	links := testutil.FindAllElements(doc, func(n *html.Node) bool {
		if n.Data != "a" {
			return false
		}
		// Check if download attribute exists (even if empty)
		for _, attr := range n.Attr {
			if attr.Key == "download" {
				return true
			}
		}
		return false
	})

	if len(links) == 0 {
		t.Fatal("no download link found with download attribute")
	}

	// Check the download link that matches our file ID
	var downloadLink *html.Node
	for _, link := range links {
		href := testutil.GetAttr(link, "href")
		if href != "" && len(href) > 11 && href[:11] == "/raw-image/" {
			downloadLink = link
			break
		}
	}

	if downloadLink == nil {
		t.Fatal("download link for raw image not found")
	}

	downloadAttr := testutil.GetAttr(downloadLink, "download")
	expectedFilename := "my-photo.jpg"
	if downloadAttr != expectedFilename {
		t.Errorf("download attribute = %q, want %q", downloadAttr, expectedFilename)
	}
}
