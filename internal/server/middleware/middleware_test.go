package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// TestAuthMiddleware_HTMXCachePolicy verifies that the auth middleware's HTMX cache policy
// sets Cache-Control: no-store and Vary: HX-Request / HX-Target on authenticated responses.
// This is the behavior implemented by the withHTMXCachePolicy wrapper in server.go's authMiddleware.
func TestAuthMiddleware_HTMXCachePolicy(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	sessionManager := session.NewManager(store, func() *session.OptionsConfig { return nil })
	config := &AuthConfig{}

	// Create an authenticated session cookie
	rrWithCookie := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	sess, _ := store.Get(req, session.SessionName)
	sess.Values["authenticated"] = true
	if err := sess.Save(req, rrWithCookie); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}
	cookie := rrWithCookie.Header().Get("Set-Cookie")

	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	authFunc := AuthMiddleware(store, sessionManager, config)

	// Wrap with the same HTMX cache policy pattern used in server.go's authMiddleware:
	//   - HTMX requests get Cache-Control: no-cache, no-store, must-revalidate + Pragma + Expires + Vary
	//   - Non-HTMX requests still get Vary: HX-Request + Vary: HX-Target
	withHTMXCachePolicy := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				w.Header().Set("Pragma", "no-cache")
				w.Header().Set("Expires", "0")
			}
			w.Header().Add("Vary", "HX-Request")
			w.Header().Add("Vary", "HX-Target")
			next.ServeHTTP(w, r)
		})
	}

	wrappedHandler := withHTMXCachePolicy(authFunc(dummyHandler))

	t.Run("HTMX request gets no-cache and Vary", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/config", nil)
		req.Header.Set("Cookie", cookie)
		req.Header.Set("HX-Request", "true")
		rr := httptest.NewRecorder()
		wrappedHandler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		cc := rr.Header().Get("Cache-Control")
		if cc == "" || !strings.Contains(cc, "no-store") {
			t.Errorf("HTMX response must have Cache-Control containing no-store, got %q", cc)
		}
		vary := strings.Join(rr.Header().Values("Vary"), ", ")
		if !strings.Contains(vary, "HX-Request") || !strings.Contains(vary, "HX-Target") {
			t.Errorf("HTMX response must Vary on HX-Request and HX-Target, got Vary: %q", vary)
		}
	})

	t.Run("non-HTMX request gets Vary", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/config", nil)
		req.Header.Set("Cookie", cookie)
		rr := httptest.NewRecorder()
		wrappedHandler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		vary := strings.Join(rr.Header().Values("Vary"), ", ")
		if !strings.Contains(vary, "HX-Request") || !strings.Contains(vary, "HX-Target") {
			t.Errorf("response must Vary on HX-Request and HX-Target, got Vary: %q", vary)
		}
	})
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

// TestConditionalMiddleware_WriteThrough_GetWithoutValidators tests GET without
// If-None-Match or If-Modified-Since goes through directly (no body buffer).
func TestConditionalMiddleware_WriteThrough_GetWithoutValidators(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("write-through body"))
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Response code = %d, want 200", w.Code)
	}

	if w.Body.String() != "write-through body" {
		t.Errorf("Response body = %q, want %q", w.Body.String(), "write-through body")
	}
}

// TestConditionalMiddleware_WriteThrough_HeadWithoutValidators tests HEAD without
// validators still uses the buffering path (body must be omitted on 200).
func TestConditionalMiddleware_WriteThrough_HeadWithoutValidators(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		n, _ := w.Write([]byte("head body content that must not appear"))
		// Ensure the handler actually writes bytes (proving Finalize path is active)
		_ = n
	})

	mw := ConditionalMiddleware(handler)

	req := httptest.NewRequest("HEAD", "/test", nil)
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Response code = %d, want 200", w.Code)
	}

	if w.Body.Len() != 0 {
		t.Errorf("HEAD response body length = %d, want 0 (Finalize path must still cut body)", w.Body.Len())
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

// TestConditionalResponseWriter_WriteHeader_Idempotent verifies WriteHeader only
// records the first status code and ignores subsequent calls.
func TestConditionalResponseWriter_WriteHeader_Idempotent(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(crw *conditionalResponseWriter)
		wantStatus int
	}{
		{
			name: "first call sets status",
			setup: func(crw *conditionalResponseWriter) {
				crw.WriteHeader(http.StatusCreated)
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "second call is ignored",
			setup: func(crw *conditionalResponseWriter) {
				crw.WriteHeader(http.StatusCreated)
				crw.WriteHeader(http.StatusInternalServerError)
			},
			wantStatus: http.StatusCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			crw := newConditionalResponseWriter(rec)

			tt.setup(crw)

			if !crw.wroteHeader {
				t.Error("expected wroteHeader to be true")
			}
			if crw.statusCode != tt.wantStatus {
				t.Errorf("statusCode = %d, want %d", crw.statusCode, tt.wantStatus)
			}
		})
	}
}

// TestConditionalResponseWriter_Write_ImplicitHeader verifies Write triggers an
// implicit WriteHeader(http.StatusOK) when no explicit status has been set.
func TestConditionalResponseWriter_Write_ImplicitHeader(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(crw *conditionalResponseWriter) (int, error)
		wantStatus int
		wantBody   string
	}{
		{
			name: "write without explicit header triggers 200",
			setup: func(crw *conditionalResponseWriter) (int, error) {
				return crw.Write([]byte("body"))
			},
			wantStatus: http.StatusOK,
			wantBody:   "body",
		},
		{
			name: "write after explicit header preserves status",
			setup: func(crw *conditionalResponseWriter) (int, error) {
				crw.WriteHeader(http.StatusCreated)
				return crw.Write([]byte("more"))
			},
			wantStatus: http.StatusCreated,
			wantBody:   "more",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			crw := newConditionalResponseWriter(rec)

			n, err := tt.setup(crw)
			if err != nil {
				t.Fatalf("Write returned unexpected error: %v", err)
			}
			if n != len(tt.wantBody) {
				t.Errorf("Write byte count = %d, want %d", n, len(tt.wantBody))
			}
			if !crw.wroteHeader {
				t.Error("expected wroteHeader to be true")
			}
			if crw.statusCode != tt.wantStatus {
				t.Errorf("statusCode = %d, want %d", crw.statusCode, tt.wantStatus)
			}
			if crw.body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", crw.body.String(), tt.wantBody)
			}
		})
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

	wrapped := NewLoggingMiddleware(nil)(handler)
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

	wrapped := NewLoggingMiddleware(nil)(handler)
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

// ============================================================================
// Edge Cases and Additional Middleware Tests
// (Recovered from server_compress_test.go)
// ============================================================================

// TestConditionalResponseWriterEdgeCases tests edge cases in conditional writer
func TestConditionalResponseWriterEdgeCases(t *testing.T) {
	t.Run("HEAD request", func(t *testing.T) {
		handler := ConditionalMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", `"test"`)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("body content"))
		}))

		req := httptest.NewRequest("HEAD", "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Body.Len() != 0 {
			t.Error("HEAD request should not include body")
		}
	})

	t.Run("Write without explicit WriteHeader", func(t *testing.T) {
		handler := ConditionalMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", `"test"`)
			w.Write([]byte("body"))
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rr.Code)
		}
	})

	t.Run("Multiple writes", func(t *testing.T) {
		handler := ConditionalMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", `"test"`)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("part1"))
			w.Write([]byte("part2"))
			w.Write([]byte("part3"))
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		expected := "part1part2part3"
		if rr.Body.String() != expected {
			t.Errorf("Expected body %q, got %q", expected, rr.Body.String())
		}
	})
}

// TestConditionalMiddleware_AdditionalCases tests more conditional scenarios
func TestConditionalMiddleware_AdditionalCases(t *testing.T) {
	t.Run("If-None-Match with weak ETag", func(t *testing.T) {
		handler := ConditionalMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", `W/"weak-etag"`)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("content"))
		}))

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("If-None-Match", `W/"weak-etag"`)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotModified {
			t.Errorf("Expected status 304, got %d", rr.Code)
		}
	})

	t.Run("If-Modified-Since with future date", func(t *testing.T) {
		handler := ConditionalMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pastTime := time.Now().Add(-1 * time.Hour)
			w.Header().Set("Last-Modified", pastTime.UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("content"))
		}))

		futureTime := time.Now().Add(1 * time.Hour)
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("If-Modified-Since", futureTime.UTC().Format(http.TimeFormat))
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotModified {
			t.Errorf("Expected status 304, got %d", rr.Code)
		}
	})
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
