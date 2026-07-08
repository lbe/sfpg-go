//go:build integration || e2e

package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/gallerylib"
	gentestfiles "github.com/lbe/sfpg-go/internal/gen-test-files"
	"github.com/lbe/sfpg-go/internal/testutil"
)

// TestGalleryByIDHandler_SortOrder verifies that folders and files within a
// gallery are rendered in the expected sort order.
func TestGalleryByIDHandler_SortOrder(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	app := CreateApp(t, WithPool())
	defer app.Shutdown()

	filePaths := []string{
		"FileA.gif",
		"FileB.png",
		"FileC.jpg",
		"FolderB/FileB.jpg",
		"FolderA/FileA.jpg",
	}
	if err := gentestfiles.CreateTestFiles(app.imagesDir, filePaths); err != nil {
		t.Fatalf("CreateTestFiles failed: %v", err)
	}

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get RW DB connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	importer := gallerylib.Importer{Q: cpcRw.Queries}
	for _, p := range filePaths {
		if _, err := importer.UpsertPathChain(app.ctx, p, time.Now().Unix(), 1024, "d41d8cd98f00b204e9800998ecf8427e", 0, 100, 100, "image/jpeg"); err != nil {
			t.Fatalf("UpsertPathChain failed for %s: %v", p, err)
		}
	}

	rootFolder, err := cpcRw.Queries.GetFolderByPath(app.ctx, "")
	if err != nil {
		t.Fatalf("failed to get root folder: %v", err)
	}

	ts := httptest.NewServer(app.getRouter())
	defer ts.Close()

	req := httptest.NewRequest(http.MethodGet, ts.URL+"/gallery/"+strconv.FormatInt(rootFolder.ID, 10), nil)
	req.AddCookie(MakeAuthCookie(t, app))
	rr := httptest.NewRecorder()
	ts.Config.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /gallery/%d returned status %d, want %d", rootFolder.ID, rr.Code, http.StatusOK)
	}

	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("failed to parse gallery HTML: %v", err)
	}

	box := testutil.FindElementByID(doc, "boxgallery")
	if box == nil {
		t.Fatal("#boxgallery not found in response")
	}

	var labels []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" && testutil.GetAttr(n, "role") == "listitem" {
			// The displayed name is on the inner <a> aria-label as "View <name>".
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
