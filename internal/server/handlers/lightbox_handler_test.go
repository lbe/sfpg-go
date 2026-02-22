package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/testutil"
	"golang.org/x/net/html"
)

func hasAttr(n *html.Node, key string) bool {
	if n == nil {
		return false
	}
	for _, attr := range n.Attr {
		if attr.Key == key {
			return true
		}
	}
	return false
}

// --- LightboxByID Tests ---

func TestLightboxByID_NoImages(t *testing.T) {
	qh := &lightboxEmptyList{fakeHandlerQueries{
		fileView: gallerydb.FileView{ID: 1, Path: "test.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
	}}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/lightbox/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.LightboxByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestLightboxByID_InvalidID(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/lightbox/abc", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()

	gh.LightboxByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestLightboxByID_FileNotFound(t *testing.T) {
	qh := &fakeHandlerQueries{getFileViewByIDErr: sql.ErrNoRows}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/lightbox/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.LightboxByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestLightboxByID_FolderListError(t *testing.T) {
	qh := &fakeHandlerQueries{getImagesErr: errors.New("db error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/lightbox/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.LightboxByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestLightboxByID_DBConnectionError(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})
	gh.DBRoPool = errConnPool{getErr: errors.New("no db")}

	req := httptest.NewRequest(http.MethodGet, "/lightbox/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.LightboxByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestLightboxByID_SingleImageNoNavigation(t *testing.T) {
	images := []gallerydb.FileView{{ID: 1}}
	qh := &lightboxList{
		fakeHandlerQueries: fakeHandlerQueries{
			fileView: gallerydb.FileView{ID: 1, Path: "test.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
		},
		images: images,
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/lightbox/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.LightboxByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	prev := testutil.FindElementByID(doc, "lightbox-prev-btn")
	if prev == nil {
		t.Fatal("missing #lightbox-prev-btn")
	}
	if !hasAttr(prev, "disabled") {
		t.Error("expected prev button to be disabled")
	}
	next := testutil.FindElementByID(doc, "lightbox-next-btn")
	if next == nil {
		t.Fatal("missing #lightbox-next-btn")
	}
	if !hasAttr(next, "disabled") {
		t.Error("expected next button to be disabled")
	}
}

func TestLightboxByID_FolderViewError(t *testing.T) {
	qh := &fakeHandlerQueries{
		fileView:             gallerydb.FileView{ID: 1, Path: "test.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
		getFolderViewByIDErr: errors.New("folder error"),
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/lightbox/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.LightboxByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestLightboxByID_BreadcrumbError(t *testing.T) {
	qh := &breadcrumbErrorQueries{
		fakeHandlerQueries: fakeHandlerQueries{
			fileView: gallerydb.FileView{ID: 1, Path: "test.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
		},
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/lightbox/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.LightboxByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestLightboxByID_RenderTemplateError(t *testing.T) {
	images := []gallerydb.FileView{{ID: 1}}
	qh := &lightboxList{
		fakeHandlerQueries: fakeHandlerQueries{
			fileView: gallerydb.FileView{ID: 1, Path: "test.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
		},
		images: images,
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/lightbox/1", nil)
	req.SetPathValue("id", "1")
	w := &errorResponseWriter{}

	gh.LightboxByID(w, req)

	if w.status != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.status)
	}
}

func TestLightboxByID_FileNotInFolderList(t *testing.T) {
	qh := &fakeHandlerQueries{
		fileView:     gallerydb.FileView{ID: 10, Path: "test.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
		getImagesErr: nil,
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/lightbox/10", nil)
	req.SetPathValue("id", "10")
	w := httptest.NewRecorder()

	gh.LightboxByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestLightboxByID_Success(t *testing.T) {
	images := []gallerydb.FileView{{ID: 1}, {ID: 2}, {ID: 3}}
	qh := &lightboxList{
		fakeHandlerQueries: fakeHandlerQueries{
			fileView: gallerydb.FileView{ID: 2, Path: "test.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
		},
		images: images,
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/lightbox/2", nil)
	req.SetPathValue("id", "2")
	w := httptest.NewRecorder()

	gh.LightboxByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Header().Get("HX-Push-URL") != "" {
		t.Errorf("expected no HX-Push-URL, got %q", w.Header().Get("HX-Push-URL"))
	}
}

func TestLightboxByID_WrapsPrevFromFirst(t *testing.T) {
	images := []gallerydb.FileView{{ID: 1}, {ID: 2}, {ID: 3}}
	qh := &lightboxList{
		fakeHandlerQueries: fakeHandlerQueries{
			fileView: gallerydb.FileView{ID: 1, Path: "test.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
		},
		images: images,
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/lightbox/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.LightboxByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	prev := testutil.FindElementByID(doc, "lightbox-prev-btn")
	if prev == nil {
		t.Fatal("missing #lightbox-prev-btn")
	}
	if got := testutil.GetAttr(prev, "hx-get"); !strings.Contains(got, "/lightbox/3") {
		t.Errorf("prev hx-get = %q, want lightbox/3", got)
	}
	if got := testutil.GetAttr(prev, "_"); !strings.Contains(got, "/raw-image/3") {
		t.Errorf("prev _ attr = %q, want raw-image/3", got)
	}

	next := testutil.FindElementByID(doc, "lightbox-next-btn")
	if next == nil {
		t.Fatal("missing #lightbox-next-btn")
	}
	if got := testutil.GetAttr(next, "hx-get"); !strings.Contains(got, "/lightbox/2") {
		t.Errorf("next hx-get = %q, want lightbox/2", got)
	}
	if got := testutil.GetAttr(next, "_"); !strings.Contains(got, "/raw-image/2") {
		t.Errorf("next _ attr = %q, want raw-image/2", got)
	}
}

func TestLightboxByID_WrapsNextFromLast(t *testing.T) {
	images := []gallerydb.FileView{{ID: 1}, {ID: 2}, {ID: 3}}
	qh := &lightboxList{
		fakeHandlerQueries: fakeHandlerQueries{
			fileView: gallerydb.FileView{ID: 3, Path: "test.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
		},
		images: images,
	}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/lightbox/3", nil)
	req.SetPathValue("id", "3")
	w := httptest.NewRecorder()

	gh.LightboxByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	next := testutil.FindElementByID(doc, "lightbox-next-btn")
	if next == nil {
		t.Fatal("missing #lightbox-next-btn")
	}
	if got := testutil.GetAttr(next, "hx-get"); !strings.Contains(got, "/lightbox/1") {
		t.Errorf("next hx-get = %q, want lightbox/1", got)
	}
	if got := testutil.GetAttr(next, "_"); !strings.Contains(got, "/raw-image/1") {
		t.Errorf("next _ attr = %q, want raw-image/1", got)
	}

	prev := testutil.FindElementByID(doc, "lightbox-prev-btn")
	if prev == nil {
		t.Fatal("missing #lightbox-prev-btn")
	}
	if got := testutil.GetAttr(prev, "hx-get"); !strings.Contains(got, "/lightbox/2") {
		t.Errorf("prev hx-get = %q, want lightbox/2", got)
	}
	if got := testutil.GetAttr(prev, "_"); !strings.Contains(got, "/raw-image/2") {
		t.Errorf("prev _ attr = %q, want raw-image/2", got)
	}
}
