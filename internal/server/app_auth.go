package server

import (
	"github.com/gorilla/sessions"

	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/session"
)

// appAuth groups authentication and session dependencies into one
// sub-struct, reducing field sprawl on the App god-object.
// Embedded into App to promote its fields.
type appAuth struct {
	sessionSecret  string
	store          *sessions.CookieStore // session cookie store used for managing user authentication sessions
	sessionManager *session.Manager      // session manager encapsulating session operations
	authService    auth.AuthService      // AuthService for authentication operations
}
