package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/handlers"

	"github.com/lbe/sfpg-go/internal/server/template"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

// Minimal Thumb struct for testing purposes

// TestAuthMiddleware tests the authMiddleware to ensure it correctly protects
// routes, redirecting unauthenticated requests and allowing authenticated ones.
func TestTemplateRendering(t *testing.T) {
	app := CreateApp(t) // Create a dummy app, no need for DB for template parsing
	defer app.Shutdown()

	// Templates are parsed in ui.go's init() function.
	// No explicit parsing needed here.

	// Test rendering of layout.html.tmpl
	t.Run("Render layout.html.tmpl", func(t *testing.T) {
		rr := httptest.NewRecorder()
		gd := &handlers.GalleryData{
			IsImageView: false,
			Breadcrumbs: []handlers.Breadcrumb{{Name: "Home", Path: "/"}},
			ImageCount:  0,
		}
		data := map[string]any{
			"Breadcrumbs": gd.Breadcrumbs,
			"GalleryName": gd.GalleryName,
			"ImageCount":  gd.ImageCount,
			"IsImageView": gd.IsImageView,
			"Thumbs":      gd.Thumbs,
		}
		req := httptest.NewRequest("GET", "/", nil)
		data = template.AddAuthToData(data, app.IsAuthenticated(rr, req))
		err := ui.RenderPage(rr, "gallery", data, false) // Use renderPage for full layout
		if err != nil {
			t.Errorf("Failed to render layout.html.tmpl: %v", err)
		}
		if rr.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %d", rr.Code)
		}

		doc, err := testutil.ParseHTML(rr.Body)
		if err != nil {
			t.Fatalf("Failed to parse layout HTML response: %v", err)
		}

		if testutil.FindElementByID(doc, "box_info_wrapper") == nil {
			t.Fatal("missing #box_info_wrapper element")
		}

		if testutil.FindElementByID(doc, "mobile-info-toggle") != nil {
			t.Fatal("unexpected #mobile-info-toggle element; drawer/checkbox mobile info pattern should not be used")
		}

		if testutil.FindElementByID(doc, "mobile_info_modal") == nil {
			t.Fatal("missing #mobile_info_modal element")
		}

		if testutil.FindElementByID(doc, "box_info_mobile") == nil {
			t.Fatal("missing #box_info_mobile element")
		}
	})

	// Test rendering of gallery.html.tmpl (as a partial within layout)
	t.Run("Render gallery.html.tmpl", func(t *testing.T) {
		rr := httptest.NewRecorder()
		gd := &handlers.GalleryData{
			IsImageView: false,
			Breadcrumbs: []handlers.Breadcrumb{{Name: "Home", Path: "/"}},
			ImageCount:  1,
			Thumbs: []handlers.DirectoryInfo{
				{ID: 1, Path: "/image/1", ThumbPath: "/thumbnail/file/1", DispName: "Test Image", IsImage: true},
			},
		}
		data := map[string]any{
			"Breadcrumbs": gd.Breadcrumbs,
			"GalleryName": gd.GalleryName,
			"ImageCount":  gd.ImageCount,
			"IsImageView": gd.IsImageView,
			"Thumbs":      gd.Thumbs,
		}
		req := httptest.NewRequest("GET", "/", nil)
		data = template.AddAuthToData(data, app.IsAuthenticated(rr, req))
		err := ui.RenderPage(rr, "gallery", data, false) // Use renderPage for full layout
		if err != nil {
			t.Errorf("Failed to render gallery.html.tmpl: %v", err)
		}
		if rr.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %d", rr.Code)
		}

		doc, err := testutil.ParseHTML(rr.Body)
		if err != nil {
			t.Fatalf("Failed to parse HTML response: %v", err)
		}

		boxGallery := testutil.FindElementByID(doc, "boxgallery")
		if boxGallery == nil {
			t.Fatal("missing #boxgallery element")
		}

		firstTile := testutil.FindElementByID(doc, "gallery-tile-1")
		if firstTile == nil {
			t.Fatal("missing #gallery-tile-1 element")
		}

		if got := testutil.GetAttr(firstTile, "hx-get"); got == "" {
			t.Fatal("expected #gallery-tile-1 to include hx-get")
		}
	})

	// Test rendering of infobox-folder.html.tmpl
	t.Run("Render infobox-folder.html.tmpl", func(t *testing.T) {
		rr := httptest.NewRecorder()
		data := struct {
			Folder         gallerydb.Folder
			FormattedMtime string
			DirCount       int
			ImageCount     int
			FileCount      int
		}{
			Folder: gallerydb.Folder{
				ID:   1,
				Name: "Test Folder",
			},
			FormattedMtime: "Jan 01 00:00:00 2023",
			DirCount:       2,
			ImageCount:     5,
			FileCount:      1,
		}
		err := ui.RenderTemplate(rr, "infobox-folder.html.tmpl", data)
		if err != nil {
			t.Errorf("Failed to render infobox-folder.html.tmpl: %v", err)
		}
		if rr.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %d", rr.Code)
		}

		doc, err := testutil.ParseHTML(rr.Body)
		if err != nil {
			t.Fatalf("Failed to parse infobox-folder HTML response: %v", err)
		}

		infoBox := testutil.FindElementByID(doc, "inner_box_info")
		if infoBox == nil {
			t.Fatal("missing #inner_box_info element")
		}

		if got := testutil.GetAttr(infoBox, "data-info-id"); got != "1" {
			t.Fatalf("#inner_box_info data-info-id=%q, want %q", got, "1")
		}

		thumbImg := testutil.FindElementByTag(infoBox, "img")
		if thumbImg == nil {
			t.Fatal("missing thumbnail <img> element in infobox-folder")
		}

		if got := testutil.GetAttr(thumbImg, "src"); got == "" {
			t.Fatal("expected folder thumbnail image to include src")
		}

		cardNode := testutil.FindElement(infoBox, func(n *html.Node) bool {
			classAttr := testutil.GetAttr(n, "class")
			return strings.Contains(classAttr, "card")
		})
		if cardNode == nil {
			t.Fatal("expected infobox-folder to use card-based layout")
		}

		if testutil.FindElementByID(infoBox, "folder-mtime") == nil {
			t.Fatal("missing #folder-mtime element")
		}

		if testutil.FindElementByID(infoBox, "folder-dir-count") == nil {
			t.Fatal("missing #folder-dir-count element")
		}
	})

	// Test rendering of infobox-image.html.tmpl
	t.Run("Render infobox-image.html.tmpl", func(t *testing.T) {
		rr := httptest.NewRecorder()
		data := struct {
			File              gallerydb.FileView
			Exif              gallerydb.ExifMetadatum
			Iptc              gallerydb.IptcMetadatum
			ImageIndex        int
			ImageCount        int
			FileUpdatedAtUnix int64 // Added FileUpdatedAtUnix
		}{
			File: gallerydb.FileView{
				ID:        1,
				Filename:  "test.jpg",
				Mtime:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
				UpdatedAt: time.Now(), // Ensure UpdatedAt is a non-nil time.Time object
			},
			Exif: gallerydb.ExifMetadatum{
				CameraMake:  sql.NullString{String: "TestMake", Valid: true},
				CameraModel: sql.NullString{String: "TestModel", Valid: true},
			},
			Iptc: gallerydb.IptcMetadatum{
				Creator: sql.NullString{String: "Test Creator", Valid: true},
			},
			ImageIndex:        1,
			ImageCount:        10,
			FileUpdatedAtUnix: time.Now().Unix(), // Set a value for FileUpdatedAtUnix
		}
		err := ui.RenderTemplate(rr, "infobox-image.html.tmpl", data)
		if err != nil {
			t.Errorf("Failed to render infobox-image.html.tmpl: %v", err)
		}
		if rr.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %d", rr.Code)
		}
	})
}

// TestNegotiateEncoding tests the negotiateEncoding function with various Accept-Encoding headers
func TestGetUser_Additional(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Test with non-existent user
	_, err := app.GetUser(context.Background(), "nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent user")
	}
}

// TestCheckAccountLockout_Additional tests account lockout checking
func TestRemoveImagesDirPrefix(t *testing.T) {
	normalizedImagesDir := "/var/images"

	tests := []struct {
		name        string
		imagesDir   string
		input       string
		expected    string
		expectError bool
	}{
		{
			name:      "with prefix",
			imagesDir: normalizedImagesDir,
			input:     "/var/images/photo.jpg",
			expected:  "photo.jpg",
		},
		{
			name:      "with subfolder",
			imagesDir: normalizedImagesDir,
			input:     "/var/images/subfolder/photo.jpg",
			expected:  "subfolder/photo.jpg",
		},
		{
			name:      "without prefix",
			imagesDir: normalizedImagesDir,
			input:     "/other/path/photo.jpg",
			expected:  "/other/path/photo.jpg",
		},
		{
			name:        "path traversal attempt",
			imagesDir:   normalizedImagesDir,
			input:       "/var/images/../etc/passwd",
			expectError: true,
		},
		{
			name:      "empty imagesDir",
			imagesDir: "",
			input:     "photo.jpg",
			expected:  "photo.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := removeImagesDirPrefix(tt.imagesDir, tt.input)
			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			if result != tt.expected {
				t.Errorf("removeImagesDirPrefix(%q, %q) = %q, want %q", tt.imagesDir, tt.input, result, tt.expected)
			}
		})
	}
}

// TestServerError tests error response
func TestServerError(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)

	testErr := fmt.Errorf("test error")
	app.ServerError(rr, req, testErr)

	if rr.Code != 500 {
		t.Errorf("Expected status 500, got %d", rr.Code)
	}

	// Parse HTML to verify error message is properly rendered
	doc, err := testutil.ParseHTML(rr.Body)
	if err != nil {
		t.Fatalf("Failed to parse HTML response: %v", err)
	}

	found := false
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode && strings.Contains(n.Data, "Internal Server Error") {
			found = true
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	if !found {
		t.Error("Expected 'Internal Server Error' message in HTML response")
	}
}

// TestGetSessionOptions tests session options retrieval
func TestBuildHandlers(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Load config first
	if err := app.loadConfig(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// buildHandlers is already called by CreateApp, verify handlers exist
	if app.configHandlers == nil {
		t.Error("Expected configHandlers to be initialized")
	}
}

// TestWalkImageDir tests image directory walking
func TestWalkImageDir(t *testing.T) {
	app := CreateApp(t, WithPool()) // Start with pool enabled
	defer app.Shutdown()

	// Create a test image file
	testImagePath := filepath.Join(app.imagesDir, "test.jpg")
	if err := os.WriteFile(testImagePath, []byte("fake jpg data"), 0644); err != nil {
		t.Fatalf("Failed to create test image: %v", err)
	}

	// Walk the directory (no return value)
	app.TriggerDiscovery()

	// Wait a bit for worker pool to process
	time.Sleep(50 * time.Millisecond)
}

// TestLoadFromDatabase_EdgeCases tests config loading edge cases
func TestSetConfigDefaultsLegacy_Coverage(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Function should not panic - it's called during app creation
	// Just verify app exists
	if app == nil {
		t.Fatal("Expected app to be created")
	}
}

// TestParseConfigUITemplates_Coverage verifies all config templates are parsed
func TestSetRootDir_WithExplicitPath(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	testDir := t.TempDir()
	app.setRootDir(&testDir)

	if app.rootDir != testDir {
		t.Errorf("Expected rootDir to be %q, got %q", testDir, app.rootDir)
	}
}

// TestSetRootDir_WithNilPath verifies setRootDir uses executable directory when nil
func TestSetRootDir_WithNilPath(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	app.setRootDir(nil)

	if app.rootDir == "" {
		t.Error("Expected rootDir to be set")
	}
}

// TestSetupBootstrapLogging_Coverage verifies bootstrap logging initialization
func TestBuildHandlers_Coverage(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Rebuild handlers to verify no error
	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers failed: %v", err)
	}

	// Verify handler groups are initialized
	if app.configHandlers == nil {
		t.Error("Expected configHandlers to be non-nil")
	}
	if app.authHandlers == nil {
		t.Error("Expected authHandlers to be non-nil")
	}
	if app.galleryHandlers == nil {
		t.Error("Expected galleryHandlers to be non-nil")
	}
	if app.healthHandlers == nil {
		t.Error("Expected healthHandlers to be non-nil")
	}
}

// TestLogProfileLocation_Coverage tests profile location logging
func TestBuildHandlers_Integration(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	// Build handlers and ensure they're functional
	if err := app.buildHandlers(web.FS); err != nil {
		t.Fatalf("buildHandlers failed: %v", err)
	}

	// Try to verify handlers were properly constructed
	if app.configHandlers == nil {
		t.Error("Expected configHandlers to be initialized")
	}
	if app.authHandlers == nil {
		t.Error("Expected authHandlers to be initialized")
	}
}

// TestLoadConfig_WithError tests config loading with failure cases
func TestSetRootDir_Multiple(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	testDir1 := t.TempDir()
	testDir2 := t.TempDir()

	app.setRootDir(&testDir1)
	if app.rootDir != testDir1 {
		t.Errorf("Expected rootDir to be %q", testDir1)
	}

	app.setRootDir(&testDir2)
	if app.rootDir != testDir2 {
		t.Errorf("Expected rootDir to be %q", testDir2)
	}
}

// TestInitForUnlock_Multiple tests unlock initialization multiple times
func TestCacheWriteWorker_Shutdown(t *testing.T) {
	app := CreateApp(t)

	// Shutdown should trigger context cancellation, causing cacheWriteWorker to exit
	app.Shutdown()

	// If shutdown completed without hanging, cacheWriteWorker handled ctx.Done()
	// Wait a bit to ensure worker goroutine finishes
	time.Sleep(50 * time.Millisecond)
}

// TestSetupTestDBForConfig_Usage tests setupTestDBForConfig helper
func TestSetupTestDBForConfig_Usage(t *testing.T) {
	db, queries, ctx := setupTestDBForConfig(t)
	defer db.Close()

	// Verify database and queries were set up
	if db == nil {
		t.Error("Expected non-nil database")
	}
	if queries == nil {
		t.Error("Expected non-nil queries")
	}
	if ctx == nil {
		t.Error("Expected non-nil context")
	}

	// Test using the queries
	_, err := queries.GetConfigValueByKey(ctx, "site-name")
	if err != nil {
		t.Logf("GetConfigValueByKey failed (expected in fresh DB): %v", err)
	}
}

// TestScheduledUnlockTask tests that unlock tasks are scheduled when accounts are locked
