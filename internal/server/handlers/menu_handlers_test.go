package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"slices"
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

// aboutModalBox parses the About modal response and returns the .modal-box node,
// asserting the container and checkbox are present.
func aboutModalBox(t *testing.T, body *bytes.Buffer) *html.Node {
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
	return box
}

func assertAboutModalVersion(t *testing.T, box *html.Node, wantVersion string) {
	t.Helper()
	p := testutil.FindElement(box, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "p" &&
			slices.Contains(strings.Fields(testutil.GetAttr(n, "class")), "text-base-content/70")
	})
	if p == nil {
		t.Fatal("missing version paragraph")
	}
	got := strings.Join(strings.Fields(testutil.GetTextContent(p)), " ")
	want := "Version " + wantVersion
	if got != want {
		t.Errorf("version paragraph = %q, want %q", got, want)
	}
}

func assertAboutModalFolders(t *testing.T, box *html.Node, wantCount string) {
	t.Helper()
	stats := testutil.FindElementByClass(box, "bg-base-200")
	if stats == nil {
		t.Fatal("missing gallery stats block")
	}
	wantLine := "Folders: " + wantCount
	list := testutil.FindElementByClass(stats, "space-y-1")
	if list == nil {
		t.Fatal("missing stats list")
	}
	var found bool
	for c := list.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "div" {
			continue
		}
		line := strings.TrimSpace(testutil.GetTextContent(c))
		if !strings.HasPrefix(line, "Folders:") {
			continue
		}
		if line != wantLine {
			t.Errorf("Folders line = %q, want %q", line, wantLine)
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("missing Folders stat line, want %q", wantLine)
	}
}

func TestMenuHandlers_AboutModal_OK(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}
	sm := &mockMenuSessionManager{authenticated: false}
	helper := &mockTemplateHelpers{}
	addCommon := func(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
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

	box := aboutModalBox(t, w.Body)
	assertAboutModalVersion(t, box, "9.9.9-test")
	assertAboutModalFolders(t, box, "10")
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
	addCommon := func(w http.ResponseWriter, r *http.Request, data map[string]any) map[string]any {
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
	box1 := aboutModalBox(t, w1.Body)
	assertAboutModalFolders(t, box1, "10")

	stats.folders = "99"

	w2 := httptest.NewRecorder()
	h.AboutModal(w2, httptest.NewRequest(http.MethodGet, "/about-modal", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("call2 status = %d", w2.Code)
	}
	box2 := aboutModalBox(t, w2.Body)
	assertAboutModalFolders(t, box2, "99")
}
