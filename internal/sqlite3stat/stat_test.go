package sqlite3stat_test

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/sqlite3stat"
)

func TestPutDebugAttrs_nilHook(t *testing.T) {
	t.Parallel()

	saved := sqlite3stat.PutDebugHook
	t.Cleanup(func() { sqlite3stat.PutDebugHook = saved })
	sqlite3stat.PutDebugHook = nil

	if attrs := sqlite3stat.PutDebugAttrs(nil); attrs != nil {
		t.Fatalf("expected nil attrs, got %v", attrs)
	}
}

func TestPutDebugAttrs_nilFunc(t *testing.T) {
	t.Parallel()

	saved := sqlite3stat.PutDebugHook
	t.Cleanup(func() { sqlite3stat.PutDebugHook = saved })

	var nilFn func(*sql.Conn) []slog.Attr
	sqlite3stat.PutDebugHook = &nilFn

	if attrs := sqlite3stat.PutDebugAttrs(nil); attrs != nil {
		t.Fatalf("expected nil attrs, got %v", attrs)
	}
}

func TestDBStatusMem_sqlite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "stat.db")
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(dbPath)+"?mode=rwc")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	mem, ok := sqlite3stat.DBStatusMem(conn)
	if !ok {
		t.Fatal("DBStatusMem returned ok=false")
	}
	if mem < 0 {
		t.Fatalf("db_status_mem = %d, want non-negative", mem)
	}

	attrs := sqlite3stat.DefaultPutDebugAttrs(conn)
	if len(attrs) != 1 || attrs[0].Key != "db_status_mem" {
		t.Fatalf("DefaultPutDebugAttrs = %#v", attrs)
	}
}
