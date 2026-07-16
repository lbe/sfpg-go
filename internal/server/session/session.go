// Package session provides session store, CSRF token handling, and session
// cookie options for the web application. It is used by the server package
// for authentication and form protection.
package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/sessions"
)

// SessionName is the canonical cookie name used across the application.
const SessionName = "session-name"

// isIPAddress checks if the given host string is an IP address (IPv4 or IPv6).
// Returns true for IP addresses, false for domain names.
// This is used to comply with RFC 6265, which specifies that the Domain attribute
// should not be set for IP addresses, only for domain names.
func isIPAddress(host string) bool {
	return net.ParseIP(host) != nil
}

// User represents a user's authentication details, including username and hashed password.
type User struct {
	Username string
	Password string
}

// OptionsConfig holds session cookie configuration. When nil is passed to
// GetSessionOptions, defaults are used with env overrides (SEPG_SESSION_HTTPONLY,
// SEPG_SESSION_SECURE).
type OptionsConfig struct {
	SessionMaxAge   int
	SessionHttpOnly bool
	SessionSecure   bool
	SessionSameSite string
}

// sameSiteStringToInt converts SessionSameSite string ("Lax", "Strict", "None")
// to the corresponding http.SameSite integer constant.
func sameSiteStringToInt(s string) http.SameSite {
	switch s {
	case "Lax":
		return http.SameSiteLaxMode
	case "Strict":
		return http.SameSiteStrictMode
	case "None":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

// GetSessionOptions returns session cookie options from cfg. If cfg is nil,
// defaults are used.
func GetSessionOptions(cfg *OptionsConfig) *sessions.Options {
	if cfg == nil {
		cfg = &OptionsConfig{
			SessionMaxAge:   7 * 24 * 3600,
			SessionHttpOnly: true,
			SessionSecure:   true,
			SessionSameSite: "Lax",
		}
	}
	sameSite := sameSiteStringToInt(cfg.SessionSameSite)
	slog.Info("Session cookie options configured",
		"maxAge", cfg.SessionMaxAge,
		"httpOnly", cfg.SessionHttpOnly,
		"secure", cfg.SessionSecure,
		"sameSite", cfg.SessionSameSite)
	return &sessions.Options{
		Path:     "/",
		MaxAge:   cfg.SessionMaxAge,
		HttpOnly: cfg.SessionHttpOnly,
		Secure:   cfg.SessionSecure,
		SameSite: sameSite,
	}
}

// ClearSessionCookie removes the session cookie using the store options so
// path/domain/flags match and browsers drop it. Per RFC 6265, Domain is only
// set for domain names, not for IP addresses.
func ClearSessionCookie(store *sessions.CookieStore, w http.ResponseWriter, r *http.Request) {
	opts := store.Options
	c := &http.Cookie{
		Name:     SessionName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		Secure:   opts != nil && opts.Secure,
		HttpOnly: opts != nil && opts.HttpOnly,
	}
	host := r.Host
	if host != "" {
		if i := strings.IndexByte(host, ':'); i != -1 {
			host = host[:i]
		}
		// Only set Domain for non-IP addresses. RFC 6265 specifies that Domain
		// should not be used with IP addresses, and different platforms (Linux vs macOS)
		// handle IP-based domains inconsistently.
		if !isIPAddress(host) {
			c.Domain = host
		}
	}
	http.SetCookie(w, c)
}

// EnsureCsrfToken ensures a CSRF token exists in the session and returns it.
// If none is present, it generates a new one. If the session cookie is invalid
// (e.g., after secret rotation), the cookie is cleared and a new session is used.
func EnsureCsrfToken(store *sessions.CookieStore, w http.ResponseWriter, r *http.Request) string {
	sess, err := store.Get(r, SessionName)
	if err != nil {
		ClearSessionCookie(store, w, r)
		// gorilla/sessions returns a usable session even on cookie decode error.
		sess, _ = store.Get(r, SessionName) //nolint:errcheck
	}
	if token, ok := sess.Values["csrf_token"].(string); ok && token != "" {
		return token
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		slog.Error("failed to generate random bytes for CSRF token", "err", err)
		return ""
	}
	token := hex.EncodeToString(bytes)
	sess.Values["csrf_token"] = token
	if err := sess.Save(r, w); err != nil {
		slog.Error("failed to save session with new CSRF token", "err", err)
	}
	return token
}

// IsAuthenticated reports whether the request has a valid authenticated session.
// If the session cookie is invalid or malformed, it clears the cookie and
// returns false.
func IsAuthenticated(store *sessions.CookieStore, w http.ResponseWriter, r *http.Request) bool {
	sess, err := store.Get(r, SessionName)
	if err != nil {
		ClearSessionCookie(store, w, r)
		return false
	}
	authenticated, _ := sess.Values["authenticated"].(bool)
	return authenticated
}

// randRead is a testable hook for crypto/rand.Read.
var randRead = rand.Read

// GenerateCSRFToken generates a cryptographically random CSRF token.
// It does NOT save the token to any session; callers must persist it
// themselves if needed. Use this for ephemeral tokens on public pages
// where emitting a Set-Cookie is undesirable.
func GenerateCSRFToken() string {
	b := make([]byte, 32)
	if _, err := randRead(b); err != nil {
		slog.Error("failed to generate CSRF token", "err", err)
		return ""
	}
	return hex.EncodeToString(b)
}

// SessionManager provides an interface for session management operations.
// It encapsulates session store access, CSRF token handling, and session options.
type SessionManager interface {
	GetOptions() *sessions.Options
	EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string
	ValidateCSRFToken(r *http.Request) bool
	ClearSession(w http.ResponseWriter, r *http.Request)

	// GetSession retrieves the session from the request.
	// If the session cookie is invalid, it clears the cookie and returns a new session.
	GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error)

	// SaveSession saves the session to the response.
	SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error

	// IsAuthenticated returns true if the user is authenticated.
	// If the session cookie is invalid or malformed, it clears the cookie and
	// returns false.
	IsAuthenticated(w http.ResponseWriter, r *http.Request) bool

	// SetAuthenticated sets the authenticated status for the session.
	SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error
}

// Manager implements SessionManager by wrapping a session store and providing
// access to session configuration. The configGetter function is called to retrieve
// the current OptionsConfig whenever GetOptions() is invoked.
type Manager struct {
	store        *sessions.CookieStore
	configGetter func() *OptionsConfig
}

// NewManager creates a new SessionManager implementation. The configGetter function
// is called each time GetOptions() is invoked to get the current session configuration.
// This allows the manager to respond to configuration changes without requiring
// explicit updates.
func NewManager(store *sessions.CookieStore, configGetter func() *OptionsConfig) *Manager {
	return &Manager{
		store:        store,
		configGetter: configGetter,
	}
}

// GetOptions returns the current session cookie options by calling GetSessionOptions
// with the configuration provided by the configGetter function.
func (m *Manager) GetOptions() *sessions.Options {
	cfg := m.configGetter()
	return GetSessionOptions(cfg)
}

// EnsureCSRFToken ensures a CSRF token exists in the session and returns it.
// If none is present, it generates a new one. If the session cookie is invalid
// (e.g., after secret rotation), the cookie is cleared and a new session is used.
func (m *Manager) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	sess, err := m.store.Get(r, SessionName)
	if err != nil {
		slog.Warn("EnsureCSRFToken: clearing invalid session cookie",
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"err", err)
		ClearSessionCookie(m.store, w, r)
		// gorilla/sessions returns a usable session even on cookie decode error.
		sess, err = m.store.Get(r, SessionName)
		if err != nil {
			slog.Warn("EnsureCSRFToken: failed to get session after clearing cookie",
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"err", err)
		}
	}
	if token, ok := sess.Values["csrf_token"].(string); ok && token != "" {
		return token
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		slog.Error("failed to generate random bytes for CSRF token", "err", err)
		return ""
	}
	token := hex.EncodeToString(bytes)
	sess.Values["csrf_token"] = token
	if err := sess.Save(r, w); err != nil {
		slog.Error("failed to save session with new CSRF token", "err", err)
	}
	return token
}

// ValidateCSRFToken validates the CSRF token in the request form against the session.
// Returns false if the session has no token or the form token is missing or doesn't match.
func (m *Manager) ValidateCSRFToken(r *http.Request) bool {
	// gorilla/sessions returns a usable session even on cookie decode error.
	sess, err := m.store.Get(r, SessionName)
	if err != nil {
		slog.Warn("ValidateCSRFToken: session decode error",
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"err", err)
	}
	sessionToken, ok := sess.Values["csrf_token"].(string)
	if !ok || sessionToken == "" {
		slog.Warn("validateCsrfToken: no token in session")
		return false
	}
	formToken := r.FormValue("csrf_token")
	if formToken == "" {
		slog.Warn("validateCsrfToken: no token in form")
		return false
	}
	return subtle.ConstantTimeCompare([]byte(sessionToken), []byte(formToken)) == 1
}

// ClearSession removes the session cookie by setting a max-age=-1 cookie
// with matching name, path, secure, and http-only flags.
func (m *Manager) ClearSession(w http.ResponseWriter, r *http.Request) {
	opts := m.store.Options
	c := &http.Cookie{
		Name:     SessionName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		Secure:   opts != nil && opts.Secure,
		HttpOnly: opts != nil && opts.HttpOnly,
	}
	host := r.Host
	if host != "" {
		if i := strings.IndexByte(host, ':'); i != -1 {
			host = host[:i]
		}
		if !isIPAddress(host) {
			c.Domain = host
		}
	}
	http.SetCookie(w, c)
}

// GetSession retrieves the session from the request.
// If the session cookie is invalid, it clears the cookie from the browser
// and returns a fresh session.
func (m *Manager) GetSession(w http.ResponseWriter, r *http.Request) (*sessions.Session, error) {
	sess, err := m.store.Get(r, SessionName)
	if err != nil {
		// Invalid cookie - clear it from the browser and create a fresh session
		ClearSessionCookie(m.store, w, r)
		// gorilla/sessions returns a usable session even on cookie decode error.
		sess, err = m.store.New(r, SessionName)
		if err != nil {
			slog.Warn("GetSession: failed to create new session after clearing invalid cookie",
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"err", err)
		}
	}
	return sess, nil
}

// SaveSession saves the session to the response.
func (m *Manager) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return sess.Save(r, w)
}

// IsAuthenticated returns true if the user is authenticated.
// If the session cookie is invalid or malformed, it clears the cookie from
// the browser and returns false. This aligns with the auth middleware and
// the package-level IsAuthenticated function.
func (m *Manager) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	sess, err := m.store.Get(r, SessionName)
	if err != nil {
		slog.Warn("IsAuthenticated: clearing invalid session cookie",
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"err", err)
		ClearSessionCookie(m.store, w, r)
		return false
	}
	authenticated, ok := sess.Values["authenticated"].(bool)
	if !ok {
		slog.Debug("IsAuthenticated: no authenticated value in session",
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"is_new", sess.IsNew)
	}
	return ok && authenticated
}

// SetAuthenticated sets the authenticated status for the session.
// On a false -> true transition (login) the session ID is rotated to mitigate
// session fixation: a new session is created and user values are copied over.
func (m *Manager) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	sess, err := m.store.Get(r, SessionName)
	alreadyNew := false
	if err != nil {
		slog.Warn("SetAuthenticated: session decode error, creating new session",
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"authenticated", authenticated,
			"err", err)
		// Invalid cookie - create a new session
		// gorilla/sessions returns a usable session even on cookie decode error.
		sess, err = m.store.New(r, SessionName)
		if err != nil {
			slog.Warn("SetAuthenticated: failed to create new session after clearing invalid cookie",
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"authenticated", authenticated,
				"err", err)
		}
		alreadyNew = true
	}

	// Detect privilege escalation (login) and rotate the session ID.
	// Skip rotation when we already created a new session above due to a decode
	// error; in that case the old session ID has already been discarded.
	wasAuthenticated, _ := sess.Values["authenticated"].(bool)
	sess.Values["authenticated"] = authenticated
	if authenticated && !wasAuthenticated && !alreadyNew {
		newSess, err := m.store.New(r, SessionName)
		if err != nil {
			return fmt.Errorf("rotate session on login: %w", err)
		}
		// Copy user values (including csrf_token) to the new session.
		for k, v := range sess.Values {
			newSess.Values[k] = v
		}
		sess = newSess
	}

	// When logging out, clear the session cookie by setting MaxAge to -1
	if !authenticated {
		sess.Options.MaxAge = -1
	}
	return sess.Save(r, w)
}
