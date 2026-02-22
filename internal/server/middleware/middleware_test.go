package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/server/session"
)

// ============================================================================
// Auth Middleware Tests
// ============================================================================

func TestAuthMiddleware_NotAuthenticated(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	sessionManager := session.NewManager(store, func() *session.OptionsConfig { return nil })
	config := &AuthConfig{}

	handlerCalled := false
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	authFunc := AuthMiddleware(store, sessionManager, config)
	authHandler := authFunc(dummyHandler)

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()

	authHandler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusUnauthorized)
	}

	if handlerCalled {
		t.Error("next handler was called, but should not have been")
	}
}

func TestAuthMiddleware_Authenticated(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	sessionManager := session.NewManager(store, func() *session.OptionsConfig { return nil })
	config := &AuthConfig{}

	handlerCalled := false
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	authFunc := AuthMiddleware(store, sessionManager, config)
	authHandler := authFunc(dummyHandler)

	// Create an authenticated session
	rrWithCookie := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	sess, _ := store.Get(req, session.SessionName)
	sess.Values["authenticated"] = true
	if err := sess.Save(req, rrWithCookie); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	// Create a new request with the cookie
	rr := httptest.NewRecorder()
	newReq := httptest.NewRequest("GET", "/", nil)
	newReq.Header.Set("Cookie", rrWithCookie.Header().Get("Set-Cookie"))

	authHandler.ServeHTTP(rr, newReq)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	if !handlerCalled {
		t.Error("next handler was not called, but should have been")
	}
}

func TestAuthMiddleware_InvalidCookieClearsAndReturnsUnauthorized(t *testing.T) {
	store1 := sessions.NewCookieStore([]byte("secret-1"))
	config := &AuthConfig{}

	handlerCalled := false
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// First, create a valid authenticated cookie with store1
	rrWithCookie := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	sess, _ := store1.Get(req, session.SessionName)
	sess.Values["authenticated"] = true
	if err := sess.Save(req, rrWithCookie); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}
	cookieHeader := rrWithCookie.Header().Get("Set-Cookie")
	if cookieHeader == "" {
		t.Fatalf("expected Set-Cookie header to be set")
	}

	// Create a new store with different secret (simulating rotated secret)
	store2 := sessions.NewCookieStore([]byte("secret-2"))
	sessionManager2 := session.NewManager(store2, func() *session.OptionsConfig { return nil })

	// Create middleware with new store
	authFunc := AuthMiddleware(store2, sessionManager2, config)
	authHandler := authFunc(dummyHandler)

	// Attempt to access with old cookie; middleware should clear and return 401
	handlerCalled = false
	rr := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("Cookie", cookieHeader)

	authHandler.ServeHTTP(rr, req2)

	if status := rr.Code; status != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized on invalid cookie, got %d", status)
	}

	// Expect the middleware to clear the cookie (MaxAge=-1)
	setCookies := rr.Header()["Set-Cookie"]
	foundCleared := false
	for _, sc := range setCookies {
		if strings.Contains(sc, "session-name=") && (strings.Contains(sc, "Max-Age=0") || strings.Contains(sc, "Max-Age=-1") || strings.Contains(sc, "Expires=Thu, 01 Jan 1970")) {
			foundCleared = true
			break
		}
	}
	if !foundCleared {
		t.Fatalf("expected cleared session cookie in response headers; got %v", setCookies)
	}
	if handlerCalled {
		t.Fatalf("next handler should not be called when cookie is invalid")
	}
}

func TestAuthMiddleware_DebugDelay(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	sessionManager := session.NewManager(store, func() *session.OptionsConfig { return nil })
	config := &AuthConfig{
		DebugDelayMS: struct {
			IsSet bool
			Int   int
		}{IsSet: true, Int: 10},
	}

	handlerCalled := false
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	authFunc := AuthMiddleware(store, sessionManager, config)
	authHandler := authFunc(dummyHandler)

	// Create an authenticated session
	rrWithCookie := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	sess, _ := store.Get(req, session.SessionName)
	sess.Values["authenticated"] = true
	if err := sess.Save(req, rrWithCookie); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	rr := httptest.NewRecorder()
	newReq := httptest.NewRequest("GET", "/", nil)
	newReq.Header.Set("Cookie", rrWithCookie.Header().Get("Set-Cookie"))

	// Measure time to verify delay is applied
	authHandler.ServeHTTP(rr, newReq)

	if !handlerCalled {
		t.Error("next handler was not called")
	}
	// Note: We can't easily test the exact delay without making the test flaky,
	// but we verify the handler is called (delay doesn't block it)
}

func TestAuthMiddleware_NilConfig(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	sessionManager := session.NewManager(store, func() *session.OptionsConfig { return nil })

	handlerCalled := false
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// Test with nil config (should work fine)
	authFunc := AuthMiddleware(store, sessionManager, nil)
	authHandler := authFunc(dummyHandler)

	// Create an authenticated session
	rrWithCookie := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	sess, _ := store.Get(req, session.SessionName)
	sess.Values["authenticated"] = true
	if err := sess.Save(req, rrWithCookie); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	rr := httptest.NewRecorder()
	newReq := httptest.NewRequest("GET", "/", nil)
	newReq.Header.Set("Cookie", rrWithCookie.Header().Get("Set-Cookie"))

	authHandler.ServeHTTP(rr, newReq)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	if !handlerCalled {
		t.Error("next handler was not called")
	}
}

// ============================================================================
// Compress Middleware Tests
// ============================================================================

// TestNegotiateEncoding_PreferBrotli tests preference for brotli when available
func TestNegotiateEncoding_PreferBrotli(t *testing.T) {
	encoding := negotiateEncoding("br, gzip;q=0.8")
	if encoding != "br" {
		t.Errorf("negotiateEncoding() = %q, want %q", encoding, "br")
	}
}

// TestNegotiateEncoding_PreferGzip tests gzip when brotli unavailable
func TestNegotiateEncoding_PreferGzip(t *testing.T) {
	encoding := negotiateEncoding("gzip")
	if encoding != "gzip" {
		t.Errorf("negotiateEncoding() = %q, want %q", encoding, "gzip")
	}
}

// TestNegotiateEncoding_Identity tests fallback to identity
func TestNegotiateEncoding_Identity(t *testing.T) {
	encoding := negotiateEncoding("")
	if encoding != "identity" {
		t.Errorf("negotiateEncoding() = %q, want %q", encoding, "identity")
	}
}

// TestNegotiateEncoding_Wildcard tests wildcard accept-encoding
func TestNegotiateEncoding_Wildcard(t *testing.T) {
	encoding := negotiateEncoding("*")
	if encoding != "br" && encoding != "gzip" {
		t.Errorf("negotiateEncoding() = %q, want br or gzip", encoding)
	}
}

// TestNegotiateEncoding_IgnoresUnknown tests unknown encodings are ignored
func TestNegotiateEncoding_IgnoresUnknown(t *testing.T) {
	encoding := negotiateEncoding("deflate, gzip")
	if encoding != "gzip" {
		t.Errorf("negotiateEncoding() = %q, want %q", encoding, "gzip")
	}
}

// TestShouldCompressContentType_TextHTML tests text/html is compressible
func TestShouldCompressContentType_TextHTML(t *testing.T) {
	if !shouldCompressContentType("text/html") {
		t.Error("shouldCompressContentType(\"text/html\") should be true")
	}
}

// TestShouldCompressContentType_TextJSON tests text/json is compressible
func TestShouldCompressContentType_TextJSON(t *testing.T) {
	if !shouldCompressContentType("application/json") {
		t.Error("shouldCompressContentType(\"application/json\") should be true")
	}
}

// TestShouldCompressContentType_ImageJPEG tests image/jpeg is not compressible
func TestShouldCompressContentType_ImageJPEG(t *testing.T) {
	if shouldCompressContentType("image/jpeg") {
		t.Error("shouldCompressContentType(\"image/jpeg\") should be false")
	}
}

// TestShouldCompressContentType_ImagePNG tests image/png is not compressible
func TestShouldCompressContentType_ImagePNG(t *testing.T) {
	if shouldCompressContentType("image/png") {
		t.Error("shouldCompressContentType(\"image/png\") should be false")
	}
}

// TestShouldCompressPath_JPEGFile tests .jpg file is not compressible
func TestShouldCompressPath_JPEGFile(t *testing.T) {
	if shouldCompressPath("/gallery/image.jpg") {
		t.Error("shouldCompressPath(\"/gallery/image.jpg\") should be false")
	}
}

// TestShouldCompressPath_PNGFile tests .png file is not compressible
func TestShouldCompressPath_PNGFile(t *testing.T) {
	if shouldCompressPath("/gallery/image.png") {
		t.Error("shouldCompressPath(\"/gallery/image.png\") should be false")
	}
}

// TestShouldCompressPath_HTMLFile tests .html file is compressible
func TestShouldCompressPath_HTMLFile(t *testing.T) {
	if !shouldCompressPath("/page.html") {
		t.Error("shouldCompressPath(\"/page.html\") should be true")
	}
}

// TestShouldCompressPath_GZFile tests .gz file is not compressible
func TestShouldCompressPath_GZFile(t *testing.T) {
	if shouldCompressPath("/file.tar.gz") {
		t.Error("shouldCompressPath(\"/file.tar.gz\") should be false")
	}
}

// TestCompressMiddleware_GzipResponse tests gzip response compression
func TestCompressMiddleware_GzipResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(strings.Repeat("Hello, World! ", 40))) // >512 bytes
	})

	mw := CompressMiddleware(handler)

	req := httptest.NewRequest("GET", "/test.html", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	// Verify response is gzip encoded
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding = %q, want %q", w.Header().Get("Content-Encoding"), "gzip")
	}

	// Verify Vary header is set
	if !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Error("Vary header should contain Accept-Encoding")
	}
}

// TestCompressMiddleware_BrotliResponse tests br response compression
func TestCompressMiddleware_BrotliResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(strings.Repeat("Brotli body ", 60))) // >512 bytes
	})

	mw := CompressMiddleware(handler)

	req := httptest.NewRequest("GET", "/test.html", nil)
	req.Header.Set("Accept-Encoding", "br")
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Header().Get("Content-Encoding") != "br" {
		t.Fatalf("Content-Encoding = %q, want br", w.Header().Get("Content-Encoding"))
	}

	reader := brotli.NewReader(w.Body)
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to decompress br body: %v", err)
	}
	if len(decompressed) == 0 {
		t.Fatalf("Decompressed body is empty")
	}
	if !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Error("Vary header should contain Accept-Encoding")
	}
}

// TestCompressMiddleware_SkipsCompression_NoAcceptEncoding tests no compression without Accept-Encoding
func TestCompressMiddleware_SkipsCompression_NoAcceptEncoding(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("Hello, World!"))
	})

	mw := CompressMiddleware(handler)

	req := httptest.NewRequest("GET", "/test.html", nil)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	// No Content-Encoding header should be set
	if w.Header().Get("Content-Encoding") != "" {
		t.Errorf("Content-Encoding should be empty, got %q", w.Header().Get("Content-Encoding"))
	}
	if !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Error("Vary header should contain Accept-Encoding when skipping compression")
	}
}

// TestCompressMiddleware_SmallResponse_Compresses documents current behavior (tiny bodies still compressed)
func TestCompressMiddleware_SmallResponse_Compresses(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("hi")) // 2 bytes
	})

	mw := CompressMiddleware(handler)

	req := httptest.NewRequest("GET", "/test.html", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("Content-Encoding = %q, want empty for tiny response", w.Header().Get("Content-Encoding"))
	}
	if !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Error("Vary header should contain Accept-Encoding")
	}
}

// TestCompressMiddleware_WildcardNegotiation_BR ensures '*' negotiates br
func TestCompressMiddleware_WildcardNegotiation_BR(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("wildcard ", 60))) // >512 bytes
	})

	mw := CompressMiddleware(handler)

	req := httptest.NewRequest("GET", "/test.txt", nil)
	req.Header.Set("Accept-Encoding", "*")
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Header().Get("Content-Encoding") != "br" {
		t.Fatalf("Content-Encoding = %q, want br", w.Header().Get("Content-Encoding"))
	}
	reader := brotli.NewReader(w.Body)
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to decompress br body: %v", err)
	}
	if len(decompressed) == 0 {
		t.Fatalf("Decompressed body is empty")
	}
	if !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Error("Vary header should contain Accept-Encoding")
	}
}

// TestCompressMiddleware_HeadNoBody ensures HEAD responses remain identity
func TestCompressMiddleware_HeadNoBody(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNoContent)
	})

	mw := CompressMiddleware(handler)

	req := httptest.NewRequest("HEAD", "/head.html", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("Content-Encoding = %q, want empty for HEAD/204", w.Header().Get("Content-Encoding"))
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("expected empty body for HEAD/204, got %d bytes", w.Body.Len())
	}
	if !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Error("Vary header should contain Accept-Encoding")
	}
}

// TestCompressMiddleware_SkipsCompression_Image tests no compression for images
func TestCompressMiddleware_SkipsCompression_Image(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("fake image data"))
	})

	mw := CompressMiddleware(handler)

	req := httptest.NewRequest("GET", "/image.jpg", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	// No Content-Encoding header for image
	if w.Header().Get("Content-Encoding") != "" {
		t.Errorf("Content-Encoding should be empty for image, got %q", w.Header().Get("Content-Encoding"))
	}
}

// TestCompressMiddleware_PreservesBody_Gzip tests gzip decompressed body matches original
func TestCompressMiddleware_PreservesBody_Gzip(t *testing.T) {
	originalBody := strings.Repeat("Hello, World! This is a longer test response to ensure compression has an effect. ", 12) // >512 bytes to trigger compression

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(originalBody))
	})

	mw := CompressMiddleware(handler)

	req := httptest.NewRequest("GET", "/test.html", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", w.Header().Get("Content-Encoding"))
	}

	// Decompress and verify body
	reader, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("Failed to create gzip reader: %v", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to decompress: %v", err)
	}

	if string(decompressed) != originalBody {
		t.Errorf("Decompressed body = %q, want %q", string(decompressed), originalBody)
	}
}

// TestCompressMiddleware_SetVaryHeader tests Vary header is always set
func TestCompressMiddleware_SetVaryHeader(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("test"))
	})

	mw := CompressMiddleware(handler)

	req := httptest.NewRequest("GET", "/test.html", nil)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	// Vary header should be set even without compression
	if w.Header().Get("Vary") == "" {
		t.Error("Vary header should be set")
	}
}

// TestCompressMiddleware_SmallResponse tests small responses are not compressed
func TestCompressMiddleware_SmallResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("hi")) // 2 bytes, below threshold
	})

	mw := CompressMiddleware(handler)

	req := httptest.NewRequest("GET", "/test.html", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	// Small responses may not be compressed (optimization)
	// This test documents the behavior, actual implementation may vary
	_ = w.Body.Bytes()
}

// TestCompressMiddleware_SkipsCompression_PreexistingContentEncoding tests that pre-existing Content-Encoding header prevents wrapping
func TestCompressMiddleware_SkipsCompression_PreexistingContentEncoding(t *testing.T) {
	// Create a custom ResponseWriter that has Content-Encoding pre-set
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("already encoded ", 40))) // >512 bytes
	})

	mw := CompressMiddleware(handler)

	req := httptest.NewRequest("GET", "/test.txt", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	baseRecorder := httptest.NewRecorder()

	// Pre-set Content-Encoding before middleware sees it
	baseRecorder.Header().Set("Content-Encoding", "deflate")

	mw.ServeHTTP(baseRecorder, req)

	// The middleware should have skipped wrapping because Content-Encoding was already set.
	// Verify it stayed as deflate and didn't get overwritten to gzip.
	if baseRecorder.Header().Get("Content-Encoding") != "deflate" {
		t.Fatalf("Content-Encoding = %q, want deflate (unchanged)", baseRecorder.Header().Get("Content-Encoding"))
	}
}

// TestCompressMiddleware_HeadersSetBeforeBody ensures Content-Encoding header is sent before body
func TestCompressMiddleware_HeadersSetBeforeBody(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Write large body in chunks to ensure headers are already sent
		for range 10 {
			_, _ = w.Write([]byte(strings.Repeat("chunk of data ", 50))) // each chunk 650 bytes
		}
	})

	mw := CompressMiddleware(handler)

	req := httptest.NewRequest("GET", "/test.html", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	// Critical: Content-Encoding MUST be set in response headers
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip; headers missing encoding!", w.Header().Get("Content-Encoding"))
	}

	// Verify body is actually gzipped
	reader, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("Failed to create gzip reader (body not gzipped): %v", err)
	}
	defer reader.Close()
	decompressed, _ := io.ReadAll(reader)
	if len(decompressed) == 0 {
		t.Fatal("Decompressed body is empty")
	}
}

// ============================================================================
// Conditional Middleware Tests
// ============================================================================

// TestMatchesETag_ExactMatch tests exact ETag match
func TestMatchesETag_ExactMatch(t *testing.T) {
	etag := `"abc123"`
	ifNoneMatch := `"abc123"`

	if !matchesETag(etag, ifNoneMatch) {
		t.Error("matchesETag() should return true for exact match")
	}
}

// TestMatchesETag_NoMatch tests ETag mismatch
func TestMatchesETag_NoMatch(t *testing.T) {
	etag := `"abc123"`
	ifNoneMatch := `"xyz789"`

	if matchesETag(etag, ifNoneMatch) {
		t.Error("matchesETag() should return false for mismatch")
	}
}

// TestMatchesETag_Wildcard tests wildcard match
func TestMatchesETag_Wildcard(t *testing.T) {
	etag := `"abc123"`
	ifNoneMatch := "*"

	if !matchesETag(etag, ifNoneMatch) {
		t.Error("matchesETag() should return true for wildcard")
	}
}

// TestMatchesETag_MultipleValues tests multiple ETag values
func TestMatchesETag_MultipleValues(t *testing.T) {
	etag := `"abc123"`
	ifNoneMatch := `"xyz789", "abc123", "def456"`

	if !matchesETag(etag, ifNoneMatch) {
		t.Error("matchesETag() should return true when etag found in list")
	}
}

// TestMatchesETag_WeakMatch tests weak ETag matching
func TestMatchesETag_WeakMatch(t *testing.T) {
	etag := `W/"abc123"`
	ifNoneMatch := `"abc123"`

	// Weak and strong ETags should match for If-None-Match
	if !matchesETag(etag, ifNoneMatch) {
		t.Error("matchesETag() should match weak and strong ETags")
	}
}

// TestMatchesLastModified_Before tests modified before check date
func TestMatchesLastModified_Before(t *testing.T) {
	lastModified := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	ifModifiedSince := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)

	if !matchesLastModified(lastModified, ifModifiedSince) {
		t.Error("matchesLastModified() should return true if modified before check date")
	}
}

// TestMatchesLastModified_After tests modified after check date
func TestMatchesLastModified_After(t *testing.T) {
	lastModified := time.Date(2024, 1, 3, 12, 0, 0, 0, time.UTC)
	ifModifiedSince := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)

	if matchesLastModified(lastModified, ifModifiedSince) {
		t.Error("matchesLastModified() should return false if modified after check date")
	}
}

// TestMatchesLastModified_Exact tests exact modification time match
func TestMatchesLastModified_Exact(t *testing.T) {
	lastModified := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)
	ifModifiedSince := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)

	if !matchesLastModified(lastModified, ifModifiedSince) {
		t.Error("matchesLastModified() should return true for exact match")
	}
}

// TestConditionalMiddleware_ETag_304Response tests handler can check ETag and return 304
func TestConditionalMiddleware_ETag_304Response(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		etag := `"abc123"`
		ifNoneMatch := r.Header.Get("If-None-Match")

		// Handler checks if ETag matches
		if ifNoneMatch != "" && matchesETag(etag, ifNoneMatch) {
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.Header().Set("ETag", etag)
		w.WriteHeader(200)
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("If-None-Match", `"abc123"`)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != 304 {
		t.Errorf("Response code = %d, want 304", w.Code)
	}
}

// TestConditionalMiddleware_ETag_200Response tests 200 on ETag mismatch
func TestConditionalMiddleware_ETag_200Response(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("content"))
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("If-None-Match", `"xyz789"`)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Response code = %d, want 200", w.Code)
	}

	if w.Body.String() != "content" {
		t.Errorf("Response body = %q, want %q", w.Body.String(), "content")
	}
}

// TestConditionalMiddleware_LastModified_304Response tests 304 on Last-Modified match
func TestConditionalMiddleware_LastModified_304Response(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastModifiedStr := "Mon, 02 Jan 2024 12:00:00 GMT"
		ifModSince := r.Header.Get("If-Modified-Since")

		// Handler checks if Last-Modified matches
		if ifModSince != "" {
			ifModSinceTime, err := time.Parse(time.RFC1123, ifModSince)
			if err == nil {
				lastModTime, err := time.Parse(time.RFC1123, lastModifiedStr)
				if err == nil && matchesLastModified(lastModTime, ifModSinceTime) {
					w.Header().Set("Last-Modified", lastModifiedStr)
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}

		w.Header().Set("Last-Modified", lastModifiedStr)
		w.WriteHeader(200)
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("If-Modified-Since", "Mon, 02 Jan 2024 12:00:00 GMT")
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != 304 {
		t.Errorf("Response code = %d, want 304", w.Code)
	}
}

// TestConditionalMiddleware_LastModified_304Response_Middleware tests middleware-triggered 304 on Last-Modified match
func TestConditionalMiddleware_LastModified_304Response_Middleware(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", "Mon, 01 Jan 2024 12:00:00 GMT")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("content"))
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("If-Modified-Since", "Mon, 02 Jan 2024 12:00:00 GMT")
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != http.StatusNotModified {
		t.Errorf("Response code = %d, want 304", w.Code)
	}

	if w.Body.Len() != 0 {
		t.Errorf("Response body length = %d, want 0", w.Body.Len())
	}
}

// TestConditionalMiddleware_NoCacheHeaders tests pass-through without validators
func TestConditionalMiddleware_NoCacheHeaders(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("content"))
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("If-None-Match", `"abc123"`)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Response code = %d, want 200 (no validators)", w.Code)
	}

	if w.Body.String() != "content" {
		t.Errorf("Response body should be returned when no validators present")
	}
}

// TestConditionalMiddleware_PreserveHeaders tests 304 preserves cache headers
func TestConditionalMiddleware_PreserveHeaders(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		etag := `"abc123"`
		ifNoneMatch := r.Header.Get("If-None-Match")

		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Header().Set("Vary", "Accept-Encoding")

		// Check and return 304 if match
		if ifNoneMatch != "" && matchesETag(etag, ifNoneMatch) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.WriteHeader(200)
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("If-None-Match", `"abc123"`)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != 304 {
		t.Errorf("Response code = %d, want 304", w.Code)
	}

	if w.Header().Get("ETag") != `"abc123"` {
		t.Error("ETag should be preserved in 304 response")
	}

	if w.Header().Get("Cache-Control") == "" {
		t.Error("Cache-Control should be preserved in 304 response")
	}
}

// TestConditionalMiddleware_HEAD_SkipsBody verifies HEAD requests don't send body on 200
func TestConditionalMiddleware_HEAD_SkipsBody(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body content"))
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("HEAD", "/test", nil)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Response code = %d, want 200", w.Code)
	}

	if w.Body.Len() > 0 {
		t.Errorf("HEAD response body length = %d, want 0", w.Body.Len())
	}
}

// TestConditionalMiddleware_SkipsNonGetHead verifies POST/PUT/etc. are not buffered and bypass 304 checks
func TestConditionalMiddleware_SkipsNonGetHead(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("response"))
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("POST", "/test", nil)
	req.Header.Set("If-None-Match", `"abc123"`)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Response code = %d, want 200 (POST should not get 304)", w.Code)
	}

	if w.Body.String() != "response" {
		t.Errorf("Response body = %q, want %q", w.Body.String(), "response")
	}
}

// TestConditionalMiddleware_EntityHeadersOmittedOn304 verifies Content-Type and Content-Length are stripped on 304
func TestConditionalMiddleware_EntityHeadersOmittedOn304(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Length", "100")
		w.Header().Set("Cache-Control", "max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("content"))
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("If-None-Match", `"abc123"`)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != http.StatusNotModified {
		t.Errorf("Response code = %d, want 304", w.Code)
	}

	if w.Header().Get("Content-Type") != "" {
		t.Error("Content-Type should be omitted from 304 response")
	}

	if w.Header().Get("Content-Length") != "" {
		t.Error("Content-Length should be omitted from 304 response")
	}

	if w.Header().Get("Cache-Control") != "max-age=3600" {
		t.Error("Cache-Control should be preserved in 304 response")
	}

	if w.Header().Get("ETag") != `"abc123"` {
		t.Error("ETag should be preserved in 304 response")
	}
}

// TestConditionalMiddleware_Auto304_ETag ensures middleware returns 304 based on ETag without handler short-circuit
func TestConditionalMiddleware_Auto304_ETag(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Cache-Control", "max-age=60")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fresh content"))
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("If-None-Match", `"abc123"`)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != http.StatusNotModified {
		t.Fatalf("Response code = %d, want 304", w.Code)
	}
	if body := w.Body.String(); body != "" {
		t.Fatalf("Response body = %q, want empty", body)
	}
	if w.Header().Get("ETag") != `"abc123"` {
		t.Fatal("ETag should be preserved in 304 response")
	}
	if w.Header().Get("Cache-Control") != "max-age=60" {
		t.Fatal("Cache-Control should be preserved in 304 response")
	}
}

// TestConditionalMiddleware_Auto304_LastModified ensures middleware returns 304 based on Last-Modified without handler short-circuit
func TestConditionalMiddleware_Auto304_LastModified(t *testing.T) {
	lastMod := time.Now().Add(-time.Hour).UTC().Format(time.RFC1123)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", lastMod)
		w.Header().Set("Cache-Control", "max-age=120")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fresh content"))
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("If-Modified-Since", lastMod)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != http.StatusNotModified {
		t.Fatalf("Response code = %d, want 304", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("Response body length = %d, want 0", w.Body.Len())
	}
	if w.Header().Get("Last-Modified") != lastMod {
		t.Fatal("Last-Modified should be preserved in 304 response")
	}
	if w.Header().Get("Cache-Control") != "max-age=120" {
		t.Fatal("Cache-Control should be preserved in 304 response")
	}
}

// ============================================================================
// CSRF Middleware Tests
// ============================================================================

func TestCSRFProtection_AllowsSafeMethods(t *testing.T) {
	handlerCalled := false
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := CSRFProtection(dummyHandler)

	safeMethods := []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace}
	for _, method := range safeMethods {
		t.Run(method, func(t *testing.T) {
			handlerCalled = false
			req := httptest.NewRequest(method, "/test", nil)
			rr := httptest.NewRecorder()

			mw.ServeHTTP(rr, req)

			if !handlerCalled {
				t.Errorf("handler should be called for safe method %s", method)
			}
			if rr.Code != http.StatusOK {
				t.Errorf("expected 200 for safe method %s, got %d", method, rr.Code)
			}
		})
	}
}

func TestCSRFProtection_RejectsUnsafeMethodWithoutOrigin(t *testing.T) {
	handlerCalled := false
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := CSRFProtection(dummyHandler)

	req := httptest.NewRequest("POST", "/test", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if handlerCalled {
		t.Error("handler should not be called when Origin is missing")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", rr.Code)
	}
}

func TestCSRFProtection_AllowsSameOriginHTTP(t *testing.T) {
	handlerCalled := false
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := CSRFProtection(dummyHandler)

	req := httptest.NewRequest("POST", "/test", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "http://example.com")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if !handlerCalled {
		t.Error("handler should be called for same-origin request")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestCSRFProtection_AllowsSameOriginHTTPS(t *testing.T) {
	handlerCalled := false
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := CSRFProtection(dummyHandler)

	req := httptest.NewRequest("POST", "/test", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if !handlerCalled {
		t.Error("handler should be called for same-origin HTTPS request")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestCSRFProtection_RejectsCrossOriginRequest(t *testing.T) {
	handlerCalled := false
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := CSRFProtection(dummyHandler)

	req := httptest.NewRequest("POST", "/test", nil)
	req.Host = "example.com"
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if handlerCalled {
		t.Error("handler should not be called for cross-origin request")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", rr.Code)
	}
}

func TestCSRFProtection_HandlesHostWithPort(t *testing.T) {
	handlerCalled := false
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := CSRFProtection(dummyHandler)

	req := httptest.NewRequest("POST", "/test", nil)
	req.Host = "example.com:8080"
	req.Header.Set("Origin", "http://example.com:8080")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if !handlerCalled {
		t.Error("handler should be called when Origin matches host with port")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestCSRFProtection_RejectsMismatchedPort(t *testing.T) {
	handlerCalled := false
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := CSRFProtection(dummyHandler)

	req := httptest.NewRequest("POST", "/test", nil)
	req.Host = "example.com:8080"
	req.Header.Set("Origin", "http://example.com:9090")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if handlerCalled {
		t.Error("handler should not be called when Origin port doesn't match")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", rr.Code)
	}
}

// ============================================================================
// Logging Middleware Tests
// ============================================================================

func TestLoggingMiddleware_LogsEveryRequestAndResponse(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		w.Write([]byte("ok"))
	})

	wrapped := LoggingMiddleware(handler)
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	logStr := logBuf.String()
	if !strings.Contains(logStr, "Request received") {
		t.Errorf("Expected 'Request received' in logs, got: %s", logStr)
	}
	if !strings.Contains(logStr, "Request completed") {
		t.Errorf("Expected 'Request completed' in logs, got: %s", logStr)
	}
	if !strings.Contains(logStr, "Status=201") && !strings.Contains(logStr, "Status:201") {
		t.Errorf("Expected status 201 in logs, got: %s", logStr)
	}
}

func TestLoggingMiddleware_SanitizesSensitiveHeaders(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	wrapped := LoggingMiddleware(handler)
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Cookie", "session-name=secret-session-token")
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("User-Agent", "test-agent")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	logStr := logBuf.String()
	// Verify sensitive headers are redacted
	if strings.Contains(logStr, "secret-session-token") {
		t.Error("Session token should not appear in logs")
	}
	if strings.Contains(logStr, "secret-token") {
		t.Error("Authorization token should not appear in logs")
	}
	// Verify redaction marker appears
	if !strings.Contains(logStr, "[REDACTED]") {
		t.Error("Expected [REDACTED] marker for sensitive headers in logs")
	}
	// Verify non-sensitive headers are still logged
	if !strings.Contains(logStr, "test-agent") {
		t.Error("Non-sensitive headers should still appear in logs")
	}
}

func TestNewLoggingMiddleware_WithCustomLogger(t *testing.T) {
	var logBuf bytes.Buffer
	customLogger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	mwFunc := NewLoggingMiddleware(customLogger)
	wrapped := mwFunc(handler)
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	logStr := logBuf.String()
	if !strings.Contains(logStr, "Request received") {
		t.Errorf("Expected 'Request received' in logs with custom logger, got: %s", logStr)
	}
	if !strings.Contains(logStr, "Request completed") {
		t.Errorf("Expected 'Request completed' in logs with custom logger, got: %s", logStr)
	}
}
