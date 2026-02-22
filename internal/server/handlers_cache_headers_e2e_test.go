//go:build integration

package server

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/getopt"
)

// TestE2E_GalleryByID_CacheHeaders verifies that /gallery/{id} sets ETag, Last-Modified, and Cache-Control.
func TestE2E_GalleryByID_CacheHeaders(t *testing.T) {
	app := CreateApp(t, false)
	app.opt.EnableCompression = getopt.OptBool{Bool: true, IsSet: true}
	app.opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}

	router := app.getRouter()

	// Create test data
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	imp := &gallerylib.Importer{Q: cpcRw.Queries}
	rootID, err := imp.CreateRootFolderEntry(app.ctx, time.Now().Unix())
	if err != nil {
		t.Fatalf("failed to create root folder: %v", err)
	}

	childPathID, err := cpcRw.Queries.UpsertFolderPathReturningID(app.ctx, "test-gallery")
	if err != nil {
		t.Fatalf("failed to create child path: %v", err)
	}
	childFolder, err := cpcRw.Queries.UpsertFolderReturningFolder(app.ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: rootID, Valid: true},
		PathID:    childPathID,
		Name:      "test-gallery",
		Mtime:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("failed to create child folder: %v", err)
	}

	authCookie := MakeAuthCookie(t, app)

	// First request - should set all cache headers
	req := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
	req.AddCookie(authCookie)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	// Verify ETag header
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Error("expected ETag header to be set, but it was empty")
	}

	// Verify Last-Modified header
	lastModified := rr.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Error("expected Last-Modified header to be set, but it was empty")
	} else {
		// Verify it's a valid HTTP date
		if _, err := http.ParseTime(lastModified); err != nil {
			t.Errorf("Last-Modified header is not valid HTTP date format: %v", err)
		}
	}

	// Verify Cache-Control header
	cacheControl := rr.Header().Get("Cache-Control")
	if cacheControl == "" {
		t.Error("expected Cache-Control header to be set, but it was empty")
	}
	if cacheControl != "public, max-age=2592000" {
		t.Errorf("expected Cache-Control 'public, max-age=2592000', got: %s", cacheControl)
	}
}

// TestE2E_ImageByID_CacheHeaders verifies that /image/{id} sets ETag, Last-Modified, and Cache-Control.
func TestE2E_ImageByID_CacheHeaders(t *testing.T) {
	app := CreateApp(t, false)
	app.opt.EnableCompression = getopt.OptBool{Bool: true, IsSet: true}
	app.opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}

	router := app.getRouter()

	// Create test data with image
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	imp := &gallerylib.Importer{Q: cpcRw.Queries}

	// Create image file on disk
	testImagePath := "test-image-cache.jpg"
	fullPath := filepath.Join(app.imagesDir, testImagePath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	// Write minimal JPEG markers
	if err := os.WriteFile(fullPath, []byte{0xFF, 0xD8, 0xFF, 0xD9}, 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	// Insert file record
	file, err := imp.UpsertPathChain(app.ctx, filepath.ToSlash(testImagePath), time.Now().Unix(), 4, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("UpsertPathChain failed: %v", err)
	}

	authCookie := MakeAuthCookie(t, app)

	// First request - should set all cache headers
	idStr := strconv.FormatInt(file.ID, 10)
	req := httptest.NewRequest(http.MethodGet, "/image/"+idStr, nil)
	req.AddCookie(authCookie)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	// Verify ETag header
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Error("expected ETag header to be set, but it was empty")
	}

	// Verify Last-Modified header
	lastModified := rr.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Error("expected Last-Modified header to be set, but it was empty")
	} else {
		if _, err := http.ParseTime(lastModified); err != nil {
			t.Errorf("Last-Modified header is not valid HTTP date format: %v", err)
		}
	}

	// Verify Cache-Control header
	cacheControl := rr.Header().Get("Cache-Control")
	if cacheControl == "" {
		t.Error("expected Cache-Control header to be set, but it was empty")
	}
	if cacheControl != "public, max-age=2592000" {
		t.Errorf("expected Cache-Control 'public, max-age=2592000', got: %s", cacheControl)
	}
}

// TestE2E_LightboxByID_CacheHeaders verifies that /lightbox/{id} sets ETag, Last-Modified, and Cache-Control.
func TestE2E_LightboxByID_CacheHeaders(t *testing.T) {
	app := CreateApp(t, false)
	app.opt.EnableCompression = getopt.OptBool{Bool: true, IsSet: true}
	app.opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}

	router := app.getRouter()

	// Create test data with image
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	imp := &gallerylib.Importer{Q: cpcRw.Queries}

	// Create image file on disk
	testImagePath := "test-lightbox-cache.jpg"
	fullPath := filepath.Join(app.imagesDir, testImagePath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte{0xFF, 0xD8, 0xFF, 0xD9}, 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	// Insert file record
	file, err := imp.UpsertPathChain(app.ctx, filepath.ToSlash(testImagePath), time.Now().Unix(), 4, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("UpsertPathChain failed: %v", err)
	}

	authCookie := MakeAuthCookie(t, app)

	// First request - should set all cache headers
	idStr := strconv.FormatInt(file.ID, 10)
	req := httptest.NewRequest(http.MethodGet, "/lightbox/"+idStr, nil)
	req.AddCookie(authCookie)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	// Verify ETag header
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Error("expected ETag header to be set, but it was empty")
	}

	// Verify Last-Modified header
	lastModified := rr.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Error("expected Last-Modified header to be set, but it was empty")
	} else {
		if _, err := http.ParseTime(lastModified); err != nil {
			t.Errorf("Last-Modified header is not valid HTTP date format: %v", err)
		}
	}

	// Verify Cache-Control header
	cacheControl := rr.Header().Get("Cache-Control")
	if cacheControl == "" {
		t.Error("expected Cache-Control header to be set, but it was empty")
	}
	if cacheControl != "public, max-age=2592000" {
		t.Errorf("expected Cache-Control 'public, max-age=2592000', got: %s", cacheControl)
	}
}

// TestE2E_CacheHeaders_304Revalidation verifies that all endpoints return 304 when If-None-Match matches ETag.
func TestE2E_CacheHeaders_304Revalidation(t *testing.T) {
	app := CreateApp(t, false)
	app.opt.EnableCompression = getopt.OptBool{Bool: true, IsSet: true}
	app.opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}

	router := app.getRouter()

	// Create test data
	cpcRw, err := app.dbRwPool.Get()
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer app.dbRwPool.Put(cpcRw)

	imp := &gallerylib.Importer{Q: cpcRw.Queries}
	rootID, err := imp.CreateRootFolderEntry(app.ctx, time.Now().Unix())
	if err != nil {
		t.Fatalf("failed to create root folder: %v", err)
	}

	childPathID, err := cpcRw.Queries.UpsertFolderPathReturningID(app.ctx, "test-304")
	if err != nil {
		t.Fatalf("failed to create child path: %v", err)
	}
	childFolder, err := cpcRw.Queries.UpsertFolderReturningFolder(app.ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: rootID, Valid: true},
		PathID:    childPathID,
		Name:      "test-304",
		Mtime:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("failed to create child folder: %v", err)
	}

	// Create image file
	testImagePath := "test-304-image.jpg"
	fullPath := filepath.Join(app.imagesDir, testImagePath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte{0xFF, 0xD8, 0xFF, 0xD9}, 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	file, err := imp.UpsertPathChain(app.ctx, filepath.ToSlash(testImagePath), time.Now().Unix(), 4, "", 0, 0, 0, "image/jpeg")
	if err != nil {
		t.Fatalf("UpsertPathChain failed: %v", err)
	}

	authCookie := MakeAuthCookie(t, app)

	tests := []struct {
		name string
		url  string
	}{
		{"Gallery", "/gallery/" + strconv.FormatInt(childFolder.ID, 10)},
		{"Image", "/image/" + strconv.FormatInt(file.ID, 10)},
		{"Lightbox", "/lightbox/" + strconv.FormatInt(file.ID, 10)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// First request to get ETag
			req1 := httptest.NewRequest(http.MethodGet, tt.url, nil)
			req1.AddCookie(authCookie)
			rr1 := httptest.NewRecorder()
			router.ServeHTTP(rr1, req1)

			if rr1.Code != http.StatusOK {
				t.Fatalf("expected status 200 on first request, got %d", rr1.Code)
			}

			etag := rr1.Header().Get("ETag")
			if etag == "" {
				t.Fatal("expected ETag header on first request")
			}

			// Allow cache to be written
			time.Sleep(100 * time.Millisecond)

			// Second request with If-None-Match should return 304
			req2 := httptest.NewRequest(http.MethodGet, tt.url, nil)
			req2.AddCookie(authCookie)
			req2.Header.Set("If-None-Match", etag)
			rr2 := httptest.NewRecorder()
			router.ServeHTTP(rr2, req2)

			if rr2.Code != http.StatusNotModified {
				t.Errorf("expected status 304, got %d", rr2.Code)
			}

			// Verify 304 includes ETag
			if rr2.Header().Get("ETag") == "" {
				t.Error("expected ETag header in 304 response")
			}

			// Verify no body in 304
			if rr2.Body.Len() > 0 {
				t.Error("expected empty body in 304 response")
			}
		})
	}
}
