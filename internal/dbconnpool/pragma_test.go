package dbconnpool

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestRunPragmaOptimize_Default(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	defer db.Close()

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get conn: %v", err)
	}
	defer conn.Close()

	if err := RunPragmaOptimize(ctx, conn, PragmaOptimizeDefault); err != nil {
		t.Fatalf("RunPragmaOptimize with default mask: %v", err)
	}
}

func TestRunPragmaOptimize_FreshConnection(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	defer db.Close()

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get conn: %v", err)
	}
	defer conn.Close()

	if err := RunPragmaOptimize(ctx, conn, PragmaOptimizeFreshConnection); err != nil {
		t.Fatalf("RunPragmaOptimize with fresh connection mask: %v", err)
	}
}
