package server

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
)

// ============================================================================
// Main Handler Tests
// ============================================================================

func TestInfoBoxFolderHandler(t *testing.T) {
	app := CreateApp(t, WithPool())
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	// 1. Setup data
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	importer := gallerylib.Importer{Q: cpcRw.Queries}
	// Create a folder with 2 subfolders, 3 images, 1 non-image file
	_, err = importer.UpsertPathChain(app.ctx, "/info-test/sub1/file.txt", 0, 0, "", 0, 0, 0, "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	_, err = importer.UpsertPathChain(app.ctx, "/info-test/sub2/file.txt", 0, 0, "", 0, 0, 0, "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	_, err = importer.UpsertPathChain(app.ctx, "/info-test/image1.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	_, err = importer.UpsertPathChain(app.ctx, "/info-test/image2.png", 0, 0, "", 0, 0, 0, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	_, err = importer.UpsertPathChain(app.ctx, "/info-test/image3.gif", 0, 0, "", 0, 0, 0, "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	_, err = importer.UpsertPathChain(app.ctx, "/info-test/document.pdf", 0, 0, "", 0, 0, 0, "application/pdf")
	if err != nil {
		t.Fatal(err)
	}

	// Get the ID of the parent folder
	testFolder, err := cpcRw.Queries.GetFolderByPath(app.ctx, "/info-test")
	if err != nil {
		t.Fatalf("Failed to get test folder: %v", err)
	}

	// 2. Make request
	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	req, err := http.NewRequest("GET", server.URL+fmt.Sprintf("/info/folder/%d", testFolder.ID), nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.AddCookie(MakeAuthCookie(t, app))

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 3. Assertions
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	doc, err := testutil.ParseHTML(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Check specific fields by ID
	checks := []struct {
		id       string
		expected string
	}{
		{"folder-dir-count", "2"},
		{"folder-image-count", "3"},
		{"folder-file-count", "1"},
	}

	for _, check := range checks {
		text, found := getElementTextByID(doc, check.id)
		if !found {
			t.Errorf("Element with ID %q not found", check.id)
			continue
		}
		if text != check.expected {
			t.Errorf("For element #%s, expected text %q, got %q", check.id, check.expected, text)
		}
	}
}

func TestInfoBoxImageHandler(t *testing.T) {
	app := CreateApp(t, WithPool())
	time.Sleep(200 * time.Millisecond) // Give some time for the worker pool to start
	defer app.Shutdown()

	// 1. Setup data
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	importer := gallerylib.Importer{Q: cpcRw.Queries}
	file, err := importer.UpsertPathChain(app.ctx, "/info-test/image1.jpg", 0, 0, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}

	// 2. Add mock EXIF and IPTC data
	mockExif := gallerydb.UpsertExifParams{
		FileID:       file.ID,
		CameraMake:   sql.NullString{String: "TestMake", Valid: true},
		CameraModel:  sql.NullString{String: "TestModel", Valid: true},
		LensModel:    sql.NullString{Valid: false},
		Latitude:     sql.NullFloat64{Float64: 34.94304, Valid: true},
		Longitude:    sql.NullFloat64{Float64: -109.77774666667, Valid: true},
		Altitude:     sql.NullFloat64{Valid: false},
		CaptureDate:  sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		Iso:          sql.NullInt64{Int64: 400, Valid: true},
		ShutterSpeed: sql.NullString{String: "1/250", Valid: true},
		Aperture:     sql.NullString{String: "f8.0", Valid: true},
		FocalLength:  sql.NullString{String: "50.0", Valid: true},
		Orientation:  sql.NullInt64{Valid: false},
	}
	err = cpcRw.Queries.UpsertExif(app.ctx, mockExif)
	if err != nil {
		t.Fatalf("Failed to insert mock EXIF data: %v", err)
	}

	mockIptc := gallerydb.UpsertIPTCParams{
		FileID: file.ID,

		Creator: sql.NullString{String: "Test Author", Valid: true},

		Copyright: sql.NullString{String: "Test Copyright", Valid: true},
	}

	err = cpcRw.Queries.UpsertIPTC(app.ctx, mockIptc)
	if err != nil {
		t.Fatalf("Failed to insert mock IPTC data: %v", err)
	}

	// 3. Make request
	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	req, err := http.NewRequest("GET", server.URL+fmt.Sprintf("/info/image/%d", file.ID), nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.AddCookie(MakeAuthCookie(t, app))

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 4. Assertions
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status OK, got %d. Body: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	doc, err := testutil.ParseHTML(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Check specific fields by ID
	checks := []struct {
		id       string
		expected string
	}{
		{"image-filename", file.Filename},
		{"image-file-size", fmt.Sprintf("%d", file.SizeBytes.Int64)},
		{"image-index", "1"},
		{"image-count", "1"},
		{"exif-camera-make", "TestMake"},
		{"exif-camera-model", "TestModel"},
		{"exif-iso", "400"},
		{"exif-aperture", "f8.0"},
		{"exif-focal-length", "50.0"},
		// {"iptc-creator", "Test Author"},
		// {"iptc-copyright", "Test Copyright"},
	}

	for _, check := range checks {
		text, found := getElementTextByID(doc, check.id)
		if !found {
			t.Errorf("Element with ID %q not found", check.id)
			continue
		}
		if strings.TrimSpace(text) != check.expected {
			t.Errorf("For element #%s, expected %q, got %q", check.id, check.expected, text)
		}
	}
}

func TestInfoFolderCacheBusting(t *testing.T) {
	app := CreateApp(t, WithPool())
	time.Sleep(200 * time.Millisecond)
	defer app.Shutdown()

	router := app.getRouter()
	server := httptest.NewServer(router)
	defer server.Close()

	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	importer := gallerylib.Importer{Q: cpcRw.Queries}
	folder, err := importer.UpsertPathChain(app.ctx, "/cachebust-folder/file.txt", 0, 0, "", 0, 0, 0, "text/plain")
	if err != nil {
		t.Fatalf("failed to upsert path chain: %v", err)
	}

	url := server.URL + "/info/folder/" + fmt.Sprint(folder.FolderID.Int64) + "?v=" + ui.GetCacheVersion()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.AddCookie(MakeAuthCookie(t, app))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request to test server: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", resp.StatusCode, http.StatusOK)
	}
	// Optionally, parse the response body for further cache-busting references if needed
}

// ============================================================================
// HX-Push-URL Regression Tests
// ============================================================================
