package modulestate

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/migrations"
)

func setupModuleStatePool(t *testing.T) (*dbconnpool.DbSQLConnPool, func()) {
	t.Helper()

	tempDir := t.TempDir()
	dbfile := filepath.Join(tempDir, "module_state.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")
	mmapSize := strconv.Itoa(39 * 1024 * 1024 * 1024)
	params := []string{
		"_cache_size=10240",
		"_pragma=cache(shared)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=temp_store(memory)",
		"_pragma=foreign_keys(true)",
		"_pragma=mmap_size(" + mmapSize + ")",
		"_txlock=deferred",
	}
	dsn := filepath.ToSlash(dbfile) + "?" + strings.Join(params, "&")

	db, err := sql.Open("sqlite3", dsn)
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

	if upErr := m.Up(); upErr != nil && upErr != migrate.ErrNoChange {
		db.Close()
		t.Fatalf("failed to apply migrations: %v", upErr)
	}

	thumbsMigrator, err := migrations.NewThumbsMigrator(thumbsDBPath)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create thumbs migrator: %v", err)
	}
	if thumbsErr := thumbsMigrator.Up(); thumbsErr != nil && thumbsErr != migrate.ErrNoChange {
		thumbsMigrator.Close()
		db.Close()
		t.Fatalf("failed to apply thumbs migrations: %v", thumbsErr)
	}
	thumbsMigrator.Close()

	ctx := context.Background()
	pool, err := dbconnpool.NewDbSQLConnPool(ctx, dsn, dbconnpool.Config{
		DriverName:         "sqlite3",
		ReadOnly:           false,
		MaxConnections:     2,
		MinIdleConnections: 1,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsDBPath,
	})
	if err != nil {
		db.Close()
		t.Fatalf("failed to create db pool: %v", err)
	}

	cleanup := func() {
		_ = pool.Close()
		_ = db.Close()
	}

	return pool, cleanup
}

func TestService_IsActive_DefaultFalse(t *testing.T) {
	pool, cleanup := setupModuleStatePool(t)
	defer cleanup()

	svc := NewService(pool)
	active, err := svc.IsActive(context.Background(), "discovery")
	if err != nil {
		t.Fatalf("IsActive error: %v", err)
	}
	if active {
		t.Fatal("expected inactive when no row exists")
	}
}

func TestService_SetActive_Toggle(t *testing.T) {
	pool, cleanup := setupModuleStatePool(t)
	defer cleanup()

	svc := NewService(pool)
	ctx := context.Background()

	if err := svc.SetActive(ctx, "discovery", true); err != nil {
		t.Fatalf("SetActive(true) error: %v", err)
	}
	active, err := svc.IsActive(ctx, "discovery")
	if err != nil {
		t.Fatalf("IsActive error: %v", err)
	}
	if !active {
		t.Fatal("expected active after SetActive(true)")
	}

	err = svc.SetActive(ctx, "discovery", false)
	if err != nil {
		t.Fatalf("SetActive(false) error: %v", err)
	}
	active, err = svc.IsActive(ctx, "discovery")
	if err != nil {
		t.Fatalf("IsActive error: %v", err)
	}
	if active {
		t.Fatal("expected inactive after SetActive(false)")
	}
}
