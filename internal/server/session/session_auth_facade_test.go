package session_test

import (
	"net/http/httptest"
	"testing"

	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/server"
	"github.com/lbe/sfpg-go/internal/server/session"
)

func TestEnsureCSRFToken_UsesSessionManager(t *testing.T) {
	s := server.NewSessionAuthFacade("secret-with-at-least-32-bytes-long")
	s.EnsureSession(func() *session.OptionsConfig { return nil })

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
	s := server.NewSessionAuthFacade("secret-with-at-least-32-bytes-long")
	s.EnsureSession(func() *session.OptionsConfig { return nil })

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	token := s.CSRFTokenForPage(w, r, true)
	if token == "" {
		t.Fatal("expected non-empty CSRF token")
	}

	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie after CSRFTokenForPage")
	}

	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(cookies[0])
	w2 := httptest.NewRecorder()
	token2 := s.CSRFTokenForPage(w2, r2, true)
	if token2 != token {
		t.Errorf("csrf token not persisted across requests: first=%q second=%q", token, token2)
	}
}

func TestCSRFTokenForPage_UnauthenticatedInvalidCookie(t *testing.T) {
	// Build a valid session cookie with store A (different secret).
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

	// Create facade with a different secret so the cookie cannot be decoded.
	s := server.NewSessionAuthFacade("secret-with-at-least-32-bytes-long")
	s.EnsureSession(func() *session.OptionsConfig { return nil })

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

	// Create facade with the matching secret.
	s := server.NewSessionAuthFacade("secret")
	s.EnsureSession(func() *session.OptionsConfig { return nil })

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

	s := server.NewSessionAuthFacade("secret")
	s.EnsureSession(func() *session.OptionsConfig { return nil })

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

	s := server.NewSessionAuthFacade("secret-with-at-least-32-bytes-long")
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
