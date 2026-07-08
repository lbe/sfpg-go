package server

import (
	"net/http/httptest"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/lbe/sfpg-go/internal/server/session"
)

func TestEnsureCSRFToken_FallsBackToSessionPackage(t *testing.T) {
	s := NewAuthService("secret")
	s.store = sessions.NewCookieStore([]byte("secret"))
	// sessionManager is intentionally left nil so EnsureCSRFToken falls back
	// to the session package helper.

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	token := s.EnsureCSRFToken(w, r)
	if token == "" {
		t.Error("expected non-empty CSRF token")
	}

	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie to be set")
	}
	if cookies[0].Value == "" {
		t.Error("expected non-empty session cookie value")
	}
}

func TestCSRFTokenForPage_AuthenticatedUsesSessionManager(t *testing.T) {
	app := CreateApp(t)
	defer app.Shutdown()

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	token := app.CSRFTokenForPage(w, r, true)
	if token == "" {
		t.Fatal("expected non-empty CSRF token")
	}

	// Retrieve the persisted token from the session manager.
	sess, err := app.sessionManager.GetSession(w, r)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if got := sess.Values["csrf_token"]; got != token {
		t.Errorf("persisted token = %v, want %v", got, token)
	}
}

func TestCSRFTokenForPage_AuthenticatedNoSessionManager(t *testing.T) {
	s := NewAuthService("secret")
	s.EnsureSession(func() *session.OptionsConfig { return nil })
	s.sessionManager = nil

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	token := s.CSRFTokenForPage(w, r, true)
	if token == "" {
		t.Fatal("expected non-empty CSRF token")
	}

	// The fallback path via EnsureCSRFToken saves the token to the store.
	sess, err := s.store.Get(r, session.SessionName)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if got := sess.Values["csrf_token"]; got != token {
		t.Errorf("persisted token = %v, want %v", got, token)
	}
}

func TestCSRFTokenForPage_UnauthenticatedInvalidCookie(t *testing.T) {
	// Build a valid session cookie with store A.
	storeA := sessions.NewCookieStore([]byte("store-a-secret"))
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest("GET", "/", nil)
	sessA, err := storeA.Get(r1, session.SessionName)
	if err != nil {
		t.Fatalf("failed to get session from store A: %v", err)
	}
	sessA.Values["csrf_token"] = "ignored-token"
	if err := sessA.Save(r1, w1); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}
	cookies := w1.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected cookie from store A")
	}

	// Construct AuthService with a different store so the cookie cannot be decoded.
	s := NewAuthService("secret")
	s.store = sessions.NewCookieStore([]byte("store-b-secret"))

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(cookies[0])

	token := s.CSRFTokenForPage(w2, r2, false)
	if token == "" {
		t.Error("expected non-empty fallback CSRF token")
	}
}

func TestCSRFTokenForPage_UnauthenticatedExistingToken(t *testing.T) {
	store := sessions.NewCookieStore([]byte("secret"))
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest("GET", "/", nil)
	sess, err := store.Get(r1, session.SessionName)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	sess.Values["csrf_token"] = "existing-token"
	if err := sess.Save(r1, w1); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}
	cookies := w1.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}

	s := NewAuthService("secret")
	s.store = store

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(cookies[0])

	token := s.CSRFTokenForPage(w2, r2, false)
	if token != "existing-token" {
		t.Errorf("token = %q, want %q", token, "existing-token")
	}
}

func TestCSRFTokenForPage_UnauthenticatedNoToken(t *testing.T) {
	store := sessions.NewCookieStore([]byte("secret"))
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest("GET", "/", nil)
	sess, err := store.Get(r1, session.SessionName)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	// Save a session without a csrf_token value.
	if err := sess.Save(r1, w1); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}
	cookies := w1.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}

	s := NewAuthService("secret")
	s.store = store

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(cookies[0])

	token := s.CSRFTokenForPage(w2, r2, false)
	if token == "" {
		t.Error("expected non-empty generated CSRF token")
	}
}

func TestGetEffectiveTheme(t *testing.T) {
	tests := []struct {
		name         string
		cookie       string
		themes       []string
		defaultTheme string
		want         string
	}{
		{
			name:         "no cookie",
			themes:       []string{"dark", "light"},
			defaultTheme: "dark",
			want:         "dark",
		},
		{
			name:         "valid cookie",
			cookie:       "theme=light",
			themes:       []string{"dark", "light"},
			defaultTheme: "dark",
			want:         "light",
		},
		{
			name:         "invalid cookie",
			cookie:       "theme=pink",
			themes:       []string{"dark", "light"},
			defaultTheme: "dark",
			want:         "dark",
		},
		{
			name:         "empty themes list",
			cookie:       "theme=light",
			themes:       []string{},
			defaultTheme: "dark",
			want:         "dark",
		},
	}

	s := NewAuthService("secret")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if tt.cookie != "" {
				r.Header.Set("Cookie", tt.cookie)
			}
			got := s.GetEffectiveTheme(r, func() []string { return tt.themes }, tt.defaultTheme)
			if got != tt.want {
				t.Errorf("GetEffectiveTheme() = %q, want %q", got, tt.want)
			}
		})
	}
}
