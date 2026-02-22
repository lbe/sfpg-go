package session

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/sessions"
)

// TestSessionManagerExpandedInterface verifies that Manager implements the expanded SessionManager interface.
// This test documents Phase 2b of the handler dependency refactor.
func TestSessionManagerExpandedInterface(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	mgr := NewManager(store, func() *OptionsConfig {
		return &OptionsConfig{
			SessionMaxAge:   3600,
			SessionHttpOnly: true,
			SessionSecure:   false,
			SessionSameSite: "Lax",
		}
	})

	// Verify Manager implements SessionManager
	var _ SessionManager = mgr

	// Test GetSession
	t.Run("GetSession", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		sess, err := mgr.GetSession(req)
		if err != nil {
			t.Errorf("GetSession() error = %v", err)
		}
		if sess == nil {
			t.Error("GetSession() returned nil session")
		}
		if sess.Name() != SessionName {
			t.Errorf("GetSession() session name = %q, want %q", sess.Name(), SessionName)
		}
	})

	// Test SaveSession
	t.Run("SaveSession", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		sess, _ := mgr.GetSession(req)
		sess.Values["test_key"] = "test_value"

		err := mgr.SaveSession(rec, req, sess)
		if err != nil {
			t.Errorf("SaveSession() error = %v", err)
		}

		// Verify cookie was set
		cookies := rec.Result().Cookies()
		if len(cookies) == 0 {
			t.Error("SaveSession() did not set any cookies")
		}
	})

	// Test IsAuthenticated (not authenticated)
	t.Run("IsAuthenticated_false", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		authenticated := mgr.IsAuthenticated(req)
		if authenticated {
			t.Error("IsAuthenticated() = true for unauthenticated request, want false")
		}
	})

	// Test SetAuthenticated and IsAuthenticated
	t.Run("SetAuthenticated_and_IsAuthenticated", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()

		// Set authenticated
		err := mgr.SetAuthenticated(rec, req, true)
		if err != nil {
			t.Errorf("SetAuthenticated() error = %v", err)
		}

		// Get the cookie from response
		cookies := rec.Result().Cookies()
		if len(cookies) == 0 {
			t.Fatal("SetAuthenticated() did not set any cookies")
		}

		// Create new request with the session cookie
		req2 := httptest.NewRequest("GET", "/", nil)
		for _, c := range cookies {
			req2.AddCookie(c)
		}

		// Verify authenticated
		authenticated := mgr.IsAuthenticated(req2)
		if !authenticated {
			t.Error("IsAuthenticated() = false after SetAuthenticated(true), want true")
		}
	})

	// Test SetAuthenticated false (logout)
	t.Run("SetAuthenticated_false", func(t *testing.T) {
		// First authenticate
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		mgr.SetAuthenticated(rec, req, true)
		cookies := rec.Result().Cookies()

		// Create request with cookie
		req2 := httptest.NewRequest("GET", "/", nil)
		for _, c := range cookies {
			req2.AddCookie(c)
		}

		// Now de-authenticate
		rec2 := httptest.NewRecorder()
		err := mgr.SetAuthenticated(rec2, req2, false)
		if err != nil {
			t.Errorf("SetAuthenticated(false) error = %v", err)
		}

		// Get new cookies
		cookies2 := rec2.Result().Cookies()
		req3 := httptest.NewRequest("GET", "/", nil)
		for _, c := range cookies2 {
			req3.AddCookie(c)
		}

		// Verify not authenticated
		authenticated := mgr.IsAuthenticated(req3)
		if authenticated {
			t.Error("IsAuthenticated() = true after SetAuthenticated(false), want false")
		}
	})
}

func TestGetSessionOptions_DefaultsAndSameSite(t *testing.T) {
	options := GetSessionOptions(nil)
	if options.Path != "/" {
		t.Errorf("Path = %q, want /", options.Path)
	}
	if options.MaxAge != 7*24*3600 {
		t.Errorf("MaxAge = %d, want %d", options.MaxAge, 7*24*3600)
	}
	if !options.HttpOnly {
		t.Error("HttpOnly = false, want true")
	}
	if !options.Secure {
		t.Error("Secure = false, want true")
	}
	if options.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", options.SameSite)
	}

	strict := GetSessionOptions(&OptionsConfig{SessionSameSite: "Strict"})
	if strict.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", strict.SameSite)
	}

	none := GetSessionOptions(&OptionsConfig{SessionSameSite: "None"})
	if none.SameSite != http.SameSiteNoneMode {
		t.Errorf("SameSite = %v, want None", none.SameSite)
	}

	unknown := GetSessionOptions(&OptionsConfig{SessionSameSite: "Bogus"})
	if unknown.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax for unknown value", unknown.SameSite)
	}
}

func TestClearSessionCookie_DomainHandling(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	store.Options = &sessions.Options{Secure: true, HttpOnly: true}

	t.Run("IPHost", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/", nil)
		req.Host = "127.0.0.1:8080"
		w := httptest.NewRecorder()

		ClearSessionCookie(store, w, req)

		cookies := w.Result().Cookies()
		if len(cookies) == 0 {
			t.Fatal("expected cookie to be set")
		}
		if cookies[0].Domain != "" {
			t.Errorf("Domain = %q, want empty for IP", cookies[0].Domain)
		}
	})

	t.Run("DomainHost", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com:8080/", nil)
		req.Host = "example.com:8080"
		w := httptest.NewRecorder()

		ClearSessionCookie(store, w, req)

		cookies := w.Result().Cookies()
		if len(cookies) == 0 {
			t.Fatal("expected cookie to be set")
		}
		if cookies[0].Domain != "example.com" {
			t.Errorf("Domain = %q, want example.com", cookies[0].Domain)
		}
	})

	t.Run("EmptyHostNoOptions", func(t *testing.T) {
		storeNoOpts := sessions.NewCookieStore([]byte("test-secret"))
		storeNoOpts.Options = nil
		req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
		req.Host = ""
		w := httptest.NewRecorder()

		ClearSessionCookie(storeNoOpts, w, req)

		cookies := w.Result().Cookies()
		if len(cookies) == 0 {
			t.Fatal("expected cookie to be set")
		}
		if cookies[0].Domain != "" {
			t.Errorf("Domain = %q, want empty", cookies[0].Domain)
		}
		if cookies[0].Secure || cookies[0].HttpOnly {
			t.Errorf("expected Secure/HttpOnly to be false when options are nil")
		}
	})
}

func TestEnsureCsrfToken_InvalidCookie(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	store.Options = &sessions.Options{Path: "/", MaxAge: 3600}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.Host = "example.com"
	req.AddCookie(&http.Cookie{Name: SessionName, Value: "bad"})
	rec := httptest.NewRecorder()

	token := EnsureCsrfToken(store, rec, req)
	if token == "" {
		t.Fatal("expected non-empty CSRF token")
	}

	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionName && c.MaxAge == -1 {
			cleared = true
			break
		}
	}
	if !cleared {
		t.Error("expected session cookie to be cleared")
	}
}

func TestValidateCsrfToken_MissingValues(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))

	// Missing session token
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("csrf_token=token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if ValidateCsrfToken(store, req) {
		t.Error("expected validation to fail when session token is missing")
	}

	// Missing form token
	reqWithSession := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	sess, _ := store.Get(reqWithSession, SessionName)
	sess.Values["csrf_token"] = "token"
	if err := sess.Save(reqWithSession, rec); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}
	reqMissingForm := httptest.NewRequest(http.MethodPost, "/", nil)
	for _, c := range rec.Result().Cookies() {
		reqMissingForm.AddCookie(c)
	}
	if ValidateCsrfToken(store, reqMissingForm) {
		t.Error("expected validation to fail when form token is missing")
	}
}

func TestValidateCsrfToken_Success(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	sess, _ := store.Get(req, SessionName)
	sess.Values["csrf_token"] = "token"
	if err := sess.Save(req, rec); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	post := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("csrf_token=token"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range rec.Result().Cookies() {
		post.AddCookie(c)
	}

	if !ValidateCsrfToken(store, post) {
		t.Error("expected validation to succeed when tokens match")
	}
}

func TestEnsureCsrfToken_ExistingToken(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	sess, _ := store.Get(req, SessionName)
	sess.Values["csrf_token"] = "existing-token"
	if err := sess.Save(req, rec); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	reqWithCookie := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		reqWithCookie.AddCookie(c)
	}
	newRec := httptest.NewRecorder()

	token := EnsureCsrfToken(store, newRec, reqWithCookie)
	if token != "existing-token" {
		t.Errorf("token = %q, want %q", token, "existing-token")
	}
}

func TestManagerGetSession_InvalidCookie(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	mgr := NewManager(store, func() *OptionsConfig { return &OptionsConfig{} })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionName, Value: "bad"})

	sess, err := mgr.GetSession(req)
	if err != nil {
		t.Fatalf("GetSession error = %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if !sess.IsNew {
		t.Error("expected new session when cookie is invalid")
	}
}

func TestValidateCsrfToken_Mismatch(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	sess, _ := store.Get(req, SessionName)
	sess.Values["csrf_token"] = "token"
	if err := sess.Save(req, rec); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	post := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("csrf_token=other"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range rec.Result().Cookies() {
		post.AddCookie(c)
	}

	if ValidateCsrfToken(store, post) {
		t.Error("expected validation to fail when tokens do not match")
	}
}

func TestManagerSetAuthenticated_InvalidCookie(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	mgr := NewManager(store, func() *OptionsConfig { return &OptionsConfig{} })

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionName, Value: "bad"})
	rec := httptest.NewRecorder()

	if err := mgr.SetAuthenticated(rec, req, true); err != nil {
		t.Fatalf("SetAuthenticated error = %v", err)
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Error("expected SetAuthenticated to set a session cookie")
	}
}

func TestGetSessionOptions_CustomOverrides(t *testing.T) {
	options := GetSessionOptions(&OptionsConfig{
		SessionMaxAge:   123,
		SessionHttpOnly: false,
		SessionSecure:   false,
		SessionSameSite: "Lax",
	})

	if options.MaxAge != 123 {
		t.Errorf("MaxAge = %d, want %d", options.MaxAge, 123)
	}
	if options.HttpOnly {
		t.Error("HttpOnly = true, want false")
	}
	if options.Secure {
		t.Error("Secure = true, want false")
	}
}

func TestManagerClearSession(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	mgr := NewManager(store, func() *OptionsConfig { return &OptionsConfig{} })

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()

	mgr.ClearSession(rec, req)

	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionName && c.MaxAge == -1 {
			cleared = true
			break
		}
	}
	if !cleared {
		t.Error("expected ClearSession to clear session cookie")
	}
}

func TestManagerGetOptions_NilConfig(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	mgr := NewManager(store, func() *OptionsConfig { return nil })

	options := mgr.GetOptions()
	if options.MaxAge != 7*24*3600 {
		t.Errorf("MaxAge = %d, want %d", options.MaxAge, 7*24*3600)
	}
	if !options.HttpOnly {
		t.Error("HttpOnly = false, want true")
	}
	if !options.Secure {
		t.Error("Secure = false, want true")
	}
}

func TestManagerSetAuthenticated_LogoutClearsCookie(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	mgr := NewManager(store, func() *OptionsConfig { return &OptionsConfig{} })

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()

	if err := mgr.SetAuthenticated(rec, req, false); err != nil {
		t.Fatalf("SetAuthenticated error = %v", err)
	}
	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionName && c.MaxAge == -1 {
			cleared = true
			break
		}
	}
	if !cleared {
		t.Error("expected logout to set MaxAge=-1")
	}
}
