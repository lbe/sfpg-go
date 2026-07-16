package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
	"github.com/lbe/sfpg-go/internal/testutil"
)

type breadcrumbErrorQueries struct {
	fakeHandlerQueries
	calls int
}

func (b *breadcrumbErrorQueries) GetFolderViewByID(ctx context.Context, id int64) (gallerydb.FolderView, error) {
	b.calls++
	if b.calls == 1 {
		return gallerydb.FolderView{ID: id, Name: "Child", ParentID: sql.NullInt64{Int64: 2, Valid: true}}, nil
	}
	return gallerydb.FolderView{}, errors.New("breadcrumb error")
}

// --- GalleryByID Tests ---

func TestGalleryByID_InvalidID(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/gallery/abc", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestGalleryByID_FolderNotFound(t *testing.T) {
	qh := &fakeHandlerQueries{getFolderViewByIDErr: sql.ErrNoRows}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestGalleryByID_SubFoldersQueryError(t *testing.T) {
	qh := &fakeHandlerQueries{getSubFoldersErr: errors.New("db error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestGalleryByID_ImagesQueryError(t *testing.T) {
	qh := &fakeHandlerQueries{getImagesErr: errors.New("db error")}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestGalleryByID_SuccessFullPage(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	pushURL := w.Header().Get("HX-Push-URL")
	if pushURL == "" {
		t.Error("expected HX-Push-URL to be set")
	}
	if pushURL != "/gallery/1?v=test-etag" {
		t.Errorf("expected HX-Push-URL to be /gallery/1?v=test-etag, got %q", pushURL)
	}
}

func TestGalleryByID_HTMXPartialDisablesCache(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.SetPathValue("id", "1")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "gallery-content")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("expected Cache-Control no-store, got %q", w.Header().Get("Cache-Control"))
	}
}

func TestGalleryByID_HTMXNonTargetKeepsCache(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.SetPathValue("id", "1")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "not-gallery-content")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Header().Get("Cache-Control") == "no-store" {
		t.Errorf("expected cache to remain public, got %q", w.Header().Get("Cache-Control"))
	}
}

func TestGetSessionIDForPreload_UsesCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session-name", Value: "abc123"})
	req.RemoteAddr = "192.0.2.1:1234"

	if got := getSessionIDForPreload(req); got != "abc123" {
		t.Errorf("getSessionIDForPreload = %q, want %q", got, "abc123")
	}
}

func TestGetSessionIDForPreload_FallsBackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:1234"

	if got := getSessionIDForPreload(req); got != "192.0.2.1:1234" {
		t.Errorf("getSessionIDForPreload = %q, want %q", got, "192.0.2.1:1234")
	}
}

func TestFixupDirectoryName_Truncates(t *testing.T) {
	name := strings.Repeat("a", 30)
	result := fixupDirectoryName(name)
	// Center-ellipsis: "..." appears in the middle, not at the end.
	if !strings.Contains(result, "...") {
		t.Errorf("expected center-ellipsis in truncated name, got %q", result)
	}
	if strings.HasSuffix(result, "...") {
		t.Errorf("expected center-ellipsis (not right-truncation), got %q", result)
	}
	if !strings.HasPrefix(result, "📁︎ ") {
		t.Errorf("expected folder prefix, got %q", result)
	}
}

func TestFixupDirectoryName_ShortName(t *testing.T) {
	name := "short"
	result := fixupDirectoryName(name)
	if !strings.HasSuffix(result, name) {
		t.Errorf("expected result to end with %q, got %q", name, result)
	}
}

func TestFixupFileName_TruncatesBaseKeepsExt(t *testing.T) {
	name := "verylongfilename012345678901234.jpg"
	result := fixupFileName(name)
	if !strings.HasSuffix(result, ".jpg") {
		t.Errorf("expected result to keep extension, got %q", result)
	}
	if !strings.Contains(result, "...") {
		t.Errorf("expected truncated base, got %q", result)
	}
}

func TestGalleryByID_BreadcrumbError(t *testing.T) {
	qh := &breadcrumbErrorQueries{}
	gh := setupTestGalleryHandlers(t, qh)

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestGalleryByID_RenderPageError(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.SetPathValue("id", "1")
	w := &errorResponseWriter{}

	gh.GalleryByID(w, req)

	if w.status != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.status)
	}
}

func TestGalleryByID_SchedulesPreload(t *testing.T) {
	preload := &mockPreloadService{called: make(chan struct{}, 1)}
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})
	gh.PreloadService = preload

	req := httptest.NewRequest(http.MethodGet, "/gallery/2", nil)
	req.SetPathValue("id", "2")
	req.RemoteAddr = "192.0.2.1:1234"
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	select {
	case <-preload.called:
		if preload.lastID != 2 {
			t.Errorf("expected preload folder id 2, got %d", preload.lastID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected preload to be scheduled")
	}
}

func TestGalleryByID_DBConnectionError(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})
	gh.DBRoPool = errConnPool{getErr: errors.New("no db")}

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestGalleryByID_SkipsPreloadForInternalRequest(t *testing.T) {
	preload := &mockPreloadService{called: make(chan struct{}, 1)}
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})
	gh.PreloadService = preload

	req := httptest.NewRequest(http.MethodGet, "/gallery/2", nil)
	req.SetPathValue("id", "2")
	req.Header.Set(cachepreload.InternalPreloadHeader, "true")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	select {
	case <-preload.called:
		t.Fatal("did not expect preload to be scheduled for internal request")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestGalleryByID_ETagIncludesTheme(t *testing.T) {
	qh := &fakeHandlerQueries{}
	gh := setupTestGalleryHandlers(t, qh)

	tests := []struct {
		name      string
		cookie    *http.Cookie
		wantTheme string
	}{
		{
			name:      "no theme cookie uses default dark",
			cookie:    nil,
			wantTheme: "dark",
		},
		{
			name:      "light theme cookie",
			cookie:    &http.Cookie{Name: "theme", Value: "light"},
			wantTheme: "light",
		},
		{
			name:      "dark theme cookie",
			cookie:    &http.Cookie{Name: "theme", Value: "dark"},
			wantTheme: "dark",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
			req.SetPathValue("id", "1")
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}
			w := httptest.NewRecorder()

			gh.GalleryByID(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", w.Code)
			}

			etag := w.Header().Get("ETag")
			wantETagSuffix := "-" + tt.wantTheme + "\""
			if !strings.HasSuffix(etag, wantETagSuffix) {
				t.Errorf("ETag = %q, want suffix %q", etag, wantETagSuffix)
			}

			vary := w.Header().Values("Vary")
			hasCookie := slices.Contains(vary, "Cookie")
			if !hasCookie {
				t.Errorf("Vary header missing 'Cookie', got %v", vary)
			}
		})
	}
}

// sortOrderFake is a fakeHandlerQueries that returns ordered subfolders and files
// for testing gallery sort order in rendered HTML.
type sortOrderFake struct {
	fakeHandlerQueries
	folders []gallerydb.FolderView
	files   []gallerydb.FileView
}

func (s *sortOrderFake) GetFoldersViewsByParentIDOrderByName(ctx context.Context, parent sql.NullInt64) ([]gallerydb.FolderView, error) {
	return s.folders, nil
}

func (s *sortOrderFake) GetFileViewsByFolderIDOrderByFileName(ctx context.Context, folderID sql.NullInt64) ([]gallerydb.FileView, error) {
	return s.files, nil
}

// TestGalleryByIDHandler_SortOrder verifies that folders and files within a
// gallery are rendered in the expected sort order: folders first, then files,
// each group sorted alphabetically.
func TestGalleryByIDHandler_SortOrder(t *testing.T) {
	// Create fake queries returning ordered data: folders then files,
	// each group alphabetically sorted.
	fq := &sortOrderFake{
		folders: []gallerydb.FolderView{
			{ID: 10, Name: "FolderA", ParentID: sql.NullInt64{Int64: 1, Valid: true}},
			{ID: 11, Name: "FolderB", ParentID: sql.NullInt64{Int64: 1, Valid: true}},
		},
		files: []gallerydb.FileView{
			{ID: 1, Path: "FileA.gif", Filename: "FileA.gif", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
			{ID: 2, Path: "FileB.png", Filename: "FileB.png", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
			{ID: 3, Path: "FileC.jpg", Filename: "FileC.jpg", FolderID: sql.NullInt64{Int64: 1, Valid: true}},
		},
	}

	gh := setupTestGalleryHandlers(t, fq)

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "gallery-content")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	box := testutil.FindElementByID(doc, "boxgallery")
	if box == nil {
		t.Fatal("#boxgallery not found in response")
	}

	var labels []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" && testutil.GetAttr(n, "role") == "listitem" {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.Data == "a" {
					label := testutil.GetAttr(c, "aria-label")
					if label != "" {
						labels = append(labels, strings.TrimPrefix(label, "View "))
					}
					break
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(box)

	expected := []string{
		"📁︎ FolderA",
		"📁︎ FolderB",
		"FileA.gif",
		"FileB.png",
		"FileC.jpg",
	}
	if len(labels) != len(expected) {
		t.Errorf("expected %d gallery items, got %d: %v", len(expected), len(labels), labels)
	}
	for i := range expected {
		if i >= len(labels) {
			break
		}
		if labels[i] != expected[i] {
			t.Errorf("gallery item %d: expected %q, got %q", i, expected[i], labels[i])
		}
	}
}

func TestHandleDBError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantStop   bool
	}{
		{"nil", nil, http.StatusOK, false},
		{"not found", sql.ErrNoRows, http.StatusNotFound, true},
		{"other", errors.New("boom"), http.StatusInternalServerError, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			galleryOps := &mockGalleryOps{}
			helper := &mockTemplateHelpers{}
			h := &GalleryHandlers{galleryOps: galleryOps, ServerError: helper.ServerError}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			got := h.handleDBError(rec, req, tt.err)
			if got != tt.wantStop {
				t.Fatalf("handleDBError() = %v, want %v", got, tt.wantStop)
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}
