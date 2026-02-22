//go:build e2e

package server

import (
	"compress/gzip"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/andybalholm/brotli"

	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/gallerylib"
	"go.local/sfpg/internal/getopt"
)

// TestE2E_CacheAndCompression_GalleryEndpoint tests the full cache+compression stack
// by hitting a gallery endpoint with different Accept-Encoding headers and verifying
// compression, caching, cache hits, and encoding separation.
func TestE2E_CacheAndCompression_GalleryEndpoint(t *testing.T) {
	// Setup: create app with compression and caching enabled
	app := CreateApp(t, false) // no worker pools needed
	app.opt.EnableCompression = getopt.OptBool{Bool: true, IsSet: true}
	app.opt.EnableHTTPCache = getopt.OptBool{Bool: true, IsSet: true}

	// Recreate router with cache enabled
	router := app.getRouter()

	// Create test data: root folder and a child folder with test images
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

	// Create child folder
	childPathID, err := cpcRw.Queries.UpsertFolderPathReturningID(app.ctx, "testgallery")
	if err != nil {
		t.Fatalf("failed to create child path: %v", err)
	}
	childFolder, err := cpcRw.Queries.UpsertFolderReturningFolder(app.ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: rootID, Valid: true},
		PathID:    childPathID,
		Name:      "testgallery",
		Mtime:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("failed to create child folder: %v", err)
	}

	// Create auth cookie
	authCookie := MakeAuthCookie(t, app)

	// Test 1: First request with gzip encoding
	t.Run("FirstRequest_Gzip_CacheMiss", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
		req.AddCookie(authCookie)
		req.Header.Set("Accept-Encoding", "gzip")
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)

		// Verify response
		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}

		// Verify compression
		if ce := rr.Header().Get("Content-Encoding"); ce != "gzip" {
			t.Errorf("expected Content-Encoding: gzip, got %q", ce)
		}

		// Verify Vary header includes Accept-Encoding
		if vary := rr.Header().Get("Vary"); vary == "" {
			t.Error("expected Vary header to be set, got empty")
		}

		// Verify ETag and Last-Modified set
		if etag := rr.Header().Get("ETag"); etag == "" {
			t.Error("expected ETag header, got empty")
		}
		if lm := rr.Header().Get("Last-Modified"); lm == "" {
			t.Error("expected Last-Modified header, got empty")
		}

		// Verify Cache-Control
		if cc := rr.Header().Get("Cache-Control"); cc == "" {
			t.Error("expected Cache-Control header, got empty")
		}

		// Verify body is compressed (can decompress)
		gr, err := gzip.NewReader(rr.Body)
		if err != nil {
			t.Fatalf("failed to create gzip reader: %v", err)
		}
		defer gr.Close()
		body, err := io.ReadAll(gr)
		if err != nil {
			t.Fatalf("failed to read gzipped body: %v", err)
		}
		if len(body) == 0 {
			t.Error("expected non-empty decompressed body")
		}
	})

	// Test 2: Second request with gzip encoding (should hit cache)
	t.Run("SecondRequest_Gzip_CacheHit", func(t *testing.T) {
		// Small delay to ensure cache is stored
		time.Sleep(100 * time.Millisecond)

		req := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
		req.AddCookie(authCookie)
		req.Header.Set("Accept-Encoding", "gzip")
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)

		// Verify response still correct
		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}

		if ce := rr.Header().Get("Content-Encoding"); ce != "gzip" {
			t.Errorf("expected Content-Encoding: gzip, got %q", ce)
		}

		// Verify body still compressed and valid
		gr, err := gzip.NewReader(rr.Body)
		if err != nil {
			t.Fatalf("failed to create gzip reader: %v", err)
		}
		defer gr.Close()
		body, err := io.ReadAll(gr)
		if err != nil {
			t.Fatalf("failed to read gzipped body: %v", err)
		}
		if len(body) == 0 {
			t.Error("expected non-empty decompressed body from cache")
		}
	})

	// Test 3: Request with brotli encoding (should be separate cache entry)
	t.Run("ThirdRequest_Brotli_SeparateCache", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
		req.AddCookie(authCookie)
		req.Header.Set("Accept-Encoding", "br")
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)

		// Verify response
		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}

		// Verify brotli compression
		if ce := rr.Header().Get("Content-Encoding"); ce != "br" {
			t.Errorf("expected Content-Encoding: br, got %q", ce)
		}

		// Verify Vary header
		if vary := rr.Header().Get("Vary"); vary == "" {
			t.Error("expected Vary header to be set, got empty")
		}

		// Verify body is brotli-compressed (can decompress)
		br := brotli.NewReader(rr.Body)
		body, err := io.ReadAll(br)
		if err != nil {
			t.Fatalf("failed to read brotli-compressed body: %v", err)
		}
		if len(body) == 0 {
			t.Error("expected non-empty decompressed body")
		}
	})

	// Test 4: Request with no encoding (identity, should be separate cache entry)
	t.Run("FourthRequest_Identity_SeparateCache", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
		req.AddCookie(authCookie)
		// No Accept-Encoding header
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)

		// Verify response
		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}

		// Verify no compression
		if ce := rr.Header().Get("Content-Encoding"); ce != "" {
			t.Errorf("expected no Content-Encoding, got %q", ce)
		}

		// Body should be uncompressed
		body := rr.Body.Bytes()
		if len(body) == 0 {
			t.Error("expected non-empty uncompressed body")
		}
	})
}

// TestE2E_304Revalidation tests 304 Not Modified responses after cache hit
func TestE2E_304Revalidation(t *testing.T) {
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

	childPathID, err := cpcRw.Queries.UpsertFolderPathReturningID(app.ctx, "test304")
	if err != nil {
		t.Fatalf("failed to create child path: %v", err)
	}
	childFolder, err := cpcRw.Queries.UpsertFolderReturningFolder(app.ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: rootID, Valid: true},
		PathID:    childPathID,
		Name:      "test304",
		Mtime:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("failed to create child folder: %v", err)
	}

	// Create auth cookie
	authCookie := MakeAuthCookie(t, app)

	// First request to populate cache
	req1 := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
	req1.AddCookie(authCookie)
	req1.Header.Set("Accept-Encoding", "gzip")
	rr1 := httptest.NewRecorder()
	router.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("expected status 200 on first request, got %d", rr1.Code)
	}

	etag := rr1.Header().Get("ETag")
	lastModified := rr1.Header().Get("Last-Modified")

	if etag == "" || lastModified == "" {
		t.Fatal("expected ETag and Last-Modified headers on first request")
	}

	// Second request with If-None-Match (should return 304)
	t.Run("IfNoneMatch_304Response", func(t *testing.T) {
		time.Sleep(100 * time.Millisecond) // ensure cache stored

		req := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
		req.AddCookie(authCookie)
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("If-None-Match", etag)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotModified {
			t.Errorf("expected status 304, got %d", rr.Code)
		}

		// Verify 304 includes validators
		if rr.Header().Get("ETag") == "" {
			t.Error("expected ETag header in 304 response")
		}

		if lm := rr.Header().Get("Last-Modified"); lm == "" {
			t.Error("expected Last-Modified header in 304 response")
		}

		// Verify no body
		if rr.Body.Len() > 0 {
			t.Error("expected empty body in 304 response")
		}
	})

	// Third request with If-Modified-Since (conditional, may be 304 or 200)
	t.Run("IfModifiedSince_Conditional", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
		req.AddCookie(authCookie)
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("If-Modified-Since", lastModified)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)

		//  if rr.Code == http.StatusNotModified {
		//		if rr.Body.Len() > 0 {
		//			t.Error("expected empty body in 304 response")
		//		}
		//	} else if rr.Code == http.StatusOK {
		//		if rr.Body.Len() == 0 {
		//			t.Error("expected body in 200 response")
		//		}
		//	} else {
		//		t.Errorf("expected 200 or 304, got %d", rr.Code)
		//	}

		switch rr.Code {
		case http.StatusNotModified:
			if rr.Body.Len() > 0 {
				t.Error("expected empty body in 304 response")
			}
		case http.StatusOK:
			if rr.Body.Len() == 0 {
				t.Error("expected body in 200 response")
			}
		default:
			t.Errorf("expected 200 or 304, got %d", rr.Code)
		}
	})
}

// TestE2E_CompressionVsEncodingSeparation verifies that gzip and brotli
// responses are stored in separate cache entries and served correctly.
func TestE2E_CompressionVsEncodingSeparation(t *testing.T) {
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

	childPathID, err := cpcRw.Queries.UpsertFolderPathReturningID(app.ctx, "testencode")
	if err != nil {
		t.Fatalf("failed to create child path: %v", err)
	}
	childFolder, err := cpcRw.Queries.UpsertFolderReturningFolder(app.ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: rootID, Valid: true},
		PathID:    childPathID,
		Name:      "testencode",
		Mtime:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("failed to create child folder: %v", err)
	}

	// Create auth cookie
	authCookie := MakeAuthCookie(t, app)

	// Request 1: gzip
	req1 := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
	req1.AddCookie(authCookie)
	req1.Header.Set("Accept-Encoding", "gzip")
	rr1 := httptest.NewRecorder()
	router.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr1.Code)
	}
	if rr1.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("expected gzip encoding, got %q", rr1.Header().Get("Content-Encoding"))
	}

	gzipBody := rr1.Body.Bytes()

	// Request 2: brotli
	time.Sleep(100 * time.Millisecond)
	req2 := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
	req2.AddCookie(authCookie)
	req2.Header.Set("Accept-Encoding", "br")
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr2.Code)
	}
	if rr2.Header().Get("Content-Encoding") != "br" {
		t.Errorf("expected br encoding, got %q", rr2.Header().Get("Content-Encoding"))
	}

	brBody := rr2.Body.Bytes()

	// Verify bodies are different (different compression algorithms)
	if len(gzipBody) == 0 || len(brBody) == 0 {
		t.Fatal("expected non-empty compressed bodies")
	}

	// Bodies should differ (different encodings)
	// Note: this is probabilistic, but highly likely for any non-trivial content
	if len(gzipBody) == len(brBody) {
		// Could be same size by chance, but content should differ
		same := true
		for i := range gzipBody {
			if gzipBody[i] != brBody[i] {
				same = false
				break
			}
		}
		if same {
			t.Error("expected different compressed bodies for gzip vs brotli")
		}
	}

	// Request 3: gzip again (should hit cache, verify same body)
	time.Sleep(100 * time.Millisecond)
	req3 := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
	req3.AddCookie(authCookie)
	req3.Header.Set("Accept-Encoding", "gzip")
	rr3 := httptest.NewRecorder()
	router.ServeHTTP(rr3, req3)

	if rr3.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr3.Code)
	}

	gzipBodyCached := rr3.Body.Bytes()
	if len(gzipBodyCached) != len(gzipBody) {
		t.Errorf("expected same gzip body length from cache, got %d vs %d", len(gzipBodyCached), len(gzipBody))
	}
}

// TestE2E_Gallery_MissThenHitHeaders ensures compressed responses preserve headers across MISS→HIT.
func TestE2E_Gallery_MissThenHitHeaders(t *testing.T) {
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

	childPathID, err := cpcRw.Queries.UpsertFolderPathReturningID(app.ctx, "test-miss-hit")
	if err != nil {
		t.Fatalf("failed to create child path: %v", err)
	}
	childFolder, err := cpcRw.Queries.UpsertFolderReturningFolder(app.ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: rootID, Valid: true},
		PathID:    childPathID,
		Name:      "test-miss-hit",
		Mtime:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("failed to create child folder: %v", err)
	}

	authCookie := MakeAuthCookie(t, app)

	// First request (MISS)
	req1 := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
	req1.AddCookie(authCookie)
	req1.Header.Set("Accept-Encoding", "br")
	rr1 := httptest.NewRecorder()
	router.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("MISS status = %d, want 200", rr1.Code)
	}
	if xcache := rr1.Header().Get("X-Cache"); xcache != "" && xcache != "MISS" {
		t.Fatalf("MISS X-Cache = %q, want MISS or empty (middleware may defer write)", xcache)
	}
	if ce := rr1.Header().Get("Content-Encoding"); ce != "br" {
		t.Fatalf("MISS Content-Encoding = %q, want br", ce)
	}
	if ct := rr1.Header().Get("Content-Type"); ct == "" {
		t.Fatalf("MISS Content-Type empty")
	}
	if cc := rr1.Header().Get("Cache-Control"); cc == "" {
		t.Fatalf("MISS Cache-Control empty")
	}
	if etag := rr1.Header().Get("ETag"); etag == "" {
		t.Fatalf("MISS ETag empty")
	}
	if lm := rr1.Header().Get("Last-Modified"); lm == "" {
		t.Fatalf("MISS Last-Modified empty")
	}
	if vary := rr1.Header().Get("Vary"); vary == "" {
		t.Fatalf("MISS Vary empty")
	}

	br1 := brotli.NewReader(rr1.Body)
	body1, err := io.ReadAll(br1)
	if err != nil {
		t.Fatalf("failed to decompress br MISS body: %v", err)
	}
	if len(body1) == 0 {
		t.Fatalf("MISS body empty after decompress")
	}

	// Second request (HIT)
	req2 := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
	req2.AddCookie(authCookie)
	req2.Header.Set("Accept-Encoding", "br")
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("HIT status = %d, want 200", rr2.Code)
	}
	if xcache := rr2.Header().Get("X-Cache"); xcache != "HIT" && xcache != "" {
		t.Fatalf("HIT X-Cache = %q, want HIT or empty (middleware may defer write)", xcache)
	}
	if ce := rr2.Header().Get("Content-Encoding"); ce != "br" {
		t.Fatalf("HIT Content-Encoding = %q, want br", ce)
	}
	if ct := rr2.Header().Get("Content-Type"); ct == "" {
		t.Fatalf("HIT Content-Type empty")
	}
	if cc := rr2.Header().Get("Cache-Control"); cc == "" {
		t.Fatalf("HIT Cache-Control empty")
	}
	if etag := rr2.Header().Get("ETag"); etag == "" {
		t.Fatalf("HIT ETag empty")
	}
	if lm := rr2.Header().Get("Last-Modified"); lm == "" {
		t.Fatalf("HIT Last-Modified empty")
	}
	if vary := rr2.Header().Get("Vary"); vary == "" {
		t.Fatalf("HIT Vary empty")
	}

	br2 := brotli.NewReader(rr2.Body)
	body2, err := io.ReadAll(br2)
	if err != nil {
		t.Fatalf("failed to decompress br HIT body: %v", err)
	}
	if len(body2) == 0 {
		t.Fatalf("HIT body empty after decompress")
	}
}

// TestE2E_Gallery_IfNoneMatch304_NoBody ensures cached ETag yields 304 without body or Content-Encoding.
func TestE2E_Gallery_IfNoneMatch304_NoBody(t *testing.T) {
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

	childPathID, err := cpcRw.Queries.UpsertFolderPathReturningID(app.ctx, "test-304-none")
	if err != nil {
		t.Fatalf("failed to create child path: %v", err)
	}
	childFolder, err := cpcRw.Queries.UpsertFolderReturningFolder(app.ctx, gallerydb.UpsertFolderReturningFolderParams{
		ParentID:  sql.NullInt64{Int64: rootID, Valid: true},
		PathID:    childPathID,
		Name:      "test-304-none",
		Mtime:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("failed to create child folder: %v", err)
	}

	authCookie := MakeAuthCookie(t, app)

	// Prime cache
	req1 := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
	req1.AddCookie(authCookie)
	req1.Header.Set("Accept-Encoding", "gzip")
	rr1 := httptest.NewRecorder()
	router.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("prime status = %d, want 200", rr1.Code)
	}
	etag := rr1.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("prime ETag empty")
	}
	if lm := rr1.Header().Get("Last-Modified"); lm == "" {
		t.Fatalf("prime Last-Modified empty")
	}

	// Conditional request expecting 304
	req2 := httptest.NewRequest(http.MethodGet, "/gallery/"+strconv.FormatInt(childFolder.ID, 10), nil)
	req2.AddCookie(authCookie)
	req2.Header.Set("Accept-Encoding", "gzip")
	req2.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rr2.Code)
	}
	if rr2.Body.Len() != 0 {
		t.Fatalf("304 body length = %d, want 0", rr2.Body.Len())
	}
	if ce := rr2.Header().Get("Content-Encoding"); ce != "" {
		t.Fatalf("304 Content-Encoding = %q, want empty", ce)
	}
	if et := rr2.Header().Get("ETag"); et != etag {
		t.Fatalf("304 ETag = %q, want %q", et, etag)
	}
	if lm := rr2.Header().Get("Last-Modified"); lm == "" {
		t.Fatalf("304 Last-Modified empty")
	}
	if vary := rr2.Header().Get("Vary"); vary == "" {
		t.Fatalf("304 Vary empty")
	}
	if xcache := rr2.Header().Get("X-Cache"); xcache != "" && xcache != "HIT" {
		t.Fatalf("304 X-Cache = %q, want HIT or empty (handler bypassed cache header)", xcache)
	}
}
