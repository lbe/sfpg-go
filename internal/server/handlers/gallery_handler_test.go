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

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/cachepreload"
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
	if w.Header().Get("HX-Push-URL") == "" {
		t.Error("expected HX-Push-URL to be set")
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
	if !strings.HasSuffix(result, "...") {
		t.Errorf("expected truncated name, got %q", result)
	}
	if !strings.Contains(result, "aaaaa") {
		t.Errorf("expected result to contain base name, got %q", result)
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
