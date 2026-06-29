package server

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
)

// ============================================================================
// Main Handler Tests
// ============================================================================

func TestRefactoredGalleryHandlerByID(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	// Get the router from the app
	router := app.getRouter()

	// Create a test server
	server := httptest.NewServer(router)
	defer server.Close()

	// Create a root folder for testing
	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	pathID, err := cpc.Queries.UpsertFolderPathReturningID(app.ctx, "/test-folder")
	if err != nil {
		t.Fatalf("failed to insert folder path: %v", err)
	}
	folder, err := cpc.Queries.UpsertFolderReturningFolder(app.ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Valid: false},
		PathID:    pathID,
		Name:      "test-folder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to insert folder: %v", err)
	}

	t.Run("unauthenticated", func(t *testing.T) {
		client := &http.Client{}

		// Make a request to the test server
		resp, err := client.Get(server.URL + fmt.Sprintf("/gallery/%d", folder.ID))
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		// Gallery routes are now public, expect 200 OK
		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to parse response body: %v", err)
		}

		if findElementByID(doc, "boxgallery") == nil {
			t.Error("response body does not contain the gallery container")
		}
	})

	t.Run("authenticated", func(t *testing.T) {
		client := &http.Client{}
		req, err := http.NewRequest("GET", server.URL+fmt.Sprintf("/gallery/%d", folder.ID), nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		// Full page response must Vary on HX-Request and HX-Target so the browser does not reuse a cached partial for a full page request.
		varyStr := strings.Join(resp.Header.Values("Vary"), ", ")
		if !strings.Contains(varyStr, "HX-Request") || !strings.Contains(varyStr, "HX-Target") {
			t.Errorf("full page gallery response must Vary on HX-Request and HX-Target, got Vary: %q", varyStr)
		}

		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to parse response body: %v", err)
		}

		if findElementByID(doc, "boxgallery") == nil {
			t.Error("response body does not contain the gallery container")
		}
	})

	t.Run("authenticated htmx partial", func(t *testing.T) {
		client := &http.Client{}
		req, err := http.NewRequest("GET", server.URL+fmt.Sprintf("/gallery/%d", folder.ID), nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", "gallery-content")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		// Read raw response body to check if it contains full HTML layout
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}
		bodyStr := string(body)

		// Parse HTML for further validation
		doc, err := testutil.ParseHTML(strings.NewReader(bodyStr))
		if err != nil {
			t.Fatalf("Failed to parse HTML response: %v", err)
		}

		// Verify response is a partial (no Doctype node)
		var hasDoctype bool
		var findDoctype func(*html.Node)
		findDoctype = func(n *html.Node) {
			if n.Type == html.DoctypeNode {
				hasDoctype = true
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				findDoctype(c)
			}
		}
		findDoctype(doc)
		if hasDoctype {
			t.Error("response should be a partial (no DOCTYPE), but contained a doctype node")
		}

		// Check for gallery container by ID
		if findElementByID(doc, "boxgallery") == nil {
			t.Error("response body does not contain the gallery container (id='boxgallery')")
		}

		// Partial response must use Cache-Control: no-store so the browser does not cache or bfcache it.
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("partial gallery response must have Cache-Control: no-store, got %q", cc)
		}
	})

	t.Run("authenticated htmx partial with oob breadcrumbs", func(t *testing.T) {
		client := &http.Client{}
		req, err := http.NewRequest("GET", server.URL+fmt.Sprintf("/gallery/%d", folder.ID), nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", "gallery-content")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		doc, err := testutil.ParseHTML(strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("Failed to parse HTML response: %v", err)
		}

		// Check for hx-swap-oob attributes
		var foundOOBImageCount, foundOOBBreadcrumbs bool
		var checkOOB func(*html.Node)
		checkOOB = func(n *html.Node) {
			if n.Type == html.ElementNode {
				for _, a := range n.Attr {
					if a.Key == "hx-swap-oob" {
						if a.Val == "true" {
							foundOOBImageCount = true
						}
						if a.Val == "innerHTML:#breadcrumbs-container" {
							foundOOBBreadcrumbs = true
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				checkOOB(c)
			}
		}
		checkOOB(doc)

		if !foundOOBImageCount {
			t.Error("response body does not contain oob image-count swap")
		}

		if !foundOOBBreadcrumbs {
			t.Error("response body does not contain oob breadcrumbs swap directive")
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		client := &http.Client{}
		req, err := http.NewRequest("GET", server.URL+"/gallery/99999", nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusNotFound)
		}
	})
}

// TestRefactoredImageHandlerByID tests the future ID-based image handler.
func TestRefactoredImageHandlerByID(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	// Get the router from the app
	router := app.getRouter()

	// Create a test server
	server := httptest.NewServer(router)
	defer server.Close()

	// Create a folder and file for testing
	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	file, err := importer.UpsertPathChain(app.ctx, "/test-folder/test-image.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}

	t.Run("authenticated cache-busting", func(t *testing.T) {
		client := &http.Client{}
		req, err := http.NewRequest("GET", server.URL+fmt.Sprintf("/image/%d", file.ID), nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to parse HTML: %v", err)
		}

		imgPathPrefix := fmt.Sprintf("/raw-image/%d?v=", file.ID)
		var found bool
		var imgSrcs []string
		var f func(*html.Node)
		f = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "img" {
				for _, a := range n.Attr {
					if a.Key == "src" {
						imgSrcs = append(imgSrcs, a.Val)
						if strings.HasPrefix(a.Val, imgPathPrefix) {
							found = true
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				f(c)
			}
		}
		f(doc)
		if !found {
			t.Fatalf("did not find <img> element with src starting with %q in response. Found srcs: %v", imgPathPrefix, imgSrcs)
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		client := &http.Client{}
		// Make a request to the test server
		resp, err := client.Get(server.URL + fmt.Sprintf("/image/%d", file.ID))
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		// Image routes are now public, expect 200 OK
		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		if findElementByID(doc, "imageContainer") == nil {
			t.Error("response body does not contain the image container")
		}
	})

	t.Run("authenticated", func(t *testing.T) {
		client := &http.Client{}
		req, err := http.NewRequest("GET", server.URL+fmt.Sprintf("/image/%d", file.ID), nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		if findElementByID(doc, "imageContainer") == nil {
			t.Error("response body does not contain the image container")
		}
	})
}

func TestRefactoredRawImageByIDHandler(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	file, err := importer.UpsertPathChain(app.ctx, "/test-folder/raw-image.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}

	// Create the physical file
	imageContent := []byte("dummy image data")
	imagePath := filepath.Join(app.imagesDir, "/test-folder/raw-image.jpg")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("failed to create test image directory: %v", err)
	}
	if err := os.WriteFile(imagePath, imageContent, 0o644); err != nil {
		t.Fatalf("failed to write test image file: %v", err)
	}

	t.Run("unauthenticated", func(t *testing.T) {
		client := &http.Client{}
		// No cache-buster
		u, _ := url.Parse(server.URL)
		u.Path = fmt.Sprintf("/raw-image/%d", file.ID)
		req, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		// Raw-image routes are now public, expect 200 OK (or appropriate status for file not found)
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("handler returned wrong status code: got %v want %v or %v", resp.StatusCode, http.StatusOK, http.StatusNotFound)
		}
		// With cache-buster
		q := u.Query()
		q.Set("v", "12345")
		u.RawQuery = q.Encode()
		req2, _ := http.NewRequest("GET", u.String(), nil)
		resp2, err := client.Do(req2)
		if err != nil {
			t.Fatalf("Failed to make request to test server (cache-buster): %v", err)
		}
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode != http.StatusOK && resp2.StatusCode != http.StatusNotFound && resp2.StatusCode != http.StatusInternalServerError {
			t.Errorf("handler (cache-buster) returned wrong status code: got %v want %v or %v", resp2.StatusCode, http.StatusOK, http.StatusNotFound)
		}
	})

	t.Run("authenticated", func(t *testing.T) {
		client := &http.Client{}
		// No cache-buster
		u, _ := url.Parse(server.URL)
		u.Path = fmt.Sprintf("/raw-image/%d", file.ID)
		req, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}
		if resp.Header.Get("Content-Type") != "image/jpeg" {
			t.Errorf("wrong content type: got %s want image/jpeg", resp.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}
		if string(body) != string(imageContent) {
			t.Errorf("response body mismatch: got %s want %s", string(body), string(imageContent))
		}
		// With cache-buster
		q := u.Query()
		q.Set("v", "12345")
		u.RawQuery = q.Encode()
		req2, _ := http.NewRequest("GET", u.String(), nil)
		req2.AddCookie(MakeAuthCookie(t, app))
		resp2, err := client.Do(req2)
		if err != nil {
			t.Fatalf("Failed to make request to test server (cache-buster): %v", err)
		}
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode != http.StatusOK {
			t.Errorf("handler (cache-buster) returned wrong status code: got %v want %v", resp2.StatusCode, http.StatusOK)
		}
		if resp2.Header.Get("Content-Type") != "image/jpeg" {
			t.Errorf("wrong content type (cache-buster): got %s want image/jpeg", resp2.Header.Get("Content-Type"))
		}
		body2, err := io.ReadAll(resp2.Body)
		if err != nil {
			t.Fatalf("Failed to read response body (cache-buster): %v", err)
		}
		if string(body2) != string(imageContent) {
			t.Errorf("response body mismatch (cache-buster): got %s want %s", string(body2), string(imageContent))
		}
	})
}

func TestRefactoredThumbnailByIDHandler(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	file, err := importer.UpsertPathChain(app.ctx, "/test-folder/thumb-image.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}

	thumbContent := []byte("dummy thumb data")
	_, err = files.UpsertThumbnail(app.ctx, cpc, file.ID, thumbContent)
	if err != nil {
		t.Fatalf("failed to upsert thumbnail: %v", err)
	}

	t.Run("authenticated", func(t *testing.T) {
		client := &http.Client{}
		req, err := http.NewRequest("GET", server.URL+fmt.Sprintf("/thumbnail/file/%d", file.ID), nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		if string(body) != string(thumbContent) {
			t.Errorf("response body mismatch: got %s want %s", string(body), string(thumbContent))
		}
	})

	t.Run("authenticated cache-busting", func(t *testing.T) {
		client := &http.Client{}
		req, err := http.NewRequest("GET", server.URL+fmt.Sprintf("/gallery/%d", file.FolderID.Int64), nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		doc, err := testutil.ParseHTML(resp.Body)
		if err != nil {
			t.Fatalf("Failed to parse HTML: %v", err)
		}

		thumbPath := fmt.Sprintf("/thumbnail/file/%d?v=", file.ID)
		var found bool
		var imgSrcs []string
		var f func(*html.Node)
		f = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "img" {
				for _, a := range n.Attr {
					if a.Key == "src" {
						imgSrcs = append(imgSrcs, a.Val)
						if strings.HasPrefix(a.Val, thumbPath) {
							found = true
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				f(c)
			}
		}
		f(doc)
		if !found {
			t.Fatalf("did not find <img> element with src starting with %q in response. Found srcs: %v", thumbPath, imgSrcs)
		}
	})
}

func TestRefactoredFolderThumbnailByIDHandler(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	file, err := importer.UpsertPathChain(app.ctx, "/tile-folder/tile-image.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}

	thumbContent := []byte("dummy tile data")
	_, err = files.UpsertThumbnail(app.ctx, cpc, file.ID, thumbContent)
	if err != nil {
		t.Fatalf("failed to upsert thumbnail: %v", err)
	}

	// Assign thumbnail to folder
	tx, err := cpc.Conn.BeginTx(app.ctx, nil)
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }() // Rollback if anything goes wrong
	qtx := cpc.Queries.WithTx(tx)
	err = qtx.UpdateFolderTileId(app.ctx, gallerydb.UpdateFolderTileIdParams{
		TileID: sql.NullInt64{Int64: file.ID, Valid: true},
		ID:     file.FolderID.Int64,
	})
	if err != nil {
		t.Fatalf("failed to update folder tile id: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit transaction: %v", err)
	}

	t.Run("authenticated", func(t *testing.T) {
		client := &http.Client{}
		req, err := http.NewRequest("GET", server.URL+fmt.Sprintf("/thumbnail/folder/%d", file.FolderID.Int64), nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		if string(body) != string(thumbContent) {
			t.Errorf("response body mismatch: got %s want %s", string(body), string(thumbContent))
		}
	})

	t.Run("authenticated cache-busting", func(t *testing.T) {
		client := &http.Client{}
		cacheBuster := time.Now().Unix()
		url := server.URL + fmt.Sprintf("/thumbnail/folder/%d?v=%d", file.FolderID.Int64, cacheBuster)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			t.Fatalf("http.NewRequest: %v", err)
		}
		req.AddCookie(MakeAuthCookie(t, app))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Failed to make request to test server: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		if string(body) != string(thumbContent) {
			t.Errorf("response body mismatch: got %s want %s", string(body), string(thumbContent))
		}
	})
}

// TestGalleryByIDHandler_SetsAllRequiredHeaders verifies that galleryByIDHandler
// sets all required response headers correctly when compression is enabled.
// It validates Content-Type, ETag, Cache-Control, Last-Modified,
// Content-Encoding, and Vary headers through an integration test.
func TestGalleryByIDHandler_SetsAllRequiredHeaders(t *testing.T) {
	// Ensure default session flags for handler tests
	// Don't set environment variables - rely on CreateAppWithOpt defaults
	app := CreateAppWithOpt(t, false, getopt.Opt{EnableCompression: getopt.OptBool{Bool: true, IsSet: true}})
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	// Ensure there is at least one gallery (root folder exists by default from migrations)
	client := &http.Client{}
	req, err := http.NewRequest("GET", server.URL+"/gallery/1", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.AddCookie(MakeAuthCookie(t, app))
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request to test server: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
	}

	// Verify all required headers are set correctly
	contentType := resp.Header.Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("expected Content-Type 'text/html; charset=utf-8', got %q", contentType)
	}

	if ce := resp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("expected Content-Encoding 'gzip', got %q", ce)
	}

	if etag := resp.Header.Get("ETag"); etag == "" {
		t.Errorf("expected ETag header to be set, got empty")
	}

	cacheControl := resp.Header.Get("Cache-Control")
	if cacheControl != "public, max-age=2592000" {
		t.Errorf("expected Cache-Control 'public, max-age=2592000', got %q", cacheControl)
	}

	if lastMod := resp.Header.Get("Last-Modified"); lastMod == "" {
		t.Errorf("expected Last-Modified header to be set, got empty")
	} else if _, err := http.ParseTime(lastMod); err != nil {
		t.Errorf("Last-Modified header is not valid HTTP date format: %v", err)
	}

	if vary := resp.Header.Get("Vary"); vary == "" {
		t.Errorf("expected Vary header to be set (from compression middleware), got empty")
	}
}

func TestLightboxLooping(t *testing.T) {
	app := CreateApp(t, true)
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	cpc, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpc)

	importer := gallerylib.Importer{Q: cpc.Queries}
	file1, err := importer.UpsertPathChain(app.ctx, "/loop-test/image1.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}
	file2, err := importer.UpsertPathChain(app.ctx, "/loop-test/image2.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}
	// Ensure order by filename
	if file1.Filename > file2.Filename {
		file1, file2 = file2, file1
	}

	t.Run("cache-busting on all lightbox controls", func(t *testing.T) {
		// Test both the first and last image for all relevant controls
		for _, tc := range []struct {
			label string
			id    int64
			prev  int64
			next  int64
			first int64
			last  int64
		}{
			{"first", file1.ID, file2.ID, file2.ID, file1.ID, file2.ID},
			{"last", file2.ID, file1.ID, file1.ID, file1.ID, file2.ID},
		} {
			req, err := http.NewRequest("GET", server.URL+fmt.Sprintf("/lightbox/%d", tc.id), nil)
			if err != nil {
				t.Fatalf("http.NewRequest: %v", err)
			}
			req.AddCookie(MakeAuthCookie(t, app))
			resp, err := (&http.Client{}).Do(req)
			if err != nil {
				t.Fatalf("Failed to make request to test server: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
			}
			doc, err := testutil.ParseHTML(resp.Body)
			if err != nil {
				t.Fatalf("Failed to parse response body: %v", err)
			}
			// Helper to check hx-get by id
			checkBtn := func(id, want string) {
				btn := findElementByID(doc, id)
				if btn == nil {
					t.Fatalf("Could not find %s in lightbox response", id)
				}
				got, ok := getAttribute(btn, "hx-get")
				if !ok {
					t.Fatalf("%s is missing hx-get attribute", id)
				}
				if got != want {
					t.Errorf("%s has wrong hx-get: got %q, want %q", id, got, want)
				}
			}
			// Navigation buttons
			checkBtn("lightbox-first-btn", fmt.Sprintf("/lightbox/%d?v=%s", tc.first, ui.GetCacheVersion()))
			checkBtn("lightbox-prev-btn", fmt.Sprintf("/lightbox/%d?v=%s", tc.prev, ui.GetCacheVersion()))
			checkBtn("lightbox-next-btn", fmt.Sprintf("/lightbox/%d?v=%s", tc.next, ui.GetCacheVersion()))
			checkBtn("lightbox-last-btn", fmt.Sprintf("/lightbox/%d?v=%s", tc.last, ui.GetCacheVersion()))

			// Info box (hx-get on #lightbox-ui)
			lightboxUI := findElementByID(doc, "lightbox-ui")
			if lightboxUI == nil {
				t.Fatal("Could not find lightbox-ui in response")
			}
			infoGet, ok := getAttribute(lightboxUI, "hx-get")
			if !ok {
				t.Fatal("lightbox-ui missing hx-get attribute for info box")
			}
			wantInfo := fmt.Sprintf("/info/image/%d?v=%s", tc.id, ui.GetCacheVersion())
			if infoGet != wantInfo {
				t.Errorf("lightbox-ui info box hx-get wrong: got %q, want %q", infoGet, wantInfo)
			}

			// Navigation overlays: find all <a> with hx-get
			var overlayCount int
			var f func(*html.Node)
			f = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "a" {
					if v, ok := getAttribute(n, "hx-get"); ok {
						overlayCount++
						// Should be /lightbox/<id>?v=ui.GetCacheVersion()
						if !strings.HasPrefix(v, "/lightbox/") || !strings.Contains(v, "?v=") {
							t.Errorf("overlay <a> hx-get malformed: %q", v)
						}
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					f(c)
				}
			}
			f(doc)
			if overlayCount == 0 {
				t.Error("No navigation overlay <a> elements with hx-get found")
			}
		}
	})
}
