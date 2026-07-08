package cachelite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

func TestGetCacheSizeBytes_NilResult(t *testing.T) {
	orig := getHttpCacheSizeBytes
	getHttpCacheSizeBytes = func(ctx context.Context, cpc *dbconnpool.CpConn) (interface{}, error) {
		return nil, nil
	}
	t.Cleanup(func() { getHttpCacheSizeBytes = orig })

	size, err := GetCacheSizeBytes(context.Background(), createTestDBPoolInternal(t))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if size != 0 {
		t.Fatalf("expected size 0, got %d", size)
	}
}

func TestGetCacheSizeBytes_UnexpectedType(t *testing.T) {
	orig := getHttpCacheSizeBytes
	getHttpCacheSizeBytes = func(ctx context.Context, cpc *dbconnpool.CpConn) (interface{}, error) {
		return "not-an-int", nil
	}
	t.Cleanup(func() { getHttpCacheSizeBytes = orig })

	_, err := GetCacheSizeBytes(context.Background(), createTestDBPoolInternal(t))
	if err == nil {
		t.Fatal("expected error for unexpected type")
	}
	if !strings.Contains(err.Error(), "unexpected type from GetHttpCacheSizeBytes: string") {
		t.Fatalf("expected error containing 'unexpected type from GetHttpCacheSizeBytes: string', got %v", err)
	}
}

func TestRotateCacheTable_ExecFailures(t *testing.T) {
	origExec := txExecContext
	t.Cleanup(func() { txExecContext = origExec })

	tests := []struct {
		name            string
		matchSQL        string
		wantErrContains string
	}{
		{
			name:            "DropStaleFails",
			matchSQL:        "DROP TABLE IF EXISTS http_cache_to_be_dropped",
			wantErrContains: "drop previous stale cache table",
		},
		{
			name:            "RenameFails",
			matchSQL:        "ALTER TABLE http_cache RENAME TO http_cache_to_be_dropped",
			wantErrContains: "rename http_cache to stale table",
		},
		{
			name:            "IndexDropFails",
			matchSQL:        "DROP INDEX IF EXISTS idx_http_cache_key",
			wantErrContains: "drop stale cache index failed",
		},
		{
			name:            "CreateTableFails",
			matchSQL:        "CREATE TABLE IF NOT EXISTS http_cache",
			wantErrContains: "create fresh http_cache table",
		},
		{
			name:            "IndexCreateFails",
			matchSQL:        "CREATE INDEX IF NOT EXISTS idx_http_cache_key",
			wantErrContains: "create cache index failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txExecContext = func(tx *sql.Tx, ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
				if strings.Contains(query, tt.matchSQL) {
					return nil, errors.New("exec denied")
				}
				return origExec(tx, ctx, query, args...)
			}
			t.Cleanup(func() { txExecContext = origExec })

			err := RotateCacheTable(context.Background(), createTestDBPoolInternal(t))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrContains) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrContains, err)
			}
		})
	}
}

func TestRotateCacheTable_CommitFails(t *testing.T) {
	origCommit := txCommit
	txCommit = func(tx *sql.Tx) error {
		return errors.New("commit denied")
	}
	t.Cleanup(func() { txCommit = origCommit })

	err := RotateCacheTable(context.Background(), createTestDBPoolInternal(t))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "commit cache table rotation") {
		t.Fatalf("expected error containing 'commit cache table rotation', got %v", err)
	}
}

func TestRotateCacheTable_RollbackAfterExecErrorLogsWarning(t *testing.T) {
	origExec := txExecContext
	origRollback := txRollback

	txExecContext = func(tx *sql.Tx, ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
		if strings.Contains(query, "ALTER TABLE http_cache RENAME TO http_cache_to_be_dropped") {
			return nil, errors.New("exec denied")
		}
		return origExec(tx, ctx, query, args...)
	}
	t.Cleanup(func() { txExecContext = origExec })

	txRollback = func(tx *sql.Tx) error {
		_ = origRollback(tx)
		return errors.New("rollback denied")
	}
	t.Cleanup(func() { txRollback = origRollback })

	err := RotateCacheTable(context.Background(), createTestDBPoolInternal(t))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "rename http_cache to stale table") {
		t.Fatalf("expected error containing 'rename http_cache to stale table', got %v", err)
	}
}
