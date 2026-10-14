package testdb

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func openImage(t *testing.T, uri string) *sql.DB {
	t.Helper()
	db, err := driver.Open(uri)
	if err != nil {
		t.Fatalf("open image: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func queryJournalMode(t *testing.T, db *sql.DB, dbName string) string {
	t.Helper()
	var mode string
	if err := db.QueryRow("PRAGMA " + dbName + ".journal_mode").Scan(&mode); err != nil {
		t.Fatalf("%s journal_mode: %v", dbName, err)
	}
	return strings.ToLower(strings.TrimSpace(mode))
}

func tablePresent(t *testing.T, db *sql.DB, dbName, table string) {
	t.Helper()
	q := "SELECT name FROM " + dbName + ".sqlite_master WHERE type='table' AND name=? LIMIT 1"
	var name string
	if err := db.QueryRow(q, table).Scan(&name); err != nil {
		t.Fatalf("%s.%s not present: %v", dbName, table, err)
	}
}

func dbFileEntries(t *testing.T, db *sql.DB) []struct{ name, file string } {
	t.Helper()
	rows, err := db.Query("SELECT name, file FROM pragma_database_list()")
	if err != nil {
		t.Fatalf("pragma_database_list: %v", err)
	}
	defer rows.Close()
	var out []struct{ name, file string }
	for rows.Next() {
		var e struct{ name, file string }
		if scanErr := rows.Scan(&e.name, &e.file); scanErr != nil {
			t.Fatalf("scan database list: %v", scanErr)
		}
		if e.name == "main" || e.name == "thumbs" {
			out = append(out, e)
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("rows: %v", rowsErr)
	}
	return out
}

const isolatedSeedSchemaKey = "testdb-seed-isolated"

func TestMemoryDatabasesMainAndThumbs(t *testing.T) {
	ctx := context.Background()
	md, newErr := NewMemoryDatabases(ctx, isolatedSeedSchemaKey+"-main-thumbs")
	if newErr != nil {
		t.Fatalf("NewMemoryDatabases: %v", newErr)
	}
	defer func() {
		if closeErr := md.Close(); closeErr != nil {
			t.Fatalf("Close: %v", closeErr)
		}
	}()

	mainURI := md.MainURI(false)
	mainDB := openImage(t, mainURI)
	if got := queryJournalMode(t, mainDB, "main"); got != "memory" {
		t.Fatalf("main journal_mode = %q, want memory", got)
	}
	tablePresent(t, mainDB, "main", "config")

	thumbsURI, thumbsErr := md.ThumbsURI(ctx, false)
	if thumbsErr != nil {
		t.Fatalf("ThumbsURI: %v", thumbsErr)
	}
	thumbsDB := openImage(t, thumbsURI)
	if got := queryJournalMode(t, thumbsDB, "main"); got != "memory" {
		t.Fatalf("thumbs journal_mode = %q, want memory", got)
	}
	tablePresent(t, thumbsDB, "main", "thumbnail_blobs")

	// ThumbsURI is stable: the same peer URI returns on repeat calls.
	again, againErr := md.ThumbsURI(ctx, false)
	if againErr != nil {
		t.Fatalf("ThumbsURI again: %v", againErr)
	}
	if again != thumbsURI {
		t.Fatalf("ThumbsURI changed: %q vs %q", again, thumbsURI)
	}

	// Read-only URIs select mode=ro.
	roMain := md.MainURI(true)
	u, parseErr := url.Parse(roMain)
	if parseErr != nil {
		t.Fatalf("parse ro main URI: %v", parseErr)
	}
	if got := u.Query().Get("mode"); got != "ro" {
		t.Fatalf("ro main mode = %q, want ro", got)
	}
	roThumbs, roThumbsErr := md.ThumbsURI(ctx, true)
	if roThumbsErr != nil {
		t.Fatalf("ThumbsURI ro: %v", roThumbsErr)
	}
	ut, parseThumbsErr := url.Parse(roThumbs)
	if parseThumbsErr != nil {
		t.Fatalf("parse ro thumbs URI: %v", parseThumbsErr)
	}
	if got := ut.Query().Get("mode"); got != "ro" {
		t.Fatalf("ro thumbs mode = %q, want ro", got)
	}
}

func TestMemoryDatabasesImagesAreNotFilesystemFiles(t *testing.T) {
	ctx := context.Background()
	md, newErr := NewMemoryDatabases(ctx, isolatedSeedSchemaKey+"-nofiles")
	if newErr != nil {
		t.Fatalf("NewMemoryDatabases: %v", newErr)
	}
	defer func() {
		if closeErr := md.Close(); closeErr != nil {
			t.Fatalf("Close: %v", closeErr)
		}
	}()

	mainDB := openImage(t, md.MainURI(false))
	for _, e := range dbFileEntries(t, mainDB) {
		if e.file == "" {
			continue
		}
		if strings.Contains(e.file, "sfpg-test-main-") {
			t.Fatalf("%s backing uses temp prefix: %q", e.name, e.file)
		}
		if _, statErr := os.Stat(e.file); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("%s backing file %q stat err = %v, want ErrNotExist", e.name, e.file, statErr)
		}
	}
}

func TestMemoryDatabasesSeedsAreRollbackJournalWithoutSidecars(t *testing.T) {
	ctx := context.Background()
	md, newErr := NewMemoryDatabases(ctx, isolatedSeedSchemaKey+"-journal")
	if newErr != nil {
		t.Fatalf("NewMemoryDatabases: %v", newErr)
	}
	defer func() {
		if closeErr := md.Close(); closeErr != nil {
			t.Fatalf("Close: %v", closeErr)
		}
	}()
	if _, thumbsErr := md.ThumbsURI(ctx, false); thumbsErr != nil {
		t.Fatalf("ThumbsURI: %v", thumbsErr)
	}

	for _, p := range []string{
		mustMainSeedPath(t, isolatedSeedSchemaKey+"-journal"),
		mustThumbsSeedPath(t, isolatedSeedSchemaKey+"-journal"),
	} {
		mode, modeErr := querySeedJournalMode(p)
		if modeErr != nil {
			t.Fatalf("seed %s journal_mode query: %v", p, modeErr)
		}
		if mode != "delete" {
			t.Fatalf("seed %s journal_mode = %q, want delete", p, mode)
		}
		for _, sidecar := range []string{p + "-wal", p + "-shm"} {
			if _, statErr := os.Stat(sidecar); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("sidecar %s stat err = %v, want ErrNotExist", sidecar, statErr)
			}
		}
	}
}

func mustMainSeedPath(t *testing.T, key string) string {
	t.Helper()
	p, pathErr := mainSeedPath(key)
	if pathErr != nil {
		t.Fatalf("mainSeedPath: %v", pathErr)
	}
	return p
}

func mustThumbsSeedPath(t *testing.T, key string) string {
	t.Helper()
	p, pathErr := thumbsSeedPath(key)
	if pathErr != nil {
		t.Fatalf("thumbsSeedPath: %v", pathErr)
	}
	return p
}

func TestMemoryDatabasesMissingMainSeedRecovery(t *testing.T) {
	ctx := context.Background()
	key := isolatedSeedSchemaKey + "-main-recovery"

	first, newErr := NewMemoryDatabases(ctx, key)
	if newErr != nil {
		t.Fatalf("first NewMemoryDatabases: %v", newErr)
	}
	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("first Close: %v", closeErr)
	}

	path := mustMainSeedPath(t, key)
	if removeErr := os.Remove(path); removeErr != nil {
		t.Fatalf("remove main seed: %v", removeErr)
	}
	removeSidecars(path)

	second, secondErr := NewMemoryDatabases(ctx, key)
	if secondErr != nil {
		t.Fatalf("NewMemoryDatabases after missing main seed: %v", secondErr)
	}
	defer func() {
		if closeErr := second.Close(); closeErr != nil {
			t.Fatalf("second Close: %v", closeErr)
		}
	}()
	if !seedReady(path) {
		t.Fatalf("main seed %q not a rollback-journal file after acquire", path)
	}
	mainDB := openImage(t, second.MainURI(false))
	tablePresent(t, mainDB, "main", "config")
}

func TestMemoryDatabasesMissingThumbsSeedRecovery(t *testing.T) {
	ctx := context.Background()
	key := isolatedSeedSchemaKey + "-thumbs-recovery"

	first, newErr := NewMemoryDatabases(ctx, key)
	if newErr != nil {
		t.Fatalf("first NewMemoryDatabases: %v", newErr)
	}
	if _, thumbsErr := first.ThumbsURI(ctx, false); thumbsErr != nil {
		t.Fatalf("first ThumbsURI: %v", thumbsErr)
	}
	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("first Close: %v", closeErr)
	}

	path := mustThumbsSeedPath(t, key)
	if removeErr := os.Remove(path); removeErr != nil {
		t.Fatalf("remove thumbs seed: %v", removeErr)
	}
	removeSidecars(path)

	second, secondErr := NewMemoryDatabases(ctx, key)
	if secondErr != nil {
		t.Fatalf("second NewMemoryDatabases: %v", secondErr)
	}
	defer func() {
		if closeErr := second.Close(); closeErr != nil {
			t.Fatalf("second Close: %v", closeErr)
		}
	}()
	thumbsURI, thumbsErr := second.ThumbsURI(ctx, false)
	if thumbsErr != nil {
		t.Fatalf("ThumbsURI after missing thumbs seed: %v", thumbsErr)
	}
	if !seedReady(path) {
		t.Fatalf("thumbs seed %q not a rollback-journal file after acquire", path)
	}
	thumbsDB := openImage(t, thumbsURI)
	tablePresent(t, thumbsDB, "main", "thumbnail_blobs")
}

func TestMemoryDatabasesCorruptThumbsSeedRecovery(t *testing.T) {
	ctx := context.Background()
	key := isolatedSeedSchemaKey + "-thumbs-corrupt"

	first, newErr := NewMemoryDatabases(ctx, key)
	if newErr != nil {
		t.Fatalf("first NewMemoryDatabases: %v", newErr)
	}
	if _, thumbsErr := first.ThumbsURI(ctx, false); thumbsErr != nil {
		t.Fatalf("first ThumbsURI: %v", thumbsErr)
	}
	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("first Close: %v", closeErr)
	}

	path := mustThumbsSeedPath(t, key)
	if writeErr := os.WriteFile(path, []byte("corrupt-not-a-database"), 0o644); writeErr != nil {
		t.Fatalf("corrupt thumbs seed: %v", writeErr)
	}
	removeSidecars(path)

	second, secondErr := NewMemoryDatabases(ctx, key)
	if secondErr != nil {
		t.Fatalf("second NewMemoryDatabases: %v", secondErr)
	}
	defer func() {
		if closeErr := second.Close(); closeErr != nil {
			t.Fatalf("second Close: %v", closeErr)
		}
	}()
	thumbsURI, thumbsErr := second.ThumbsURI(ctx, false)
	if thumbsErr != nil {
		t.Fatalf("ThumbsURI after corrupt thumbs seed: %v", thumbsErr)
	}
	if !seedReady(path) {
		t.Fatalf("thumbs seed %q not a rollback-journal file after recovery", path)
	}
	thumbsDB := openImage(t, thumbsURI)
	tablePresent(t, thumbsDB, "main", "thumbnail_blobs")
}

func TestMemoryDatabasesCloseIsIdempotentAndFresh(t *testing.T) {
	ctx := context.Background()
	key := isolatedSeedSchemaKey + "-close-fresh"

	first, newErr := NewMemoryDatabases(ctx, key)
	if newErr != nil {
		t.Fatalf("first NewMemoryDatabases: %v", newErr)
	}
	firstMain := first.MainURI(false)
	mainDB := openImage(t, firstMain)
	if _, execErr := mainDB.Exec("CREATE TABLE close_probe (id INTEGER PRIMARY KEY)"); execErr != nil {
		t.Fatalf("create probe table: %v", execErr)
	}
	if _, thumbsErr := first.ThumbsURI(ctx, false); thumbsErr != nil {
		t.Fatalf("first ThumbsURI: %v", thumbsErr)
	}
	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("first Close: %v", closeErr)
	}
	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("second Close: %v", closeErr)
	}

	second, secondErr := NewMemoryDatabases(ctx, key)
	if secondErr != nil {
		t.Fatalf("second NewMemoryDatabases: %v", secondErr)
	}
	defer func() {
		if closeErr := second.Close(); closeErr != nil {
			t.Fatalf("second Close: %v", closeErr)
		}
	}()
	freshDB := openImage(t, second.MainURI(false))
	var n int
	if scanErr := freshDB.QueryRow("SELECT COUNT(*) FROM close_probe").Scan(&n); scanErr == nil {
		t.Fatalf("stale probe table survived Close (count=%d)", n)
	}
	tablePresent(t, freshDB, "main", "config")
}

func TestMemoryDatabasesEmptySchemaKey(t *testing.T) {
	if _, newErr := NewMemoryDatabases(context.Background(), " "); newErr == nil {
		t.Fatal("expected error for empty schema key")
	}
}

func TestMemoryDatabasesConcurrentThumbsInit(t *testing.T) {
	ctx := context.Background()
	md, newErr := NewMemoryDatabases(ctx, isolatedSeedSchemaKey+"-concurrent")
	if newErr != nil {
		t.Fatalf("NewMemoryDatabases: %v", newErr)
	}
	defer func() {
		if closeErr := md.Close(); closeErr != nil {
			t.Fatalf("Close: %v", closeErr)
		}
	}()

	const workers = 8
	uris := make([]string, workers)
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := range uris {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u, thumbsErr := md.ThumbsURI(ctx, false)
			uris[i], errs[i] = u, thumbsErr
		}(i)
	}
	wg.Wait()
	for i, thumbsErr := range errs {
		if thumbsErr != nil {
			t.Fatalf("ThumbsURI worker %d: %v", i, thumbsErr)
		}
		if uris[i] != uris[0] {
			t.Fatalf("ThumbsURI worker %d = %q, want %q", i, uris[i], uris[0])
		}
	}
}
