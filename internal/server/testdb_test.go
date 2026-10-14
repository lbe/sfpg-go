package server

import (
	"context"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

func resetInfraMemDB(t *testing.T, pool *dbconnpool.DbSQLConnPool) {
	t.Helper()
	if pool == nil {
		t.Fatal("resetInfraMemDB: pool is nil")
	}
	ctx := context.Background()
	cpc, err := pool.Get()
	if err != nil {
		t.Fatalf("reset infra db: get conn: %v", err)
	}
	defer pool.Put(cpc)
	stmts := []string{
		"DELETE FROM http_cache",
		"DROP TABLE IF EXISTS file_folder_index_new",
		"DELETE FROM file_folder_index",
		"DELETE FROM files",
		"DELETE FROM folders",
		"DELETE FROM file_paths",
		"DELETE FROM folder_paths",
		"DELETE FROM thumbnails",
		"DELETE FROM thumbnail_blobs",
		"DELETE FROM exif_metadata",
		"DELETE FROM iptc_metadata",
		"DELETE FROM iptc_keywords",
		"DELETE FROM xmp_properties",
		"DELETE FROM xmp_raw",
		"DELETE FROM config",
		"DELETE FROM login_attempts",
		"DELETE FROM invalid_files",
		"DELETE FROM module_state",
		"DELETE FROM sqlite_sequence",
	}
	for _, q := range stmts {
		if _, execErr := cpc.Conn.ExecContext(ctx, q); execErr != nil {
			t.Fatalf("reset infra db (%s): %v", q, execErr)
		}
	}
}
