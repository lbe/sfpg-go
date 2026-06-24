package server

import (
	"database/sql"
	"sync/atomic"

	"github.com/lbe/sfpg-go/internal/cachelite"
	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/files"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// appInfra groups infrastructure dependencies that are set up once during
// application startup and remain stable for the lifetime of the App.
// Embedded into App to promote its fields, reducing the god-object surface.
type appInfra struct {
	cacheStore     cachelite.CacheStore // CacheStore for cache operations
	cacheSizeBytes atomic.Int64         // atomic cache size in bytes (avoids DB query)
	cacheMW        *cachelite.HTTPCacheMiddleware
	dbPaths        database.DatabasePaths
	dbRoPool       *dbconnpool.DbSQLConnPool
	dbRwPool       *dbconnpool.DbSQLConnPool
	dqueDirPath    string
	// testHookHandlerQueries allows tests to inject query behavior for handlers
	testHookHandlerQueries interfaces.HandlerQueries
	imagesDir              string
	// ImporterFactory allows tests or callers to override how Importer instances are constructed.
	ImporterFactory     func(conn *sql.Conn, q *gallerydb.CustomQueries) files.Importer
	normalizedImagesDir string // Cached filepath.ToSlash(imagesDir) to avoid repeated allocations
	rootDir             string
	writeBatcher        *writebatcher.WriteBatcher[BatchedWrite]
	batcherQueries      *gallerydb.CustomQueries // prepared queries for the in-flight writebatcher tx (set in BeginTx, read by flushBatchedWrites)
}
