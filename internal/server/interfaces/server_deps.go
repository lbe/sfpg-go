// Package interfaces holds shared contracts consumed by both the server
// orchestrator (App) and the handlers package. These interfaces live here
// to avoid circular dependencies while keeping the contracts in a neutral,
// stable location.
package interfaces

import (
	"context"
	"net/http"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/session"
)

// StartCacheBatchLoadResult describes the result of attempting to start cache batch load.
type StartCacheBatchLoadResult struct {
	Blocked bool   // true if discovery is active
	Message string // toast message
}

// CredentialStore provides credential-related operations for authentication
// and user management. Extracted as a narrow interface from ServerDeps.
type CredentialStore interface {
	CheckAccountLockout(ctx context.Context, username string) (bool, error)
	GetUser(ctx context.Context, username string) (*session.User, error)
	RecordFailedLoginAttempt(ctx context.Context, username string) error
	ClearLoginAttempts(ctx context.Context, username string) error
	UpdateUsername(ctx context.Context, username string) error
	UpdatePassword(ctx context.Context, passwordHash string) error
}

// ConfigOps provides configuration update operations for applying config
// changes, managing restart flags, and invalidating caches. Extracted as a
// narrow interface from ServerDeps.
type ConfigOps interface {
	UpdateConfigWithPrecedence(cfg *config.Config, changedFields []string)
	ApplyConfig()
	InvalidateHTTPCache()
	SetPreloadEnabled(enabled bool)
	SetRestartRequired(b bool)
	TriggerRestart()
}

// GalleryOps provides gallery query operations — handler queries, metadata,
// config queries, ETag versioning, and the images directory path. Extracted
// as a narrow interface from ServerDeps.
type GalleryOps interface {
	GetHandlerQueries(cpc *dbconnpool.CpConn) HandlerQueries
	GetMetadataQueries(cpc *dbconnpool.CpConn) MetadataQueries
	GetConfigQueries(cpc *dbconnpool.CpConn) config.ConfigQueries
	GetETagVersion() string
	ImagesDir() string
}

// ServerControl provides server lifecycle operations — shutdown, discovery,
// stats reset, and cache batch loading. Extracted as a narrow interface
// from ServerDeps.
type ServerControl interface {
	Shutdown()
	TriggerDiscovery()
	ResetStats()
	StartCacheBatchLoad() (StartCacheBatchLoadResult, error)
}

// ServerDeps provides all server-level dependencies consumed by handler groups.
// Implemented by *server.App, it replaces the previous callback-field wiring
// pattern with a single compile-time-checked interface.
//
// CredentialStore, ConfigOps, GalleryOps, and ServerControl are embedded for
// backward compatibility — handler groups that need only narrow interfaces
// can accept them directly instead of the full ServerDeps.
type ServerDeps interface {
	CredentialStore
	ConfigOps
	GalleryOps
	ServerControl

	// --- Config access ---
	GetConfig() *config.Config

	// --- Template helpers ---
	AddCommonTemplateData(w http.ResponseWriter, r *http.Request, data map[string]any, fullPage bool) map[string]any
	ServerError(w http.ResponseWriter, r *http.Request, err error)
}
