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

// ServerDeps provides all server-level dependencies consumed by handler groups.
// Implemented by *server.App, it replaces the previous callback-field wiring
// pattern with a single compile-time-checked interface.
type ServerDeps interface {
	// --- Credential operations ---
	CheckAccountLockout(ctx context.Context, username string) (bool, error)
	GetUser(ctx context.Context, username string) (*session.User, error)
	RecordFailedLoginAttempt(ctx context.Context, username string) error
	ClearLoginAttempts(ctx context.Context, username string) error
	UpdateUsername(ctx context.Context, username string) error
	UpdatePassword(ctx context.Context, passwordHash string) error

	// --- Config operations ---
	UpdateConfigWithPrecedence(cfg *config.Config, changedFields []string)
	ApplyConfig()
	InvalidateHTTPCache()
	SetPreloadEnabled(enabled bool)
	SetRestartRequired(b bool)
	TriggerRestart()

	// --- Gallery queries ---
	GetHandlerQueries(cpc *dbconnpool.CpConn) HandlerQueries
	GetMetadataQueries(cpc *dbconnpool.CpConn) MetadataQueries
	GetConfigQueries(cpc *dbconnpool.CpConn) config.ConfigQueries
	GetETagVersion() string
	ImagesDir() string

	// --- Server control ---
	Shutdown()
	TriggerDiscovery()
	ResetStats()
	StartCacheBatchLoad() (StartCacheBatchLoadResult, error)

	// --- Config access ---
	GetConfig() *config.Config

	// --- Template helpers ---
	AddCommonTemplateData(w http.ResponseWriter, r *http.Request, data map[string]any, fullPage bool) map[string]any
	ServerError(w http.ResponseWriter, r *http.Request, err error)
}
