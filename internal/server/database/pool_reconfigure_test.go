package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/migrations"
)

// TestPoolReconfiguration_NoCloseWhileActive verifies that pool reconfiguration
// does not close pools while there are active connections or statements.
//
// EXPECTED BEHAVIOR: Reconfiguration should wait for active connections to be
// returned to the pool before closing them, or should gracefully handle active
// connections without causing "database is locked" or "unfinalized statements" errors.
//
// CURRENT BEHAVIOR (DEFECT): RecreatePoolsWithConfig immediately calls Close()
// on old pools without checking for active connections, potentially causing errors.
//
// This test SHOULD FAIL until the pool reconfiguration logic is fixed to handle
// active connections gracefully.
func TestPoolReconfiguration_NoCloseWhileActive(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")

	// Run migrations
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		db.Close()
		t.Fatalf("failed to create sqlite driver instance: %v", err)
	}

	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		db.Close()
		t.Fatalf("failed to create iofs source driver: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create migrate instance: %v", err)
	}

	if upErr := m.Up(); upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		db.Close()
		t.Fatalf("failed to apply migrations: %v", upErr)
	}
	db.Close()

	if _, migErr := migrateBlobsDB(thumbsDBPath); migErr != nil {
		t.Fatalf("migrateBlobsDB failed: %v", migErr)
	}

	// Create initial pools with default config
	cfg := config.DefaultConfig()
	cfg.DBMaxPoolSize = 10
	cfg.DBMinIdleConnections = 2

	roDsn, rwDsn := configureDatabaseDSN(dbPath)
	rwPool, roPool, err := createDatabasePools(ctx, roDsn, rwDsn, thumbsDBPath, cfg)
	if err != nil {
		t.Fatalf("failed to create initial pools: %v", err)
	}

	// Start a background goroutine that holds a connection and keeps it active
	// This simulates an in-flight query/operation during reconfiguration
	var wg sync.WaitGroup
	errorCh := make(chan error, 1)
	holderDone := make(chan struct{})
	release := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()

		// Get a connection and hold it
		conn, getErr := rwPool.Get()
		if getErr != nil {
			errorCh <- getErr
			return
		}

		// Keep the connection active by starting a transaction
		tx, txErr := conn.Conn.BeginTx(ctx, nil)
		if txErr != nil {
			rwPool.Put(conn)
			errorCh <- txErr
			return
		}

		// Signal that we're holding the connection
		close(holderDone)

		// Hold the connection until reconfiguration has completed
		<-release

		// Cleanup
		_ = tx.Rollback()
		rwPool.Put(conn)
	}()

	// Wait for holder to acquire connection
	<-holderDone

	// Now trigger pool reconfiguration while the connection is active
	// This should NOT cause errors, but currently it might
	newCfg := config.DefaultConfig()
	newCfg.DBMaxPoolSize = 20       // Change pool size
	newCfg.DBMinIdleConnections = 5 // Change idle connections

	// RecreatePoolsWithConfig should handle active connections gracefully
	newRwPool, newRoPool, reconfigErr := RecreatePoolsWithConfig(ctx, DatabasePaths{Main: dbPath, Thumbs: thumbsDBPath}, newCfg, rwPool, roPool)

	// Release the holder now that reconfiguration has completed.
	close(release)

	// Check if any errors occurred in the holder goroutine
	select {
	case holdErr := <-errorCh:
		t.Errorf("holder goroutine encountered error: %v", holdErr)
	default:
		// No error from holder
	}

	if reconfigErr != nil {
		t.Errorf("RecreatePoolsWithConfig failed: %v", reconfigErr)
	}

	// Wait for holder goroutine to complete
	wg.Wait()

	if newRwPool == nil || newRoPool == nil {
		t.Fatal("new pools should not be nil after reconfiguration")
	}

	// Verify new pools work
	testConn, err := newRwPool.Get()
	if err != nil {
		t.Errorf("failed to get connection from new pool: %v", err)
	} else {
		newRwPool.Put(testConn)
	}

	// Cleanup
	_ = newRwPool.Close()
	_ = newRoPool.Close()

	// ASSERTION: The test should pass without any "database is locked" or
	// "unfinalized statements" errors in the logs.
	// If RecreatePoolsWithConfig closes pools immediately, the active connection
	// in the holder goroutine may cause errors.
	//
	// EXPECTED: This test SHOULD FAIL until pool reconfiguration is fixed to
	// handle active connections gracefully (e.g., draining active connections
	// before closing, or using a grace period).
}

func TestRecreatePoolsWithConfig_CreatePoolsFails(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")

	// Run main migrations
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		db.Close()
		t.Fatalf("failed to create sqlite driver instance: %v", err)
	}

	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		db.Close()
		t.Fatalf("failed to create iofs source driver: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create migrate instance: %v", err)
	}

	if upErr := m.Up(); upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		db.Close()
		t.Fatalf("failed to apply migrations: %v", upErr)
	}
	db.Close()

	// Run thumbs migration
	if _, migErr := migrateBlobsDB(thumbsDBPath); migErr != nil {
		t.Fatalf("migrateBlobsDB failed: %v", migErr)
	}

	cfg := config.DefaultConfig()
	roDsn, rwDsn := configureDatabaseDSN(dbPath)
	rwPool, roPool, err := createDatabasePools(ctx, roDsn, rwDsn, thumbsDBPath, cfg)
	if err != nil {
		t.Fatalf("failed to create initial pools: %v", err)
	}
	defer func() {
		_ = rwPool.Close()
		_ = roPool.Close()
	}()

	paths := DatabasePaths{Main: dbPath, Thumbs: thumbsDBPath}
	badCfg := config.DefaultConfig()
	badCfg.DBMaxPoolSize = 1
	badCfg.DBMinIdleConnections = 10

	_, _, err = RecreatePoolsWithConfig(ctx, paths, badCfg, rwPool, roPool)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to recreate pools with config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecreatePoolsWithConfig_CloseOldPoolsErrors(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")

	// Run main migrations
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		db.Close()
		t.Fatalf("failed to create sqlite driver instance: %v", err)
	}

	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		db.Close()
		t.Fatalf("failed to create iofs source driver: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create migrate instance: %v", err)
	}

	if upErr := m.Up(); upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		db.Close()
		t.Fatalf("failed to apply migrations: %v", upErr)
	}
	db.Close()

	// Run thumbs migration
	if _, migErr := migrateBlobsDB(thumbsDBPath); migErr != nil {
		t.Fatalf("migrateBlobsDB failed: %v", migErr)
	}

	cfg := config.DefaultConfig()
	roDsn, rwDsn := configureDatabaseDSN(dbPath)
	rwPool, roPool, err := createDatabasePools(ctx, roDsn, rwDsn, thumbsDBPath, cfg)
	if err != nil {
		t.Fatalf("failed to create initial pools: %v", err)
	}

	// Close old pools explicitly so RecreatePoolsWithConfig's Close calls return errors.
	_ = rwPool.Close()
	_ = roPool.Close()

	paths := DatabasePaths{Main: dbPath, Thumbs: thumbsDBPath}
	newRwPool, newRoPool, err := RecreatePoolsWithConfig(ctx, paths, cfg, rwPool, roPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newRwPool == nil || newRoPool == nil {
		t.Fatal("expected new pools")
	}

	_ = newRwPool.Close()
	_ = newRoPool.Close()
}
