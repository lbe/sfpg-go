package server

import (
	"context"
	"database/sql"
	"io/fs"
	"net/http"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/log"
	"github.com/lbe/sfpg-go/internal/profiler"
	"github.com/lbe/sfpg-go/internal/scheduler"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/database"
	"github.com/lbe/sfpg-go/internal/server/interfaces"
	"github.com/lbe/sfpg-go/internal/writebatcher"
)

// AppTestSeams holds optional test doubles for App lifecycle paths.
// The zero value means use production implementations.
type AppTestSeams struct {
	// NewParseTemplates replaces ui.ParseTemplates in New().
	NewParseTemplates func(fs.FS) error
	// NewExit replaces os.Exit in New() when template parsing fails.
	NewExit func(code int)

	// Serve replaces App.Serve and RuntimeManager.Serve for tests.
	Serve func(handler http.Handler, addr string) error
	// ProfilerStart replaces profiler.Start in Run().
	ProfilerStart func(cfg profiler.Config) (stop func(), err error)
	// MemoryReclaimer replaces the memoryReclaimer goroutine in Run().
	MemoryReclaimer func(cfg MemoryReclaimerConfig)
	// ModuleStateActive replaces moduleStateService.IsActive in StartCacheBatchLoad.
	ModuleStateActive func() (bool, error)
	// BatchLoadManagerRun replaces batchLoadManager.Run in StartCacheBatchLoad.
	BatchLoadManagerRun func(ctx context.Context) error
	// GalleryStatsStartup replaces the async startup stats goroutine in Run().
	GalleryStatsStartup func()
	// TriggerDiscovery replaces app.TriggerDiscovery in startup when non-nil.
	TriggerDiscovery func()
	// FallbackConfig supplies the config used when loadConfig fails in Run.
	FallbackConfig func() *config.Config
	// ConfigService replaces config.NewService(...) in setDB and reconfigurePoolsFromConfig.
	ConfigService config.ConfigService
	// LoadConfig replaces config.Load in loadConfig.
	LoadConfig func() (*config.Config, error)
	// Executable replaces os.Executable() in setRootDir.
	Executable func() (string, error)
	// SetupBootstrapLogging replaces logging.SetupBootstrap in setupBootstrapLogging.
	SetupBootstrapLogging func(rootDir string, scheduler *scheduler.Scheduler, version string) (*log.Logger, error)
	// DatabaseSetup replaces database.Setup in InitForUnlock/InitForIncrementETag.
	DatabaseSetup func(ctx context.Context, rootDir string, cfg *config.Config) (database.DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error)
}

// InfrastructureTestSeams holds optional test doubles for InfrastructureService.
// The zero value means use production implementations.
type InfrastructureTestSeams struct {
	BuildWriteBatcher          func(ctx context.Context, maxBatchSize int, flushInterval time.Duration) (*writebatcher.WriteBatcher[BatchedWrite], error)
	ShutdownWriteBatcher       func() error
	PerformWALCheckpoint       func(ctx context.Context)
	PragmaOptimize             func(ctx context.Context, pool dbPoolForCheckpoint)
	WALCheckpointQuery         func(ctx context.Context, conn *sql.Conn) (*sql.Rows, error)
	GetCacheSizeBytes          func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error)
	GetCacheEntryCount         func(ctx context.Context, pool *dbconnpool.DbSQLConnPool) (int64, error)
	EvictLRU                   func(ctx context.Context, pool *dbconnpool.DbSQLConnPool, targetFree int64) (int64, int64, error)
	FlushBatchedWrites         func(ctx context.Context, tx *sql.Tx, batch []BatchedWrite) error
	HandlerQueries             interfaces.HandlerQueries
	RecreatePoolsWithConfig    func(ctx context.Context, dbPaths database.DatabasePaths, cfg *config.Config, oldRw, oldRo *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error)
	PragmaOptimizePollInterval time.Duration
	PragmaOptimizeMaxWait      time.Duration
}

// RuntimeManagerTestSeams holds optional test doubles for RuntimeManager.
// The zero value means use production implementations.
type RuntimeManagerTestSeams struct {
	Executable   func() (string, error)
	ExecCommand  func(path string, args []string, env []string) error
	Exit         func(code int)
	BeforeListen func()
	Shutdown     func(ctx context.Context) error
}

// HandlerManagerTestSeams holds optional test doubles for HandlerManager.
// The zero value means use production implementations.
type HandlerManagerTestSeams struct {
	BuildHandlers func(fs fs.FS) error
}

// defaultNewTestSeams holds package-level test doubles that must be applied
// before New() creates the App. New() copies these into app.testSeams so that
// production code only reads from app.testSeams, while tests that need to
// influence New() can set this variable before calling New().
var defaultNewTestSeams AppTestSeams
