package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite" // Import the SQLite database driver (modernc-based)
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/gallerydb"
	"go.local/sfpg/internal/gallerylib"
	"go.local/sfpg/internal/server/config"
	"go.local/sfpg/migrations"
)

// Setup initializes the database environment:
// 1. Sets up the directory struct
// 2. Runs schema migrations
// 3. Establishes connection pools (RW/RO)
// 4. Ensures root folder entry exists
// 5. Schedules periodic optimization
func Setup(ctx context.Context, rootDir string, cfg *config.Config) (string, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	// 1. Directory Setup
	dbDir := filepath.Join(rootDir, "DB")
	if _, err := os.Stat(dbDir); os.IsNotExist(err) {
		if err := os.MkdirAll(dbDir, 0o755); err != nil {
			return "", nil, nil, fmt.Errorf("failed to create DB directory: %w", err)
		}
	}
	dbPath := filepath.Join(dbDir, "sfpg.db")

	// 2. Migrations
	if err := migrateDB(dbPath); err != nil {
		return "", nil, nil, fmt.Errorf("migration failed: %w", err)
	}

	// 3. Connection Pools
	roDsn, rwDsn := configureDatabaseDSN(dbPath)
	dbRwPool, dbRoPool, err := createDatabasePools(ctx, roDsn, rwDsn, cfg)
	if err != nil {
		return "", nil, nil, fmt.Errorf("pool creation failed: %w", err)
	}

	// 4. Root Folder Check
	cpcRw, err := dbRwPool.Get()
	if err != nil {
		dbRwPool.Close()
		dbRoPool.Close()
		return "", nil, nil, fmt.Errorf("failed to get RW conn for root check: %w", err)
	}
	defer dbRwPool.Put(cpcRw)

	if err := ensureRootFolderExists(ctx, cpcRw, rootDir); err != nil {
		dbRwPool.Close()
		dbRoPool.Close()
		return "", nil, nil, fmt.Errorf("root folder check failed: %w", err)
	}

	return dbPath, dbRwPool, dbRoPool, nil
}

func migrateDB(dbPath string) error {
	// Open a temporary connection to ensure file exists
	db, err := os.OpenFile(dbPath, os.O_RDWR|os.O_CREATE, 0o666)
	if err != nil {
		return fmt.Errorf("failed to open database file: %w", err)
	}
	db.Close() // Ignore close error on empty file

	dbConn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite connection: %w", err)
	}
	defer dbConn.Close()

	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create iofs source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, "sqlite://"+filepath.ToSlash(dbPath))
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("up migration failed: %w", err)
	}
	return nil
}

func configureDatabaseDSN(dbPath string) (roDsn, rwDsn string) {
	mmapSize := strconv.Itoa(39 * 1024 * 1024 * 1024)

	// ncruces/go-sqlite3 requires 'file:' prefix for pragmas to work
	// All pragmas must use _pragma=name(value) format
	basePath := "file:" + filepath.ToSlash(dbPath)

	// Common params for both pools (avoiding WAL mode on RO pool)
	commonParams := []string{
		"_pragma=cache_size(10240)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=busy_timeout(5000)", // Keep explicit - ncruces defaults to 1 minute
		"_pragma=temp_store(memory)",
		"_pragma=foreign_keys(1)",
		"_pragma=mmap_size(" + mmapSize + ")",
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

func createDatabasePools(ctx context.Context, roDsn, rwDsn string, cfg *config.Config) (*dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error) {
	maxPoolSize := int64(100)
	minIdleConns := int64(10)
	if cfg != nil {
		maxPoolSize = int64(cfg.DBMaxPoolSize)
		minIdleConns = int64(cfg.DBMinIdleConnections)
	}

	dbRwPool, err := dbconnpool.NewDbSQLConnPool(ctx, rwDsn,
		dbconnpool.Config{
			DriverName:         "sqlite3",
			MaxConnections:     maxPoolSize,
			MinIdleConnections: minIdleConns,
			ReadOnly:           false,
			QueriesFunc:        gallerydb.NewCustomQueries,
		})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create RW pool: %w", err)
	}

	// Optimize immediately
	cpcRw, err := dbRwPool.Get()
	if err == nil {
		cpcRw.Conn.ExecContext(ctx, `PRAGMA optimize=0x10002`)
		dbRwPool.Put(cpcRw)
	}

	dbRoPool, err := dbconnpool.NewDbSQLConnPool(ctx, roDsn,
		dbconnpool.Config{
			DriverName:         "sqlite3",
			MaxConnections:     maxPoolSize,
			MinIdleConnections: minIdleConns,
			ReadOnly:           true,
			QueriesFunc:        gallerydb.NewCustomQueries,
		})
	if err != nil {
		dbRwPool.Close()
		return nil, nil, fmt.Errorf("failed to create RO pool: %w", err)
	}

	return dbRwPool, dbRoPool, nil
}

func ensureRootFolderExists(ctx context.Context, cpcRw *dbconnpool.CpConn, rootDir string) error {
	_, err := cpcRw.Queries.GetFolderIDByPath(ctx, "")
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
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

func ScheduleOptimization(ctx context.Context, pool *dbconnpool.DbSQLConnPool, wg *sync.WaitGroup) {
	wg.Go(func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop() // Ensure ticker is stopped on exit
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cpcRw, err := pool.Get()
				if err != nil {
					slog.Error("failed to get RW DB connection from pool for optimizer", "err", err)
					continue
				}
				cpcRw.PragmaOptimize(ctx)
				pool.Put(cpcRw)
			}
		}
	})
}
