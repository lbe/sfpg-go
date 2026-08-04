package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
	"golang.org/x/net/html"
)

func TestMenuHandlers_HamburgerMenu_Unauthenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockMenuSessionManager{authenticated: false}
	helper := &mockTemplateHelpers{}
	menuHandlers := NewMenuHandlers(sm, helper.AddCommonTemplateData, helper.ServerError)

	req := httptest.NewRequest(http.MethodGet, "/hamburger-menu", nil)
	w := httptest.NewRecorder()

	menuHandlers.HamburgerMenu(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Verify Cache-Control: no-store header
	cacheControl := w.Header().Get("Cache-Control")
	if cacheControl != "no-store, no-cache, must-revalidate" {
		t.Errorf("expected Cache-Control: no-store, no-cache, must-revalidate, got %q", cacheControl)
	}

	// Verify Content-Type
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("expected Content-Type: text/html; charset=utf-8, got %q", contentType)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && testutil.GetAttr(n, "aria-label") == "Login"
	}) == nil {
		t.Error("expected unauthenticated menu to contain 'Login'")
	}
	if testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && testutil.GetAttr(n, "aria-label") == "Dashboard"
	}) != nil {
		t.Error("expected unauthenticated menu to NOT contain 'Dashboard'")
	}

	aboutLink := testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode &&
			n.Data == "a" &&
			testutil.GetAttr(n, "aria-label") == "About"
	})
	if aboutLink == nil {
		t.Fatal("expected About <a> in menu")
	}
	if testutil.GetAttr(aboutLink, "hx-get") != "/about-modal" {
		t.Errorf("About hx-get = %q, want /about-modal", testutil.GetAttr(aboutLink, "hx-get"))
	}
	if testutil.GetAttr(aboutLink, "hx-target") != "body" {
		t.Errorf("About hx-target = %q, want body", testutil.GetAttr(aboutLink, "hx-target"))
	}
	if testutil.GetAttr(aboutLink, "hx-swap") != "beforeend" {
		t.Errorf("About hx-swap = %q, want beforeend", testutil.GetAttr(aboutLink, "hx-swap"))
	}
	if testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode &&
			n.Data == "label" &&
			testutil.GetAttr(n, "aria-label") == "About"
	}) != nil {
		t.Error("About must not be a <label for=about_modal>")
	}
}

func TestMenuHandlers_HamburgerMenu_Authenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockMenuSessionManager{authenticated: true}
	helper := &mockTemplateHelpers{}
	menuHandlers := NewMenuHandlers(sm, helper.AddCommonTemplateData, helper.ServerError)

	req := httptest.NewRequest(http.MethodGet, "/hamburger-menu", nil)
	w := httptest.NewRecorder()

	menuHandlers.HamburgerMenu(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Verify Cache-Control: no-store header
	cacheControl := w.Header().Get("Cache-Control")
	if cacheControl != "no-store, no-cache, must-revalidate" {
		t.Errorf("expected Cache-Control: no-store, no-cache, must-revalidate, got %q", cacheControl)
	}

	// Verify Content-Type
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("expected Content-Type: text/html; charset=utf-8, got %q", contentType)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && testutil.GetAttr(n, "aria-label") == "Dashboard"
	}) == nil {
		t.Error("expected authenticated menu to contain 'Dashboard'")
	}
	if testutil.FindElement(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && testutil.GetAttr(n, "aria-label") == "Login"
	}) != nil {
		t.Error("expected authenticated menu to NOT contain 'Login'")
	}
}

func TestMenuHandlers_HamburgerMenu_RenderError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	sm := &mockMenuSessionManager{authenticated: false}
	helper := &mockTemplateHelpers{}
	menuHandlers := NewMenuHandlers(sm, helper.AddCommonTemplateData, helper.ServerError)

	req := httptest.NewRequest(http.MethodGet, "/hamburger-menu", nil)
	// errorResponseWriter fails on Write, forcing RenderTemplate to return error
	w := &errorResponseWriter{}
	// Render failure invokes h.ServerError which writes to the response.
	// With errorResponseWriter, Write may return an error that is logged but
	// does not cause a test failure. Just verify no panic occurs.
	menuHandlers.HamburgerMenu(w, req)
}

// mockMenuSessionManager satisfies the SessionManager interface for menu handler tests.
type mockMenuSessionManager struct {
	authenticated bool
}

func (m *mockMenuSessionManager) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	return m.authenticated
}

// aboutModalBoxText parses the About modal response and returns the text content
// of the .modal-box element, asserting the container and checkbox are present.
func aboutModalBoxText(t *testing.T, body *bytes.Buffer) string {
	t.Helper()
	doc, err := testutil.ParseHTML(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if testutil.FindElementByID(doc, "about_modal") == nil {
		t.Fatal("missing #about_modal")
	}
	box := testutil.FindElementByClass(doc, "modal-box")
	if box == nil {
		t.Fatal("missing .modal-box")
	}
	return testutil.GetTextContent(box)
}

func TestMenuHandlers_AboutModal_OK(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}
	sm := &mockMenuSessionManager{authenticated: false}
	helper := &mockTemplateHelpers{}
	addCommon := func(w http.ResponseWriter, r *http.Request, data map[string]any, partial bool) map[string]any {
		if data == nil {
			data = make(map[string]any)
		}
		data["Version"] = "9.9.9-test"
		data["GalleryStats"] = fakeGalleryStats{}
		return data
	}
	h := NewMenuHandlers(sm, addCommon, helper.ServerError)

	req := httptest.NewRequest(http.MethodGet, "/about-modal", nil)
	w := httptest.NewRecorder()
	h.AboutModal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := w.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	text := aboutModalBoxText(t, w.Body)
	if !strings.Contains(text, "Version") || !strings.Contains(text, "9.9.9-test") {
		t.Errorf("modal-box text missing Version/9.9.9-test: %q", text)
	}
	if !strings.Contains(text, "Folders:") || !strings.Contains(text, "10") {
		t.Errorf("modal-box text missing Folders/10: %q", text)
	}
}

// mutableFakeGalleryStats is a fake GalleryStats whose Folders value can be
// changed between requests to prove the About modal renders live data.
type mutableFakeGalleryStats struct {
	folders string
}

func (m *mutableFakeGalleryStats) Folders() string        { return m.folders }
func (m *mutableFakeGalleryStats) Images() string         { return "100" }
func (m *mutableFakeGalleryStats) ImagesSize() int64      { return 1024 }
func (m *mutableFakeGalleryStats) FirstDiscovery() string { return "2026-01-01 00:00:00" }
func (m *mutableFakeGalleryStats) LastDiscovery() string  { return "2026-01-02 00:00:00" }
func (m *mutableFakeGalleryStats) FoldersCount() int64    { return 0 }
func (m *mutableFakeGalleryStats) ImagesCount() int64     { return 100 }

func TestMenuHandlers_AboutModal_FreshStatsEachRequest(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}
	sm := &mockMenuSessionManager{authenticated: false}
	helper := &mockTemplateHelpers{}
	stats := &mutableFakeGalleryStats{folders: "10"}
	addCommon := func(w http.ResponseWriter, r *http.Request, data map[string]any, partial bool) map[string]any {
		if data == nil {
			data = make(map[string]any)
		}
		data["Version"] = "9.9.9-test"
		data["GalleryStats"] = stats
		return data
	}
	h := NewMenuHandlers(sm, addCommon, helper.ServerError)

	w1 := httptest.NewRecorder()
	h.AboutModal(w1, httptest.NewRequest(http.MethodGet, "/about-modal", nil))
	if w1.Code != http.StatusOK {
		t.Fatalf("call1 status = %d", w1.Code)
	}
	text1 := aboutModalBoxText(t, w1.Body)
	if !strings.Contains(text1, "Folders:") || !strings.Contains(text1, "10") {
		t.Fatalf("call1 missing Folders 10: %q", text1)
	}

	stats.folders = "99"

	w2 := httptest.NewRecorder()
	h.AboutModal(w2, httptest.NewRequest(http.MethodGet, "/about-modal", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("call2 status = %d", w2.Code)
	}
	text2 := aboutModalBoxText(t, w2.Body)
	if !strings.Contains(text2, "Folders:") || !strings.Contains(text2, "99") {
		t.Fatalf("call2 missing Folders 99: %q", text2)
	}
	if strings.Contains(text2, "Folders: 10") {
		t.Fatalf("call2 still shows stale Folders 10: %q", text2)
	}
}
