package cachebatch

import (
	"context"
	"net/http"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/modulestate"
)

// Config holds dependencies for BatchLoadManager.
type Config struct {
	// GetQueries returns HandlerQueries-like access; must provide GetBatchLoadTargets
	// and HttpCacheExistsByKey. Typically from dbRoPool.Get().Queries (CustomQueries).
	GetQueries func() (BatchLoadQueries, func())

	// GetHandler returns the full HTTP handler chain for internal requests.
	GetHandler func() http.Handler

	// GetETagVersion returns the current ETag version for cache keys.
	GetETagVersion func() string

	// ModuleStateService for discovery active check; nil skips guard.
	ModuleStateService *modulestate.Service
}

// BatchLoadQueries is the minimal interface needed for batch load.
type BatchLoadQueries interface {
	GetBatchLoadTargets(ctx context.Context) ([]gallerydb.BatchLoadTarget, error)
	HttpCacheExistsByKey(ctx context.Context, key string) (int64, error)
}
