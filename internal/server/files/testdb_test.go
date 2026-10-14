package files

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/gallerylib"
)

var (
	sharedFilesRWP     *dbconnpool.DbSQLConnPool
	sharedFilesROP     *dbconnpool.DbSQLConnPool
	sharedFilesOnce    sync.Once
	sharedFilesInitErr error
	sharedFilesMu      sync.Mutex
)

func ensureSharedFilesPools() {
	sharedFilesOnce.Do(func() {
		ctx := context.Background()
		poolCfg := func(readOnly bool) dbconnpool.Config {
			return dbconnpool.Config{
				DriverName:         "sqlite3",
				MaxConnections:     10,
				MinIdleConnections: 1,
				ReadOnly:           readOnly,
				QueriesFunc:        gallerydb.NewCustomQueries,
				ThumbsDBPath:       "thumbs.db",
				TestUseMemDB:       true,
			}
		}
		sharedFilesRWP, sharedFilesInitErr = dbconnpool.NewDbSQLConnPool(ctx, "", poolCfg(false))
		if sharedFilesInitErr != nil {
			return
		}
		sharedFilesROP, sharedFilesInitErr = dbconnpool.NewDbSQLConnPool(ctx, "", poolCfg(true))
		if sharedFilesInitErr != nil {
			_ = sharedFilesRWP.Close()
			sharedFilesRWP = nil
		}
	})
}

func resetSharedFilesDB(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	cpc, err := sharedFilesRWP.Get()
	if err != nil {
		t.Fatalf("reset files db: get conn: %v", err)
	}
	defer sharedFilesRWP.Put(cpc)
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
			t.Fatalf("reset files db (%s): %v", q, execErr)
		}
	}
	imp := &gallerylib.Importer{Q: cpc.Queries}
	if _, err := imp.CreateRootFolderEntry(ctx, time.Now().Unix()); err != nil {
		t.Fatalf("create root folder: %v", err)
	}
}
