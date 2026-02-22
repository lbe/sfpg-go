// Package session provides session store, CSRF token handling, and session
// cookie options for the web application. It is used by the server package
// for authentication and form protection.
package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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
		sess, _ = store.Get(r, SessionName)
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

// ValidateCsrfToken checks the CSRF token in the request form against the session.
// Returns false if the session has no token or the form token is missing or doesn't match.
func ValidateCsrfToken(store *sessions.CookieStore, r *http.Request) bool {
	sess, _ := store.Get(r, SessionName)
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

// SessionManager provides an interface for session management operations.
// It encapsulates session store access, CSRF token handling, and session options.
type SessionManager interface {
	GetOptions() *sessions.Options
	EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string
	ValidateCSRFToken(r *http.Request) bool
	ClearSession(w http.ResponseWriter, r *http.Request)

	// GetSession retrieves the session from the request.
	// Returns a new session if one doesn't exist.
	GetSession(r *http.Request) (*sessions.Session, error)

	// SaveSession saves the session to the response.
	SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error

	// IsAuthenticated returns true if the user is authenticated.
	IsAuthenticated(r *http.Request) bool

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
// Delegates to the package-level EnsureCsrfToken function.
func (m *Manager) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	return EnsureCsrfToken(m.store, w, r)
}

// ValidateCSRFToken validates the CSRF token in the request form against the session.
// Delegates to the package-level ValidateCsrfToken function.
func (m *Manager) ValidateCSRFToken(r *http.Request) bool {
	return ValidateCsrfToken(m.store, r)
}

// ClearSession removes the session cookie. Delegates to the package-level ClearSessionCookie function.
func (m *Manager) ClearSession(w http.ResponseWriter, r *http.Request) {
	ClearSessionCookie(m.store, w, r)
}

// GetSession retrieves the session from the request.
// If the session cookie is invalid, it clears the cookie and returns a new session.
func (m *Manager) GetSession(r *http.Request) (*sessions.Session, error) {
	sess, err := m.store.Get(r, SessionName)
	if err != nil {
		// Invalid cookie - clear it and get a fresh session
		sess, _ = m.store.New(r, SessionName)
	}
	return sess, nil
}

// SaveSession saves the session to the response.
func (m *Manager) SaveSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) error {
	return sess.Save(r, w)
}

// IsAuthenticated returns true if the user is authenticated.
func (m *Manager) IsAuthenticated(r *http.Request) bool {
	sess, err := m.store.Get(r, SessionName)
	if err != nil {
		return false
	}
	authenticated, ok := sess.Values["authenticated"].(bool)
	return ok && authenticated
}

// SetAuthenticated sets the authenticated status for the session.
func (m *Manager) SetAuthenticated(w http.ResponseWriter, r *http.Request, authenticated bool) error {
	sess, err := m.store.Get(r, SessionName)
	if err != nil {
		// Invalid cookie - create a new session
		sess, _ = m.store.New(r, SessionName)
	}
	sess.Values["authenticated"] = authenticated
	// When logging out, clear the session cookie by setting MaxAge to -1
	if !authenticated {
		sess.Options.MaxAge = -1
	}
	return sess.Save(r, w)
}
