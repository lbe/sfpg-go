package database

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/config"
)

func TestRecreatePoolsWithConfig_CreatePoolsFails(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")
	copyTestDBFixture(t, dbPath, thumbsDBPath)

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
	copyTestDBFixture(t, dbPath, thumbsDBPath)

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
