// Package database wires SQLite connection pools and database lifecycle for the server.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite" // Import the SQLite database driver (modernc-based)
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/migrations"
)

// Testable hooks for dependency injection. Defaults delegate to real implementations.
var (
	osMkdirAll               = os.MkdirAll
	osOpenFile               = os.OpenFile
	osChmod                  = os.Chmod
	migrateDBFn              = migrateDB
	migrateBlobsDBFn         = migrateBlobsDB
	newDbSQLConnPool         = dbconnpool.NewDbSQLConnPool
	dbRwPoolGet              = (*dbconnpool.DbSQLConnPool).Get
	ensureRootFolderExistsFn = ensureRootFolderExists
)

// DatabasePaths holds the file paths for both application databases.
type DatabasePaths struct {
	Main   string // Path to sfpg.db
	Thumbs string // Path to thumbs.db
}

// Setup initializes the database environment:
// 1. Sets up the directory struct
// 2. Runs schema migrations
// 3. Establishes connection pools (RW/RO)
// 4. Ensures root folder entry exists
// 5. Schedules periodic optimization
func Setup(ctx context.Context, rootDir string, cfg *config.Config) (DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	// 1. Directory Setup
	dbDir := filepath.Join(rootDir, "DB")
	if _, err := os.Stat(dbDir); os.IsNotExist(err) {
		if err := osMkdirAll(dbDir, 0o755); err != nil {
			return DatabasePaths{}, nil, nil, fmt.Errorf("failed to create DB directory: %w", err)
		}
	}
	dbPath := filepath.Join(dbDir, "sfpg.db")
	thumbsDir := filepath.Join(dbDir, "thumbs")
	if _, err := os.Stat(thumbsDir); os.IsNotExist(err) {
		if err := osMkdirAll(thumbsDir, 0o755); err != nil {
			return DatabasePaths{}, nil, nil, fmt.Errorf("failed to create thumbs DB directory: %w", err)
		}
	}
	thumbsDBPath := filepath.Join(thumbsDir, "thumbs.db")

	// 2. Migrations
	mainApplied, err := migrateDBFn(dbPath)
	if err != nil {
		return DatabasePaths{}, nil, nil, fmt.Errorf("migration failed: %w", err)
	}
	thumbsApplied, err := migrateBlobsDBFn(thumbsDBPath)
	if err != nil {
		return DatabasePaths{}, nil, nil, fmt.Errorf("thumbs migration failed: %w", err)
	}

	// 3. Connection Pools
	roDsn, rwDsn := configureDatabaseDSN(dbPath)
	dbRwPool, dbRoPool, err := createDatabasePools(ctx, roDsn, rwDsn, thumbsDBPath, cfg)
	if err != nil {
		return DatabasePaths{}, nil, nil, fmt.Errorf("pool creation failed: %w", err)
	}

	// 4. Post-migration PRAGMA optimize (only when migrations were applied)
	if mainApplied || thumbsApplied {
		optCpc, optErr := dbRwPool.Get()
		if optErr != nil {
			dbRwPool.Close()
			dbRoPool.Close()
			return DatabasePaths{}, nil, nil, fmt.Errorf("failed to get conn for post-migration optimize: %w", optErr)
		}
		if runErr := dbconnpool.RunPragmaOptimize(ctx, optCpc.Conn, dbconnpool.PragmaOptimizeDefault); runErr != nil {
			slog.Warn("post-migration PRAGMA optimize failed", "err", runErr)
		}
		dbRwPool.Put(optCpc)
	}

	// 5. Root Folder Check
	cpcRw, err := dbRwPoolGet(dbRwPool)
	if err != nil {
		dbRwPool.Close()
		dbRoPool.Close()
		return DatabasePaths{}, nil, nil, fmt.Errorf("failed to get RW conn for root check: %w", err)
	}
	defer dbRwPool.Put(cpcRw)

	if err := ensureRootFolderExistsFn(ctx, cpcRw, rootDir); err != nil {
		dbRwPool.Close()
		dbRoPool.Close()
		return DatabasePaths{}, nil, nil, fmt.Errorf("root folder check failed: %w", err)
	}

	return DatabasePaths{Main: dbPath, Thumbs: thumbsDBPath}, dbRwPool, dbRoPool, nil
}

func migrateDB(dbPath string) (bool, error) {
	// Open a temporary connection to ensure file exists
	db, err := osOpenFile(dbPath, os.O_RDWR|os.O_CREATE, 0o664)
	if err != nil {
		return false, fmt.Errorf("failed to open database file: %w", err)
	}
	db.Close() // Ignore close error on empty file

	// Guarantee the required mode; os.OpenFile is subject to the process umask.
	if err = osChmod(dbPath, 0o664); err != nil {
		return false, fmt.Errorf("failed to set database file permissions: %w", err)
	}

	dbConn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return false, fmt.Errorf("failed to open sqlite connection: %w", err)
	}
	defer dbConn.Close()

	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		return false, fmt.Errorf("failed to create iofs source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, "sqlite://"+filepath.ToSlash(dbPath))
	if err != nil {
		return false, fmt.Errorf("failed to create migrate instance: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			return false, nil
		}
		return false, fmt.Errorf("up migration failed: %w", err)
	}
	return true, nil
}

func migrateBlobsDB(dbPath string) (bool, error) {
	db, err := osOpenFile(dbPath, os.O_RDWR|os.O_CREATE, 0o664)
	if err != nil {
		return false, fmt.Errorf("failed to open thumbs database file: %w", err)
	}
	db.Close()

	// Guarantee the required mode; os.OpenFile is subject to the process umask.
	if err = osChmod(dbPath, 0o664); err != nil {
		return false, fmt.Errorf("failed to set thumbs database file permissions: %w", err)
	}

	m, err := migrations.NewThumbsMigrator(dbPath)
	if err != nil {
		return false, fmt.Errorf("failed to create thumbs migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			return false, nil
		}
		return false, fmt.Errorf("thumbs up migration failed: %w", err)
	}
	return true, nil
}

func configureDatabaseDSN(dbPath string) (roDsn, rwDsn string) {
	mmapSize := strconv.Itoa(39 * 1024 * 1024 * 1024)

	// ncruces/go-sqlite3 requires 'file:' prefix for pragmas to work
	// All pragmas must use _pragma=name(value) format
	basePath := "file:" + filepath.ToSlash(dbPath)

	// Common params for both pools (avoiding WAL mode on RO pool)
	commonParams := []string{
		"_pragma=cache(shared)",
		"_pragma=cache_size(10240)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=busy_timeout(5000)", // Keep explicit - ncruces defaults to 1 minute
		"_pragma=temp_store(memory)",
		"_pragma=foreign_keys(1)",
		"_pragma=mmap_size(" + mmapSize + ")",
		// Disable SQLite's inline automatic WAL checkpoint at commit. The default
		// (wal_autocheckpoint=1000 frames) runs a PASSIVE checkpoint inside Commit
		// whenever the WAL crosses ~1000 frames. With byte_limit batches of ~8MB
		// (~2000 frames) every commit tripped the autocheckpoint, and when any
		// reader held the WAL the writer busy-waited up to busy_timeout (5s) —
		// producing the 1-5s whole-second commit stalls. Instead, WAL is bounded
		// by the explicit TRUNCATE checkpoint in walCheckpointAfterCommit (2GB
		// size threshold or 5-minute maintenance timer), which runs in the
		// writebatcher worker with no active transaction and so cannot contend
		// with itself.
		"_pragma=wal_autocheckpoint(0)",
	}

	// Read-Only DSN: simple mode=ro, no pragmas that require write
	// WAL mode is persistent and was already set by RW pool
	roDsn = basePath + "?mode=ro"

	// Read-Write DSN: set WAL mode and use immediate locking
	rwParams := make([]string, len(commonParams), len(commonParams)+3)
	copy(rwParams, commonParams)
	rwParams = append(rwParams, "_pragma=journal_mode(WAL)", "_txlock=immediate", "mode=rwc")
	rwDsn = basePath + "?" + strings.Join(rwParams, "&")
	return
}

// EffectivePoolLimits returns the connection pool limits (max connections,
// min idle connections) and monitor interval that would be applied for the
// given config. A nil config falls back to config.DefaultConfig (100/10/1m),
// and a non-positive monitor interval is clamped to the default (1m) so the
// pool monitor always runs. Min idle 0 is preserved as-is (effective 0).
func EffectivePoolLimits(cfg *config.Config) (max, minIdle int64, monitorInterval time.Duration) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	max = int64(cfg.DBMaxPoolSize)
	minIdle = int64(cfg.DBMinIdleConnections)
	monitorInterval = cfg.DBPoolMonitorInterval
	if monitorInterval <= 0 {
		monitorInterval = config.DefaultConfig().DBPoolMonitorInterval
	}
	return max, minIdle, monitorInterval
}

func createDatabasePools(ctx context.Context, roDsn, rwDsn, thumbsDBPath string, cfg *config.Config) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	maxPoolSize, minIdleConns, monitorInterval := EffectivePoolLimits(cfg)

	dbRwPool, err := newDbSQLConnPool(ctx, rwDsn,
		dbconnpool.Config{
			DriverName:         "sqlite3",
			MaxConnections:     maxPoolSize,
			MinIdleConnections: minIdleConns,
			MonitorInterval:    monitorInterval,
			ReadOnly:           false,
			QueriesFunc:        gallerydb.NewCustomQueries,
			ThumbsDBPath:       thumbsDBPath,
		})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create RW pool: %w", err)
	}

	dbRoPool, err := newDbSQLConnPool(ctx, roDsn,
		dbconnpool.Config{
			DriverName:         "sqlite3",
			MaxConnections:     maxPoolSize,
			MinIdleConnections: minIdleConns,
			MonitorInterval:    monitorInterval,
			ReadOnly:           true,
			QueriesFunc:        gallerydb.NewCustomQueries,
			ThumbsDBPath:       thumbsDBPath,
		})
	if err != nil {
		dbRwPool.Close()
		return nil, nil, fmt.Errorf("failed to create RO pool: %w", err)
	}

	return dbRwPool, dbRoPool, nil
}

// RecreatePoolsWithConfig closes old pools and creates new ones with the given config.
// This is used when configuration is loaded after initial pool creation to honor
// database-stored pool settings during startup.
func RecreatePoolsWithConfig(ctx context.Context, paths DatabasePaths, cfg *config.Config, oldRwPool, oldRoPool *dbconnpool.DbSQLConnPool) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	// Close old pools
	if oldRwPool != nil {
		if err := oldRwPool.Close(); err != nil {
			slog.Warn("error closing old RW pool during reconfiguration", "err", err)
		}
	}
	if oldRoPool != nil {
		if err := oldRoPool.Close(); err != nil {
			slog.Warn("error closing old RO pool during reconfiguration", "err", err)
		}
	}

	// Create new pools with updated config
	roDsn, rwDsn := configureDatabaseDSN(paths.Main)
	newRwPool, newRoPool, err := createDatabasePools(ctx, roDsn, rwDsn, paths.Thumbs, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to recreate pools with config: %w", err)
	}

	return newRwPool, newRoPool, nil
}

func ensureRootFolderExists(ctx context.Context, cpcRw *dbconnpool.CpConn, rootDir string) error {
	_, err := cpcRw.Queries.GetFolderIDByPath(ctx, "")
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	rootMtime := time.Now().Unix()
	if stat, statErr := os.Stat(rootDir); statErr == nil {
		rootMtime = stat.ModTime().Unix()
	}

	imp := &gallerylib.Importer{Q: cpcRw.Queries}
	_, err = imp.CreateRootFolderEntry(ctx, rootMtime)
	return err
}
