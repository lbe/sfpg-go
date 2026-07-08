package cachebatch

import (
	"context"
	"net/http"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// moduleStateService is the minimal interface needed to guard batch load against active discovery.
type moduleStateService interface {
	IsActive(ctx context.Context, name string) (bool, error)
}

// Config holds dependencies for BatchLoadManager.
type Config struct {
	// GetQueries returns HandlerQueries-like access; must provide GetBatchLoadTargets
	// and HttpCacheExistsByKey. Typically from dbRoPool.Get().Queries (CustomQueries).
	GetQueries func() (BatchLoadQueries, func())

	// GetHandler returns the HTTP handler wrapped with full middleware chain
	// (cache middleware, compression middleware, etc.)
	GetHandler func() http.Handler

	// GetETagVersion returns the current ETag version for cache keys.
	GetETagVersion func() string

	// ModuleStateService for discovery active check; nil skips guard.
	ModuleStateService moduleStateService
}

// BatchLoadQueries is the minimal interface needed for batch load.
type BatchLoadQueries interface {
	GetBatchLoadTargets(ctx context.Context) ([]gallerydb.BatchLoadTarget, error)
	HttpCacheExistsByKey(ctx context.Context, key string) (bool, error)
}
