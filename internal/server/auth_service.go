package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/security"
	"github.com/lbe/sfpg-go/internal/server/session"
)

// SessionAuthFacade owns session management, authentication, and CSRF.
type SessionAuthFacade struct {
	sessionSecret  string
	store          *sessions.CookieStore
	sessionManager *session.Manager
	authService    auth.AuthService
}

// NewSessionAuthFacade constructs a facade for session, auth, and CSRF operations.
func NewSessionAuthFacade(sessionSecret string) *SessionAuthFacade {
	return &SessionAuthFacade{sessionSecret: sessionSecret}
}

// ─── Session lifecycle ──────────────────────────────────────────────

// EnsureSession initializes the cookie store and session manager if needed.
func (s *SessionAuthFacade) EnsureSession(getOptionsConfig func() *session.OptionsConfig) {
	if s.store == nil {
		s.store = sessions.NewCookieStore([]byte(s.sessionSecret))
		opts := session.GetSessionOptions(getOptionsConfig())
		s.store.Options = opts
		s.store.MaxAge(opts.MaxAge)
	}
	if s.sessionManager == nil && s.store != nil {
		s.sessionManager = session.NewManager(s.store, getOptionsConfig)
	}
}

// IsAuthenticated reports whether the request has a valid authenticated session.
func (s *SessionAuthFacade) IsAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	return session.IsAuthenticated(s.store, w, r)
}

// ─── CSRF ───────────────────────────────────────────────────────────

// EnsureCSRFToken returns a CSRF token for the current session, creating one if needed.
func (s *SessionAuthFacade) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	if s.sessionManager != nil {
		return s.sessionManager.EnsureCSRFToken(w, r)
	}
	return session.EnsureCsrfToken(s.store, w, r)
}

// CSRFTokenForPage returns the CSRF token to embed in rendered pages.
func (s *SessionAuthFacade) CSRFTokenForPage(w http.ResponseWriter, r *http.Request, authenticated bool) string {
	if authenticated {
		return s.EnsureCSRFToken(w, r)
	}
	sess, err := s.store.Get(r, session.SessionName)
	if err != nil {
		slog.Debug("failed to get session for CSRF token", "err", err)
		return session.GenerateCSRFToken()
	}
	if token, ok := sess.Values["csrf_token"].(string); ok && token != "" {
		return token
	}
	return session.GenerateCSRFToken()
}

// ─── Credential DB operations ───────────────────────────────────────

// GetUser loads the stored admin credentials for the given username.
func (s *SessionAuthFacade) GetUser(ctx context.Context, username string, roPool, rwPool *dbconnpool.DbSQLConnPool) (*session.User, error) {
	user := &session.User{}
	cpcRo, err := roPool.Get()
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}
	defer roPool.Put(cpcRo)

	storedUsername, err := cpcRo.Queries.GetConfigValueByKey(ctx, "user")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("failed to get username: %w", err)
	}
	storedPasswordHash, err := cpcRo.Queries.GetConfigValueByKey(ctx, "password")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("failed to get password hash: %w", err)
	}
	if storedUsername != username {
		return nil, sql.ErrNoRows
	}
	user.Username = storedUsername
	user.Password = storedPasswordHash
	return user, nil
}

// CheckAccountLockout reports whether the account is currently locked.
func (s *SessionAuthFacade) CheckAccountLockout(ctx context.Context, username string, pool *dbconnpool.DbSQLConnPool) (bool, error) {
	cpcRw, err := pool.Get()
	if err != nil {
		return false, err
	}
	defer pool.Put(cpcRw)

	attempt, err := cpcRw.Queries.GetLoginAttempt(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !attempt.LockedUntil.Valid {
		return false, nil
	}
	now := time.Now().Unix()
	if security.ShouldClearLockout(attempt.LockedUntil, now) {
		if clearErr := cpcRw.Queries.ClearLoginAttempts(ctx, username); clearErr != nil {
			slog.Warn("failed to clear login attempts during lockout check", "username", username, "err", clearErr)
		}
		return false, nil
	}
	return security.IsLocked(attempt.LockedUntil, now), nil
}

// RecordFailedLoginAttempt increments failed attempts and locks the account when the threshold is reached.
func (s *SessionAuthFacade) RecordFailedLoginAttempt(ctx context.Context, username string,
	pool *dbconnpool.DbSQLConnPool, lockoutDuration int64, lockoutThreshold int64,
	sched *scheduler.Scheduler,
	unlockFn func(ctx context.Context, username string) error,
) error {
	cpcRw, err := pool.Get()
	if err != nil {
		return err
	}
	defer pool.Put(cpcRw)

	now := time.Now().Unix()
	var failedAttempts int64 = 1
	attempt, err := cpcRw.Queries.GetLoginAttempt(ctx, username)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		failedAttempts = security.IncrementFailedAttempts(attempt.FailedAttempts)
	}
	lockedUntil := security.CalculateLockout(failedAttempts, now, lockoutDuration, lockoutThreshold)

	err = cpcRw.Queries.UpsertLoginAttempt(ctx, gallerydb.UpsertLoginAttemptParams{
		Username: username, FailedAttempts: failedAttempts,
		LastAttemptAt: now, LockedUntil: lockedUntil,
	})
	if err != nil {
		return err
	}

	if lockedUntil.Valid && sched != nil {
		unlockTime := time.Unix(lockedUntil.Int64, 0).Add(1 * time.Second)
		_, schedErr := sched.AddTask(&security.UnlockAccountTask{
			Username: username, UnlockFn: unlockFn,
		}, scheduler.OneTime, unlockTime)
		if schedErr != nil {
			slog.Error("failed to schedule unlock task", "username", username, "error", schedErr)
		}
	}
	return nil
}

// ClearLoginAttempts resets failed login attempts after a successful login.
func (s *SessionAuthFacade) ClearLoginAttempts(ctx context.Context, username string, pool *dbconnpool.DbSQLConnPool) error {
	cpcRw, err := pool.Get()
	if err != nil {
		return err
	}
	defer pool.Put(cpcRw)
	return cpcRw.Queries.ClearLoginAttempts(ctx, username)
}

// UnlockAccountFromTask clears a scheduled lockout for the given username.
func (s *SessionAuthFacade) UnlockAccountFromTask(ctx context.Context, username string, pool *dbconnpool.DbSQLConnPool) error {
	cpcRw, err := pool.Get()
	if err != nil {
		return err
	}
	defer pool.Put(cpcRw)
	return cpcRw.Queries.UnlockAccount(ctx, username)
}

// GetAdminUsername returns the configured admin username from the database.
func (s *SessionAuthFacade) GetAdminUsername(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (string, error) {
	cpcRo, err := pool.Get()
	if err != nil {
		return "", err
	}
	defer pool.Put(cpcRo)
	return cpcRo.Queries.GetConfigValueByKey(ctx, "user")
}

// ─── Theme ───────────────────────────────────────────────────────────

// GetEffectiveTheme resolves the active theme from cookie or configured default.
func (s *SessionAuthFacade) GetEffectiveTheme(r *http.Request, getThemes func() []string, defaultTheme string) string {
	if cookie, err := r.Cookie("theme"); err == nil {
		themes := getThemes()
		if slices.Contains(themes, cookie.Value) {
			return cookie.Value
		}
	}
	return defaultTheme
}
