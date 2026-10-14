package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
)

func newSessionAuthFacadeTestPool(t *testing.T) *dbconnpool.DbSQLConnPool {
	t.Helper()

	tempDir := t.TempDir()
	mainPath := filepath.Join(tempDir, "main.db")
	thumbsPath := filepath.Join(tempDir, "thumbs.db")

	if err := dbconnpool.CopyMainTestDatabaseTo(mainPath); err != nil {
		t.Fatalf("CopyMainTestDatabaseTo: %v", err)
	}
	if err := dbconnpool.CopyThumbsTestDatabaseTo(thumbsPath); err != nil {
		t.Fatalf("CopyThumbsTestDatabaseTo: %v", err)
	}

	ctx := context.Background()
	dsn := "file:" + filepath.ToSlash(mainPath) + "?_txlock=immediate&mode=rwc"
	pool, err := dbconnpool.NewDbSQLConnPool(ctx, dsn, dbconnpool.Config{
		DriverName:         "sqlite3",
		ReadOnly:           false,
		MaxConnections:     2,
		MinIdleConnections: 1,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsPath,
	})
	if err != nil {
		t.Fatalf("NewDbSQLConnPool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

func testThemeList() []string {
	return []string{"light", "dark", "cupcake"}
}

func TestSessionAuthFacade_GetEffectiveTheme(t *testing.T) {
	facade := NewSessionAuthFacade("test-session-secret-with-at-least-32-bytes-long")
	getThemes := testThemeList
	const defaultTheme = "light"

	tests := []struct {
		name      string
		cookie    *http.Cookie
		wantTheme string
	}{
		{
			name:      "valid theme cookie",
			cookie:    &http.Cookie{Name: "theme", Value: "dark"},
			wantTheme: "dark",
		},
		{
			name:      "invalid theme cookie",
			cookie:    &http.Cookie{Name: "theme", Value: "not-a-theme"},
			wantTheme: defaultTheme,
		},
		{
			name:      "no cookie",
			cookie:    nil,
			wantTheme: defaultTheme,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			got := facade.GetEffectiveTheme(req, getThemes, defaultTheme)
			if got != tc.wantTheme {
				t.Errorf("GetEffectiveTheme() = %q, want %q", got, tc.wantTheme)
			}
		})
	}
}

func TestSessionAuthFacade_CheckAccountLockout(t *testing.T) {
	facade := NewSessionAuthFacade("test-session-secret-with-at-least-32-bytes-long")
	pool := newSessionAuthFacadeTestPool(t)
	ctx := context.Background()

	t.Run("no login_attempts row", func(t *testing.T) {
		locked, err := facade.CheckAccountLockout(ctx, "nobody", pool)
		if err != nil {
			t.Fatalf("CheckAccountLockout: %v", err)
		}
		if locked {
			t.Error("expected locked=false with no row")
		}
	})

	t.Run("locked_until in the future", func(t *testing.T) {
		const username = "locked-user"
		now := time.Now().Unix()

		cpcRw, err := pool.Get()
		if err != nil {
			t.Fatalf("pool.Get: %v", err)
		}
		defer pool.Put(cpcRw)

		if upsertErr := cpcRw.Queries.UpsertLoginAttempt(ctx, gallerydb.UpsertLoginAttemptParams{
			Username:       username,
			FailedAttempts: 3,
			LastAttemptAt:  now,
			LockedUntil:    sql.NullInt64{Int64: now + 3600, Valid: true},
		}); upsertErr != nil {
			t.Fatalf("UpsertLoginAttempt: %v", upsertErr)
		}

		locked, err := facade.CheckAccountLockout(ctx, username, pool)
		if err != nil {
			t.Fatalf("CheckAccountLockout: %v", err)
		}
		if !locked {
			t.Error("expected locked=true")
		}
	})

	t.Run("expired lock clears attempts", func(t *testing.T) {
		const username = "expired-lock-user"
		now := time.Now().Unix()
		past := now - 7200

		cpcRw, err := pool.Get()
		if err != nil {
			t.Fatalf("pool.Get: %v", err)
		}
		defer pool.Put(cpcRw)

		if upsertErr := cpcRw.Queries.UpsertLoginAttempt(ctx, gallerydb.UpsertLoginAttemptParams{
			Username:       username,
			FailedAttempts: 3,
			LastAttemptAt:  past,
			LockedUntil:    sql.NullInt64{Int64: past, Valid: true},
		}); upsertErr != nil {
			t.Fatalf("UpsertLoginAttempt: %v", upsertErr)
		}

		locked, err := facade.CheckAccountLockout(ctx, username, pool)
		if err != nil {
			t.Fatalf("CheckAccountLockout: %v", err)
		}
		if locked {
			t.Error("expected locked=false after expired lockout")
		}

		_, getErr := cpcRw.Queries.GetLoginAttempt(ctx, username)
		if getErr == nil {
			t.Fatal("expected login_attempts row cleared")
		}
		if !errors.Is(getErr, sql.ErrNoRows) {
			t.Fatalf("GetLoginAttempt after clear: %v", getErr)
		}
	})
}

func seedConfigCredentials(t *testing.T, pool *dbconnpool.DbSQLConnPool, username, passwordHash string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Unix()

	cpcRw, err := pool.Get()
	if err != nil {
		t.Fatalf("pool.Get: %v", err)
	}
	defer pool.Put(cpcRw)

	for _, kv := range []struct {
		key, value string
	}{
		{"user", username},
		{"password", passwordHash},
	} {
		if upsertErr := cpcRw.Queries.UpsertConfigValueOnly(ctx, gallerydb.UpsertConfigValueOnlyParams{
			Key: kv.key, Value: kv.value, CreatedAt: now, UpdatedAt: now,
		}); upsertErr != nil {
			t.Fatalf("UpsertConfigValueOnly(%q): %v", kv.key, upsertErr)
		}
	}
}

func TestSessionAuthFacade_GetUser(t *testing.T) {
	facade := NewSessionAuthFacade("test-session-secret-with-at-least-32-bytes-long")
	pool := newSessionAuthFacadeTestPool(t)
	ctx := context.Background()

	const storedUser = "testadmin"
	const storedHash = "bcrypt-hash-placeholder"

	t.Run("matching username", func(t *testing.T) {
		seedConfigCredentials(t, pool, storedUser, storedHash)

		user, err := facade.GetUser(ctx, storedUser, pool, pool)
		if err != nil {
			t.Fatalf("GetUser: %v", err)
		}
		if user.Username != storedUser {
			t.Errorf("Username = %q, want %q", user.Username, storedUser)
		}
		if user.Password != storedHash {
			t.Errorf("Password = %q, want %q", user.Password, storedHash)
		}
	})

	t.Run("different username", func(t *testing.T) {
		seedConfigCredentials(t, pool, storedUser, storedHash)

		_, err := facade.GetUser(ctx, "other-user", pool, pool)
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("GetUser: %v", err)
		}
	})

	t.Run("missing user config key", func(t *testing.T) {
		seedConfigCredentials(t, pool, storedUser, storedHash)

		cpcRw, err := pool.Get()
		if err != nil {
			t.Fatalf("pool.Get: %v", err)
		}
		defer pool.Put(cpcRw)

		if _, execErr := cpcRw.Conn.ExecContext(ctx, "DELETE FROM config WHERE key = 'user'"); execErr != nil {
			t.Fatalf("delete user config: %v", execErr)
		}

		_, err = facade.GetUser(ctx, storedUser, pool, pool)
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("GetUser: %v", err)
		}
	})
}
